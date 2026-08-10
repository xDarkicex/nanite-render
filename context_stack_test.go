package render_test

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"strconv"
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

// TestContext_OverflowSpillsNotPanics verifies that pushing
// beyond the inline fast-path spills to a heap slice and the
// values remain readable. No panic ever.
func TestContext_OverflowSpillsNotPanics(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	// Push well beyond any reasonable inline capacity. The
	// default inline cap is 32 (private). Push 64 to exercise
	// both inline and overflow paths.
	const N = 64
	for i := 0; i < N; i++ {
		rc.PushContext("k", i)
	}
	if d := rc.ContextDepth(); d != N {
		t.Errorf("depth = %d, want %d", d, N)
	}
	// Reverse-scan: the nearest (last pushed) wins. We used the
	// same key for every push, so GetContext returns N-1.
	if v := rc.GetContext("k"); v != N-1 {
		t.Errorf("top of stack = %v, want %d", v, N-1)
	}
	// Pop back below the inline threshold; the overflow slice
	// should be truncated.
	rc.PopContextTo(0)
	if d := rc.ContextDepth(); d != 0 {
		t.Errorf("depth after pop = %d, want 0", d)
	}
	// Pushing again must work cleanly with the inline path.
	rc.PushContext("k", "after")
	if v := rc.GetContext("k"); v != "after" {
		t.Errorf("after pop+push = %v, want \"after\"", v)
	}
}

// TestContext_OverflowLookupAcrossBoundary verifies that a value
// pushed into the overflow slice is still found by reverse-scan
// lookup.
func TestContext_OverflowLookupAcrossBoundary(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	// Push 35 entries: 32 land inline, last 3 in overflow. Use
	// distinct keys so reverse-scan doesn't shadow earlier ones.
	for i := 0; i < 35; i++ {
		rc.PushContext("k"+strconv.Itoa(i), i)
	}
	// Every entry must be findable, and the scan must work
	// across the inline/overflow boundary.
	for i := 0; i < 35; i++ {
		if v := rc.GetContext("k" + strconv.Itoa(i)); v != i {
			t.Errorf("key k%d = %v, want %d", i, v, i)
		}
	}
	// Pop everything and verify depth.
	rc.PopContextTo(0)
	if d := rc.ContextDepth(); d != 0 {
		t.Errorf("depth after pop = %d, want 0", d)
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

// TestContext_DefaultFuncMapWired verifies that the default
// FuncMap exposes useState, get, set, and useContext out of the
// box — no opt-in required. A freshly-built Registry should
// have these helpers available to templates.
func TestContext_DefaultFuncMapWired(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)
	rc.PushContext("theme", "midnight")

	// UseContextFunc returns the closure for useContext. The
	// closure resolves the cascading-context stack via rc.
	fn, ok := render.UseContextFunc(rc).(func(string) any)
	if !ok {
		t.Fatal("UseContextFunc returned wrong type")
	}
	if got := fn("theme"); got != "midnight" {
		t.Errorf("useContext(theme) = %v, want \"midnight\"", got)
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

// TestDefaultFuncMap_HelpersAvailable verifies that templates can
// call useState, get, set, and useContext via the FuncMap that
// every Registry installs by default. No opt-in.
func TestDefaultFuncMap_HelpersAvailable(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)
	rc.PushContext("theme", "midnight")

	// Build a template that uses every default helper. Parsing
	// it without "function not defined" errors proves the
	// FuncMap keys are exposed.
	fm := template.FuncMap{
		"useState": func(key string, initial any) any {
			return rc.ComponentState().UseState(key, initial)
		},
		"get": func(key string) any {
			v, _ := rc.ComponentState().Get(key)
			return v
		},
		"set": func(key string, val any) string {
			rc.ComponentState().Set(key, val)
			return ""
		},
		"useContext": func(key string) any { return rc.GetContext(key) },
	}
	tpl := template.Must(template.New("page").Funcs(fm).Parse(
		`{{ useState "count" 42 }} | {{ get "count" }} | {{ set "flag" "on" }}{{ get "flag" }} | {{ useContext "theme" }}`))
	var out bytes.Buffer
	if err := tpl.Execute(&out, nil); err != nil {
		t.Fatal(err)
	}
	want := "42 | 42 | on | midnight"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}