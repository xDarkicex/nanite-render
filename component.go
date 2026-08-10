package render

import (
	"bytes"
	"context"
	"html/template"
	"io"
	"sync"
	"sync/atomic"
)

// memoComponent wraps a Component so its rendered output is cached by
// key. On a cache hit it writes the stored HTML directly; on a miss it
// renders to a buffer, stores, and writes. Created by
// ComponentRegistry.Memoize.
type memoComponent struct {
	name  string
	inner Component
	keyer func(rc *RenderContext, data any) string
	reg   *ComponentRegistry
}

// Render implements Component.
func (m *memoComponent) Render(w ByteWriter, rc *RenderContext, data any) error {
	key := m.keyer(rc, data)
	if key != "" {
		if html, ok := m.reg.memoHTML(m.name, key); ok {
			_, err := w.WriteString(html)
			return err
		}
	}
	// Render to a buffer.
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	err := m.inner.Render(bw, rc, data)
	if ferr := bw.Flush(); err == nil {
		err = ferr
	}
	if err == nil && key != "" {
		m.reg.memoStore(m.name, key, buf.String())
	}
	_, werr := w.Write(buf.Bytes())
	if err != nil {
		return err
	}
	return werr
}

// Component is a renderable component. Components are registered by
// name and referenced from templates via `<NAVBAR data={...} />` (temmpl),
// `+navbar(data)` (jade), or `{{ template "navbar" . }}` (html/template).
//
// The Component interface is engine-agnostic: each engine adapter
// translates its native component syntax into a Component reference
// in the NodeStream. The executor dispatches to the registered
// Component when the node has one set.
type Component interface {
	Render(w ByteWriter, rc *RenderContext, data any) error
}

// ComponentFunc is a function adapter for Component.
type ComponentFunc func(w ByteWriter, rc *RenderContext, data any) error

// Render implements Component.
func (f ComponentFunc) Render(w ByteWriter, rc *RenderContext, data any) error {
	return f(w, rc, data)
}

// ComponentWithFuncs is an optional interface a Component can
// implement to provide a component-specific FuncMap. The FuncMap
// is set on the RenderContext for the duration of this component's
// render. This is the per-component equivalent of render.SuperpowerFuncs
// (which is global).
//
// Components that compose themselves via html/template (or templ)
// can use this to expose helpers scoped to the component.
type ComponentWithFuncs interface {
	Component
	ComponentFuncs() template.FuncMap
}

// componentWithFuncs adapts a ComponentFunc with a FuncMap into
// a Component. The FuncMap is set on the RenderContext for the
// duration of the component's render.
type componentWithFuncs struct {
	Fn    ComponentFunc
	Funcs template.FuncMap
}

// WithFuncs returns a Component that wraps the ComponentFunc with
// the given FuncMap. Use it when registering a component:
//
//	cr.Register("NAVBAR", render.ComponentFunc(myFunc).WithFuncs(
//	    template.FuncMap{
//	        "currentUser": func() any { return loadUser() },
//	    },
//	))
func (f ComponentFunc) WithFuncs(funcs template.FuncMap) Component {
	return &componentWithFuncs{Fn: f, Funcs: funcs}
}

// Render implements Component.
func (c *componentWithFuncs) Render(w ByteWriter, rc *RenderContext, data any) error {
	return c.Fn(w, rc, data)
}

// ComponentFuncs implements ComponentWithFuncs.
func (c *componentWithFuncs) ComponentFuncs() template.FuncMap {
	return c.Funcs
}

// renderComponentWithFuncMap is the canonical render path. It sets
// the per-component FuncMap on the RenderContext (if the component
// implements ComponentWithFuncs), calls Render, and restores the
// previous FuncMap. All callers (SoA executor, html/template
// superpower, direct) should use this helper. Nil-safe for rc
// (superpower dispatches may pass nil rc).
func renderComponentWithFuncMap(c Component, w ByteWriter, rc *RenderContext, data any) error {
	if rc != nil {
		prev := rc.ComponentFuncMap
		if cf, ok := c.(ComponentWithFuncs); ok {
			rc.ComponentFuncMap = cf.ComponentFuncs()
		} else {
			rc.ComponentFuncMap = nil
		}
		defer func() { rc.ComponentFuncMap = prev }()
	}
	return c.Render(w, rc, data)
}

// Children is the pre-rendered HTML of a component's children. Each
// entry is a string chunk that the parent component can inject into
// its layout (React-style composition).
type Children []string

