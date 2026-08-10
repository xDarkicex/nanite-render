# nanite-render — Design

A composition + cache layer for any Go template engine. Sits between
the engine and the router, handles per-part caching, per-request
`FuncMap`, layout composition, and lock-free lookup.

The core has **zero router dependencies**. Adapters live in sub-packages
(`nano/`, `chi/`, `stdlib/`, etc.).

---

## 1. What this is

nanite-render is *not* a parser. It's a hub:

```
       ┌────────────────────────────┐
       │  Your template engine
       │  (jade, templ, html/template, mustache, ...)
       └──────────────┬─────────────┘
                      │  Engine.Compile / Engine.Execute
                      ▼
       ┌────────────────────────────┐
       │  nanite-render core
       │  • Per-part cache (liteLRU)
       │  • Per-request FuncMap
       │  • Layout + view + partials composition
       │  • Pooled Writer / RenderContext
       └──────────────┬─────────────┘
                      │  router-agnostic RenderContext
                      ▼
       ┌────────────────────────────┐
       │  Router adapter
       │  (nano, chi, gin, stdlib http, ...)
       └────────────────────────────┘
```

Every engine adapter wraps an existing parser. The core does not know
HTML, jade, templ, or anything specific to a template format.

---

## 2. What this is not

- **Not a parser.** We removed the lexer/parser/optimizer/minifier
  pipeline because every template engine already has its own. Our
  defaults do not parse anything.
- **Not a router.** The core has no `nanite`, `chi`, `gin`, or
  `net/http` import. Adapters do.
- **Not engine-specific.** The same `Registry` accepts jade, templ,
  html/template, and any user-defined engine.

---

## 3. GO-Portfolio patterns we keep

From `helpers/render.go` and `helpers/cached.go`:

- **Per-part caching** — `Cache.Get("layout", fallback)` and
  `Cache.Get(view, fallback)` keep the layout and view independently
  cached. We generalise to **every cacheable part**: layout, view,
  partials, components. `Registry.Part()` and `Registry.RenderComposition()`
  expose this directly.
- **Per-render `funcMap`** — built fresh each render
  (`render.go:83-106`). We layer this on top of the engine's
  compile-time defaults via `rc.WithFuncMap()`.
- **Compile-on-demand with fallback** — fallback closure parses,
  minifies, and returns the cached object. The fallback runs inside
  the engine's `Compile`; the cache lookup happens in `Registry.loadPart`.
- **Request-time header set** — `Cache-Control`, `Connection`,
  `Transfer-Encoding` set before render (`render.go:29-33`). Adapters
  do this.
- **Best-effort timing** — `times["jade"]`, `times["render-page"]`.
  Replaced by `rc.WithDebug()` + the `Pipeline.Debug` hook.

What we replace:

- `html/template` parse-per-render → cached template, per-render FuncMap
  layered on top.
- `tdewolff/minify` post-parse → engine's own minify at compile time.
- Single-mutex `InMemoryCache` → `liteLRU`-backed lock-free cache.
- Hard-coded `jade` + `html/template` → `Engine` interface, pluggable.

---

## 4. Core surface

### 4.1 Types

```go
type Program struct {
    Engine string              // engine name
    Name   string              // template name
    Nodes  NodeStream          // optional; for engines that emit SoA IR
    Layout *Program            // optional layout program
    FuncMap map[string]any     // compile-time FuncMap defaults
    // engine-private
}

type Engine interface {
    Name() string
    Compile(src []byte, name string) (*Program, error)
    Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error
}

type RenderContext struct {
    Writer   ByteWriter
    Request  *http.Request
    Loader   SourceFunc
    FuncMap  template.FuncMap   // per-request; overrides Program.FuncMap
    Times    map[string]time.Duration  // opt-in via Debug
    Headers  http.Header
    Layout   string
}

type Composition struct {
    Engine string
    Layout string
    View   string
    Parts  []string
}
```

### 4.2 Registry

