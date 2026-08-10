package render

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strings"
	"sync"
)

// ComponentContext bundles all the render arguments into a single
// value. Components written with the fluent API receive a
// ComponentContext instead of the multi-arg ComponentFunc signature.
// The included State and Children/Slots are read from the render
// state set by the dispatcher.
type ComponentContext struct {
	// Writer is the ByteWriter render writes to.
	Writer ByteWriter
	// Context is the per-request RenderContext.
	Context *RenderContext
	// Data is the user-supplied data passed to Render or RenderPage.
	Data any
	// Children is the pre-rendered HTML of the component's slot-less
	// children (slot children live in Slots).
	Children Children
	// Slots maps slot name to the pre-rendered HTML of the slot's
	// children.
	Slots Slots
	// State is the per-render state. Use for useState-style hooks.
	State *State
	// DataMap is the data map (may be nil). The component can
	// read/modify it for child propagation.
	DataMap map[string]any

	// oobID is the OOB target ID when this component was declared
	// with WithOOB(). Empty for normal components. When set, the
	// render wrapper buffers the component's bytes and emits them
	// wrapped in <div id="oobID" hx-swap-oob="true">...</div> if
	// any SetState/UseState mutation occurred during this render.
	oobID string

	// dirty is set by SetState when oobID is non-empty. The render
	// wrapper checks this after the user's render returns; if true,
	// it emits the OOB swap.
	dirty bool

	// savedCtxPtr is the RenderContext's context-stack depth at
	// the moment this ComponentContext was created. The dispatch
	// path (fluentComponent.Render and friends) pops the stack
	// back to this depth after the user's render returns, so
	// ProvideContext bindings made during this component's scope
	// don't leak out into siblings or parents.
	savedCtxPtr int

	// name is the component's registered name. Used by ActionURL
	// to build the colocated-action URL.
	name string
}

// ActionURL builds the URL for a colocated server action on this
// component:
//
//	{prefix}/{COMPONENT}/{action}
//
// e.g. with the default prefix:
//
//	c.ActionURL("toggle") // "/_nano/action/LIKE_BUTTON/toggle"
//
// The prefix is configurable via WithActionPrefix; the URL is
// safe for hx-post / form action attributes. Returns "" for a
// nil receiver or empty action name.
func (c *ComponentContext) ActionURL(action string) string {
	if c == nil || c.name == "" || action == "" {
		return ""
	}
	prefix := DefaultActionPrefix
	if c.Context != nil && c.Context.actionPrefix != "" {
		prefix = c.Context.actionPrefix
	}
	return prefix + c.name + "/" + action
}

// Write writes a byte slice to the output. Convenience over Writer.
func (c *ComponentContext) Write(p []byte) (int, error) { return c.Writer.Write(p) }

// WriteString writes a string to the output.
func (c *ComponentContext) WriteString(s string) (int, error) { return c.Writer.WriteString(s) }

// WriteChildren writes all slot-less children to the output. The
// component controls the placement; the framework just provides
// the pre-rendered HTML.
func (c *ComponentContext) WriteChildren() {
	for _, child := range c.Children {
		_, _ = c.Writer.WriteString(child)
	}
}

// WriteSlot writes the named slot's content to the output. If the
// slot is not present, nothing is written.
func (c *ComponentContext) WriteSlot(name string) {
	if c.Slots == nil {
		return
	}
	_, _ = c.Writer.WriteString(c.Slots[name])
}

// ---------------------------------------------------------------------------
// React-style hooks, first-class on the ComponentContext. These make
// component authoring uniform across every engine: a component written
// against ComponentContext gets state, props, and inline composition
// no matter whether it is rendered from templ, jade, html/template, or
// the plain HTML executor.
// ---------------------------------------------------------------------------

// GetState reads a value from the per-render state.
func (c *ComponentContext) GetState(key string) (any, bool) {
	if c == nil || c.State == nil {
		return nil, false
	}
	return c.State.Get(key)
}

