# ⚡ nanite-render

### The lock-free, zero-alloc composition hub for Go server-side rendering

Compose any template engine with a hot render path that allocates **0 bytes** on cache hits. Layouts, components, slots, state, and per-request FuncMaps — all wired into a single 60ns hot loop.

---

## Badges

| | |
|---|---|
| **Allocations (hot path)** | `0 B/op` |
| **Cache hit** | ~60 ns |
| **Concurrency** | 100% lock-free (`atomic.Pointer` everywhere) |
| **Engine surface** | Jade · html/template · plain HTML · templ |
| **HTMX** | 100% first-class (all 12 response headers + OOB + 3 trigger lifecycles) |
| **License** | MIT |
| **Go** | 1.25+ |

---

## Hero

```
         ┌────────────────────────────┐
         │  Your template engine      │  Jade, html/template, templ,
         │  (jade, templ, html/tmpl)  │  plain HTML, or a custom Engine
         └──────────────┬─────────────┘
                        │  Engine.Compile / Engine.Execute
                        ▼
         ┌────────────────────────────┐
         │  nanite-render core        │
         │  • Lock-free SoA cache     │  ~60ns hit, 0 allocs
         │  • Per-request FuncMap     │  closures, not globals
         │  • Layout + view + slots   │  React-style composition
         │  • Components + State      │  UseState[T], children, slots
         │  • Pooled writer           │  zero-alloc byte streaming
         └──────────────┬─────────────┘
                        │  router-agnostic RenderContext
                        ▼
         ┌────────────────────────────┐
         │  Your router               │  nanite, chi, gin, stdlib http
         └────────────────────────────┘
```

The core has **zero router dependencies**. The `nano/` subpackage is the only code that imports `xDarkicex/nanite`.

---

## Why nanite-render

- **Not a parser.** Jade parses jade. templ parses templ. html/template parses html/template. nanite-render *composes around them* — it's the cache + composition + lifecycle layer that makes them all feel like one system.
- **Lock-free hot path.** Every read in the Registry goes through `atomic.Pointer`. No `sync.RWMutex` on any render path. This matters at the 190k req/sec that the nanite router targets.
- **Zero allocations on render.** The cache stores pre-compiled programs; the executor walks a Structure-of-Arrays node stream with no per-render allocation. `BenchmarkRegistry_RenderHotPath` reports **0 B/op**.
- **React-style composition in Go.** Components, children, named slots, per-render state (`UseState[T]`), and type-safe props (`BindProps[T]`).
- **Router-agnostic.** Use it with nanite, chi, gin, or plain `net/http`. The adapters are thin and live outside the core.
- **Inspired by the original GO-Portfolio pipeline** — per-part caching, per-render funcMap, layout+view composition — but rebuilt lock-free and zero-alloc.

---

## Benchmarks

Measured on Apple M2, Go 1.25, 3 KB HTML document (30 cards + nav + footer).

### Hot-path primitives

| Operation | ns/op | allocs | B/op |
|---|---|---|---|
| `Cache.Get` (lock-free, off-heap) | 11 | 0 | 0 |
| **SoA bytecode executor (our renderer)** | **85** | **0** | **0** |
| SoA tree walk (fallback) | 14,474 | 0 | 0 |

### End-to-end render (3 KB doc)

| Path | ns/op | allocs |
|---|---|---|
| **nanite bytecode executor (plain HTML)** | **85** | **0** |
| templ (direct) | 1,395 | 20 |
| templ (via nanite adapter) | 1,523 | 22 |
| SoA tree walk (fallback) | 14,474 | 0 |
| nanite cached (jade/HTMLTemplate) | 40,088 | 567* |
| html/template compiled | 40,076 | 567* |
| raw re-parse every render (GO-Portfolio style) | 148,600 | 1,967 |

\* html/template's reflection + re-escaping — identical to the raw compiled baseline, not nanite-render's overhead.

The bytecode executor compiles the template once into a flat instruction
stream: all static HTML (tags, attrs, text, escaping) is coalesced into a
single output buffer at compile time — exactly what templ's compiler does —
so a static-heavy template renders as one write. It is **16× faster than
templ** on the same document (85 ns vs 1.4 µs) because templ still writes
piece-by-piece while we merge the whole static region.

### Cache latency savings (p50/p99/p99.9, 20k samples)

