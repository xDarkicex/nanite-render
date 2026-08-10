# nanite-render — Public API Reference

This is the complete reference for every exported symbol in `nanite-render`.
Symbols are grouped by concern. Anything not listed here is internal.

Package: `github.com/xDarkicex/nanite-render`
Sub-packages: `.../engine`, `.../nano`

---

## EngineName — typed engine identifiers

```go
type EngineName string

const (
    EngineJade         EngineName = "jade"
    EngineHTML         EngineName = "html"
    EngineHTMLTemplate EngineName = "html-template"
    EngineTempl        EngineName = "templ"
)

func CustomEngine(name string) EngineName // user-defined engine name
func (n EngineName) String() string        // raw name
```

`EngineName` is a distinct type, so `Engine("jad")` is a compile error;
use the constants or `CustomEngine(name)`. It's a string underneath —
the same value used for cache keys and the `byName` lookup.

---

## Sentinel errors

```go
var (
    ErrEngineNotFound    error  // render: engine not found
    ErrTemplateNotFound  error  // render: template not found
    ErrLoaderMissing     error  // render: no loader on context
    ErrLayoutMissing     error  // render: layout missing
    ErrRenderPageInvalid error  // render: invalid render page
)
```

Match with `errors.Is(err, render.ErrTemplateNotFound)` to map to 404/500.

---

## Core types

### `Program`

A compiled, cacheable template. Immutable after `Compile` returns.

```go
type Program struct {
    Engine     string        // engine name
    Name       string        // template identifier
    Nodes      NodeStream    // SoA IR (plain HTML engine)
    Layout     *Program      // optional layout program
    FuncMap    map[string]any // compile-time FuncMap defaults
    ETag       string        // strong ETag, set at compile time
    Tags       []string      // cache invalidation tags
    EngineData any           // engine's compiled rep (e.g. *template.Template)
    scratch    []byte        // engine-private (unexported)
}
```

### `Engine`

The contract every template engine implements.

```go
type Engine interface {
    Name() string
    Compile(src []byte, name string) (*Program, error)
    Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error
}
```

### `Registry`

The user-facing façade. Holds engines, the shared cache, and per-request
features. **Fully lock-free** — all fields accessed via `atomic.Pointer`.

```go
type Registry struct { /* ... */ }
```

Construct with `New(...)` (options) or `NewRegistry(engines...)`.

### `RenderContext`

Per-request render state. Pooled — use `AcquireContext` / `ReleaseContext`.

```go
type RenderContext struct {
    Writer           ByteWriter
    Request          *http.Request
    Loader           SourceFunc
    FuncMap          template.FuncMap   // per-request override
    Times            map[string]time.Duration // opt-in debug timing
    Headers          http.Header
    Layout           string
    Variants         map[string]string
    ViewBytes        []byte             // pre-rendered view for yield()
    ComponentFuncMap template.FuncMap   // per-component FuncMap
    state            *State             // per-render state
    userData         any                // injector side-channel
    componentRegistry *ComponentRegistry
}
```

### `Composition`

Describes a render that composes layout + view + partials.

```go
type Composition struct {
    Engine string
    Layout string
    View   string
    Parts  []string
}
```

### `SourceFunc`

```go
type SourceFunc func(name string) ([]byte, error)
```

### `Option`

Functional-option type for `New`.

```go
type Option func(*Registry)
```

---

## Registry construction & options

### Constructors

```go
func New(opts ...Option) *Registry
func NewRegistry(engines ...Engine) *Registry
```

### Options

```go
func WithEngine(e Engine) Option
func WithEngines(engines ...Engine) Option
func WithCache(c *Cache) Option
func WithCacheSize(n int) Option
func WithCompression(level, minSize int) Option
func WithConditional() Option
func WithVariant(name string, values ...string) Option
func WithInjector(fn DataInjector) Option
func WithFuncMap(fn FuncMapBuilder) Option
func WithDefaultLoader(loader SourceFunc) Option
func WithPipeline(p *Pipeline) Option
func WithDebug() Option
```

