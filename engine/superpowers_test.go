package engine

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestSuperpowers_Yield verifies that the yield() FuncMap is wired
// into the html/template engine and returns the pre-rendered view.
func TestSuperpowers_Yield(t *testing.T) {
	layout := `<html><body><h1>Layout</h1>{{ yield }}</body></html>`
	view := `<p>{{.Title}}</p>`

	r := render.NewRegistry(NewHTML(), NewHTMLTemplate())
	loader := func(name string) ([]byte, error) {
		switch name {
		case "layout":
			return []byte(layout), nil
		case "view":
			return []byte(view), nil
		}
		return nil, nil
	}

	// 1. Pre-render the view via the html-template engine.
	var viewBuf bytes.Buffer
	viewBw := render.AcquireWriter(&viewBuf)
	defer render.ReleaseWriter(viewBw)
	viewRc := render.AcquireContext(viewBw, &http.Request{})
	defer render.ReleaseContext(viewRc)
	viewRc.Loader = loader
	viewRc.SetComponentRegistry(render.NewComponentRegistry())
	if err := r.Render(viewRc, "html-template", "view", map[string]any{"Title": "Hello"}); err != nil {
		t.Fatal(err)
	}
	if err := viewBw.Flush(); err != nil {
		t.Fatal(err)
	}
	viewBytes := viewBuf.Bytes()

	// 2. Render the layout with viewBytes set in the render state.
	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader
	rc.SetComponentRegistry(render.NewComponentRegistry())
	rc.ViewBytes = viewBytes

	if err := r.Render(rc, "html-template", "layout", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<h1>Layout</h1>") {
		t.Errorf("missing layout header: %s", out)
	}
	if !strings.Contains(out, "<p>Hello</p>") {
		t.Errorf("missing rendered view: %s", out)
	}
}

// TestSuperpowers_Component verifies that {{ component "Name" .Data }}
// dispatches to the registered ComponentRegistry.
func TestSuperpowers_Component(t *testing.T) {
	layout := `<html><body>{{ component "GREETING" . }}</body></html>`

	r := render.NewRegistry(NewHTMLTemplate())
	loader := func(name string) ([]byte, error) {
		return []byte(layout), nil
	}

	cr := render.NewComponentRegistry()
	cr.Register("GREETING", render.ComponentFunc(func(w render.ByteWriter, rc *render.RenderContext, data any) error {
		_, err := w.WriteString("<h1>Hi!</h1>")
		return err
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader
	rc.SetComponentRegistry(cr)

	if err := r.Render(rc, "html-template", "layout", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<h1>Hi!</h1>") {
		t.Errorf("missing component output: %s", out)
	}
}

// TestPlainHTML_NoInterpolation confirms that the plain HTML engine
// does NOT evaluate {{.Var}}. Plain HTML is raw — use HTMLTemplate
// if you need data binding.
func TestPlainHTML_NoInterpolation(t *testing.T) {
	src := `<p>{{.Title}}</p>`

	r := render.NewRegistry(NewHTML())
	loader := func(name string) ([]byte, error) { return []byte(src), nil }

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader

	if err := r.Render(rc, "html", "view", map[string]any{"Title": "Hello"}); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "{{.Title}}") {
		t.Errorf("plain HTML should not interpolate: %s", out)
	}
	if strings.Contains(out, "Hello") {
		t.Errorf("plain HTML should not contain interpolated data: %s", out)
	}
}