| Path | p50 | p99 | p99.9 |
|---|---|---|---|
| **nanite hot (cached)** | **66.8 µs** | **104.7 µs** | **253.8 µs** |
| nanite cold (compile) | 249.3 µs | 546.3 µs | 745.6 µs |
| raw html/template re-parse | 241.9 µs | 616.5 µs | 1,367.8 µs |
| raw html/template compiled | 71.1 µs | 235.5 µs | 393.5 µs |

**The cache saves 182.5 µs at p50 — 3.7× faster** than re-rendering, and tames the p99.9 tail from 1.37 ms (re-parse) to 254 µs (cached).

### Compile-time (one-time, cached forever)

| Operation | ns/op | allocs |
|---|---|---|
| Compile (parse → program) | 80,035 | 1,043 |
| Minify | 65,207 | 31 |

### Minification data savings

```
raw bytes:        6,068
minified bytes:   3,938   (35.1% saved)
```

### The templ story

templ is the fastest engine (~2.4 µs, 20 allocs) because it compiles templates to Go — no reflection. nanite-render's own SoA executor is **0 allocs** but a generic tree walker (23.5 µs). html/template is the alloc-heavy baseline (567 allocs) — its reflection + re-escaping is inherent to its design. Pick whichever engine; the composition layer (cache, state, slots, components, memoization) is identical on top.

Run them yourself:

```
go test ./bench/ -bench=. -benchmem -benchtime=1s
go test ./bench/ -run TestLatency -v
go test ./bench/ -run TestMinify -v
```

---

## Used by xDarkicex

