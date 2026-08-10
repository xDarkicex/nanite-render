# nanite-render — LLM Developer Guide

This file is written for LLM agents that auto-generate code against
`nanite-render`. It distils the API, the invariants, and the sharp edges
so generated code compiles on the first try and follows the framework's
conventions.

For the full API see [docs/api.md](docs/api.md). For the why see
[docs/architecture.md](docs/architecture.md).

---

## 1. Module & import paths

```
module github.com/xDarkicex/nanite-render

go 1.25.7
```

Imports:

```go
import (
    "github.com/xDarkicex/nanite-render"          // core
    "github.com/xDarkicex/nanite-render/engine"   // engine adapters
    "github.com/xDarkicex/nanite-render/nano"     // nanite router adapter
)
```

The core imports **no router**. Only `nano/` imports `xDarkicex/nanite`.

---

## 2. Golden rule for generated code

> **nanite-render is not a parser.** If you're generating code that
> parses templates, you're in the wrong place. Compose with the existing
> engines; the framework supplies cache + composition + lifecycle.

The three execution paths to know:

1. **SoA executor** — plain HTML engine (`engine.NewHTML()`). Walks
   `Program.Nodes`, dispatches `<COMPONENT/>` tags, no `{{.Var}}`.
2. **html/template** — jade (`engine.NewJade()`) and
   `engine.NewHTMLTemplate()`. `Program.EngineData` is a
   `*template.Template`. Superpowers are template functions.
3. **templ** — `engine.NewTempl()`. Adapts pre-compiled components.

---

## 3. The minimal correct program

```go
reg := render.New(
    render.WithEngine(engine.NewJade()),
    render.WithEngine(engine.NewHTMLTemplate()),
    render.WithDefaultLoader(render.NewFileLoader("./views", ".jade")),
    render.WithCacheSize(2048),
)
```

Handlers render like this (nanite):

```go
r.Get("/x", func(c *nanite.Context) {
    if err := nano.Render(c, reg, "jade", "view_name", data); err != nil {
        c.Error(http.StatusInternalServerError, err)
    }
})
```

Handlers render like this (net/http / chi / gin):

```go
bw := render.AcquireWriter(w)
defer render.ReleaseWriter(bw)
rc := render.AcquireContext(bw, r)
defer render.ReleaseContext(rc)
rc.Loader = reg.DefaultLoader()
err := reg.Render(rc, "jade", "view_name", data)
```

**Always `Flush` the writer** before reading output or returning:

```go
_ = bw.Flush()  // drains the pooled buffer into the underlying writer
```

---

## 4. Signatures that trip people up

### Cache keys are `(engine, name)` pairs — not a single string

```go
cache.Put("jade", "layouts/app", prog)      // ✅
cache.Put("jade/layouts/app", prog)         // ❌ single string is wrong
p, ok := cache.Get("jade", "layouts/app")   // ✅
cache.Delete("jade", "layouts/app")         // ✅
```

### `Cache.Get` returns `(*Program, bool)`

```go
p, ok := cache.Get("jade", "x")
if ok { /* use p */ }
```

### `Engine.Execute` takes a `*RenderContext` — 4 args

```go
type Engine interface {
    Name() string
    Compile(src []byte, name string) (*Program, error)
    Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error
}
```

### `Program.EngineData` is the engine's opaque compiled rep

For jade / HTMLTemplate it is a `*template.Template`:

```go
tmpl, ok := p.EngineData.(*template.Template)
if !ok { /* type error */ }
```

### `RenderPage` composes layout + view

```go
err := reg.RenderPage(rc, "layouts/app", "posts/show", data)
```

The view renders first, then the layout. The layout uses `{{ yield }}`
(html/template engines) or `<YIELD/>` (plain HTML engine) to inject the
body.

### Fluent `RenderBuilder` (preferred for multi-part renders)

```go
err := reg.Page(rc).
    Engine(render.EngineJade).          // typed constant
    // or: EngineInstance(engine.NewJade())  // a live engine value
    Layout("layouts/app").
    View("posts/show").
    Parts("partials/nav", "partials/footer").
    With(data).
    Render()              // or .RenderComposition() for parts
```

The `nano.Page(reg, c)` variant acquires and releases the pooled writer +
context automatically; the core `reg.Page(rc)` variant leaves release to
the caller (you own the pooled `rc`).

---

## 5. Components — what to generate

