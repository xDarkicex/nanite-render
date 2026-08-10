package engine

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestEngineName_EngineInstance renders via a concrete engine
// value — no name cast needed.
func TestEngineName_EngineInstance(t *testing.T) {
	src := `<p>hi</p>`
	reg := render.NewRegistry(NewHTML())
	loader := func(name string) ([]byte, error) { return []byte(src), nil }

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader

	err := reg.Page(rc).
		EngineInstance(NewHTML()).
		View("view").
		Render()
	if err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<p>hi</p>") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

// TestEngineName_EngineTyped renders via the typed constant.
func TestEngineName_EngineTyped(t *testing.T) {
	src := `<p>{{.Title}}</p>`
	reg := render.NewRegistry(NewHTMLTemplate())
	loader := func(name string) ([]byte, error) { return []byte(src), nil }

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader

	err := reg.Page(rc).
		Engine(render.EngineHTMLTemplate).
		View("view").
		With(map[string]any{"Title": "Hello"}).
		Render()
	if err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<p>Hello</p>") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}