// SetState writes a value to the per-render state. When this context
// is bound to a component declared with WithOOB, SetState also marks
// the frame dirty so the render wrapper emits an HTMX OOB swap.
func (c *ComponentContext) SetState(key string, v any) {
	if c == nil || c.State == nil {
		return
	}
	c.State.Set(key, v)
	if c.oobID != "" {
		c.dirty = true
	}
}

// UseState is the React-style hook. It returns the current value for
// key (initial on first use) and a setter that writes back. The setter
// affects subsequent renders; the current render keeps its value.
//
// For a typed variant use render.UseState[T](c.State, key, initial).
//
// When this context is bound to a component declared with WithOOB,
// the setter also marks the frame dirty so the render wrapper emits
// an HTMX OOB swap.
func (c *ComponentContext) UseState(key string, initial any) (any, func(any)) {
	if c == nil || c.State == nil {
		return initial, func(any) {}
	}
	return c.State.UseState(key, initial), func(v any) {
		c.State.Set(key, v)
		if c.oobID != "" {
			c.dirty = true
		}
	}
}

// ProvideContext pushes (key, val) onto the cascading-context
// stack. The binding is visible to this component and every
// component it renders (children, sub-children, …) until the
// component's render scope exits — the dispatcher automatically
// pops the stack back to savedCtxPtr on return, so bindings
// don't leak into siblings or parent frames.
//
// Shadowing: an inner ProvideContext("theme", "light") overrides
// an outer binding for the duration of the inner component's
// scope. Lookup is reverse-scan (newest first), so the nearest
// binding wins.
//
// No hard cap: bindings beyond the inline fast-path spill to a
// heap slice so genuinely deep trees still work. There's no
// panic — if you need hundreds of nested ambient contexts
// you're probably doing it wrong, but the framework won't stop
// you.
//
// Nil-safe: a nil receiver is a no-op.
func (c *ComponentContext) ProvideContext(key string, val any) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.PushContext(key, val)
}

// UseContext returns the value bound to the nearest matching key
// in the cascading-context stack, or nil if no binding exists.
// The caller type-asserts the result:
//
//	theme := c.UseContext("theme").(string)
//
// Nil-safe for the receiver.
func (c *ComponentContext) UseContext(key string) any {
	if c == nil || c.Context == nil {
		return nil
	}
	return c.Context.GetContext(key)
}

// SetFormError records a flash validation error for a form field.
// During a server-action re-render, the error is readable via
// GetFormError and rendered inline. Flash semantics: the error
// lives for this request only — cleared on pool reuse.
func (c *ComponentContext) SetFormError(key, msg string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.SetFormError(key, msg)
}

// GetFormError returns the flash validation error for a form
// field, or "" if none was set for this request.
func (c *ComponentContext) GetFormError(key string) string {
	if c == nil || c.Context == nil {
		return ""
	}
	return c.Context.GetFormError(key)
}

// SetTitle sets the document <title> for this request. On full
// page loads the view renders before the layout, so the layout's
// <NANO_HEAD/> (or {{ nanoHead }}) emits it. On HTMX partial
// swaps, write `<title>` directly to the output instead — HTMX
// hoists it from the response body.
func (c *ComponentContext) SetTitle(title string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.SetTitle(title)
}

// AddMeta adds a <meta name="..." content="..."> tag for this
// request, emitted by the layout's <NANO_HEAD/> on full page
// loads. Last write wins per name.
func (c *ComponentContext) AddMeta(name, content string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.AddMeta(name, content)
}

// UseId returns a unique per-request identifier ("nano-1", …)
// for pairing <label for> with <input id>. Zero-alloc for the
// first 256 ids.
func (c *ComponentContext) UseId() string {
	if c == nil || c.Context == nil {
		return "nano-0"
	}
	return c.Context.UseId()
}

// RequiresCSS declares a CSS dependency for this component. The
// path is emitted once (deduplicated) by the layout's
// <NANO_ASSETS/> as a <link rel="stylesheet"> tag. Safe to call
// from components rendered in loops — the head gets one tag.
func (c *ComponentContext) RequiresCSS(href string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.RequiresCSS(href)
}