- **[xDarkicex/nanite](https://github.com/xDarkicex/nanite)** — the high-performance HTTP router this renders for.
- **[xDarkicex/GO-Portfolio](https://github.com/xDarkicex/GO-Portfolio)** — the original render pipeline whose shape inspired this one.
- **[xDarkicex/memory](https://github.com/xDarkicex/memory)** — off-heap mmap allocator backing the cache.
- **[xDarkicex/liteLRU](https://github.com/xDarkicex/liteLRU)** — lock-free LRU reference design.
- **[xDarkicex/lexer](https://github.com/xDarkicex/lexer)** — SWAR lexer primitives (pattern reference).

---

## Quick start

```go
package main

import (
    "net/http"

    "github.com/xDarkicex/nanite-render"
    "github.com/xDarkicex/nanite-render/engine"
)

func main() {
    // Compose engines into one registry.
    reg := render.New(
        render.WithEngines(
            engine.NewJade(),
            engine.NewHTMLTemplate(),
            engine.NewHTML(),   // plain HTML, no data binding
        ),
        render.WithDefaultLoader(render.NewFileLoader("./views", ".jade")),
        render.WithCacheSize(2048),
    )

    // Works with plain net/http — no router needed.
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        bw := render.AcquireWriter(w)
        defer render.ReleaseWriter(bw)
        rc := render.AcquireContext(bw, r)
        defer render.ReleaseContext(rc)
        rc.Loader = reg.DefaultLoader()

        if err := reg.Render(rc, "jade", "index", map[string]any{"Title": "Hello"}); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
        }
    })

    http.ListenAndServe(":8080", nil)
}
```

---

## Router integrations

### xDarkicex/nanite (the fast one)

The `nano` adapter is a **direct-call helper** — no middleware indirection. Handlers call `nano.Render` at the end, exactly like the original GO-Portfolio pattern.

```go
import (
    "github.com/xDarkicex/nanite"
    "github.com/xDarkicex/nanite-render/nano"
    "github.com/xDarkicex/nanite-render"
    "github.com/xDarkicex/nanite-render/engine"
)

func main() {
    reg := render.New(
        render.WithEngines(engine.NewJade(), engine.NewHTMLTemplate()),
        render.WithDefaultLoader(render.NewFileLoader("./views", ".jade")),
        render.WithCacheSize(2048),
        render.WithConditional(),   // ETag / 304
        render.WithCompression(6, 1024),
    )

    r := nanite.New()
    r.Get("/posts/:slug", func(c *nanite.Context) {
        post := loadPost(c.Param("slug"))
        // Direct call, no middleware, no setup.
        if err := nano.Render(c, reg, "jade", "posts/show", post); err != nil {
            c.Error(http.StatusInternalServerError, err)
        }
    })
    r.Start(":8080")
}
```

**Fluent variant** — compose layout + view + parts with a chainable builder:

```go
r.Get("/posts/:slug", func(c *nanite.Context) {
    post := loadPost(c.Param("slug"))
    if err := nano.Page(reg, c).
        Engine(render.EngineJade).
        Layout("layouts/app").
        View("posts/show").
        With(post).
        Render(); err != nil {
        c.Error(http.StatusInternalServerError, err)
    }
})
```

**Or the direct-call** — one line, no builder:

```go
r.Get("/posts/:slug", func(c *nanite.Context) {
    post := loadPost(c.Param("slug"))
    if err := nano.RenderPage(c, reg, "layouts/app", "posts/show", post); err != nil {
        c.Error(http.StatusInternalServerError, err)
    }
})
```

Both acquire a pooled `RenderContext` and `ByteWriter`, run the engine, and release them — the fluent builder's `Render()` releases automatically via its `WithDone` hook. No middleware to mount, nothing to configure per-request.

The same fluent builder works in the core without nanite:

```go
reg.Page(rc).
    Engine(render.EngineJade).          // typed constant, or:
    EngineInstance(engine.NewJade()).   // a live engine value
    Layout("layouts/app").
    View("posts/show").
    Parts("partials/nav", "partials/footer").
    With(post).
    RenderComposition()   // or .Render() for layout+view only
```

Engine names are a distinct type — `Engine("jad")` is a compile error.
Use the constants (`EngineJade`, `EngineHTML`, `EngineHTMLTemplate`,
`EngineTempl`) or `render.CustomEngine("my-engine")` for a user-defined
engine.

### chi / gin / stdlib http

The core is router-agnostic. Any handler that can produce an `http.ResponseWriter` and `*http.Request` works:

```go
func renderHandler(w http.ResponseWriter, r *http.Request) {
    bw := render.AcquireWriter(w)
    defer render.ReleaseWriter(bw)
    rc := render.AcquireContext(bw, r)
    defer render.ReleaseContext(rc)
    rc.Loader = reg.DefaultLoader()

    err := reg.Render(rc, "jade", "index", data)
    if errors.Is(err, render.ErrTemplateNotFound) {
        http.Error(w, "not found", http.StatusNotFound)
        return
    }
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

// chi
r := chi.NewRouter()
r.Get("/", renderHandler)

// gin
g := gin.Default()
g.GET("/", func(c *gin.Context) { renderHandler(c.Writer, c.Request) })
```

The `RenderContext` is the only thing that crosses the router boundary — it's a plain struct holding a writer, a loader, and per-request config. No router types leak into the core.

---

## The cache

The `Cache` is an **off-heap, lock-free, cache-line-padded** program store. It's the heart of the hot path.

```go
// Get a cache; the Registry lazily creates one if not provided.
cache := reg.Cache()

// Per-part caching — layout, view, and partials are separate entries.
cache.Put("jade", "layouts/app", layoutProgram)
cache.Put("jade", "posts/show", viewProgram)

p, ok := cache.Get("jade", "posts/show")  // ~50ns, 0 allocs
```

- **Keys are `(engine, name)` pairs.** No string concat on the hot path — the FNV-1a hash composes both directly.
- **Off-heap slots** via `xDarkicex/memory.MmapAnonymous`. The GC never scans the slot array.
- **Cache-line padded slots.** One cache line per slot on x86 (128B on Apple Silicon), so concurrent slots don't false-share.
- **Lock-free.** Get is a seqlock read: load seq, load payload, load seq again, verify. Put is a CAS-loop.
- **Tag-based invalidation.** Group entries and drop them together.

```go
reg.SetTag("jade", "layouts/app", "chrome")   // tag this entry
reg.SetTag("jade", "posts/show", "content")
n := reg.InvalidateTag("chrome")              // drop all chrome-tagged entries
```

Cache-level stats are exposed:

```go
stats := cache.Stats()  // CacheStats{Hits, Misses, Evicts, Capacity}
```

---

## Custom FuncMaps (per-request)

The original GO-Portfolio built a fresh FuncMap per render — closures capturing the request handler's locals. nanite-render does the same, but makes it declarative.

**Register a factory:**

```go
reg.FuncMap(func(rc *render.RenderContext) template.FuncMap {
    return template.FuncMap{
        "currentUser": func() *User { return userFromContext(rc) },
        "csrfToken":   func() string { return issueToken(rc) },
        "shout":       func(s string) string { return strings.ToUpper(s) + "!" },
    }
})
```

The factory runs on every render and the result is layered on top of engine defaults. Per-request values win.

**Or set directly on a specific render context:**

```go
rc.WithFuncMap(template.FuncMap{
    "currentUser": func() *User { return c.Params... },
})
```

**Data injectors** run before the FuncMap and mutate the data map with whatever keys you want:

```go
reg.Inject(func(rc *render.RenderContext, data map[string]any) {
    data["current_user"] = loadUser(rc.Request)
    data["flashes"]      = loadFlashes(rc.Request)
    data["theme"]        = themeFromCookie(rc.Request)
})
```

No hardcoded keys — inject anything.

---

## React-style components

nanite-render gives you a React-flavoured composition model in Go — components, typed props, hooks, context, error boundaries, and Suspense, all server-side, all zero-alloc where it matters.

| React concept | nanite-render equivalent |
|---|---|
| JSX component | `cr.Define("NAME").Render(fn).Register(cr)` — the [fluent builder](#the-fluent-builder) |
| Props | `c.Data` + [`render.BindProps[T]`](#type-safe-props) — typed, zero-alloc |
| `props.children` | [`c.WriteChildren()`](#named-slots) + named slots `c.WriteSlot("name")` |
| `useState` / state hooks | [`c.UseState(key, initial)` / `render.UseState[T]`](#per-component-state-hooks) |
| Context (`createContext` / `useContext`) | [`c.ProvideContext` / `c.UseContext`](#cascading-context-react-style-zero-alloc) — zero-alloc stack, auto-scoped |
| Error Boundaries (`componentDidCatch`) | [`cr.Define(...).ErrorBoundary(fn)`](#error-boundaries-panic-isolation) |
| `<Suspense>` / `fallback` | [`cr.Define(...).Async().Fallback(fn)`](#server-side-suspense-async--fallback) — server-side, streams via HTMX OOB |
| `React.memo` | [`cr.Memoize(name, keyer)`](#per-component-memoization) |
| `createPortal` (render elsewhere) | [`WithOOB(id)`](#out-of-band-swaps-hx-swap-oobtrue) — HTMX out-of-band swaps |
| Inline composition (`<Navbar/>`) | [`c.Render("Navbar", props)`](#the-fluent-builder) |

The same component runs identically from **templ**, **html/template**, **jade**, and **plain HTML** — the React layer is engine-agnostic.

### The fluent builder

```go
cr := render.NewComponentRegistry()

cr.Define("NAVBAR").
    WithFuncs(template.FuncMap{
        "currentUser": func() *User { return loadUser() },
    }).
    RenderChildren(func(c *render.ComponentContext) error {
        c.WriteString("<nav class='navbar'>")
        c.WriteChildren()
        c.WriteString("</nav>")
        return nil
    }).
    Register(cr)

reg.AttachComponents(cr)
```

Then use it in any template:

```jade
html
  body
    NAVBAR
      ul
        li Home
        li About
```

### Named slots

```go
cr.Define("CARD").
    Render(func(c *render.ComponentContext) error {
        c.WriteString(`<div class="card">`)
        c.WriteString(`<div class="header">`); c.WriteSlot("header"); c.WriteString(`</div>`)
        c.WriteString(`<div class="body">`);   c.WriteSlot("body");   c.WriteString(`</div>`)
        c.WriteString(`<div class="footer">`); c.WriteSlot("footer"); c.WriteString(`</div>`)
        c.WriteString(`</div>`)
        return nil
    }).
    Register(cr)
```

```jade
CARD
  header
    h1 Welcome
  body
    p This is the body
  footer
    button Submit
```

### Per-component state (hooks)

```go
cr.Define("COUNTER").
    Render(func(c *render.ComponentContext) error {
        count, setCount := render.UseState(c.State, "count", 0)
        setCount(count + 1)
        _, err := c.WriteString("<span>" + strconv.Itoa(count) + "</span>")
        return err
    }).
    Register(cr)
```

State is per-render and shared across components. `UseState[T]` is generic — no type assertions.

The React layer is **first-class on `ComponentContext`** — state, typed props, and inline composition, uniformly across every engine:

```go
cr.Define("CARD").
    Render(func(c *render.ComponentContext) error {
        val, setVal := c.UseState("key", 0)     // React-style hook
        setVal(val.(int) + 1)
        props := render.Props[CardProps](c)      // typed props (nanite:"key" tags)
        c.WriteSlot("header")                    // named slots
        c.WriteChildren()                        // slot-less children
        return c.Render("Navbar", props)         // compose another component inline
    }).
    Register(cr)
```

The same component runs identically from **templ** (`render.RenderComponent(ctx, w, "CARD", props)`), **html/template** (`{{ component "CARD" . }}`), and **plain HTML** (`<CARD/>`).

### Type-safe props

```go
type NavbarProps struct {
    Theme string `nanite:"theme"`
    Size  int    `nanite:"size"`
}

cr.Define("NAVBAR").
    Render(func(c *render.ComponentContext) error {
        props := render.BindProps[NavbarProps](c.Data)
        // props.Theme, props.Size are strongly typed
        return nil
    }).
    Register(cr)
```

**Zero allocations.** `BindProps[T]` binds `nanite:"key"` tags from
a `map[string]any` into a typed struct with **0 B/op, 0 allocs** on
the scalar fast path:

- Per-type field layouts (byte offsets + exact types + resolved
  tag keys) are built once per distinct `T` via reflection and
  cached; the hot path never touches reflection.
- Scalar fields (string, bool, int*, uint*, float*) are written
  directly via `unsafe.Pointer` arithmetic — typed stores at
  computed struct offsets.
- The props struct stays on the stack (the runtime's `noescape`
  intrinsic defeats escape analysis' conservative handling of
  `unsafe.Pointer` args).
- Custom named types (`type UserID string`) and complex fields
  (slices, maps, pointers, nested structs) fall through to a
  reflect-based slow path that respects assignability.

Measured (Apple M2, scalar 4-field struct): **137 ns/op, 0 B/op**,
down from 246 ns/op and 48 B/op for the previous reflect
implementation.

---

## Superpowers

Every engine gets the framework's cross-cutting superpowers automatically:

| Superpower | Template syntax | What it does |
|---|---|---|
| `component` | `{{ component "NAVBAR" . }}` | Renders a registered component inline |
| `yield` | `{{ yield }}` | Injects the pre-rendered view body (layout composition) |
| `useState` | `{{ useState "count" 0 }}` | React-style per-render state |
| `get` / `set` | `{{ get "count" }}` / `{{ set "count" 5 }}` | State access |
| `<SLOW_DATA/>` with `Async()` + `Fallback()` | Server-side Suspense — fallback hits the wire immediately, real output streams as an HTMX OOB chunk when the worker finishes | Turns `Sum(component_times)` into `Max(component_times)` |
| `ErrorBoundary(fn)` on a `Definition` | Isolates panics in the component's render — boundary runs against a fresh context, the failed component's bytes are discarded, the rest of the page continues | One broken widget never kills the whole request |
| `ProvideContext(key, val)` / `UseContext(key)` on `ComponentContext` | Push/pop a value onto a fixed-size stack on the request context; reverse-scan lookup; auto-pop on scope exit; zero heap allocations | Cascading state without prop-drilling, with template-engine reach via `useContext` |

The `component` and `yield` superpowers are injected into the FuncMap of any engine that compiles via html/template (jade, html-template). The plain HTML engine uses the SoA executor's native `<COMPONENT/>` dispatch instead.

Layout composition is seamless:

```jade
// layouts/app.jade
html
  head
    title= .Title
  body
    NAVBAR
    {{ yield }}
    FOOTER

// posts/show.jade
h1= .Post.Title
p= .Post.Body
```

```go
err := reg.RenderPage(rc, "layouts/app", "posts/show", data)
```

### templ-native layout composition

templ composes natively — a layout wraps a view component (`Layout(view)`).
Register both and `RenderPageWith` routes to templ's own composition:

```go
eng.Register("view", func(data any) templ.Component { return View(data) })
eng.RegisterLayout("layouts/app", func(child templ.Component) templ.Component {
    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
        io.WriteString(w, "<html><body>")
        child.Render(ctx, w)          // the view, injected natively
        io.WriteString(w, "</body></html>")
        return nil
    })
})

err := reg.RenderPageWith(rc, render.EngineTempl, "layouts/app", "view", data)
```

The layout and view both receive the framework context, so state, slots, and
superpowers work inside templ too (`rc := render.FromContext(ctx)`).

### Mixed-engine composition (the GO-Portfolio pattern)

The handler names the view; the layout maps it in; partials are components.
`RenderPageEngines` composes a layout and view rendered by **different**
engines — the layout as plain HTML (compiled to bytecode, ~85 ns) with a
`<YIELD/>` view slot, the view as templ (or jade, or html-template):

```go
// Layout file (plain HTML → bytecode):
//   <html><body><NAVBAR/><YIELD/><FOOTER/></body></html>
//
// View: a templ component.
eng.Register("posts/show", func(data any) templ.Component { return View(data) })

// Partials: React-style components (memoizable).
cr.Define("NAVBAR").Render(func(c *render.ComponentContext) error { ... }).Register(cr)
cr.Memoize("NAVBAR", func(rc *render.RenderContext, data any) string { return "static" })

// The handler call — name the view, the layout wraps it:
err := reg.RenderPageEngines(rc,
    render.EngineHTML,  "layouts/app",   // bytecode layout
    render.EngineTempl, "posts/show",    // templ view
    data)
```

Output: `<html><body><nav ...>nav</nav><h1>the post</h1><footer>footer</footer></body></html>`.
The view is rendered to a buffer, the layout's `<YIELD/>` injects it, and the
partials dispatch through the ComponentRegistry — with state, slots, props,
and memoization available to every part.

### Per-component memoization

GO-Portfolio cached each part; we apply the same idea per component.
`cr.Memoize` caches a component's rendered HTML by key:

```go
// Data-independent → render once, cache forever.
cr.Memoize("Navbar", func(rc *render.RenderContext, data any) string { return "static" })

// Keyed by data → cache per ID.
cr.Memoize("UserCard", func(rc *render.RenderContext, data any) string {
    return data.(UserCardProps).ID
})
```

Repeat keys serve the cached HTML directly — no re-render.

---

## Server-side `<Suspense>` (Async / Fallback)

Slow components don't block the rest of the page. Opt in per-component with `Async()` + `Fallback()`:

```go
cr.Define("USER_PROFILE").
    Async().
    Fallback(func(c *render.ComponentContext) error {
        // Skeleton hits the wire immediately (TTFB ≈ 0ms).
        return c.WriteString(`<div id="profile" class="skeleton">Loading…</div>`)
    }).
    Render(func(c *render.ComponentContext) error {
        // Expensive query / IO — runs in a worker goroutine.
        user := db.LoadUser(c.Data.(ProfileID))
        return c.WriteString(fmt.Sprintf(`<div id="profile">%s</div>`, user.Name))
    }).
    Register(cr)
```

When the executor hits `USER_PROFILE`:

1. **Inline:** the Fallback output is written and flushed to the response immediately. The browser paints the skeleton.
2. **Background:** a worker goroutine runs the render to a pooled buffer.
3. **Streaming:** when the worker finishes, the buffered bytes are wrapped in `<div id="user_profile" hx-swap-oob="true">…</div>` and emitted as a trailing chunk.

The win: `Sum(component_times)` becomes `Max(component_times)` for independent components. Cancellation is wired through `rc.Request.Context()` — a client disconnect aborts the worker and releases its buffer.

The coordinator is **lazy** — zero channels, zero goroutines, zero allocations on the non-async path. Use `rc.Suspense()` to inspect whether suspense is in flight.

Call `rc.CloseSuspense()` after the render walk completes (typically in a `defer` in the handler) so the trailing OOB chunks flush before the response closes.

---

## Error Boundaries (panic isolation)

A panic deep inside a component used to crash the whole request. With `ErrorBoundary()`, only the failed component is replaced — the rest of the page renders normally.

```go
cr.Define("DASHBOARD_WIDGET").
    ErrorBoundary(func(c *render.ComponentContext, err any) error {
        // Safe to log the raw panic value (it's the unfiltered
        // value passed to panic()). Be careful NOT to echo it
        // directly into the response — internal errors may
        // contain credentials, stack traces, etc.
        log.Printf("widget failed: %v", err)
        return c.WriteString(`<div class="error">Widget unavailable</div>`)
    }).
    Render(func(c *render.ComponentContext) error {
        // If this panics, only THIS component fails. The rest of
        // the page keeps rendering.
        panic("database timeout")
    })
```

**How it works:** the component's render runs into an isolated buffer (same pattern as `WithOOB`). On panic, the buffer is discarded, the boundary is invoked with a fresh context pointing at the live response, and the boundary's writes replace the failed component in the page. Zero overhead for components without a boundary — no `defer/recover` is set up unless one is registered.

**Async + ErrorBoundary:** workers that panic invoke their boundary too. The boundary's output replaces the expected OOB chunk, so the client sees a consistent error shape (`<div id="..." hx-swap-oob="true">...error...</div>`). A panic in the boundary itself falls back to a generic `<!-- error boundary failed -->` placeholder so the page never crashes.

**Nested safety:** the boundary call is itself wrapped in `defer/recover`. If the boundary panics or returns an error, the generic placeholder is emitted and rendering continues.

**Without a boundary:** a panic re-panics out of the component (sync) or silently drops the OOB chunk (async). Use boundaries when you want graceful degradation; rely on re-panic when you want loud failures during development.

---

## Cascading Context (React-style, zero-alloc)

Prop-drilling theme/user/locale/CSRF-token through 15 layers is painful. `ProvideContext` + `UseContext` solves it without heap allocations:

```go
cr.Define("LAYOUT").
    Render(func(c *render.ComponentContext) error {
        c.ProvideContext("theme", "dark")
        c.ProvideContext("user", currentUser)
        return c.RenderChildren(/* ... */)
    })

cr.Define("DEEP_BUTTON").
    Render(func(c *render.ComponentContext) error {
        theme := c.UseContext("theme").(string)  // "dark"
        return c.WriteString(`<button class="` + theme + `">...</button>`)
    })
```

**How it works:** a fixed-size `[32]contextKV` array lives directly on `*RenderContext`. The first 32 nested bindings land in this inline array — `PushContext` is a pure memory move (zero alloc). If a render tree exceeds 32 nested bindings (unusual — admin UIs with deeply nested layouts), pushes spill into a heap slice so the framework never panics.

**Scope isolation:** the dispatcher pops the stack back to the saved depth when each component's render returns. Bindings made inside `LAYOUT.Render` are visible to `DEEP_BUTTON` (nested), but NOT to a sibling component rendered after `LAYOUT` finishes.

**Templates too:** the default FuncMap already includes `useState`, `get`, `set`, and `useContext` — batteries included. Templates call `{{ useContext "theme" }}` directly, no opt-in.

**Async workers** get a fresh `*RenderContext` (their own empty stack), so they don't inherit cascading state from the inline render. Provide what the worker needs in its `Fallback` or pass via props.

---

## HTMX — first-class support (100% of the 2.0 spec)

nanite-render has full, native support for the HTMX 2.0 request/response
contract — request detection, OOB swaps, every trigger lifecycle, every
server-driven response header. The router remains in charge of the wire;
the renderer is purely about producing the right HTMX-aware bytes and
headers for any given request.

### Direct component dispatch (`hx-get` / `hx-post`)

Render one named component to a `ByteWriter` without a parent template or
layout — the handler for `hx-get="/posts/123/card"`:

```go
err := reg.RenderComponent(bw, rc, "CARD", props)
```

Missing name is a no-op (no error, no output) — stale HTMX targets don't
crash the handler.

### Out-of-Band swaps (`hx-swap-oob="true"`)

Opt a component in with an explicit target DOM id. When the component's
render mutates state via `SetState`/`UseState`, its output is wrapped in
`<div id="<id>" hx-swap-oob="true">…</div>` and emitted automatically.
Clean renders write through unchanged — no buffer copy on the no-op path:

```go
cr.Define("CARD").
    WithOOB("card-slot").                            // required target id
    Render(func(c *render.ComponentContext) error {
        _, set := c.UseState("count", 0)
        set(c.GetState("count").(int) + 1)          // marks frame dirty
        return c.WriteString("<div class='card'>…</div>")
    }).
    Register(cr)
```

The render buffer comes from a `sync.Pool` of `bytes.Buffer` — zero
steady-state allocations even on the dirty path. The wrapper is HTML-escaped.

`rc.SetOOBSink(w)` redirects the OOB output to a separate writer when the
router wants to interleave OOB swaps with a different stream.

### HTMX request detection

Pure helpers over `*http.Request`, nil-safe:

```go
render.IsHTMXRequest(r)              // canonical: HX-Request: true
render.IsHTMXBoosted(r)              // HX-Boosted
render.IsHTMXHistoryRestore(r)       // HX-History-Restore-Request
render.HXTargetID(r)                 // id of the targeted element
render.HXTriggerID(r)                // id of the triggered element
render.TriggerName(r)                // name attribute of the trigger
render.CurrentURL(r)                 // URL of the page that initiated the request
render.HXPromptResponse(r)           // user's answer to an hx-prompt dialog
```

A typical handler branches on these:

```go
if render.IsHTMXHistoryRestore(r) {
    // Serve the full page; HTMX is rebuilding its history snapshot.
    reg.RenderPage(rc, "layout", "view", data)
} else if render.IsHTMXRequest(r) {
    // Targeted swap: render one component, then write HTMX headers.
    reg.RenderComponent(bw, rc, "CARD", props)
} else {
    // Full page load.
    reg.RenderPage(rc, "layout", "view", data)
}
```

### Trigger events (`HX-Trigger`, `HX-Trigger-After-Swap`, `HX-Trigger-After-Settle`)

Components accumulate event names during their render. The router writes
the joined set into the response headers — but `rc.WriteHTMXHeaders(w)`
does it in one call:

```go
// In the component:
c.AddHXTrigger("card-updated")
c.AddHXTriggerWithDetail("item-changed", map[string]any{"id": 42})
c.AddHXTriggerAfterSwap("settled")
c.AddHXTriggerAfterSettle("done")

// In the handler:
rc.WriteHTMXHeaders(w)
```

`AddHXTriggerWithDetail` upgrades an entry to carry JSON detail. The
format is automatic: plain comma-separated names when no entry has detail,
JSON object otherwise — matching HTMX's wire format:

```
HX-Trigger: card-updated,nav-refresh                          # plain
HX-Trigger: {"item-changed":{"id":42},"card-updated":null}   # JSON detail
```

### Server-driven response decisions

Every `HX-*` decision header is one setter on `RenderContext`. Apply them
all in one call at the end of the handler:

```go
rc.SetHXRetarget("#main-panel")
rc.SetHXReswap("outerHTML swap:200ms")
rc.SetHXPushURL("true")             // or a URL
rc.SetHXRedirect("/done")           // full URL required
rc.SetHXRefresh()                   // full client refresh
rc.SetHXReplaceURL("/current")      // or "true"
rc.SetHXLocation("/alt")            // navigation without history
rc.SetHXReselect("#content")        // CSS selector for response extraction
rc.SetHXResettle("true")            // "true" forces scroll reset

rc.WriteHTMXHeaders(w)              // emits only the headers actually set
```

Every header is covered:

| Header | Setter | Constant |
|---|---|---|
| `HX-Trigger` | `AddHXTrigger` / `AddHXTriggerWithDetail` | `render.HXTriggerHeader` |
| `HX-Trigger-After-Swap` | `AddHXTriggerAfterSwap` | `render.HXTriggerAfterSwap` |
| `HX-Trigger-After-Settle` | `AddHXTriggerAfterSettle` | `render.HXTriggerAfterSettle` |
| `HX-Retarget` | `SetHXRetarget` | `render.HXRetarget` |
| `HX-Reswap` | `SetHXReswap` | `render.HXReswapHeader` |
| `HX-Push-Url` | `SetHXPushURL` | `render.HXPushURL` |
| `HX-Redirect` | `SetHXRedirect` | `render.HXRedirect` |
| `HX-Refresh` | `SetHXRefresh` | `render.HXRefresh` |
| `HX-Replace-Url` | `SetHXReplaceURL` | `render.HXReplaceURL` |
| `HX-Location` | `SetHXLocation` | `render.HXLocation` |
| `HX-Reselect` | `SetHXReselect` | `render.HXReselect` |
| `HX-Resettle` | `SetHXResettle` | `render.HXResettle` |

### Auto-OOB → auto-trigger

When a `WithOOB` component's render dirties state, the lowercased
component name is **automatically added** to `HX-Trigger`. Components
that want different event names call `AddHXTrigger` explicitly — both
coexist (dedup keeps the set clean):

```go
// CARD with WithOOB("card-slot") that mutates state →
// HX-Trigger: card     (auto, from name)
// HX-Trigger: custom-event  (if c.AddHXTrigger("custom-event") also called)
```

### Complete handler pattern

The full router integration in one function:

```go
func PostHandler(w http.ResponseWriter, r *http.Request) {
    bw := render.AcquireWriter(w)
    defer render.ReleaseWriter(bw)
    rc := render.AcquireContext(bw, r)
    defer render.ReleaseContext(rc)

    if render.IsHTMXHistoryRestore(r) {
        reg.RenderPage(rc, "layout", "view", nil)
    } else if render.IsHTMXRequest(r) {
        reg.RenderComponent(bw, rc, "CARD", postData)
        // Component already added "card" to rc.HXTriggers via WithOOB.
        rc.SetHXRetarget("#post-feed")
        rc.SetHXReswap("outerHTML settle:200ms")
    } else {
        reg.RenderPage(rc, "layout", "view", postData)
    }

    // Writes HX-Trigger, HX-Retarget, HX-Reswap — only the headers
    // actually set during the handler.
    rc.WriteHTMXHeaders(w)
}
```

One call to `WriteHTMXHeaders` writes all accumulated HTMX decisions;
the router stays in charge of the wire and lifecycle.

---

## Sentinel errors

Routers translate errors to HTTP responses with `errors.Is`:

```go
var (
    ErrEngineNotFound    // unknown engine → 500
    ErrTemplateNotFound  // missing template → 404
    ErrLoaderMissing     // no loader on context → 500
    ErrLayoutMissing     // required layout absent → 500
    ErrRenderPageInvalid // invalid RenderPage args → 500
)
```

---

## License

MIT — see [LICENSE](LICENSE). Copyright (c) 2026 xDarkicex.
