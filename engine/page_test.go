package engine

import (
	"bytes"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestRenderPage_LayoutViewComponent demonstrates the full workflow:
//  1. Register a Navbar component.
//  2. Define a layout and view as HTML strings.
//  3. Render with RenderPage.
//  4. Verify the output contains both the layout chrome and the view
//     body, and that the component was dispatched.
func TestRenderPage_LayoutViewComponent(t *testing.T) {
	// 1. Register a Navbar component.
	cr := render.NewComponentRegistry()
	navbar := render.ComponentFunc(func(w render.ByteWriter, rc *render.RenderContext, data any) error {
		_, err := w.WriteString("<nav>Navbar</nav>")
		return err
	})
	cr.Register("NAVBAR", navbar)

	// 2. Define layout and view.
	layout := `<html><body><NAVBAR/><YIELD/><FOOTER/></body></html>`
	view := `<h1>Hello</h1><p>World</p>`

	// 3. Set up the registry.
	reg := render.NewRegistry(NewHTML())
	reg.AttachComponents(cr)

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
	rc := render.AcquireContext(bw, nil)
	rc.Loader = loader
	rc.SetComponentRegistry(cr)

	// 4. Render.
	if err := reg.RenderPage(rc, "layout", "view", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}

	// 5. Verify.
	out := buf.String()
	t.Logf("output: %s", out)
	if !bytes.Contains([]byte(out), []byte("<nav>Navbar</nav>")) {
		t.Errorf("missing NAVBAR component in output: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("<h1>Hello</h1>")) {
		t.Errorf("missing view body in output: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("<p>World</p>")) {
		t.Errorf("missing view body in output: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("missing component: FOOTER")) {
		t.Errorf("missing FOOTER placeholder: %s", out)
	}
}

// TestRenderPage_Variants confirms RenderPage propagates variants.
func TestRenderPage_Variants(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Register("GREETING", render.ComponentFunc(func(w render.ByteWriter, rc *render.RenderContext, data any) error {
		_, err := w.WriteString("Hello from greeting")
		return err
	}))
	layout := `<html><YIELD/></html>`
	view := `<GREETING/>`

	reg := render.NewRegistry(NewHTML())
	reg.AttachComponents(cr)
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
	rc := render.AcquireContext(bw, nil)
	rc.Loader = loader
	rc.SetComponentRegistry(cr)

	if err := reg.RenderPage(rc, "layout", "view", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Hello from greeting")) {
		t.Errorf("missing greeting: %s", buf.String())
	}
}
