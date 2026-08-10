package engine

import "github.com/xDarkicex/nanite-render"

// HTML is the plain HTML engine adapter. It is the "no DSL" path:
// source HTML is parsed via render.ParseHTML, components dispatch
// through the SoA executor, and there is no data binding.
//
// Use this when you want raw HTML, no {{.Variables}} interpolation.
// For HTML with data binding, use HTMLTemplate.
type HTML struct {
	EngineName string
}

// NewHTML returns a plain HTML engine with the default name "html".
func NewHTML() *HTML { return &HTML{EngineName: "html"} }

// Name implements render.Engine.
func (h *HTML) Name() string {
	if h.EngineName == "" {
		return "html"
	}
	return h.EngineName
}

// Compile parses src via render.ParseHTML. The NodeStream is stored
// in Program.Nodes; the SoA executor walks it at render time.
func (h *HTML) Compile(src []byte, name string) (*render.Program, error) {
	b, err := render.ParseHTML(src, name)
	if err != nil {
		return nil, err
	}
	return &render.Program{
		Engine: h.Name(),
		Name:   name,
		Nodes:  b.Stream(),
	}, nil
}

// Execute walks the NodeStream via the SoA executor. Components
// dispatch through the active ComponentRegistry. Layout composition
// is handled via the Layout field.
func (h *HTML) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error {
	if p == nil {
		return nil
	}
	if p.Layout != nil && rc != nil {
		return render.Execute(p.Layout, w, rc, data)
	}
	return render.Execute(p, w, rc, data)
}