// RequiresJS declares a JS dependency for this component. The
// path is emitted once (deduplicated) by the layout's
// <NANO_ASSETS/> as a <script defer> tag.
func (c *ComponentContext) RequiresJS(src string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.RequiresJS(src)
}

// Render dispatches a named, registered component inline, writing to
// this context's writer. The component receives a fresh
// ComponentContext (same state, its own data). This is the
// `{{ component "Name" . }}` superpower as a method — usable from any
// component on any engine.
func (c *ComponentContext) Render(name string, props any) error {
	return dispatchComponent(c.Writer, c.Context, name, props)
}

// AddHXTrigger records an HTMX client-side event to fire after this
// render completes. Convenience over RenderContext.AddHXTrigger for
// component authors. Safe to call with nil receiver.
func (c *ComponentContext) AddHXTrigger(name string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.AddHXTrigger(name)
}

// AddHXTriggerWithDetail records an event with a JSON-encodable
// detail payload. Convenience over RenderContext.AddHXTriggerWithDetail.
func (c *ComponentContext) AddHXTriggerWithDetail(name string, detail any) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.AddHXTriggerWithDetail(name, detail)
}

// AddHXTriggerAfterSwap records an HTMX event to fire after the swap
// step. Convenience over RenderContext.AddHXTriggerAfterSwap.
func (c *ComponentContext) AddHXTriggerAfterSwap(name string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.AddHXTriggerAfterSwap(name)
}

// AddHXTriggerAfterSettle records an HTMX event to fire after the
// settle step. Convenience over RenderContext.AddHXTriggerAfterSettle.
func (c *ComponentContext) AddHXTriggerAfterSettle(name string) {
	if c == nil || c.Context == nil {
		return
	}
	c.Context.AddHXTriggerAfterSettle(name)
}

// Props binds c.Data to a typed struct via nanite:"key" tags.
// Equivalent to render.BindProps[T](c.Data).
func Props[T any](c *ComponentContext) T {
	if c == nil {
		var zero T
		return zero
	}
	return BindProps[T](c.Data)
}

// ComponentRenderFunc is the function signature for the fluent
// component API. The receiver is a *ComponentContext that bundles
// the render arguments.
type ComponentRenderFunc func(c *ComponentContext) error

// panicValue is a sentinel error wrapping a recovered panic value.
// The boundary path checks for this type to distinguish "user
// render panicked" from "user render returned an error". The
// underlying value is exposed via Val() so the boundary sees the
// raw panic (any) without it being wrapped in an interface
// assertion dance.
type panicValue struct {
	val any
}

// Error implements error so panicValue can flow through the same
// error-returning code path as a regular Render error.
func (p *panicValue) Error() string {
	if e, ok := p.val.(error); ok {
		return e.Error()
	}
	return fmt.Sprintf("panic: %v", p.val)
}

// Val returns the raw panic value passed to panic().
func (p *panicValue) Val() any { return p.val }

// Definition is the fluent builder for a single component. It is
// returned by cr.Define(name) and chained with WithFuncs, Render,
// and RenderChildren. The definition is finalised by calling
// Register(cr) on the resulting ComponentRegistry.
//
// Usage:
//
//	cr.Define("WRAP").
//	    WithFuncs(template.FuncMap{"currentUser": ...}).
//	    RenderChildren(func(c *render.ComponentContext) error {
//	        c.WriteString("<div class='wrap'>")
//	        c.WriteChildren()
//	        c.WriteString("</div>")
//	        return nil
//	    }).
//	    Register(cr)
type Definition struct {
	name         string
	funcs        template.FuncMap
	render       ComponentRenderFunc
	withChildren bool
	oobID        string

	// async marks this component for Suspense-style async rendering.
	// When true and Fallback is set, the executor writes the fallback
	// output inline and runs the main render in a background
	// goroutine; the real output is emitted as a trailing OOB chunk.
	async bool

	// fallback is the synchronous placeholder rendered inline before
	// the async render finishes. Required when async is true.
	fallback ComponentRenderFunc

	// errBoundary is the panic recovery callback. When non-nil, the
	// component's Render runs inside a deferred recover; if it
	// panics, the boundary is invoked with the panic value and the
	// rest of the page continues rendering. nil means no boundary
	// — the render runs on the existing fast path with zero
	// defer/recover overhead.
	errBoundary ErrorBoundaryFunc

	// actions are the colocated server actions registered via
	// Action(name, fn). Keyed by action name; dispatched by
	// Registry.HandleAction. nil when no actions are registered.
	actions map[string]ActionFunc

	// metadata is the head-metadata closure run before the page
	// streams (see MetadataProvider). nil when not registered.
	metadata func(rc *RenderContext, data any) error
}

