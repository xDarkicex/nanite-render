package engine

import (
	"html/template"

	"github.com/Joker/jade"

	"github.com/xDarkicex/nanite-render"
)

// Jade is the joker/jade engine adapter. Source .jade files are
// parsed to HTML, then compiled via html/template with the
// nanite-render superpowers (component, yield) injected. The
// resulting *template.Template is stored in Program.EngineData.
//
// At render time, Jade.Execute calls the html/template's Execute
// against the user-supplied data. The {{ component "Name" .Data }}
// and {{ yield }} superpowers are available throughout.
type Jade struct {
	EngineName string
	// Funcs is the engine-level FuncMap. Set once at construction;
	// merged with render.SuperpowerFuncs() at compile time.
	Funcs template.FuncMap
}

// NewJade returns a Jade engine with the default name "jade".
func NewJade() *Jade { return &Jade{EngineName: "jade"} }

// Name implements render.Engine.
func (j *Jade) Name() string {
	if j.EngineName == "" {
		return "jade"
	}
	return j.EngineName
}

// Compile parses src as jade, then compiles the result via
// html/template with the superpower FuncMap. The *template.Template
// is stored in Program.EngineData.
func (j *Jade) Compile(src []byte, name string) (*render.Program, error) {
	parsed, err := jade.Parse(name, src)
	if err != nil {
		return nil, err
	}
	funcs := mergeFuncs(j.Funcs, render.SuperpowerFuncs())
	tmpl, err := template.New(name).Funcs(funcs).Parse(parsed)
	if err != nil {
		return nil, err
	}
	return &render.Program{
		Engine:     j.Name(),
		Name:       name,
		EngineData: tmpl,
	}, nil
}

// Execute sets the per-request superpower state, then runs the
// compiled html/template against the user data. The FuncMap
// closures read from the per-goroutine state.
func (j *Jade) Execute(p *render.Program, w render.ByteWriter, rc *render.RenderContext, data any) error {
	if p == nil {
		return nil
	}
	tmpl, ok := p.EngineData.(*template.Template)
	if !ok {
		return &render.ParseError{Name: p.Name, Msg: "jade: invalid EngineData type"}
	}
	render.SetRenderState(rc)
	defer render.ClearRenderState()
	return tmpl.Execute(w, data)
}

// mergeFuncs combines the engine's funcs with the framework's
// superpowers. Engine funcs take precedence; superpowers can be
// overridden if the engine defines them.
func mergeFuncs(engineFuncs, superpowers template.FuncMap) template.FuncMap {
	out := make(template.FuncMap, len(engineFuncs)+len(superpowers))
	for k, v := range superpowers {
		out[k] = v
	}
	for k, v := range engineFuncs {
		out[k] = v
	}
	return out
}
