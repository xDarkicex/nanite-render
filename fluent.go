package render

import (
	"html/template"
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

// SetState writes a value to the per-render state.
func (c *ComponentContext) SetState(key string, v any) {
	if c == nil || c.State == nil {
		return
	}
	c.State.Set(key, v)
}

// UseState is the React-style hook. It returns the current value for
// key (initial on first use) and a setter that writes back. The setter
// affects subsequent renders; the current render keeps its value.
//
// For a typed variant use render.UseState[T](c.State, key, initial).
func (c *ComponentContext) UseState(key string, initial any) (any, func(any)) {
	if c == nil || c.State == nil {
		return initial, func(any) {}
	}
	return c.State.UseState(key, initial), func(v any) { c.State.Set(key, v) }
}

// Render dispatches a named, registered component inline, writing to
// this context's writer. The component receives a fresh
// ComponentContext (same state, its own data). This is the
// `{{ component "Name" . }}` superpower as a method — usable from any
// component on any engine.
func (c *ComponentContext) Render(name string, props any) error {
	return dispatchComponent(c.Writer, c.Context, name, props)
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
	}
	cr.Register(d.name, c)
}

// fluentComponent adapts a ComponentRenderFunc to the Component
// interface. It builds a ComponentContext on each render and
// delegates to the user's function.
type fluentComponent struct {
	name         string
	fn           ComponentRenderFunc
	funcs        template.FuncMap
	withChildren bool
}

// Render implements Component. It builds the ComponentContext from
// the render arguments and delegates to the user's function. It is
// nil-safe for rc: superpower dispatches (e.g. componentSuperpower)
// pass nil rc, so the fluent component tolerates a nil context and
// only sets state/slots when rc is present.
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

	if f.withChildren {
		children := extractChildren(data)
		slots := extractSlots(data)
		ctx := &ComponentContext{
			Writer:   w,
			Context:  rc,
			Data:     data,
			Children: children,
			Slots:    slots,
			State:    getCompState(rc),
			DataMap:  asMap(data),
		}
		return f.fn(ctx)
	}
	ctx := &ComponentContext{
		Writer:   w,
		Context:  rc,
		Data:     data,
		State:    getCompState(rc),
		DataMap:  asMap(data),
	}
	return f.fn(ctx)
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