// ErrorBoundaryFunc is the panic recovery callback for a component.
// It receives the ComponentContext (a fresh one pointing at the
// same underlying writer as the failed render) and the raw panic
// value (any — string, error, *runtime.Error, etc.). Returning nil
// swallows the panic and lets page rendering continue; returning a
// non-nil error propagates it.
//
// The boundary's writes land in the live response, NOT in the
// discarded buffer the panicking render was using. This keeps the
// rest of the page intact — only the failed component's output is
// replaced.
//
// Security note: the panic value is the raw value passed to
// panic(). Boundaries that print it to the response must take care
// not to leak internal state (DB errors with credentials, stack
// traces, etc.) to end users.
type ErrorBoundaryFunc func(c *ComponentContext, err any) error

// WithFuncs declares the per-component FuncMap. The FuncMap is
// available to the component's Render via the render state
// (rc.ComponentFuncMap). It is also passed to the component as
// ComponentContext if the component is a ComponentWithFuncs.
func (d *Definition) WithFuncs(funcs template.FuncMap) *Definition {
	d.funcs = funcs
	return d
}

// Render sets the render function. The function receives a
// ComponentContext that bundles the render arguments. The component
// has no children (use RenderChildren for that).
func (d *Definition) Render(fn ComponentRenderFunc) *Definition {
	d.render = fn
	d.withChildren = false
	return d
}

// RenderChildren sets the render function and indicates the
// component receives children. The component should call
// c.WriteChildren() to inject them at the desired location.
func (d *Definition) RenderChildren(fn ComponentRenderFunc) *Definition {
	d.render = fn
	d.withChildren = true
	return d
}

// WithOOB enables HTMX Out-of-Band swap emission for this component.
// targetID is the DOM `id` of the element the swap replaces — it
// must match an `id` attribute in the page. The id is required and
// is a hard contract: HTMX selectors are case-sensitive and a
// mismatch silently breaks the swap.
//
// When the component mutates state via SetState / UseState during
// its render, its output is wrapped in
//
//	<div id="<targetID>" hx-swap-oob="true">...</div>
//
// and written to rc.OOBSink() (which falls back to rc.Writer when no
// explicit sink is configured). When the component does NOT mutate
// state, its bytes are written to the original writer unchanged.
//
// WithOOB is strictly opt-in — components without it render exactly
// as before, with no buffer copy or wrapper overhead.
func (d *Definition) WithOOB(targetID string) *Definition {
	d.oobID = targetID
	return d
}

// Async marks the component for Suspense-style asynchronous
// rendering. Requires Fallback(fn) to set the placeholder; calling
// Register without a fallback when Async() is set is a no-op.
//
// When the executor encounters an Async component:
//   - It writes the Fallback output inline and flushes immediately
//     so the browser paints the skeleton with TTFB ≈ 0ms.
//   - It spawns a worker goroutine that runs Render to a pooled
//     buffer.
//   - When the worker finishes (or the request context cancels), the
//     real output is emitted as a trailing OOB chunk via the
//     suspense coordinator, wrapped in
//     <div id="<component>" hx-swap-oob="true">...</div>.
//
// Async turns Sum(component_times) into Max(component_times) for
// independent components. It is strictly opt-in and lazy: the
// coordinator allocates no channels and no goroutines unless an
// Async component is actually hit during the render walk.
func (d *Definition) Async() *Definition {
	d.async = true
	return d
}

