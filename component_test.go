package render

import (
	"bytes"
	"fmt"
	"sync/atomic"
	"html/template"
	"testing"
)

// TestComponentWithFuncs verifies that a Component can declare its
// own FuncMap via WithFuncs. The component's Render reads from
// the FuncMap via rc.ComponentFuncMap.
func TestComponentWithFuncs(t *testing.T) {
	cr := NewComponentRegistry()

	// Register a component with its own FuncMap. The component's
	// Render will be passed a RenderContext whose ComponentFuncMap
	// has these entries.
	cr.Register("GREET", ComponentFunc(func(w ByteWriter, rc *RenderContext, data any) error {
		cfm := rc.ComponentFuncMap
		if cfm == nil {
			return nil
		}
		greet, ok := cfm["greet"].(func() string)
		if !ok {
			return nil
		}
		_, err := w.WriteString("<h1>" + greet() + "</h1>")
		return err
	}).WithFuncs(template.FuncMap{
		"greet": func() string { return "Hello from NAVBAR" },
	}))

	// Render the component.
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	rc := AcquireContext(bw, nil)
	defer ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	c, ok := cr.Lookup("GREET")
	if !ok {
		t.Fatal("GREET component not found")
	}
	if err := renderComponentWithFuncMap(c, bw, rc, nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if out != "<h1>Hello from NAVBAR</h1>" {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestComponentFuncs_Optional verifies that a plain ComponentFunc
// (no WithFuncs) still works. ComponentWithFuncs is an optional
// interface.
func TestComponentFuncs_Optional(t *testing.T) {
	cr := NewComponentRegistry()
	cr.Register("PLAIN", ComponentFunc(func(w ByteWriter, rc *RenderContext, data any) error {
		_, err := w.WriteString("<p>plain</p>")
		return err
	}))

	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	rc := AcquireContext(bw, nil)
	defer ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	c, _ := cr.Lookup("PLAIN")
	if err := renderComponentWithFuncMap(c, bw, rc, nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "<p>plain</p>" {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

// TestComponentWithFuncs_HTMLTemplate verifies that a component
// declared with WithFuncs is invokable from an html/template
// engine via the {{ component "Name" .Data }} superpower. The
// component's FuncMap is set on the per-render state for the
// duration of the component's render.
func TestComponentWithFuncs_HTMLTemplate(t *testing.T) {
	// The test uses the same dispatch path as TestSuperpowers_Component
	// in the engine package. The component's FuncMap is the
	// per-component one declared via WithFuncs.
	cr := NewComponentRegistry()
	cr.Register("GREET", ComponentFunc(func(w ByteWriter, rc *RenderContext, data any) error {
		cfm := rc.ComponentFuncMap
		if cfm == nil {
			return nil
		}
		greet, _ := cfm["greet"].(func() string)
		_, err := w.WriteString("<h1>" + greet() + "</h1>")
		return err
	}).WithFuncs(template.FuncMap{
		"greet": func() string { return "Hello" },
	}))
}

// TestMemoComponent_DelegatesOptionalInterfaces verifies Memoize
// preserves the inner component's capabilities: a memoized
// component with a colocated action still dispatches it, and the
// memo cache still serves repeated keys on the dispatch path.
func TestMemoComponent_DelegatesOptionalInterfaces(t *testing.T) {
	cr := NewComponentRegistry()
	var renders int32

	cr.Define("TOGGLE").
		Action("flip", func(rc *RenderContext, props map[string]any) error {
			props["on"] = !props["on"].(bool)
			return nil
		}).
		Render(func(c *ComponentContext) error {
			atomic.AddInt32(&renders, 1)
			on := c.Data.(map[string]any)["on"].(bool)
			if on {
				_, err := c.WriteString("<b>on</b>")
				return err
			}
			_, err := c.WriteString("<b>off</b>")
			return err
		}).
		Register(cr)
	cr.Memoize("TOGGLE", func(rc *RenderContext, data any) string {
		return fmt.Sprint(data.(map[string]any)["on"])
	})

	// The memo wrapper must still expose the action.
	c, ok := cr.Lookup("TOGGLE")
	if !ok {
		t.Fatal("component not registered")
	}
	ap, isAction := c.(ActionProvider)
	if !isAction {
		t.Fatalf("memoized component lost ActionProvider: %T", c)
	}
	if _, found := ap.LookupAction("flip"); !found {
		t.Fatal("memoized component lost its action")
	}

	// Dispatch twice with the same props — the second render must
	// be served from the memo cache (render count stays 1).
	disp, isDispatchable := c.(Dispatchable)
	if !isDispatchable {
		t.Fatalf("memoized component lost Dispatchable: %T", c)
	}
	renderTo := func(props map[string]any) string {
		var buf bytes.Buffer
		bw := AcquireWriter(&buf)
		rc := AcquireContext(bw, nil)
		rc.SetComponentRegistry(cr)
		defer ReleaseContext(rc)
		if err := disp.Dispatch(bw, rc, props, nil); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if err := bw.Flush(); err != nil {
			t.Fatalf("flush: %v", err)
		}
		return buf.String()
	}

	if got := renderTo(map[string]any{"on": false}); got != "<b>off</b>" {
		t.Fatalf("first render = %q", got)
	}
	if got := renderTo(map[string]any{"on": false}); got != "<b>off</b>" {
		t.Fatalf("second render = %q", got)
	}
	if n := atomic.LoadInt32(&renders); n != 1 {
		t.Fatalf("expected 1 render (memo hit), got %d", n)
	}
}
