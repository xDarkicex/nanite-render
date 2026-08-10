package engine

import (
	"context"
	"sync"

	"github.com/a-h/templ"

	"github.com/xDarkicex/nanite-render"
)

// Templ is the templ engine adapter. Templ files are compiled to Go
// at build time by `templ generate`; the resulting templ.Component is
// registered by name and invoked through the nanite-render pipeline.
//
// Because Templ implements the full Engine interface, a templ
// component composes through Registry.Render: it is cached by
// (engine, name), runs through injectors + FuncMap, and is reachable
// from RenderPage. The component receives a context carrying the
// framework's *RenderContext, so it can use state, slots, children,
// and superpowers exactly like a component on any other engine:
//
//	rc := render.FromContext(ctx)
//	count, setCount := render.UseState(rc.ComponentState(), "count", 0)
//
// Register components with:
//
//	t := engine.NewTempl()
//	t.Register("home", func(data any) templ.Component { return Home(data) })
//	reg.AddEngine(t)
//	reg.Render(rc, render.EngineTempl, "home", data)
type Templ struct {
	EngineName string
	components map[string]TemplComponent
	layouts    map[string]TemplLayout
	mu         sync.RWMutex
}

// TemplComponent wraps a templ.Component so the engine interface is
// preserved. The data value is passed to Component.Render.
type TemplComponent func(data any) templ.Component

// TemplLayout is a templ layout: a function that wraps a child
// component. This is templ's native Layout(view) pattern — the layout
// renders its own chrome and injects the child where it wants.
//
//	t.RegisterLayout("layouts/app", func(child templ.Component) templ.Component {
//	    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
//	        io.WriteString(w, "<html><body>")
//	        child.Render(ctx, w)   // the view, injected natively
//	        io.WriteString(w, "</body></html>")
//	        return nil
//	    })
//	})
type TemplLayout func(children templ.Component) templ.Component

// NewTempl returns a templ engine adapter that resolves Programs from
// pre-compiled templ components.
func NewTempl() *Templ {
	return &Templ{
		EngineName: "templ",
		components: make(map[string]TemplComponent),
		layouts:    make(map[string]TemplLayout),
	}
}

// Register associates a templ component with a name.
func (t *Templ) Register(name string, c TemplComponent) {
	t.mu.Lock()
	t.components[name] = c
	t.mu.Unlock()
}

// RegisterLayout associates a templ layout with a name. Layouts wrap a
// child component (the view) natively — see TemplLayout. Combined with
// Register, it enables Registry.RenderPageWith(rc, EngineTempl, ...).
func (t *Templ) RegisterLayout(name string, l TemplLayout) {
	t.mu.Lock()
	t.layouts[name] = l
	t.mu.Unlock()
}

// Name implements render.Engine.
func (t *Templ) Name() string {
	if t.EngineName == "" {
		return "templ"
	}
	return t.EngineName
}

// NoSource implements render.Sourceless: templ components are
// pre-compiled and registered, so the registry does not require a
// Loader on the RenderContext for this engine.
func (t *Templ) NoSource() {}

// Compile implements render.Engine. Templ programs are registered,
// not compiled from source — Compile returns a cached Program whose
// Name dispatches to the registered component at Execute time.
func (t *Templ) Compile(_ []byte, name string) (*render.Program, error) {
	t.mu.RLock()
	_, ok := t.components[name]
	t.mu.RUnlock()
	if !ok {
		return nil, errTemplNotRegistered
	}
	return &render.Program{Engine: t.Name(), Name: name}, nil
}

// Execute implements render.Engine. It looks up the registered
// component by the program's name, bridges the framework's
// *RenderContext into the templ context, and renders.
func (t *Templ) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error {
	if p == nil {
		return nil
	}
	t.mu.RLock()
	c, ok := t.components[p.Name]
	t.mu.RUnlock()
	if !ok {
		return errTemplNotRegistered
	}
	comp := c(data)
	// Bridge the framework context so the templ component can use
	// state, slots, children, and superpowers.
	ctx := render.WithContext(context.Background(), rc)
	return comp.Render(ctx, w)
}

// RenderPage implements render.PageComposer: native templ composition.
// The view component (registered via Register) is rendered with data;
// the layout component (registered via RegisterLayout) wraps it. Both
// receive the framework context (state, slots, superpowers) through
// the bridge. This is templ's Layout(view) pattern, first-class.
//
//	eng.Register("view", func(data any) templ.Component { return View(data) })
//	eng.RegisterLayout("layouts/app", func(child templ.Component) templ.Component {
//	    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
//	        io.WriteString(w, "<html><body>")
//	        child.Render(ctx, w)
//	        io.WriteString(w, "</body></html>")
//	        return nil
//	    })
//	})
//	reg.RenderPageWith(rc, render.EngineTempl, "layouts/app", "view", data)
func (t *Templ) RenderPage(rc *render.RenderContext, w render.ByteWriter, layout, view string, data any) error {
	t.mu.RLock()
	v, okV := t.components[view]
	l, okL := t.layouts[layout]
	t.mu.RUnlock()
	if !okV {
		return errTemplNotRegistered
	}
	if !okL {
		return &render.ParseError{Name: layout, Msg: "templ: layout not registered"}
	}
	// Native composition: layout wraps the view component.
	comp := l(v(data))
	ctx := render.WithContext(context.Background(), rc)
	return comp.Render(ctx, w)
}

// Render invokes the registered templ component directly (bypassing
// the registry pipeline). Prefer Registry.Render so caching and
// injectors apply; Render exists for direct-use cases.
func (t *Templ) Render(name string, w render.ByteWriter, data any) error {
	t.mu.RLock()
	c, ok := t.components[name]
	t.mu.RUnlock()
	if !ok {
		return errTemplNotRegistered
	}
	comp := c(data)
	return comp.Render(render.WithContext(context.Background(), nil), w)
}

var (
	errTemplNoCompile     = &render.ParseError{Msg: "templ: Compile is not supported; use Register"}
	errTemplNotRegistered = &render.ParseError{Msg: "templ: component not registered"}
)