// Fallback sets the synchronous placeholder rendered inline before
// the async component's real output is available. fn runs against
// the same ComponentContext as Render — it can read props, write
// placeholder markup, and emit any inline CSS/JS the skeleton needs.
//
// Fallback is required when Async() is used; calling Fallback
// without Async() is harmless (no async dispatch happens) but the
// fallback function never runs.
func (d *Definition) Fallback(fn ComponentRenderFunc) *Definition {
	d.fallback = fn
	return d
}

// ErrorBoundary installs a panic recovery callback for this
// component. When set, the component's Render runs inside a
// deferred recover; a panic invokes fn with the panic value, the
// failed render's buffer is discarded, and fn's output replaces
// the failed component in the page. The rest of the render tree
// continues normally.
//
// Components without ErrorBoundary run on the existing fast path
// (no defer/recover overhead).
//
//	fn := func(c *render.ComponentContext, err any) error {
//	    log.Printf("widget failed: %v", err) // safe to log
//	    return c.WriteString(`<div class="error">Widget unavailable</div>`)
//	}
//	cr.Define("DASHBOARD_WIDGET").ErrorBoundary(fn).Render(...)
//
// If fn itself panics or returns an error, a generic
// `<!-- error boundary failed -->` placeholder is emitted so the
// page never crashes. This is the silent-fallback contract
// documented in the design.
func (d *Definition) ErrorBoundary(fn ErrorBoundaryFunc) *Definition {
	d.errBoundary = fn
	return d
}

// Action registers a colocated server action: mutation logic
// that lives next to the component. The action is dispatched by
// Registry.HandleAction when its URL is POSTed; after it runs,
// the component re-renders with the post-action state.
//
//	name is the action name — it becomes part of the URL:
//	{prefix}/{COMPONENT}/{name}. Use c.ActionURL(name) in the
//	component's Render to generate the URL.
//
//	fn receives the per-request RenderContext and the parsed
//	request body as props (JSON → typed map, forms → best-effort
//	conversion). It should mutate the user's own storage — the
//	framework does not persist state across requests.
//
// The action's error contract: returning nil triggers the
// re-render; returning an error yields a 500 and no re-render.
func (d *Definition) Action(name string, fn ActionFunc) *Definition {
	if name == "" || fn == nil {
		return d
	}
	if d.actions == nil {
		d.actions = make(map[string]ActionFunc, 1)
	}
	d.actions[name] = fn
	return d
}

// Metadata registers a head-metadata closure that runs BEFORE
// the page streams (see MetadataProvider). It receives the page
// data and sets the document title / meta tags via
// rc.SetTitle / rc.AddMeta; the layout's <NANO_HEAD/> (or
// {{ nanoHead }}) emits them.
//
// For the common case this is redundant: components rendered in
// the view can call c.SetTitle / c.AddMeta during their render —
// the view renders before the layout in the two-pass page
// pipeline, and the head state is transferred to the layout's
// context. Metadata is for views that are NOT registered
// components, or metadata that should run even if the render
// walk is skipped.
//
//	fn := func(rc *render.RenderContext, data any) error {
//	    user := data.(User)
//	    rc.SetTitle(user.Name + " - Profile")
//	    rc.AddMeta("description", "Profile page for " + user.Name)
//	    return nil
//	}
//	cr.Define("USER_PROFILE").Metadata(fn).Render(...)
func (d *Definition) Metadata(fn func(rc *RenderContext, data any) error) *Definition {
	d.metadata = fn
	return d
}

// Register finalises the definition and registers it with the
// ComponentRegistry.
func (d *Definition) Register(cr *ComponentRegistry) {
	if d.name == "" || d.render == nil {
		return
	}
	// Async requires a fallback; if the user forgot to set one we
	// silently downgrade to a synchronous render rather than panic.
	async := d.async && d.fallback != nil
	// Build a Component that delegates to the fluent function.
	c := &fluentComponent{
		name:         d.name,
		fn:           d.render,
		fallback:     d.fallback,
		funcs:        d.funcs,
		withChildren: d.withChildren,
		oobID:        d.oobID,
		async:        async,
		errBoundary:  d.errBoundary,
		actions:      d.actions,
		metadata:     d.metadata,
	}
	cr.Register(d.name, c)
}