### Fluent Definition (preferred)

```go
cr.Define("NAVBAR").
    WithFuncs(template.FuncMap{
        "currentUser": func() *User { return loadUser() },
    }).
    RenderChildren(func(c *render.ComponentContext) error {
        c.WriteString("<nav>")
        c.WriteChildren()
        c.WriteString("</nav>")
        return nil
    }).
    Register(cr)
```

`ComponentContext` fields:

| Field | Type | Purpose |
|---|---|---|
| `Writer` | `ByteWriter` | output sink |
| `Context` | `*RenderContext` | request context |
| `Data` | `any` | user data |
| `Children` | `Children` | pre-rendered slot-less children |
| `Slots` | `Slots` | named slots map |
| `State` | `*State` | per-render state |
| `DataMap` | `map[string]any` | data as map |

Convenience methods: `Write`, `WriteString`, `WriteChildren`, `WriteSlot`.

### Plain ComponentFunc (when you don't need children/slots)

```go
cr.Register("GREET", render.ComponentFunc(func(w render.ByteWriter, rc *render.RenderContext, data any) error {
    _, err := w.WriteString("<h1>Hello</h1>")
    return err
}))
```

### Type-safe props

```go
type NavbarProps struct {
    Theme string `nanite:"theme"`
    Size  int    `nanite:"size"`
}
props := render.BindProps[NavbarProps](c.Data)
```

Default key = lowercased field name. Type mismatch → zero value.

### State (React hooks style)

```go
count, setCount := render.UseState(c.State, "count", 0)
setCount(count + 1)
```

`UseState[T]` is generic — the value comes back typed. No assertion.

### Registering components

```go
cr := render.NewComponentRegistry()
cr.Define("NAVBAR")...Register(cr)      // fluent
cr.Register("X", render.ComponentFunc(...))  // plain
reg.AttachComponents(cr)                 // attach to the registry
```

---

## 6. Superpowers in templates

Engines compiled via html/template get these template functions:

```
{{ component "NAME" .Data }}   → renders a registered Component
{{ yield }}                    → injects pre-rendered view body
{{ useState "key" initial }}   → per-render state
{{ get "key" }}                → read state
{{ set "key" value }}          → write state
```

If you generate a template with `{{ component "NAVBAR" . }}`, ensure the
component is registered via `reg.AttachComponents(cr)` **before** render,
or it silently renders nothing.

### 6.1 Async / Suspense (server-side streaming)

Components declared with `Async()` + `Fallback()` opt into server-side
streaming. The Fallback output is written inline and flushed
immediately; the real render runs in a worker goroutine and the
finished bytes arrive as a trailing HTMX OOB chunk
(`<div id="<component>" hx-swap-oob="true">…</div>`).

```go
cr.Define("USER_PROFILE").
    Async().
    Fallback(func(c *render.ComponentContext) error {
        return c.WriteString(`<div id="profile" class="skeleton">Loading…</div>`)
    }).
    Render(func(c *render.ComponentContext) error {
        user := db.LoadUser(c.Data.(ProfileID))
        return c.WriteString(fmt.Sprintf(`<div id="profile">%s</div>`, user.Name))
    }).
    Register(cr)
```

The coordinator is **lazy**: no channels, no goroutines, no allocs
when no async component is in the tree. `rc.CloseSuspense()` flushes
the trailing chunks (call it from a defer in the handler).

The win is `Sum(component_times)` → `Max(component_times)` for
independent components. Note that standard HTMX `hx-get` buffers the
entire response body before swapping, so the visible win is server-
side parallelism plus an immediate skeleton on the wire (browser
paints the shell while the worker runs).

Cancellation is wired through `rc.Request.Context()` — a client
disconnect aborts the worker and releases its pooled buffer.

---

## 7. Sharp edges

0. **Engine names are a distinct type (`EngineName`), not `string`.**
   In the fluent builder, use the constants (`render.EngineJade`,
   `render.EngineHTML`, `render.EngineHTMLTemplate`, `render.EngineTempl`)
   or `render.CustomEngine(name)` for a user-defined engine. A raw
   string like `Engine("jade")` is a **compile error**. The direct-call
   helpers (`reg.Render`, `nano.Render`) still take `string` — those are
   the dynamic path.
1. **`unsafe.String` contract.** `ParseHTML` returns strings referencing
   the source bytes. Keep the source alive for the Program's lifetime.
   The loader-cache pattern does this automatically.
