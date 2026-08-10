# nanite-render — Architecture

Deep dive into how nanite-render is built, why it's shaped this way, and
the design decisions behind each layer. Read this before contributing.

---

## 1. Design principles

1. **nanite-render is not a parser.** Jade parses jade. templ parses templ.
   html/template parses html/template. nanite-render is the *hub* — cache,
   composition, lifecycle — that makes engines feel like one system.
2. **The hot path is lock-free and zero-alloc.** Every read in the
   Registry goes through `atomic.Pointer`. The executor walks a SoA node
   stream with no per-render allocation.
3. **Router-agnostic core.** The core `render` package imports no router.
   Adapters (like `nano/`) live outside.
4. **Typed engine identifiers.** `EngineName` is a distinct string type
   with exported constants (`EngineJade`, `EngineHTML`, ...). A typo'd
   `"jad"` is a compile error; the fluent builder accepts either the
   constant or a live `Engine` via `EngineInstance`. The identifier is a
   string at rest (it's half the cache key), but the *call site* is
   compiler-checked. Custom engines use `CustomEngine(name)`.
5. **Engine-specific work stays in engines.** Interpolation, escaping,
   and parsing are the engine's job. nanite-render provides the cross-cutting
   glue: components, slots, state, layouts, superpowers.

---

## 2. Package layout

```
nanite-render/
├── go.mod
├── render.go          // Program, Engine, Registry, sentinels (construction)
├── cache.go           // off-heap lock-free Cache
├── context.go         // pooled RenderContext
├── writer.go          // pooled ByteWriter
├── execute.go         // SoA executor + component dispatch + slots
├── parse_html.go      // minimal HTML parser → NodeStream
├── component.go       // Component, ComponentRegistry, fluent Definition
├── fluent.go          // ComponentContext, Definition builder
├── state.go           // State, generic UseState[T]
├── props.go           // BindProps[T] — type-safe props
├── superpowers.go     // component/yield/useState FuncMap superpowers
├── slots.go           // Slots map, SlotsDataKey  (in soa.go)
├── soa.go             // NodeStream + NodeStreamBuilder
├── inject.go          // DataInjector, FuncMapBuilder
├── options.go         // functional options + chainable API
├── minify.go          // tdewolff/minify at compile time
├── compress.go        // gzip writer
├── conditional.go     // ETag / 304
├── variants.go        // locale/theme variant resolution
├── pipeline.go        // compile/execute hooks
├── watcher.go         // fsnotify hot-reload
├── loader.go          // file/map/prefix loaders
├── errors.go          // ParseError + sentinel errors
├── engine/
│   ├── jade.go        // Jade adapter (html/template under the hood)
│   ├── stdhtml.go     // HTML — plain, SoA executor, no data binding
│   ├── htmltemplate.go// HTMLTemplate — html/template + superpowers
│   └── templ.go       // templ component adapter
├── nano/
│   └── nano.go        // nanite router direct-call adapter
└── docs/              // this doc set
```

---

## 3. The render pipeline

```
Load source → Engine.Compile → cache.Put(engine, name, program)
                       │
                       ▼  (per request)
Registry.Render(rc, engine, name, data)
    ├─ InjectFuncMap(rc, data)         // injectors + FuncMap factory
    ├─ cache.Get(engine, name)         // ~50ns lock-free
    ├─ (miss) Loader(name) → Compile → Put
    ├─ (optional) CheckConditional     // ETag → 304
    └─ Engine.Execute(program, writer, rc, data)
         ├─ SoA executor walk          // plain HTML engine
         │    ├─ emit text / tags / attrs
         │    ├─ dispatch components (ComponentRegistry)
         │    └─ collect children + slots
         └─ html/template Execute      // jade, HTMLTemplate engines
              └─ superpowers (component, yield, useState)
```

### 3.1 Two execution paths

There are **two** execution engines, and the choice is per-`Engine`:

**Path A — the SoA executor** (`execute.go`). Used by the plain HTML
engine. Walks the `NodeStream` depth-first, emits bytes to the
`ByteWriter`. Zero-alloc on the hot loop. Components dispatch via
`ComponentRegistry.Lookup`; children and slots are collected from the
`Parent` index.

**Path B — html/template** (`EngineData`). Used by the jade and
`HTMLTemplate` engines. The engine's `Compile` produces a
`*template.Template` (stored in `Program.EngineData`), and `Execute`
calls `tmpl.Execute(w, data)`. The framework's superpowers are injected
as template functions.

The choice is made by the engine's `Execute` implementation, not by the
framework.

---

## 4. The cache — lock-free, off-heap

`Cache` is the heart of the hot path. Design goals: **zero-alloc reads,
no mutexes, cache-line isolation**.

### 4.1 Off-heap slots

The slot array is allocated via `xDarkicex/memory.MmapAnonymous`. The Go
GC never scans it — no write barriers, no mark-phase scanning. Each slot
is a 32-byte struct (half a cache line on x86, quarter on Apple Silicon):

```go
type cacheSlot struct {
    seq     uint64  // sequence lock; LSB = write in progress
    hash    uint64  // FNV-1a 64 of (engine, name)
    program uint64  // *Program stored as uintptr
    tagBits uint64  // per-slot tag bitmask
}
```

### 4.2 Key hashing — no string concat

Cache keys are `(engine, name)` pairs. Instead of building
`"engine/name"` (which allocates), the hash composes two FNV-1a hashes:

```go
func fnvPair(engine, name string) uint64 {
    h := fnv1a64(engine)
    h = h*prime + fnv1a64(name)
    return h
}
```

This is why the hot path is 0 allocs — no intermediate string is ever
built.

### 4.3 Concurrency

- **Get** is a seqlock read: load `seq`, load `hash` + `program`, load
  `seq` again. If the seq is even and unchanged, the read is valid.
  Concurrent writes cause the seq to change → the read retries or misses.
- **Put** is a CAS-loop: `CompareAndSwap(seq, seq, seq+1)` locks the slot,
  writes, `Store(seq, seq+2)` unlocks.
- **Delete** is Put with `hash=0`.
- **Tag invalidation** scans slots by tag bit. Cold path, O(n).

Linear probing handles collisions; a full table evicts the "oldest" slot
via a coarse LRU approximation in the seq's high bits.

### 4.4 Tag invalidation

Tags live on the Registry (`atomic.Pointer[map[string]map[tagPair]struct{}]`).
`SetTag(engine, name, tags...)` and `InvalidateTag(tag)` let ops drop
groups of cached programs without enumerating keys. The `tagPair` struct
avoids the `"engine/name"` string round-trip.

---

## 5. The SoA NodeStream

The executor walks a Structure-of-Arrays node stream. Each "node" is an
index into parallel slices:

```go
type NodeStream struct {
    Tag        []string   // element name
    Flags      []uint8    // Flag* bitmap
    NS         []uint16   // namespace id
    ChildStart []uint32   // [start, end) child range
    ChildEnd   []uint32
    AttrStart  []uint32   // [start, end) attr range
    AttrEnd    []uint32
    TextOff    []uint32   // [start, end) into textBuf (2 per text node)
    Parent     []int32    // parent node index, -1 = root
    ComponentName []string // component name when FlagComponent set
    AttrKeys   []string
    AttrVals   []any
    textBuf    []byte
    Count      int
}
```

### 5.1 Why Parent-based walking

The executor walks by `Parent` index, not by flat child ranges. This
solves a subtle correctness issue: children of different parents are
interleaved in the stream (document order), so a flat `ChildStart/ChildEnd`
range can accidentally include another parent's subtree. The `Parent`
field makes the tree explicit.

```go
func walk(p *Program, nodes NodeStream, parentIdx int32, w, rc, data) {
    for i := range nodes.Parent {
        if nodes.Parent[i] != parentIdx { continue }
        emitNode(p, nodes, i, w, rc, data)  // recurse into children
    }
}
```

### 5.2 Node flags

```go
FlagVoid, FlagSelfClosing, FlagComponent, FlagFragment,
FlagRaw, FlagText, FlagDynamicTag, FlagHasChildren
```

Slots are encoded as nodes with a non-empty `ComponentName` but no
`FlagComponent`. The walker treats them as invisible containers — only
their children are emitted.

### 5.3 Zero-copy strings

The parser uses `unsafe.String(&src[off], len)` to build tag/attr strings
that reference the source bytes directly — no per-string copy. The
contract: the caller must keep `src` alive for the lifetime of the
Program. The default loader-cache pattern satisfies this automatically.

---

## 6. Components, children, slots, state

### 6.1 Component interfaces

```go
type Component interface {
    Render(w ByteWriter, rc *RenderContext, data any) error
}

// Optional capabilities:
type ComponentWithFuncs interface {        // per-component FuncMap
    Component
    ComponentFuncs() template.FuncMap
}
type ComponentWithChildren interface {     // receives pre-rendered children
    Component
    RenderWithChildren(w ByteWriter, rc *RenderContext, data any, children Children) error
}
```

### 6.2 Dispatch flow (SoA executor)

1. `emitComponent` looks up the node's name in `ComponentRegistry`.
2. It collects children (nodes whose `Parent == componentIdx`) and renders
   each to a string via `walkRender`.
3. It collects slots (children with a non-empty `ComponentName` but no
   `FlagComponent`) into a `Slots` map.
4. `renderComponent(c, w, rc, data, children)`:
   - If `ComponentWithChildren` → call `RenderWithChildren` directly.
   - Else stash children in `data[ChildrenDataKey]` and call `Render`.
5. Per-component FuncMap is set on `rc.ComponentFuncMap` for the duration
   of the render and restored after.

### 6.3 The fluent builder

`cr.Define(name)` returns a `Definition` that wraps a plain `Component`
with a `ComponentContext` — a single struct bundling writer, context,
data, children, slots, and state. The fluent `RenderChildren` form reads
children from the data map entries the dispatcher stashed.

### 6.4 State

`State` is a per-render `map[string]any` behind an RWMutex. The generic
`UseState[T]` wrapper eliminates type assertions:

```go
func UseState[T any](s *State, key string, initial T) (T, func(T))
```

The setter writes back to the same key. State survives across components
within one render (it lives on the pooled `RenderContext`).

### 6.5 Props

`BindProps[T](data)` uses reflection to fill a struct from the data map,
honoring `nanite:"key"` tags. Type mismatches leave fields at zero —
no silent conversion. Nested `*Struct` fields recurse.

---

## 7. Superpowers

The "superpowers" are framework-provided template functions injected into
engines that compile via html/template (jade, HTMLTemplate):

| Superpower | Purpose |
|---|---|
| `component(name, data)` | Dispatch to a registered Component |
| `yield()` | Inject the pre-rendered view body (layout composition) |
| `useState(key, initial)` | Per-render state |
| `get(key)` / `set(key, val)` | State access |

### 7.1 Per-goroutine state

The superpower closures need access to the per-request
`ComponentRegistry`, the pre-rendered view bytes, and the per-component
FuncMap. Because html/template's FuncMap is fixed at parse time, the
framework uses a global `renderState` (mutex-guarded, but the critical
section is a few pointer copies) that the engine's `Execute` sets before
running the template and clears after.

```go
SetRenderState(rc)  // engine calls before tmpl.Execute
...
tmpl.Execute(w, data)  // superpowers read the state
...
ClearRenderState()   // engine calls after
```

This is correct for the typical HTTP pattern (one goroutine per request).

---

## 8. Layout composition (RenderPage)

`Registry.RenderPage(rc, layout, view, data)`:

1. Renders the view to a buffer via the specified engine.
2. Stashes the view bytes on `rc.ViewBytes` (for the `yield()` superpower).
3. Registers a temporary `YIELD` component (for the SoA executor path,
   where `<YIELD/>` is a component tag).
4. Renders the layout.

Layout authors write:

```jade
html
  body
    NAVBAR
    {{ yield }}   // or <YIELD/> for the SoA path
    FOOTER
```

---

## 9. Conditional GET (ETag / 304)

`HashContent(p)` computes a strong ETag from the program's SoA slices at
compile time and caches it on `Program.ETag`. `CheckConditional` compares
against `If-None-Match` at render time — a header read, not a re-hash. On
a match, the response is 304 with no body.

---

## 10. Variants

Per-request dimensions (locale, theme) that rewrite the cache key. The
cache key already composes `(engine, name)`; variants append a
deterministic suffix. `rc.SetVariant("locale", "fr")` → the cache stores
a distinct program per variant, so no manual invalidation when you add a
locale.

---

## 11. Router integration (nano)

`nano.Render` is a **direct-call helper**, not middleware — matching the
original GO-Portfolio pattern where rendering happens at the end of a
handler. It:

1. Acquires a pooled `ByteWriter` wrapping `c.Writer`.
2. Acquires a pooled `RenderContext`.
3. Sets the loader from `reg.DefaultLoader()`.
4. Calls `reg.Render(rc, engine, view, data)`.
5. Releases both on the way out.

No middleware indirection, no route introspection, no global setup.
Handlers that don't render pay nothing extra.

---

## 12. Why it's fast

| Layer | Mechanism | Cost |
|---|---|---|
| Engine lookup | `atomic.Pointer[map[string]Engine]` load | ~5ns |
| Cache lookup | FNV pair hash + seqlock probe | ~50ns |
| Injectors | `atomic.Pointer[[]DataInjector]` read | ~0ns (empty) |
| Conditional | precomputed ETag header compare | ~0ns (no header) |
| Executor walk | SoA contiguous slices | ~0 allocs |
| Writer | pooled buffer, direct writes | ~0 allocs |
| **Total hot path** | | **~60ns, 0 allocs** |

---

## 13. Known limitations / future work

- **Global render state.** The superpower state is a single mutex-guarded
  value. Fine for one-goroutine-per-request servers. A per-goroutine
  state (via runtime goroutine-local) is the next step.
- **Parse allocs.** Parsing a template allocates ~41 times for a 1KB file
  (one-time per template, cached forever). Pushing to zero requires
  pooling the builder or pre-pass sizing.
- **`BindProps` uses reflection.** Correct, but not free. It runs once per
  component render, not per byte — negligible at typical scale.
- **Plain HTML engine has no data binding.** By design. Use `HTMLTemplate`
  or jade for `{{.Var}}`.
