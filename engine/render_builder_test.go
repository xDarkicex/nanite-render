package engine

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestRenderBuilder_View renders a single view via the fluent builder.
func TestRenderBuilder_View(t *testing.T) {
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
		Engine("html-template").
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

// TestRenderBuilder_LayoutView renders a layout + view composition.
func TestRenderBuilder_LayoutView(t *testing.T) {
	layout := `<html><body><h1>Layout</h1>{{ yield }}</body></html>`
	view := `<p>{{.Title}}</p>`
	reg := render.NewRegistry(NewHTMLTemplate())
	loader := func(name string) ([]byte, error) {
		switch name {
		case "layout":
			return []byte(layout), nil
		case "view":
			return []byte(view), nil
		}
		return nil, nil
	}

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader

	err := reg.Page(rc).
		Engine("html-template").
		Layout("layout").
		View("view").
		With(map[string]any{"Title": "Hello"}).
		Render()
	if err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<h1>Layout</h1>") {
		t.Errorf("missing layout: %q", out)
	}
	if !strings.Contains(out, "<p>Hello</p>") {
		t.Errorf("missing view: %q", out)
	}
}

// TestRenderBuilder_WithDone verifies the cleanup hook runs after
// Render.
func TestRenderBuilder_WithDone(t *testing.T) {
	src := `<p>x</p>`
	reg := render.NewRegistry(NewHTML())
	loader := func(name string) ([]byte, error) { return []byte(src), nil }

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.Loader = loader

	called := false
	err := reg.Page(rc).
		Engine("html").
		View("view").
		WithDone(func() { called = true }).
		Render()
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("WithDone hook was not called")
	}
}

// TestRenderBuilder_NilContext: Render on a nil context errors.
func TestRenderBuilder_NilContext(t *testing.T) {
	reg := render.NewRegistry()
	err := reg.Page(nil).Engine("html").View("view").Render()
	if err == nil {
		t.Fatal("expected error for nil RenderContext")
	}
}