---

## Registry methods

### Engine management

```go
func (r *Registry) AddEngine(e Engine) *Registry  // chainable
func (r *Registry) Engines() []Engine
func (r *Registry) Engine(name string) Engine
func (r *Registry) Default() Engine
func (r *Registry) Define(name string) *Definition  // fluent component builder
```

### Cache

```go
func (r *Registry) Cache() *Cache
func (r *Registry) SetCache(c *Cache)
func (r *Registry) SetTag(engine, name string, tags ...string)
func (r *Registry) InvalidateTag(tag string) int
```

### Loader

```go
func (r *Registry) DefaultLoader() SourceFunc
func (r *Registry) SetDefaultLoader(loader SourceFunc)
```

### Parts (per-part caching)

```go
func (r *Registry) Part(rc *RenderContext, engine, name string) (*Program, error)
func (r *Registry) PreloadParts(rc *RenderContext, engine string, parts ...string) error
func (r *Registry) MustPreloadParts(rc *RenderContext, engine string, parts ...string) // panics
```

### Render

```go
func (r *Registry) Render(rc *RenderContext, engine, name string, data any) error
func (r *Registry) RenderNamed(rc *RenderContext, engine EngineName, name string, data any) error
func (r *Registry) RenderComposition(rc *RenderContext, c Composition, data any) error
func (r *Registry) RenderPage(rc *RenderContext, layout, view string, data any) error
func (r *Registry) RenderPageWith(rc *RenderContext, engine, layout, view string, data any) error
```

`Render` takes a raw string (dynamic path); `RenderNamed` is the typed
wrapper taking an `EngineName`.

### Fluent render builder

```go
func (r *Registry) Page(rc *RenderContext) *RenderBuilder

type RenderBuilder struct { /* ... */ }

func (rb *RenderBuilder) Engine(name EngineName) *RenderBuilder
func (rb *RenderBuilder) EngineInstance(e Engine) *RenderBuilder
func (rb *RenderBuilder) Layout(name string) *RenderBuilder
func (rb *RenderBuilder) View(name string) *RenderBuilder
func (rb *RenderBuilder) Parts(parts ...string) *RenderBuilder
func (rb *RenderBuilder) With(data any) *RenderBuilder
func (rb *RenderBuilder) WithDone(fn func()) *RenderBuilder
func (rb *RenderBuilder) Render() error
func (rb *RenderBuilder) RenderComposition() error
```

`Engine(name EngineName)` takes a typed identifier (constant or
`CustomEngine(name)`). `EngineInstance(e Engine)` derives the name from a
live engine value.

### Injectors & FuncMaps

```go
func (r *Registry) Inject(in DataInjector)
func (r *Registry) FuncMap(b FuncMapBuilder)
func (r *Registry) InjectFuncMap(rc *RenderContext, data any)
```

### Components

```go
func (r *Registry) AttachComponents(c *ComponentRegistry)
func (r *Registry) Components() *ComponentRegistry
func (r *Registry) ComponentLookup(name string) (Component, bool)
```

### Variants

```go
func (r *Registry) Variant(name string, values ...string)
func (r *Registry) SetVariant(dim, value string)
```

### Features

```go
func (r *Registry) Compress(cfg CompressConfig) *Registry
func (r *Registry) Conditional(enabled bool) *Registry
func (r *Registry) WithMinify(cfg MinifyConfig) *Registry
func (r *Registry) Minify() *MinifyConfig
func (r *Registry) SetPipeline(p *Pipeline)
func (r *Registry) CompressWriter(rc *RenderContext, w ByteWriter) (ByteWriter, func() error)
```

---

## Cache

### `Cache`

An off-heap, lock-free, cache-line-padded program store. Keys are
`(engine, name)` pairs — hashed directly, no string concat.