2. **The plain HTML engine does NOT interpolate.** `{{.Var}}` renders
   literally. Use jade or HTMLTemplate for data binding.
3. **`BindProps` is reflection** — correct but not free. It runs once per
   component render.
4. **Global superpower state** is single-mutex-guarded. Fine for
   one-goroutine-per-request servers.
5. **`engine.Templ`** does not implement the full `Engine.Execute`
   signature — it has `Render(name, w, data)` instead. Don't pass it to
   code that expects the `Engine` interface to call `Execute`.
6. **Sentinel errors** — match with `errors.Is`:

   ```go
   switch {
   case errors.Is(err, render.ErrTemplateNotFound): // 404
   case errors.Is(err, render.ErrEngineNotFound):   // 500
   case errors.Is(err, render.ErrLoaderMissing):    // 500
   case errors.Is(err, render.ErrLayoutMissing):    // 500
   case errors.Is(err, render.ErrRenderPageInvalid): // 500
   }
   ```

7. **Pooled objects must be released.** `AcquireWriter`/`ReleaseWriter`,
   `AcquireContext`/`ReleaseContext` — always pair them with `defer`.

---

## 8. Conventions for generated code

- Match the surrounding style: `package render`-level code uses
  `atomic.Pointer` for anything written at runtime.
- Chainable methods return the receiver (`*Registry`, `*Definition`).
- FuncMaps are per-request (`rc.WithFuncMap`) or per-component
  (`WithFuncs`); never mutate a global.
- If you add an engine, implement `Name`, `Compile`, `Execute`. Store the
  compiled rep in `Program.EngineData`. Call `SetRenderState(rc)` before
  `tmpl.Execute` and `ClearRenderState()` after.
- If you add a test, prefer property-based `testing/quick` for invariants
  (see `fuzz_test.go`) plus targeted unit tests.
- Keep the hot path zero-alloc. If a new feature allocates per byte or
  per render, push it to compile time (parse) or per-request setup
  (RenderContext).

---

## 9. The executors, one paragraph each

- **`execute.go`** — walks `Program.Nodes` by `Parent` index, emits
  tags/attrs/text, dispatches components, collects children+slots.
- **`parse_html.go`** — tokenizes `<tag>`, `</tag>`, attrs, text, and
  `<COMPONENT/>`; builds a `NodeStream` with zero-copy strings.
- **`cache.go`** — off-heap, lock-free, cache-line-padded slot array
  keyed by FNV pair hash.
- **`superpowers.go`** — `component`/`yield`/state FuncMap closures backed
  by per-render state.
- **`state.go`** — per-render `map[string]any` with generic `UseState[T]`.
- **`props.go`** — `BindProps[T]` struct binding via `nanite:"key"` tags.

---

## 10. HTMX first-class support

nanite-render implements 100% of the HTMX 2.0 server-side contract:
request detection, every trigger lifecycle, every response header, and
OOB swap emission. Routers remain in charge of the wire and lifecycle;
the renderer is purely about producing HTMX-aware bytes and headers.

### When to use what

- **`hx-get` / `hx-post` handler** → `reg.RenderComponent(bw, rc, name, props)`
  renders a single component as the response, no parent template.
- **Dirty component updates** → opt in with `cr.Define("X").WithOOB("x-id")`;
  when `SetState`/`UseState` mutates state during render, output is
  auto-wrapped in `<div id="x-id" hx-swap-oob="true">…</div>`. Clean
  renders write through unchanged.
- **Trigger client events** → `c.AddHXTrigger(name)` or
  `c.AddHXTriggerWithDetail(name, any)` from inside a component's render.
  Three trigger sets: `HX-Trigger`, `HX-Trigger-After-Swap`,
  `HX-Trigger-After-Settle`.
- **Server-driven decisions** (`retarget`, `reswap`, `push-url`,
  `redirect`, `refresh`, `replace-url`, `location`, `reselect`,
  `resettle`) → call the matching `SetHX*` method during the handler,
  then `rc.WriteHTMXHeaders(w)` once at the end.

### Key API surface