// oobBufPool is a sync.Pool of *bytes.Buffer used by OOB-enabled
// components to capture their rendered bytes before deciding whether
// to wrap them in an HTMX OOB swap. Pooled so the steady-state
// allocation count stays at zero.
var oobBufPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

// acquireOOBBuffer returns a clean buffer from the pool.
func acquireOOBBuffer() *bytes.Buffer {
	b := oobBufPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

// releaseOOBBuffer returns buf to the pool. Safe to call multiple
// times (Reset first).
func releaseOOBBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	buf.Reset()
	oobBufPool.Put(buf)
}

// fluentComponent adapts a ComponentRenderFunc to the Component
// interface. It builds a ComponentContext on each render and
// delegates to the user's function.
type fluentComponent struct {
	name         string
	fn           ComponentRenderFunc
	fallback     ComponentRenderFunc
	funcs        template.FuncMap
	withChildren bool
	oobID        string
	async        bool
	errBoundary  ErrorBoundaryFunc
	actions      map[string]ActionFunc
	metadata     func(rc *RenderContext, data any) error
}

// OOBID implements OOBOptioner. Returns the target DOM element id
// when the component was declared with WithOOB(), or "" otherwise.
func (f *fluentComponent) OOBID() string { return f.oobID }

// LookupAction implements ActionProvider. Returns the colocated
// server action registered under name, or ok=false.
func (f *fluentComponent) LookupAction(name string) (ActionFunc, bool) {
	fn, ok := f.actions[name]
	return fn, ok
}

// Metadata implements MetadataProvider. Returns the head-metadata
// closure registered via Definition.Metadata, or nil.
func (f *fluentComponent) Metadata() func(rc *RenderContext, data any) error {
	return f.metadata
}

// ErrorBoundary returns the panic recovery callback registered via
// Definition.ErrorBoundary, or nil if no boundary was set. The
// async worker checks this to decide whether to invoke the
// boundary on render failure (writing its output as the OOB
// payload) or silently drop the failed chunk.
func (f *fluentComponent) ErrorBoundary() ErrorBoundaryFunc { return f.errBoundary }

// IsAsync reports whether the component declared Async(). The
// executor checks this to decide whether to dispatch via the
// suspense coordinator (write fallback + spawn worker) or render
// synchronously.
func (f *fluentComponent) IsAsync() bool { return f.async }

// AsyncFallback implements AsyncOptioner. The executor calls this
// when an async dispatch is needed; the returned id becomes the
// OOB target id (defaulting to the component name lowercased),
// and render is run in a worker goroutine.
func (f *fluentComponent) AsyncFallback() (string, func(w ByteWriter, rc *RenderContext, data any) error) {
	if !f.async {
		return "", nil
	}
	// Capture the render function so we can pass it across the
	// goroutine boundary without retaining the whole component.
	fn := f.fn
	return strings.ToLower(f.name), func(w ByteWriter, rc *RenderContext, data any) error {
		ctx := &ComponentContext{
			Writer:  w,
			Context: rc,
			Data:    data,
			State:   getCompState(rc),
			DataMap: asMap(data),
		}
		return fn(ctx)
	}
}

// AsyncFallbackFunc returns the user-supplied Fallback function.
// Returns nil when the component is not async.
func (f *fluentComponent) AsyncFallbackFunc() ComponentRenderFunc {
	if !f.async {
		return nil
	}
	return f.fallback
}

