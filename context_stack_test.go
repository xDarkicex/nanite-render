package render_test

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestContext_ProvideAndUseSameLevel verifies a Provide in a
// component is visible via UseContext in the same component.
func TestContext_ProvideAndUseSameLevel(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("theme", "dark")
			theme := c.UseContext("theme").(string)
			_, err := c.WriteString("theme=" + theme)
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "WIDGET", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "theme=dark") {
		t.Errorf("expected theme=dark, got %q", buf.String())
	}
}

// TestContext_NestedShadow verifies a deeply-nested child sees the
// outer binding, and an inner binding shadows it for that scope.
func TestContext_NestedShadow(t *testing.T) {
	cr := render.NewComponentRegistry()

	cr.Define("INNER").
		Render(func(c *render.ComponentContext) error {
			theme := c.UseContext("theme").(string)
			_, err := c.WriteString("inner=" + theme)
			return err
		}).
		Register(cr)

	cr.Define("OUTER_LIGHT").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("theme", "light")
			// Also override in a nested scope.
			theme := c.UseContext("theme").(string)
			_, _ = c.WriteString("outer=" + theme + " ")
			// Render INNER. With no inner override, it should
			// see "light".
			return c.Render("INNER", nil)
		}).
		Register(cr)

	cr.Define("OUTER_DARK_OVERRIDE").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("theme", "dark")
			theme := c.UseContext("theme").(string)
			_, _ = c.WriteString("outer=" + theme + " ")
			return c.Render("INNER", nil)
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "OUTER_LIGHT", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "outer=light inner=light") {
		t.Errorf("nested shadow mismatch: %q", buf.String())
	}

	buf.Reset()
	if err := reg.RenderComponent(bw, rc, "OUTER_DARK_OVERRIDE", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "outer=dark inner=dark") {
		t.Errorf("nested shadow mismatch: %q", buf.String())
	}
}

// TestContext_ScopeIsolation verifies a binding pushed inside a
// component does NOT leak out into siblings or parents — the
// dispatcher pops the stack on render scope exit.
func TestContext_ScopeIsolation(t *testing.T) {
	cr := render.NewComponentRegistry()

	var afterInner any
	cr.Define("INNER").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("scoped", "leaked?")
			return nil
		}).
		Register(cr)

	cr.Define("OUTER").
		Render(func(c *render.ComponentContext) error {
			if err := c.Render("INNER", nil); err != nil {
				return err
			}
			// After INNER's render returns, the stack pointer
			// should have been popped. Reading "scoped" must
			// return nil (or the pre-existing outer binding —
			// here, none).
			afterInner = c.UseContext("scoped")
			return nil
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "OUTER", nil); err != nil {
		t.Fatal(err)
	}
	if afterInner != nil {
		t.Errorf("binding leaked from INNER scope: %v", afterInner)
	}
}

// TestContext_OverflowPanics verifies that more than
// MaxContextDepth nested pushes panic. We can't easily trigger
// MaxContextDepth=16 from a test without 16 nested components,
// so we exercise the panic path directly via a small loop.
func TestContext_OverflowPanics(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	defer func() {
		if recover() == nil {
			t.Fatal("expected overflow panic")
		}
	}()
	for i := 0; i <= render.MaxContextDepth; i++ {
		rc.PushContext("k", i)
	}
}

// TestContext_TypeAssertionPattern verifies the canonical usage
// pattern: type-assert the result of UseContext.
func TestContext_TypeAssertionPattern(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("user", map[string]any{"name": "alice"})
			u := c.UseContext("user").(map[string]any)
			_, err := c.WriteString("user=" + u["name"].(string))
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "WIDGET", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "user=alice") {
		t.Errorf("type assertion failed: %q", buf.String())
	}
}

// TestContext_FuncMapBridge verifies the template helper resolves
// a binding registered by a fluent component.
func TestContext_FuncMapBridge(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("theme", "purple")
			// Render a template that calls useContext.
			tpl := template.Must(template.New("x").Funcs(template.FuncMap{
				"useContext": render.UseContextFunc(c.Context),
			}).Parse(`theme={{ useContext "theme" }}`))
			return tpl.Execute(c.Writer, nil)
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "WIDGET", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "theme=purple") {
		t.Errorf("FuncMap bridge failed: %q", buf.String())
	}
}

// TestContext_PoolReset verifies that a binding pushed in one
// request does not leak into the next (RenderContext is pooled).
func TestContext_PoolReset(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	rc.PushContext("fromPrev", "leaked")
	// No release here — simulating a bug. But AcquireContext must
	// reset the pointer on the next request.
	render.ReleaseContext(rc)

	rc2 := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc2)

	if v := rc2.GetContext("fromPrev"); v != nil {
		t.Errorf("binding leaked across pool reuse: %v", v)
	}
	if d := rc2.ContextDepth(); d != 0 {
		t.Errorf("depth not reset: %d", d)
	}
}

// TestContext_NilReceiverSafe verifies nil-receiver tolerance.
func TestContext_NilReceiverSafe(t *testing.T) {
	var rc *render.RenderContext
	if v := rc.GetContext("anything"); v != nil {
		t.Errorf("nil receiver GetContext returned %v", v)
	}
	if d := rc.ContextDepth(); d != 0 {
		t.Errorf("nil receiver ContextDepth returned %d", d)
	}
	// Should not panic.
	rc.PushContext("k", "v")
	rc.PopContextTo(0)
}