```go
reg := render.NewRegistry(engine.NewJade(), engine.NewHTML(nil))

// Per-part cache lookup with compile-on-miss.
prog, err := reg.Part(rc, "jade", "post")

// Single-part render.
err = reg.Render(rc, "jade", "post", data)

// Layout + view + partials composition.
err = reg.RenderComposition(rc, render.Composition{
    Engine: "jade",
    Layout: "layouts/application",
    View:   "post",
    Parts:  []string{"partials/nav"},
}, data)

// Pre-warm the cache at boot.
err = reg.PreloadParts(rc, "jade", "layouts/application", "post")
```

### 4.3 Cache

```go
cache := render.NewProgramCache(1024)
reg.SetCache(cache)
```

`ProgramCache` wraps `xDarkicex/liteLRU` (~30 ns lock-free lookup)
with a parallel `[]atomic.Pointer[Program]` slot table. The pointer
index lives in `liteLRU.Param.Value` so the cache stays zero-GC on
the hot path.

### 4.4 Per-request FuncMap

```go
rc.WithFuncMap(template.FuncMap{
    "currentUser": loadUser,
    "csrfToken":   issueToken,
})
```

Engines call `rc.EffectiveFuncMap(programDefaults)` to merge the
two layers (per-request wins).

### 4.5 Debug timing

```go
rc.WithDebug()  // populates rc.Times
p := render.NewPipeline().Debug(os.Stderr)
reg.SetPipeline(p)
```

Mirrors `GO-Portfolio`'s `times["jade"]` / `times["render-page"]`.

---

## 5. Engine adapters

### 5.1 `engine/jade`

Wraps `github.com/Joker/jade`. Parses at compile time, caches the
resulting HTML string. Render is a single byte write.

### 5.2 `engine/html`

Wraps `html/template`. Parses at compile time, caches the
`*template.Template`. Per-render `FuncMap` re-parses a fresh
template (cheap) so each request gets its own closures.

### 5.3 `engine/templ`

Wraps `github.com/a-h/templ`. `templ` files are compiled to Go by
`templ generate`; the adapter is a registry mapping template names
to `templ.Component` factories. No source parsing at runtime.

### 5.4 Custom engines

Implement the three-method `Engine` interface. The base packages
take no dependency on any specific engine.

---

## 6. Router adapters

Each adapter:

1. Acquires a `ByteWriter` from the pool, wrapping the response writer.
2. Acquires a `RenderContext` from the pool.
3. Stores it on the router's per-request context (under `render.ContextKey`).
4. On defer, flushes the writer, releases the context.

Provided:

- `nano/nano.go` — `nanite.MiddlewareFunc`
- `chi/chi.go` — `func(http.Handler) http.Handler` (TODO)
- `stdlib/stdlib.go` — `func(http.Handler) http.Handler` (TODO)

---

## 7. What we removed

Originally the scaffold had a custom lexer, parser, optimiser, and
minifier. These were removed because:

- Every template engine has its own parser.
- We've standardised on `Engine.Compile` for parse + minify.
- A custom IR walker would only benefit engines that don't have
  their own walker (templ, html/template). The SoA walker is in
  `execute.go` for those engines; engines with their own walker
  (templ, html/template) skip it.

The `Pipeline` is now a thin hook layer for users who want to inject
their own preprocessing (sanitisation, i18n, custom funcs). Default
behaviour is *no hooks*.

---

## 8. Performance expectations

| Stage | Goal | Why |
|---|---|---|
| Cache hit | ~30 ns | liteLRU + atomic.Pointer.Load |
| Cache miss | compile + put | Engine-specific |
| Single render (cache hit) | < 1 µs | liteLRU + walk + write |
| Streaming emit | ≥ 1 GB/s | Direct `[]byte` writes |
| Memory (per request) | 0 allocs | Pooled `RenderContext` + `ByteWriter` |

---

## 9. Open design questions

None at this time. The original feedback loop has closed.

## 10. Resolved design decisions

1. **Engine extensibility** — `Engine` interface, pluggable.
2. **Hot-reload** — yes, `Watcher` ships.
3. **Pipeline flexibility** — stages are hooks, not a parser.
4. **Nanite coupling** — decoupled core + thin adapter in `nano/`.
5. **Cache** — `liteLRU` (with a typed wrapper for `*Program`).
6. **Pipeline shape** — per-template-part, per-render `FuncMap`.
7. **Lexer** — removed from the core; engines parse themselves.