// Render implements Component. It builds the ComponentContext from
// the render arguments and delegates to the user's function. It is
// nil-safe for rc: superpower dispatches (e.g. componentSuperpower)
// pass nil rc, so the fluent component tolerates a nil context and
// only sets state/slots when rc is present.
//
// When the component has oobID set, the user's render writes to a
// pooled buffer; after the user's render returns, the wrapper emits
// the buffered bytes either:
//   - to rc.OOBSink() wrapped in <div id="oobID" hx-swap-oob="true">
//     when ctx.dirty was set by SetState/UseState during the render, or
//   - to the original writer unchanged when no state was mutated.
//
// The buffer is released to the pool via defer, so even a panic in
// the user's render returns the buffer cleanly.
func (f *fluentComponent) Render(w ByteWriter, rc *RenderContext, data any) error {
	// Set the per-component FuncMap if the fluent component
	// declared one. Nil-safe for rc.
	if rc != nil {
		prev := rc.ComponentFuncMap
		if f.funcs != nil {
			rc.ComponentFuncMap = f.funcs
		}
		defer func() { rc.ComponentFuncMap = prev }()
	}

	// Error boundary fast path: if no boundary is registered AND
	// no OOB is needed, render directly with no defer/recover
	// overhead. This is the common case.
	if f.errBoundary == nil && f.oobID == "" {
		ctx := f.buildCtx(w, rc, data, "")
		// Auto-pop any cascading-context pushes the user's render
		// made, so bindings don't leak into sibling/parent frames.
		if rc != nil {
			defer rc.PopContextTo(ctx.savedCtxPtr)
		}
		return f.fn(ctx)
	}

	// Isolated-buffer path: render to a pooled buffer so we can
	// discard the bytes on panic (without leaking a half-written
	// <div> into the live response). The buffer is shared by both
	// the boundary-enabled and OOB-enabled paths.
	buf := acquireOOBBuffer()
	defer releaseOOBBuffer(buf)
	bw := AcquireWriter(buf)
	defer ReleaseWriter(bw)
	ctx := f.buildCtx(bw, rc, data, f.oobID)
	// Auto-pop any cascading-context pushes from this render.
	// Goes AFTER buildCtx so we have the saved pointer, but the
	// defer fires at function exit regardless of panic.
	if rc != nil {
		defer rc.PopContextTo(ctx.savedCtxPtr)
	}

	var renderErr error
	if f.errBoundary != nil {
		// Wrap the user's render in a deferred recover. If it
		// panics, the panic value is captured and the boundary
		// is invoked below. We use a named function so the
		// deferred recover is scoped to the user's render ONLY
		// — not to the surrounding code (defers in the rest of
		// Render still fire normally).
		func() {
			defer func() {
				if r := recover(); r != nil {
					renderErr = &panicValue{val: r}
				}
			}()
			renderErr = f.fn(ctx)
		}()
	} else {
		renderErr = f.fn(ctx)
	}

	// Panic path: discard buf, invoke boundary. The boundary
	// writes to the LIVE writer (w), so the failed component's
	// bytes are replaced by the boundary output in the response.
	if pe, ok := renderErr.(*panicValue); ok {
		if f.errBoundary != nil {
			return f.runBoundary(w, rc, data, pe.val)
		}
		// No boundary registered — surface the panic. Re-panic
		// so the runtime prints a stack trace and the request
		// handler can decide what to do.
		panic(pe.val)
	}
	if renderErr != nil {
		return renderErr
	}
	// Drain the wrapper buffer so its bytes are visible to the
	// OOB / boundary handling below.
	if err := bw.Flush(); err != nil {
		return err
	}

	// Clean render — write directly to the original writer without
	// the OOB wrapper. This is the common case for an OOB-enabled
	// component that didn't actually mutate state this round.
	if !ctx.dirty {
		_, err := w.Write(buf.Bytes())
		return err
	}

	// Dirty render — wrap in <div id="..." hx-swap-oob="true"> and
	// emit to the OOB sink. Attribute values are escaped so a
	// malicious oobID can't break out of the attribute.
	sink := w
	if rc != nil {
		sink = rc.OOBSink()
		if sink == nil {
			sink = w
		}
	}
	// Auto-emit an HX-Trigger event so the client knows this
	// component was updated. The event name is the lowercased
	// component name; users can call AddHXTrigger explicitly to
	// add more events (dedup keeps the set small).
	if rc != nil {
		rc.AddHXTrigger(strings.ToLower(f.name))
	}
	if _, err := sink.WriteString(`<div id="`); err != nil {
		return err
	}
	if _, err := sink.WriteString(html.EscapeString(f.oobID)); err != nil {
		return err
	}
	if _, err := sink.WriteString(`" hx-swap-oob="true">`); err != nil {
		return err
	}
	if _, err := sink.Write(buf.Bytes()); err != nil {
		return err
	}
	_, err := sink.WriteString(`</div>`)
	return err
}