// ChildrenDataKey is the data-map key where the dispatcher stores
// a component's pre-rendered children. Components that compose via
// data maps can read this key to inject the children into their
// output:
//
//	cr.Register("Parent", render.ComponentFunc(
//	    func(w, rc, data) error {
//	        children := data[render.ChildrenDataKey].(render.Children)
//	        for _, child := range children {
//	            w.WriteString(child)
//	        }
//	        return nil
//	    },
//	))
const ChildrenDataKey = "__children__"

// ComponentWithChildren is an optional interface a Component can
// implement to receive pre-rendered children. The children are
// computed by the dispatcher (the SoA executor walks the parent
// node's children; the html/template engine receives the rendered
// inner content). A component that implements this interface gets
// a direct call with the children list.
type ComponentWithChildren interface {
	Component
	RenderWithChildren(w ByteWriter, rc *RenderContext, data any, children Children) error
}

// renderComponent dispatches to RenderWithChildren if the component
// implements it, otherwise to Render. The children are passed via
// ChildrenDataKey in the data map for components that don't.
func renderComponent(c Component, w ByteWriter, rc *RenderContext, data any, children Children) error {
	if len(children) == 0 {
		return renderComponentWithFuncMap(c, w, rc, data)
	}
	if cw, ok := c.(ComponentWithChildren); ok {
		// Direct call: parent receives the children list.
		prev := rc.ComponentFuncMap
		if cf, ok := c.(ComponentWithFuncs); ok {
			rc.ComponentFuncMap = cf.ComponentFuncs()
		} else {
			rc.ComponentFuncMap = nil
		}
		defer func() { rc.ComponentFuncMap = prev }()
		return cw.RenderWithChildren(w, rc, data, children)
	}
	// Data-map call: stash children in the data map.
	if data == nil {
		data = make(map[string]any)
	}
	m, ok := data.(map[string]any)
	if !ok {
		wrapped := map[string]any{"_data": data}
		m = wrapped
	}
	m[ChildrenDataKey] = children
	data = m
	return renderComponentWithFuncMap(c, w, rc, data)
}

// dispatchComponent looks up a named component in the registry and
// renders it, constructing a fresh ComponentContext. It is the single
// entry point every engine uses to render a component inline:
//
//   - html/template: the `{{ component "Name" . }}` superpower
//   - plain HTML:      the SoA `<Name/>` executor
//   - templ:           render.Component(ctx, w, "Name", props)
//   - any component:   c.Render("Name", props)
func dispatchComponent(w ByteWriter, rc *RenderContext, name string, props any) error {
	if rc == nil {
		return nil
	}
	cr := rc.ComponentRegistry()
	if cr == nil {
		return nil
	}
	c, ok := cr.Lookup(name)
	if !ok {
		return nil
	}
	return renderComponent(c, w, rc, props, nil)
}

// OOBOptioner is an optional interface a Component implements to
// participate in HTMX-style Out-of-Band swaps. Components that opt
// in via OOBID() have their rendered output wrapped in
//
//	<div id="<oob-id>" hx-swap-oob="true">...</div>
//
// when their render mutates state via SetState / UseState. The id is
// a hard DOM contract — it must match the `id` attribute of the
// element the swap replaces.
//
// Engines that pre-compile components (templ, html/template) can
// implement OOBOptioner directly without using the fluent builder.
// The fluent builder exposes this via Definition.WithOOB(id).
type OOBOptioner interface {
	Component
	OOBID() string
}

// RenderComponent renders a named, registered component inline,
// writing to w. It recovers the *RenderContext from ctx (see
// WithContext), so a templ component can compose a nanite-render
// component like this:
//
//	rc := render.FromContext(ctx)
//	render.RenderComponent(ctx, w, "Navbar", props)
//
// w may be any io.Writer (templ components receive io.Writer); if it
// is not a ByteWriter, a pooled writer is used and released.
func RenderComponent(ctx context.Context, w io.Writer, name string, props any) error {
	rc := FromContext(ctx)
	if rc == nil {
		return nil
	}
	bw, ok := w.(ByteWriter)
	if !ok {
		bw = AcquireWriter(w)
		defer ReleaseWriter(bw)
	}
	return dispatchComponent(bw, rc, name, props)
}