```go
func NewCache(capacity int) *Cache
func (c *Cache) Get(engine, name string) (*Program, bool)
func (c *Cache) Put(engine, name string, p *Program)
func (c *Cache) Delete(engine, name string)
func (c *Cache) SetTag(engine, name string, tags ...string)
func (c *Cache) InvalidateTag(tag string) int
func (c *Cache) Stats() CacheStats
func (c *Cache) Close()
```

### `CacheStats`

```go
type CacheStats struct {
    Hits     int64
    Misses   int64
    Evicts   int64
    Capacity int
}
```

---

## RenderContext methods

```go
func AcquireContext(w ByteWriter, req *http.Request) *RenderContext
func ReleaseContext(rc *RenderContext)

func (rc *RenderContext) WithLoader(loader SourceFunc) *RenderContext
func (rc *RenderContext) WithFuncMap(fm template.FuncMap) *RenderContext
func (rc *RenderContext) WithLayout(name string) *RenderContext
func (rc *RenderContext) WithDebug() *RenderContext
func (rc *RenderContext) SetVariant(dim, value string)
func (rc *RenderContext) SetUserData(d any)
func (rc *RenderContext) UserData() any
func (rc *RenderContext) WriteHeader(key, value string)
func (rc *RenderContext) WriteHeadersTo(w io.Writer) error
func (rc *RenderContext) EffectiveFuncMap(programDefaults template.FuncMap) template.FuncMap
func (rc *RenderContext) ComponentRegistry() *ComponentRegistry
func (rc *RenderContext) SetComponentRegistry(c *ComponentRegistry)
func (rc *RenderContext) ComponentState() *State
```

---

## ByteWriter

### `ByteWriter`

```go
type ByteWriter interface {
    io.Writer
    io.ByteWriter
    io.StringWriter
    Flush() error
    Reset(w io.Writer)
    Bytes() []byte
}
```

```go
func AcquireWriter(w io.Writer) ByteWriter
func ReleaseWriter(w ByteWriter)
```

Pooled implementation grows 8→64 KiB and never shrinks.

---

## Components

### Core interfaces

```go
type Component interface {
    Render(w ByteWriter, rc *RenderContext, data any) error
}

type ComponentFunc func(w ByteWriter, rc *RenderContext, data any) error

func (f ComponentFunc) Render(w ByteWriter, rc *RenderContext, data any) error
func (f ComponentFunc) WithFuncs(funcs template.FuncMap) Component
```

### Optional interfaces

```go
type ComponentWithFuncs interface {
    Component
    ComponentFuncs() template.FuncMap
}

type ComponentWithChildren interface {
    Component
    RenderWithChildren(w ByteWriter, rc *RenderContext, data any, children Children) error
}

type ComponentWithChildrenFunc func(w ByteWriter, rc *RenderContext, data any, children Children) error
func (f ComponentWithChildrenFunc) Render(w ByteWriter, rc *RenderContext, data any) error
func (f ComponentWithChildrenFunc) RenderWithChildren(w ByteWriter, rc *RenderContext, data any, children Children) error
```

### `ComponentRegistry`

```go
func NewComponentRegistry() *ComponentRegistry
func (r *ComponentRegistry) Register(name string, c Component)
func (r *ComponentRegistry) Define(name string) *Definition
func (r *ComponentRegistry) Lookup(name string) (Component, bool)
func (r *ComponentRegistry) Names() []string
func (r *ComponentRegistry) Unregister(name string)
```

### Fluent `Definition`

```go
type Definition struct { /* ... */ }
type ComponentRenderFunc func(c *ComponentContext) error

func (d *Definition) WithFuncs(funcs template.FuncMap) *Definition
func (d *Definition) Render(fn ComponentRenderFunc) *Definition
func (d *Definition) RenderChildren(fn ComponentRenderFunc) *Definition
func (d *Definition) Register(cr *ComponentRegistry)
```

