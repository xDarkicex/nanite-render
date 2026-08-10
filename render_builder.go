package render

// RenderBuilder is a fluent builder for a render call. It hides the
// Composition struct and the RenderContext lifecycle behind a
// chainable API:
//
//	reg.Page(rc).
//	    Engine("jade").
//	    Layout("layouts/app").
//	    View("posts/show").
//	    Parts("partials/nav", "partials/footer").
//	    With(post).
//	    Render()
//
// The builder accumulates fields in a single struct; the chain
// methods mutate the receiver and return it, so no per-call
// allocations occur.
type RenderBuilder struct {
	reg    *Registry
	rc     *RenderContext
	engine string
	layout string
	view   string
	parts  []string
	data   any
	// onDone, if set, is called after Render/RenderComposition
	// completes. The nano adapter uses it to release the pooled
	// writer and context it acquired. Set via withDone.
	onDone func()
}

// WithDone installs a cleanup hook invoked after Render or
// RenderComposition returns. Returns the builder for chaining.
// Intended for adapters that manage pooled resources.
func (rb *RenderBuilder) WithDone(fn func()) *RenderBuilder {
	rb.onDone = fn
	return rb
}

// Page returns a fresh RenderBuilder bound to the given RenderContext
// and the Registry. The RenderContext is typically pooled (see
// AcquireContext); the caller owns its release unless Render is
// called via a nano adapter that manages the lifecycle.
func (r *Registry) Page(rc *RenderContext) *RenderBuilder {
	return &RenderBuilder{reg: r, rc: rc}
}

// Engine sets the engine by name. Chainable. Use the typed
// constants (EngineJade, EngineHTML, ...) or CustomEngine(name)
// for a user-defined engine.
//
//	reg.Page(rc).Engine(EngineJade).View("home").Render()
func (rb *RenderBuilder) Engine(name EngineName) *RenderBuilder {
	rb.engine = name.String()
	return rb
}

// EngineInstance sets the engine by value. The engine's Name() is
// derived automatically, so custom engines need no name cast.
// Chainable.
//
//	reg.Page(rc).EngineInstance(engine.NewJade()).View("home").Render()
func (rb *RenderBuilder) EngineInstance(e Engine) *RenderBuilder {
	if e != nil {
		rb.engine = e.Name()
	}
	return rb
}

// Layout sets the layout template name. Chainable.
func (rb *RenderBuilder) Layout(name string) *RenderBuilder {
	rb.layout = name
	return rb
}

// View sets the view template name. Chainable.
func (rb *RenderBuilder) View(name string) *RenderBuilder {
	rb.view = name
	return rb
}

// Parts adds partial template names to the composition. Multiple
// calls append. Chainable.
func (rb *RenderBuilder) Parts(parts ...string) *RenderBuilder {
	rb.parts = append(rb.parts, parts...)
	return rb
}

// With sets the render data. Chainable.
func (rb *RenderBuilder) With(data any) *RenderBuilder {
	rb.data = data
	return rb
}

// Render executes the composition.
//
//   - If Layout is set, it renders via RenderPageWith (layout wraps
//     view) using the configured engine.
//   - Otherwise it renders the view directly via Render.
//
// Returns the underlying Registry error if any. If a cleanup hook
// was installed (via WithDone), it runs after the render completes.
func (rb *RenderBuilder) Render() error {
	defer rb.runDone()
	if rb.rc == nil {
		return &ParseError{Msg: "RenderBuilder.Render: nil RenderContext"}
	}
	if rb.layout != "" && rb.view != "" {
		return rb.reg.RenderPageWith(rb.rc, rb.engine, rb.layout, rb.view, rb.data)
	}
	return rb.reg.Render(rb.rc, rb.engine, rb.view, rb.data)
}

// RenderComposition executes the full Composition (layout + view +
// parts). Use this when parts need to be rendered alongside the view.
func (rb *RenderBuilder) RenderComposition() error {
	defer rb.runDone()
	if rb.rc == nil {
		return &ParseError{Msg: "RenderBuilder.RenderComposition: nil RenderContext"}
	}
	return rb.reg.RenderComposition(rb.rc, Composition{
		Engine: rb.engine,
		Layout: rb.layout,
		View:   rb.view,
		Parts:  rb.parts,
	}, rb.data)
}

// runDone invokes the cleanup hook exactly once, if set.
func (rb *RenderBuilder) runDone() {
	if rb.onDone != nil {
		rb.onDone()
		rb.onDone = nil
	}
}
