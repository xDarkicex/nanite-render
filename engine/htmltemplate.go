package engine

import (
	"html/template"

	"github.com/xDarkicex/nanite-render"
)

// HTMLTemplate is the engine adapter for HTML templates that need
// data binding via html/template. Source HTML is parsed via
// html/template with the nanite-render superpowers (component,
// yield) injected. The resulting *template.Template is stored
// in Program.EngineData.
//
// This is the bridge for users who want plain HTML with
// `{{.Variables}}` interpolation. The plain HTML engine (no data
// binding) is used when the user wants raw HTML without templating.
type HTMLTemplate struct {
	EngineName string
	Funcs      template.FuncMap
}

// NewHTMLTemplate returns an html/template-based HTML engine with
// the default name "html-template".
func NewHTMLTemplate() *HTMLTemplate { return &HTMLTemplate{EngineName: "html-template"} }

// Name implements render.Engine.
func (h *HTMLTemplate) Name() string {
	if h.EngineName == "" {
		return "html-template"
	}
	return h.EngineName
}

// Compile parses src via html/template with the superpower FuncMap.
// The *template.Template is stored in Program.EngineData.
func (h *HTMLTemplate) Compile(src []byte, name string) (*render.Program, error) {
	funcs := mergeFuncs(h.Funcs, render.SuperpowerFuncs())
	tmpl, err := template.New(name).Funcs(funcs).Parse(string(src))
	if err != nil {
		return nil, err
	}
	return &render.Program{
		Engine:     h.Name(),
		Name:       name,
		EngineData: tmpl,
	}, nil
}

// Execute sets the per-request superpower state, then runs the
// compiled html/template against the user data.
func (h *HTMLTemplate) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error {
	if p == nil {
		return nil
	}
	tmpl, ok := p.EngineData.(*template.Template)
	if !ok {
		return &render.ParseError{Name: p.Name, Msg: "html-template: invalid EngineData type"}
	}
	render.SetRenderState(rc)
	defer render.ClearRenderState()
	return tmpl.Execute(w, data)
}