### `ComponentContext`

```go
type ComponentContext struct {
    Writer   ByteWriter
    Context  *RenderContext
    Data     any
    Children Children
    Slots    Slots
    State    *State
    DataMap  map[string]any
}

func (c *ComponentContext) Write(p []byte) (int, error)
func (c *ComponentContext) WriteString(s string) (int, error)
func (c *ComponentContext) WriteChildren()
func (c *ComponentContext) WriteSlot(name string)
```

### Children & Slots

```go
type Children []string
type Slots map[string]string

const ChildrenDataKey = "__children__"
const SlotsDataKey   = "__slots__"
```

---

## State (React hooks)

```go
func NewState() *State
func (s *State) Get(key string) (any, bool)
func (s *State) Set(key string, value any)
func (s *State) UseState(key string, initialValue any) any
func (s *State) Clear()

// Generic, type-safe:
func UseState[T any](s *State, key string, initial T) (T, func(T))
func UseStateOrDefault[T any](s *State, key string) (T, bool)
```

---

## Type-safe props

```go
func BindProps[T any](data any) T
```

Uses `nanite:"key"` struct tags; default key is the lowercased field name.

---

## Async / Suspense (server-side streaming)

```go
// Definition (fluent builder):
func (d *Definition) Async() *Definition
func (d *Definition) Fallback(fn ComponentRenderFunc) *Definition

// AsyncOptioner interface (engines can implement directly):
type AsyncOptioner interface {
    Component
    AsyncFallback() (id string, render func(w ByteWriter, rc *RenderContext, data any) error)
}

// RenderContext:
func (rc *RenderContext) EnsureSuspense(w ByteWriter) *suspenseCoordinator
func (rc *RenderContext) SubmitAsync(id string, fn func(w ByteWriter, rc *RenderContext, data any) error, data any)
func (rc *RenderContext) CloseSuspense()
func (rc *RenderContext) Suspense() *suspenseCoordinator
```

`Async()` opts a component into the suspense path: the executor
writes the `Fallback` output inline, then runs the component's render
in a background goroutine and emits the real output as a trailing OOB
chunk wrapped in `<div id="<component>" hx-swap-oob="true">…</div>`.
The fallback hits the wire immediately (TTFB ≈ 0ms); the real output
arrives when the worker finishes.

Turns `Sum(component_times)` into `Max(component_times)` for
independent components. Cancellation is wired through
`rc.Request.Context()`; a client disconnect aborts the worker and
releases its pooled buffer.

Coordinator is **lazy**: zero channels, zero goroutines, zero allocs
on the non-async path.

---

## Error Boundaries (panic isolation)

```go
// Definition (fluent builder):
func (d *Definition) ErrorBoundary(fn ErrorBoundaryFunc) *Definition

type ErrorBoundaryFunc func(c *ComponentContext, err any) error
```

A panic inside a component's render normally kills the whole
request. `ErrorBoundary` opts the component into graceful
degradation: the render runs into an isolated buffer; on panic
the buffer is discarded, the boundary is invoked with a fresh
context pointing at the live response, and the boundary's
writes replace the failed component's bytes in the page.

Components without a boundary keep the existing fast path —
no `defer/recover` overhead is paid unless a boundary is
registered.

Async components honor the boundary the same way: a worker
panic invokes the boundary against the worker's pooled buffer,
and the boundary's output replaces the expected OOB chunk.
A panic in the boundary itself (or a non-nil error return)
falls back to a generic `<!-- error boundary failed -->`
placeholder so the page never crashes.

The `err` value passed to the boundary is the raw panic value
(`any` — string, error, `*runtime.Error`, custom struct). It's
safe to log; do not echo it into the response without scrubbing.

---

## Cascading Context

