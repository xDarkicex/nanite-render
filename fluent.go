package render

import (
	"bytes"
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
}

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

// Register finalises the definition and registers it with the
// ComponentRegistry.
func (d *Definition) Register(cr *ComponentRegistry) {
	if d.name == "" || d.render == nil {
		return
	}
	// Build a Component that delegates to the fluent function.
	c := &fluentComponent{
		name:         d.name,
		fn:           d.render,
		funcs:        d.funcs,
		withChildren: d.withChildren,
		oobID:        d.oobID,
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
	funcs        template.FuncMap
	withChildren bool
	oobID        string
}

// OOBID implements OOBOptioner. Returns the target DOM element id
// when the component was declared with WithOOB(), or "" otherwise.
func (f *fluentComponent) OOBID() string { return f.oobID }

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

	// Fast path: no OOB → render directly to the original writer.
	if f.oobID == "" {
		ctx := f.buildCtx(w, rc, data, "")
		return f.fn(ctx)
	}

	// OOB path: render to a pooled buffer, then decide whether to
	// wrap based on whether SetState was called during the render.
	buf := acquireOOBBuffer()
	defer releaseOOBBuffer(buf)
	bw := AcquireWriter(buf)
	defer ReleaseWriter(bw)

	ctx := f.buildCtx(bw, rc, data, f.oobID)
	if err := f.fn(ctx); err != nil {
		return err
	}
	// Drain the wrapper buffer so its bytes are visible.
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
// frame dirty during execution.
func (f *fluentComponent) buildCtx(w ByteWriter, rc *RenderContext, data any, oobID string) *ComponentContext {
	if f.withChildren {
		return &ComponentContext{
			Writer:   w,
			Context:  rc,
			Data:     data,
			Children: extractChildren(data),
			Slots:    extractSlots(data),
			State:    getCompState(rc),
			DataMap:  asMap(data),
			oobID:    oobID,
		}
	}
	return &ComponentContext{
		Writer:  w,
		Context: rc,
		Data:    data,
		State:   getCompState(rc),
		DataMap: asMap(data),
		oobID:   oobID,
	}
}

// getCompState returns rc.ComponentState(), or nil if rc is nil.
func getCompState(rc *RenderContext) *State {
	if rc == nil {
		return nil
	}
	return rc.ComponentState()
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