```go
// Request detection (nil-safe, pure functions)
render.IsHTMXRequest(r)
render.IsHTMXBoosted(r)
render.IsHTMXHistoryRestore(r)
render.HXTargetID(r)
render.HXTriggerID(r)
render.TriggerName(r)
render.CurrentURL(r)
render.HXPromptResponse(r)

// Direct dispatch (HTMX-targeted components)
reg.RenderComponent(bw, rc, "CARD", props)

// OOB opt-in
cr.Define("CARD").WithOOB("card-slot").Render(func(c *render.ComponentContext) error {
    // SetState/UseState inside → auto OOB wrap on emit
    // AddHXTrigger inside → event fires after swap
}).Register(cr)

// All 12 response headers (one setter each)
rc.AddHXTrigger(name)
rc.AddHXTriggerWithDetail(name, detail)
rc.AddHXTriggerAfterSwap(name)
rc.AddHXTriggerAfterSettle(name)
rc.SetHXRetarget(selector)
rc.SetHXReswap(strategy)
rc.SetHXPushURL(url)
rc.SetHXRedirect(url)
rc.SetHXRefresh()
rc.SetHXReplaceURL(url)
rc.SetHXLocation(url)
rc.SetHXReselect(selector)
rc.SetHXResettle("true"|"false")

// Apply all of the above to the response writer
rc.WriteHTMXHeaders(w)
```

### Handler pattern

```go
func handler(w http.ResponseWriter, r *http.Request) {
    bw := render.AcquireWriter(w); defer render.ReleaseWriter(bw)
    rc := render.AcquireContext(bw, r); defer render.ReleaseContext(rc)

    if render.IsHTMXHistoryRestore(r) {
        reg.RenderPage(rc, "layout", "view", nil)
    } else if render.IsHTMXRequest(r) {
        reg.RenderComponent(bw, rc, "CARD", data)
        rc.SetHXRetarget("#main")
    } else {
        reg.RenderPage(rc, "layout", "view", data)
    }
    rc.WriteHTMXHeaders(w)  // emits every HX-* header the handler set
}
```

### Conventions for generated HTMX code

- Always pair `AcquireContext`/`ReleaseContext` and
  `AcquireWriter`/`ReleaseWriter` with `defer`. The per-request state
  (`hxTriggers`, `hxTriggersAfterSwap`, `hxTriggersAfterSettle`,
  `hmx`, `componentRegistry`) is cleared on Acquire/Release — don't
  leak across handlers.
- `WithOOB(targetID string)` requires an explicit target id. The id is
  a hard DOM contract; do not infer from the component name.
- `AddHXTriggerWithDetail` upgrades an entry to carry JSON detail.
  `Join()` auto-selects the wire format (plain comma list when no entry
  has detail, JSON object otherwise).
- Auto-trigger on dirty OOB emits the lowercased component name;
  components that want different event names add them explicitly
  (both coexist, dedup keeps the set clean).
- All `SetHX*` methods are nil-safe on `*RenderContext`. All header
  writes are nil-safe on `http.ResponseWriter`.

---

## 11. Quick reference cheat-sheet

```go
// Construct
reg := render.New(render.WithEngines(engine.NewJade(), engine.NewHTMLTemplate()))

// Render single view
reg.Render(rc, "jade", "name", data)

// Render layout + view
reg.RenderPage(rc, "layouts/app", "posts/show", data)

// Fluent builder (layout + view + parts) — engine is a typed EngineName
reg.Page(rc).Engine(render.EngineJade).Layout("layouts/app").View("posts/show").With(data).Render()

// Compose explicit parts
reg.RenderComposition(rc, render.Composition{Engine: "jade", Layout: "layouts/app", View: "posts/show"}, data)

// Preload at boot
reg.MustPreloadParts(rc, "jade", "layouts/app", "posts/show")

// Components
cr := render.NewComponentRegistry()
cr.Define("NAVBAR").WithFuncs(...).RenderChildren(func(c *render.ComponentContext) error {...}).Register(cr)
reg.AttachComponents(cr)

// Cache
cache := reg.Cache()
cache.Put("jade", "x", prog)
p, ok := cache.Get("jade", "x")
reg.SetTag("jade", "x", "content"); reg.InvalidateTag("content")

// Per-request FuncMap
rc.WithFuncMap(template.FuncMap{"shout": ...})

// Data injectors
reg.Inject(func(rc *render.RenderContext, data map[string]any) { data["user"] = ... })

// Features
reg.Compress(render.CompressConfig{Level: 6, MinSize: 1024})
reg.Conditional(true)
reg.Variant("locale", "en", "fr")
```

Happy generating.