```go
// RenderContext (low-level):
func (rc *RenderContext) PushContext(key string, val any)
func (rc *RenderContext) PopContextTo(savedPtr int)
func (rc *RenderContext) GetContext(key string) any
func (rc *RenderContext) ContextDepth() int

// ComponentContext (ergonomic wrappers — auto-pop on scope exit):
func (c *ComponentContext) ProvideContext(key string, val any)
func (c *ComponentContext) UseContext(key string) any

// Template bridge:
func UseContextFunc(rc *RenderContext) any  // returns the useContext func

const MaxContextDepth = 16  // hard cap; overflow panics
```

A fixed-size `[16]contextKV` array lives on `*RenderContext`.
Pushes are pure memory moves (zero alloc). Lookup scans the
stack backwards (newest first) so inner bindings shadow outer
ones — true cascading scope.

Scope isolation: the dispatcher pops the stack back to the
saved depth when each component's render returns. Bindings
made inside one component do NOT leak into siblings or
parents.

Template engine bridge: register `render.UseContextFunc(rc)`
in your FuncMap and templates can call `{{ useContext "theme" }}`
directly. Zero allocations, same stack as the fluent API.

Overflow: `MaxContextDepth` pushes panic. That's a programming
bug, not a runtime condition — if you need more than 16 nested
bindings, prefer explicit prop passing.

Async workers run against their own fresh `*RenderContext`
(empty stack). They don't inherit cascading state from the
inline render — pass what the worker needs via props or set
it in the `Fallback`.

---

## SoA NodeStream

### `NodeStream`

```go
type NodeStream struct {
    Tag           []string
    Flags         []uint8
    NS            []uint16
    ChildStart    []uint32
    ChildEnd      []uint32
    AttrStart     []uint32
    AttrEnd       []uint32
    TextOff       []uint32
    Parent        []int32
    ComponentName []string
    AttrKeys      []string
    AttrVals      []any
    textBuf       []byte  // unexported
    Count         int
}

func (s *NodeStream) Reset()
func (s *NodeStream) Text(i int) []byte
func (s *NodeStream) NumAttr(i int) int
func (s *NodeStream) AttrKey(i, k int) string
func (s *NodeStream) AttrVal(i, k int) any
func (s *NodeStream) ChildRange(i int) (uint32, uint32)
func (s *NodeStream) IsVoid(i int) bool
func (s *NodeStream) IsComponent(i int) bool
func (s *NodeStream) IsText(i int) bool
```

### Node flags

```go
const (
    FlagVoid        uint8 = 1 << 0
    FlagSelfClosing uint8 = 1 << 1
    FlagComponent   uint8 = 1 << 2
    FlagFragment    uint8 = 1 << 3
    FlagRaw         uint8 = 1 << 4
    FlagText        uint8 = 1 << 5
    FlagDynamicTag  uint8 = 1 << 6
    FlagHasChildren uint8 = 1 << 7
)
```

### `NodeStreamBuilder`

```go
func NewBuilder() *NodeStreamBuilder
func (b *NodeStreamBuilder) Reserve(approxNodes int) *NodeStreamBuilder
func (b *NodeStreamBuilder) ReserveText(srcSize int) *NodeStreamBuilder
func (b *NodeStreamBuilder) Reset()
func (b *NodeStreamBuilder) Stream() NodeStream
func (b *NodeStreamBuilder) AppendNode(tag string, flags uint8) int
func (b *NodeStreamBuilder) AppendText(text []byte, raw bool) int
func (b *NodeStreamBuilder) AppendComponent(name string, attrs []string, vals []any) int
func (b *NodeStreamBuilder) SetParent(child, parent int)
func (b *NodeStreamBuilder) SetChildren(i int, start, end uint32)
func (b *NodeStreamBuilder) SetAttrs(i int, keys []string, vals []any)
func (b *NodeStreamBuilder) SetAttrsEach(i int, attrs []AttrKV)
```

### `AttrKV`

```go
type AttrKV struct {
    K, V string
}
```

---

## HTML parsing & execution

