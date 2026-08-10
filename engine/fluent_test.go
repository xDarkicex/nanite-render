package engine

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestFluent_Render verifies that a component defined via the
// fluent API (Definition) renders correctly. The component's
// ComponentContext bundles the render arguments.
func TestFluent_Render(t *testing.T) {
	cr := render.NewComponentRegistry()

	cr.Define("GREET").
		WithFuncs(template.FuncMap{
			"name": func() string { return "World" },
		}).
		Render(func(c *render.ComponentContext) error {
			n, _ := c.Context.ComponentFuncMap["name"].(func() string)
			_, err := c.WriteString("<h1>Hello, " + n() + "!</h1>")
			return err
		}).
		Register(cr)

	c, _ := cr.Lookup("GREET")
	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	if err := c.Render(bw, rc, nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Hello, World!") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

// TestFluent_RenderChildren verifies that a component using
// RenderChildren receives children via the data map.
func TestFluent_RenderChildren(t *testing.T) {
	cr := render.NewComponentRegistry()

	cr.Define("WRAP").
		RenderChildren(func(c *render.ComponentContext) error {
			_, err := c.WriteString("<wrap>")
			if err != nil {
				return err
			}
			c.WriteChildren()
			_, err = c.WriteString("</wrap>")
			return err
		}).
		Register(cr)

	c, _ := cr.Lookup("WRAP")
	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	// Simulate children being pre-rendered to strings. The
	// dispatcher would do this; here we test the component's
	// read path.
	childData := map[string]any{
		render.ChildrenDataKey: render.Children{"hello ", "world"},
	}
	if err := c.Render(bw, rc, childData); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<wrap>") || !strings.Contains(out, "</wrap>") {
		t.Errorf("missing wrap tags: %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("missing children: %q", out)
	}
}
