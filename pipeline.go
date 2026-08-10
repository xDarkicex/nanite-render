package render

import (
	"fmt"
	"io"
	"time"
)

// Pipeline is a thin hook layer for custom preprocessing. Each
// engine compiles its own source — nanite-render does not parse
// templates. The Pipeline is for users who want to inject
// preprocessing (e.g. sanitisation, i18n key expansion, custom
// FuncMap wrapping) around the engine's compile step.
//
// The pipeline is *not* templating-engine-specific. The hook points
// are: before-compile, after-compile, before-execute, after-execute.
//
// Most users do not need a Pipeline. The default flow is:
//
//	Registry.Part(name) → Engine.Compile → cache
//	Registry.Render(name) → Engine.Execute → ByteWriter
//
// The Pipeline is the seam for users who want to layer in their own
// behaviour at any of those points.
type Pipeline struct {
	// BeforeCompile runs before Engine.Compile. Useful for source
	// pre-processing (e.g. stripping comments, normalising whitespace).
	BeforeCompile func(name string, src []byte) ([]byte, error)

	// AfterCompile runs after Engine.Compile. Useful for additional
	// optimisation or instrumentation on the resulting *Program.
	AfterCompile func(p *Program) error

	// BeforeExecute runs before Engine.Execute. Useful for setting
	// up per-request FunMaps, layout decisions, or response headers.
	BeforeExecute func(p *Program, rc *RenderContext, data any) error

	// AfterExecute runs after Engine.Execute. Useful for measuring
	// render time or post-processing the writer's bytes.
	AfterExecute func(p *Program, rc *RenderContext, data any) error
}

// NewPipeline returns an empty Pipeline. Hooks are populated by the
// caller; missing hooks are skipped.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Debug returns a Pipeline that prints per-stage timing to w. The
// pipeline populates rc.Times so callers can read the map back
// programmatically. Mirrors GO-Portfolio's `times["jade"]`,
// `times["render-page"]`.
//
// Usage:
//
//	p := render.NewPipeline().Debug(os.Stderr)
//	reg.SetPipeline(p)
func (p *Pipeline) Debug(w io.Writer) *Pipeline {
	hook := func(stage string) func() time.Time {
		return func() time.Time {
			return time.Now()
		}
	}
	_ = hook // (placeholder for future expansion)
	after := func(stage string, start time.Time) {
		fmt.Fprintf(w, "[render] %s: %s\n", stage, time.Since(start))
	}
	p.BeforeCompile = func(name string, src []byte) ([]byte, error) {
		after("load:"+name, time.Now())
		return src, nil
	}
	p.AfterCompile = func(prog *Program) error {
		after("compile:"+prog.Name, time.Now())
		return nil
	}
	p.BeforeExecute = func(prog *Program, rc *RenderContext, _ any) error {
		if rc != nil && rc.Times != nil {
			rc.Times["execute_start"] = time.Since(time.Time{})
		}
		return nil
	}
	p.AfterExecute = func(prog *Program, rc *RenderContext, _ any) error {
		after("execute:"+prog.Name, time.Now())
		return nil
	}
	return p
}