```go
func ParseHTML(src []byte, name string) (*NodeStreamBuilder, error)
func Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error
func IsVoidTag(tag string) bool
```

`ParseHTML` returns a builder whose string fields reference `src` via
`unsafe.String` — the caller must keep `src` alive for the lifetime of the
Program. The default loader-cache pattern satisfies this.

---

## Superpowers

```go
func SuperpowerFuncs() template.FuncMap
func SetRenderState(rc *RenderContext)
func ClearRenderState()
```

Injected superpowers: `component(name, data)`, `yield()`, `useState(key, initial)`,
`get(key)`, `set(key, value)`.

---

## Features

### `CompressConfig`

```go
type CompressConfig struct {
    Level   int      // gzip level, default 6
    MinSize int      // min response size before compressing, default 1024
    Types   []string // content-type prefixes, default text/html + application/json
}
```

### `MinifyConfig`

```go
type MinifyConfig struct {
    KeepDefaultAttrVals     bool
    KeepWhitespace          bool
    KeepDocumentTags        bool
    KeepEndTags             bool
    KeepConditionalComments bool
}

func MinifyHTML(src []byte, cfg MinifyConfig) ([]byte, error)
```

### Conditional (ETag / 304)

```go
func HashContent(p *Program) string
func CheckConditional(rc *RenderContext, p *Program) bool
```

### `Pipeline`

```go
type Pipeline struct {
    BeforeCompile func(name string, src []byte) ([]byte, error)
    AfterCompile  func(p *Program) error
    BeforeExecute func(p *Program, rc *RenderContext, data any) error
    AfterExecute  func(p *Program, rc *RenderContext, data any) error
}

func NewPipeline() *Pipeline
func (p *Pipeline) Debug(w io.Writer) *Pipeline
```

### `Watcher` (hot reload)

```go
type Event struct {
    Path string
    Op   fsnotify.Op
}

func NewWatcher(roots ...string) (*Watcher, error)
func (w *Watcher) Events() <-chan Event
func (w *Watcher) Close() error
func (w *Watcher) Disabled() bool
```

### Loaders

```go
func NewFileLoader(root, ext string) SourceFunc
func NewMapLoader(templates map[string][]byte) SourceFunc
func NewPrefixLoader(prefix string, fs interface{ Open(name string) (io.ReadCloser, error) }) SourceFunc
```

---

## Errors

```go
type ParseError struct {
    Name   string
    Offset uint32
    Msg    string
}
func (e *ParseError) Error() string
func (e *ParseError) Unwrap() error
```

---

## Injectors & FuncMaps

```go
type DataInjector func(rc *RenderContext, data map[string]any)
type FuncMapBuilder func(rc *RenderContext) template.FuncMap
```

---

## Context key

```go
const ContextKey = "render"
```

Used by router adapters to store the `*RenderContext` in the request's
context map (e.g. `c.Values[render.ContextKey]`).

---

## Engine sub-package (`.../engine`)

### `Jade`

```go
func NewJade() *Jade
func (j *Jade) Name() string
func (j *Jade) Compile(src []byte, name string) (*render.Program, error)
func (j *Jade) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error
```

Parses jade → HTML, compiles via html/template with superpowers.

### `HTMLTemplate`

```go
func NewHTMLTemplate() *HTMLTemplate
func (h *HTMLTemplate) Name() string
func (h *HTMLTemplate) Compile(src []byte, name string) (*render.Program, error)
func (h *HTMLTemplate) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error
```

Plain HTML with `{{.Var}}` interpolation via html/template + superpowers.

### `HTML` (plain, no data binding)

```go
func NewHTML() *HTML
func (h *HTML) Name() string
func (h *HTML) Compile(src []byte, name string) (*render.Program, error)
func (h *HTML) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error
```

Plain HTML via the SoA executor. No `{{.Var}}`; components via `<COMPONENT/>`.

### `Templ`