// buildCtx constructs the ComponentContext for a render. oobID is
// passed through to the context so SetState/UseState can flag the
// frame dirty during execution. savedCtxPtr captures the
// cascading-context stack depth at this moment so the dispatch
// path can unwind any pushes made via ProvideContext during the
// component's render scope.
func (f *fluentComponent) buildCtx(w ByteWriter, rc *RenderContext, data any, oobID string) *ComponentContext {
	var ptr int
	if rc != nil {
		ptr = rc.ctxPtr
	}
	if f.withChildren {
		return &ComponentContext{
			Writer:      w,
			Context:     rc,
			Data:        data,
			Children:    extractChildren(data),
			Slots:       extractSlots(data),
			State:       getCompState(rc),
			DataMap:     asMap(data),
			oobID:       oobID,
			savedCtxPtr: ptr,
			name:        f.name,
		}
	}
	return &ComponentContext{
		Writer:      w,
		Context:     rc,
		Data:        data,
		State:       getCompState(rc),
		DataMap:     asMap(data),
		oobID:       oobID,
		savedCtxPtr: ptr,
		name:        f.name,
	}
}

// getCompState returns rc.ComponentState(), or nil if rc is nil.
func getCompState(rc *RenderContext) *State {
	if rc == nil {
		return nil
	}
	return rc.ComponentState()
}

// runBoundary invokes the panic recovery callback. The boundary
// receives a fresh ComponentContext pointing at the LIVE writer
// (not the discarded buffer) so its writes land directly in the
// response, replacing the failed component's bytes.
//
// If the boundary itself panics or returns an error, a generic
// `<!-- error boundary failed -->` placeholder is emitted so the
// page never crashes — this is the documented silent-fallback
// contract.
func (f *fluentComponent) runBoundary(w ByteWriter, rc *RenderContext, data any, panicVal any) (returnErr error) {
	// The boundary might also panic. Wrap its execution in a
	// deferred recover so even a buggy boundary can't crash the
	// page. The outer recover guarantees the response always
	// contains SOMETHING where the failed component was.
	defer func() {
		if r := recover(); r != nil {
			_, _ = w.WriteString(`<!-- error boundary failed -->`)
		}
	}()
	ctx := f.buildCtx(w, rc, data, f.oobID)
	if err := f.errBoundary(ctx, panicVal); err != nil {
		// Boundary returned an error rather than panicking.
		// Same fallback: write a generic placeholder so we don't
		// crash the page render.
		_, _ = w.WriteString(`<!-- error boundary failed -->`)
		return nil
	}
	return nil
}

// ComponentFuncs returns the per-component FuncMap if set.
func (f *fluentComponent) ComponentFuncs() template.FuncMap { return f.funcs }

// extractChildren pulls the Children data-map entry off the data
// before the user's Render sees it (the entry is consumed).
func extractChildren(data any) Children {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	if c, ok := m[ChildrenDataKey].(Children); ok {
		delete(m, ChildrenDataKey)
		return c
	}
	return nil
}

// extractSlots pulls the Slots data-map entry off the data.
func extractSlots(data any) Slots {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	if s, ok := m[SlotsDataKey].(Slots); ok {
		delete(m, SlotsDataKey)
		return s
	}
	return nil
}

// asMap returns the data as a map, or nil if it isn't one.
func asMap(data any) map[string]any {
	if m, ok := data.(map[string]any); ok {
		return m
	}
	return nil
}