// ComponentRegistry is the user-facing façade for components.
// Components are registered by name and looked up by name. The
// executor reaches into the registry at render time.
//
// The registry also holds per-component memo caches (see Memoize):
// name → key → rendered HTML. Memoized components skip re-rendering
// when their key repeats, mirroring GO-Portfolio's per-part cache.
type ComponentRegistry struct {
	components atomic.Pointer[map[string]Component]

	memoMu sync.Mutex
	// memo: name → (key → rendered HTML).
	memo map[string]map[string]string
}

// NewComponentRegistry returns an empty ComponentRegistry.
func NewComponentRegistry() *ComponentRegistry {
	r := &ComponentRegistry{}
	empty := map[string]Component{}
	r.components.Store(&empty)
	return r
}

// Memoize wraps the component registered under name so its rendered
// HTML is cached by key. keyer maps (rc, data) to a cache key; when
// two renders produce the same key, the second writes the cached HTML
// without re-rendering the component. Return "" from keyer to disable
// caching for a particular render.
//
// This is the GO-Portfolio per-part cache applied at the component
// level. Use it for components whose output is data-independent or
// keyable by a small digest (e.g. a heavily reused navbar or a
// component that renders an expensive query result keyed by an id).
//
//	cr.Memoize("Navbar", func(rc *render.RenderContext, data any) string {
//	    return "static" // data-independent → render once, cache forever
//	})
//	cr.Memoize("UserCard", func(rc *render.RenderContext, data any) string {
//	    return data.(UserCardProps).ID
//	})
func (r *ComponentRegistry) Memoize(name string, keyer func(rc *RenderContext, data any) string) {
	c, ok := r.Lookup(name)
	if !ok {
		return
	}
	if keyer == nil {
		return
	}
	r.Register(name, &memoComponent{name: name, inner: c, keyer: keyer, reg: r})
}

// memoHTML returns the cached HTML for (name, key), or "" if absent.
func (r *ComponentRegistry) memoHTML(name, key string) (string, bool) {
	r.memoMu.Lock()
	defer r.memoMu.Unlock()
	m := r.memo[name]
	if m == nil {
		return "", false
	}
	h, ok := m[key]
	return h, ok
}

// memoStore caches the HTML for (name, key).
func (r *ComponentRegistry) memoStore(name, key, html string) {
	r.memoMu.Lock()
	defer r.memoMu.Unlock()
	if r.memo == nil {
		r.memo = make(map[string]map[string]string)
	}
	m := r.memo[name]
	if m == nil {
		m = make(map[string]string)
		r.memo[name] = m
	}
	m[key] = html
}

// Register adds a component under the given name.
func (r *ComponentRegistry) Register(name string, c Component) {
	for {
		old := r.components.Load()
		newMap := make(map[string]Component, len(*old)+1)
		for k, v := range *old {
			newMap[k] = v
		}
		newMap[name] = c
		if r.components.CompareAndSwap(old, &newMap) {
			return
		}
	}
}

// Lookup returns the component registered under the given name.
func (r *ComponentRegistry) Lookup(name string) (Component, bool) {
	m := r.components.Load()
	if m == nil {
		return nil, false
	}
	c, ok := (*m)[name]
	return c, ok
}

// Names returns the registered component names.
func (r *ComponentRegistry) Names() []string {
	m := r.components.Load()
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(*m))
	for k := range *m {
		names = append(names, k)
	}
	return names
}

// Define returns a fluent Definition builder for a component. The
// component is registered with this ComponentRegistry when
// Register is called on the Definition.
//
// Usage:
//
//	cr.Define("NAVBAR").
//	    WithFuncs(template.FuncMap{"currentUser": ...}).
//	    RenderChildren(func(c *render.ComponentContext) error {
//	        c.WriteString("<nav>")
//	        c.WriteChildren()
//	        c.WriteString("</nav>")
//	        return nil
//	    }).
//	    Register(cr)
func (r *ComponentRegistry) Define(name string) *Definition {
	return &Definition{name: name}
}

// Attach binds a ComponentRegistry to a Registry. Components are
// reachable via rc.Components() in the executor.
func (r *Registry) AttachComponents(c *ComponentRegistry) {
	r.components.Store(c)
}

// Components returns the attached ComponentRegistry, or nil.
func (r *Registry) Components() *ComponentRegistry {
	return r.components.Load()
}

// ComponentLookup is a convenience used by the executor and engines
// to find a component by name.
func (r *Registry) ComponentLookup(name string) (Component, bool) {
	c := r.components.Load()
	if c == nil {
		return nil, false
	}
	return c.Lookup(name)
}