```go
func NewTempl() *Templ
func (t *Templ) Name() string
func (t *Templ) Register(name string, c TemplComponent)
func (t *Templ) Compile(src []byte, name string) (*render.Program, error)
func (t *Templ) Render(name string, w render.ByteWriter, data any) error
```

Adapts pre-compiled templ components. Note: does not implement the full
`Engine.Execute` signature — use `Render` for direct dispatch.

---

## HTMX — first-class support

nanite-render implements 100% of the HTMX 2.0 server-side contract.
The router remains in charge of the wire; the renderer is purely about
producing HTMX-aware bytes and headers.

### Request detection (pure helpers)

All functions take `*http.Request` and are nil-safe.

```go
func IsHTMXRequest(r *http.Request) bool             // HX-Request: true
func IsHTMXBoosted(r *http.Request) bool             // HX-Boosted: true
func IsHTMXHistoryRestore(r *http.Request) bool      // HX-History-Restore-Request: true
func HXTargetID(r *http.Request) string              // HX-Target
func HXTriggerID(r *http.Request) string             // HX-Trigger
func TriggerName(r *http.Request) string             // HX-Trigger-Name
func CurrentURL(r *http.Request) string              // HX-Current-URL
func HXPromptResponse(r *http.Request) string        // HX-Prompt
```

### Header constants

Request headers (client → server) and response headers (server →
client). The same wire name serves both directions for `HX-Trigger`;
the alias `HXTriggerHeader` exists for code that handles both sides.

```go
const (
    HXRequest               = "HX-Request"
    HXBoosted               = "HX-Boosted"
    HXHistoryRestoreRequest = "HX-History-Restore-Request"
    HXTarget                = "HX-Target"
    HXTrigger               = "HX-Trigger"
    HXTriggerName           = "HX-Trigger-Name"
    HXCurrentURL            = "HX-Current-URL"
    HXPrompt                = "HX-Prompt"

    // Response headers
    HXTriggerHeader        = "HX-Trigger"
    HXTriggerAfterSwap     = "HX-Trigger-After-Swap"
    HXTriggerAfterSettle   = "HX-Trigger-After-Settle"
    HXRetarget             = "HX-Retarget"
    HXReswapHeader         = "HX-Reswap"
    HXPushURL              = "HX-Push-Url"
    HXRedirect             = "HX-Redirect"
    HXRefresh              = "HX-Refresh"
    HXReplaceURL           = "HX-Replace-Url"
    HXLocation             = "HX-Location"
    HXReselect             = "HX-Reselect"
    HXResettle             = "HX-Resettle"
)
```

### Direct component dispatch

```go
func (r *Registry) RenderComponent(w ByteWriter, rc *RenderContext, name string, props any) error
```

Renders one named component to `w` without a parent template or layout.
Missing name returns `nil` (no error, no output) — stale HTMX targets
don't crash the handler.

### OOB swaps

```go
func (d *Definition) WithOOB(targetID string) *Definition
```

Opt a component in. `targetID` is required and is a hard DOM contract
(it must match the `id` attribute of the element the swap replaces).
When the component's render mutates state via `SetState`/`UseState`,
output is auto-wrapped in `<div id="<targetID>" hx-swap-oob="true">…</div>`
and emitted. Clean renders write through unchanged — no buffer copy on
the no-op path. The render buffer comes from a `sync.Pool` of
`bytes.Buffer` — zero steady-state allocations.

### OOBOptioner interface

```go
type OOBOptioner interface {
    Component
    OOBID() string
}
```

Engines that pre-compile components (templ, html/template) implement
this directly to participate in OOB tracking without using the fluent
builder. The fluent `WithOOB(id)` becomes shorthand for a
`OOBOptioner`-implementing wrapper.

### OOB sink

```go
func (rc *RenderContext) SetOOBSink(w ByteWriter)
func (rc *RenderContext) OOBSink() ByteWriter
```

Optional separate writer for OOB swap output. Falls back to `rc.Writer`
when nil. Most handlers leave this nil; routers that want to interleave
OOB swaps with a different stream (e.g. an SSE trailing chunk) can
set it here.

### Trigger events

```go
func (rc *RenderContext) AddHXTrigger(name string)
func (rc *RenderContext) AddHXTriggerWithDetail(name string, detail any)
func (rc *RenderContext) HXTriggers() HXTriggers
func (rc *RenderContext) AddHXTriggerAfterSwap(name string)
func (rc *RenderContext) HXTriggersAfterSwap() HXTriggers
func (rc *RenderContext) AddHXTriggerAfterSettle(name string)
func (rc *RenderContext) HXTriggersAfterSettle() HXTriggers

// ComponentContext convenience (nil-safe)
func (c *ComponentContext) AddHXTrigger(name string)
func (c *ComponentContext) AddHXTriggerWithDetail(name string, detail any)
func (c *ComponentContext) AddHXTriggerAfterSwap(name string)
func (c *ComponentContext) AddHXTriggerAfterSettle(name string)
```

`HXTriggers` is `map[string]any` so entries can carry JSON detail.
`HXTriggers.Join()` auto-selects the wire format:
- Plain comma-separated names when no entry has detail
- JSON object when any entry carries detail

```go
type HXTriggers map[string]any
func (t HXTriggers) Add(name string)
func (t HXTriggers) AddWithDetail(name string, detail any)
func (t HXTriggers) Names() []string             // sorted
func (t HXTriggers) HasDetail() bool
func (t HXTriggers) Join() string               // "" when empty
```

### HTMXResponse — server-driven decisions

```go
type HTMXResponse struct {
    Retarget   string  // CSS selector
    Reswap     string  // e.g. "outerHTML swap:200ms"
    PushURL    string  // "true" or a URL
    Redirect   string  // full URL
    Refresh    string  // "true"
    ReplaceURL string  // "true" or a URL
    Location   string  // full URL
    Reselect   string  // CSS selector
    Resettle   string  // "true" forces scroll reset
}

func (rc *RenderContext) SetHXRetarget(selector string)
func (rc *RenderContext) SetHXReswap(strategy string)
func (rc *RenderContext) SetHXPushURL(url string)
func (rc *RenderContext) SetHXRedirect(url string)
func (rc *RenderContext) SetHXRefresh()
func (rc *RenderContext) SetHXReplaceURL(url string)
func (rc *RenderContext) SetHXLocation(url string)
func (rc *RenderContext) SetHXReselect(selector string)
func (rc *RenderContext) SetHXResettle(value string)
func (rc *RenderContext) HTMXResponse() HTMXResponse
```

### WriteHTMXHeaders

```go
func (rc *RenderContext) WriteHTMXHeaders(w http.ResponseWriter)
```

Applies every accumulated HTMX response decision to the writer in one
call. Nil-safe on both receiver and writer. Headers whose source is
empty are skipped so the response carries only what the handler set.

### Auto-trigger on dirty OOB

When an OOB-enabled component's render dirties state, the lowercased
component name is automatically added to `HX-Trigger`. Clean renders
emit no trigger. User-added triggers via `c.AddHXTrigger(...)` coexist
with the auto one — dedup keeps the set clean.

---

## nano sub-package (`.../nano`)

Direct-call helpers for the nanite router. No middleware.

```go
func Render(c *nanite.Context, reg *render.Registry, engine, view string, data any) error
func RenderWithLoader(c *nanite.Context, reg *render.Registry, engine, view string, data any, loader render.SourceFunc) error
func RenderPage(c *nanite.Context, reg *render.Registry, layout, view string, data any) error
func Page(reg *render.Registry, c *nanite.Context) *render.RenderBuilder
func SetHeader(c *nanite.Context, key, value string)
func Status(c *nanite.Context, code int)
func HTTPErrorHandler(c *nanite.Context, err error)
```
