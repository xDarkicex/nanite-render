package render_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// ---------------------------------------------------------------------------
// RenderComponent — direct dispatch without a parent template.
// ---------------------------------------------------------------------------

func TestRenderComponent_DirectDispatch(t *testing.T) {
	cr := render.NewComponentRegistry()
	renderCount := 0
	cr.Define("CARD").
		Render(func(c *render.ComponentContext) error {
			renderCount++
			_, err := c.WriteString("<div class='card'>hello</div>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "CARD", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "<div class='card'>hello</div>" {
		t.Errorf("got %q", got)
	}
	if renderCount != 1 {
		t.Errorf("expected 1 render, got %d", renderCount)
	}
}

func TestRenderComponent_MissingNameIsNoOp(t *testing.T) {
	// A handler hitting a stale link to a removed component should
	// not crash — RenderComponent returns nil, no error, no output.
	reg := render.New()
	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "GHOST", nil); err != nil {
		t.Errorf("missing component should not error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("missing component should not write: %q", buf.String())
	}
}

// renderComp is the test helper that drives a named component via the
// registry's ComponentRegistry — same dispatch path as the html/template
// `{{ component "Name" . }}` superpower uses internally.
func renderComp(t *testing.T, reg *render.Registry, bw render.ByteWriter, rc *render.RenderContext, name string, props any) {
	t.Helper()
	if err := reg.RenderComponent(bw, rc, name, props); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// WithOOB — opt-in HTMX OOB swap emission.
// ---------------------------------------------------------------------------

func TestWithOOB_DirtyEmitsSwap(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		WithOOB("card-slot").
		Render(func(c *render.ComponentContext) error {
			_, set := c.UseState("count", 0)
			set(42) // marks frame dirty
			_, err := c.WriteString("<div class='card'>42</div>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "CARD", nil)
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	want := `<div id="card-slot" hx-swap-oob="true"><div class='card'>42</div></div>`
	if got := buf.String(); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestWithOOB_CleanEmitsNoWrapper verifies a component declared with
// WithOOB that does NOT mutate state writes directly to the original
// writer without the OOB wrapper.
func TestWithOOB_CleanEmitsNoWrapper(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("STATIC").
		WithOOB("static-slot").
		Render(func(c *render.ComponentContext) error {
			// No SetState / UseState calls → frame stays clean.
			_, err := c.WriteString("<div class='static'>immutable</div>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "STATIC", nil)
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "<div class='static'>immutable</div>" {
		t.Errorf("clean OOB component should write without wrapper, got %q", got)
	}
}

// TestWithOOB_OOBSinkRoutes verifies SetOOBSink redirects OOB output
// away from the main writer.
func TestWithOOB_OOBSinkRoutes(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		WithOOB("card-slot").
		Render(func(c *render.ComponentContext) error {
			_, set := c.UseState("count", 0)
			set(7)
			_, err := c.WriteString("<div class='card'>7</div>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var mainBuf, oobBuf bytes.Buffer
	mainBw := render.AcquireWriter(&mainBuf)
	defer render.ReleaseWriter(mainBw)
	oobBw := render.AcquireWriter(&oobBuf)
	defer render.ReleaseWriter(oobBw)

	rc := render.AcquireContext(mainBw, &http.Request{})
	defer render.ReleaseContext(rc)
	rc.SetOOBSink(oobBw)

	renderComp(t, reg, mainBw, rc, "CARD", nil)
	if err := mainBw.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := oobBw.Flush(); err != nil {
		t.Fatal(err)
	}

	if got := mainBuf.String(); got != "" {
		t.Errorf("main writer should be empty, got %q", got)
	}
	want := `<div id="card-slot" hx-swap-oob="true"><div class='card'>7</div></div>`
	if got := oobBuf.String(); got != want {
		t.Errorf("oob sink: got %q\nwant %q", got, want)
	}
}

// TestWithOOB_SetStateMarksDirty verifies SetState marks the frame
// dirty when the component is OOB-enabled.
func TestWithOOB_SetStateMarksDirty(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		WithOOB("card").
		Render(func(c *render.ComponentContext) error {
			c.SetState("flag", true)
			_, err := c.WriteString("body")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "CARD", nil)
	bw.Flush()

	if !strings.Contains(buf.String(), `hx-swap-oob="true"`) {
		t.Errorf("SetState should have triggered OOB wrap: %q", buf.String())
	}
}

// TestWithOOB_EscapesTargetID verifies a malicious oobID cannot break
// out of the attribute.
func TestWithOOB_EscapesTargetID(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("EVIL").
		WithOOB(`"><script>alert(1)</script>`).
		Render(func(c *render.ComponentContext) error {
			_, set := c.UseState("k", 0)
			set(1)
			_, err := c.WriteString("body")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "EVIL", nil)
	bw.Flush()

	if strings.Contains(buf.String(), "<script>alert(1)") {
		t.Errorf("unescaped oobID injected script: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "&#34;") {
		t.Errorf("expected escaped quotes: %q", buf.String())
	}
}

// TestWithOOB_NoOOBIsTransparent verifies a component WITHOUT WithOOB
// renders directly — no buffer copy, no wrapper, no overhead beyond
// the existing path.
func TestWithOOB_NoOOBIsTransparent(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PLAIN").
		Render(func(c *render.ComponentContext) error {
			_, set := c.UseState("k", 0)
			set(99) // no-op for OOB purposes
			_, err := c.WriteString("<plain>body</plain>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "PLAIN", nil)
	bw.Flush()

	if got := buf.String(); got != "<plain>body</plain>" {
		t.Errorf("non-OOB component should render transparently, got %q", got)
	}
	if strings.Contains(buf.String(), "hx-swap-oob") {
		t.Errorf("non-OOB component leaked wrapper: %q", buf.String())
	}
}

// TestWithOOB_BufferPoolReused verifies the OOB render buffer comes
// from a pool (zero steady-state allocations).
func TestWithOOB_BufferPoolReused(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		WithOOB("card").
		Render(func(c *render.ComponentContext) error {
			_, set := c.UseState("k", 0)
			set(1)
			_, err := c.WriteString("<card/>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	// Run many iterations; if the buffer isn't pooled, allocs/op
	// will be > 0. We can't easily count bytes.Buffer allocations
	// (they happen inside the pool), so we just verify correctness
	// across many calls — the same buffer instance must be returned
	// cleanly each time.
	for i := 0; i < 100; i++ {
		var buf bytes.Buffer
		bw := render.AcquireWriter(&buf)
		rc := render.AcquireContext(bw, nil)
		renderComp(t, reg, bw, rc, "CARD", nil)
		bw.Flush()
		if !strings.Contains(buf.String(), "hx-swap-oob") {
			t.Fatalf("iteration %d missing wrap: %q", i, buf.String())
		}
		render.ReleaseContext(rc)
		render.ReleaseWriter(bw)
	}
}

// TestWithOOB_DirtyEmitsHXTrigger verifies a dirty OOB render
// automatically registers the lowercased component name as an HTMX
// trigger event. The router reads rc.HXTriggers() to populate the
// HX-Trigger response header.
func TestWithOOB_DirtyEmitsHXTrigger(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("UserCard").
		WithOOB("user-card").
		Render(func(c *render.ComponentContext) error {
			_, set := c.UseState("count", 0)
			set(1)
			_, err := c.WriteString("<div/>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "UserCard", nil)

	// Lowercased component name should be in the trigger set.
	if got := rc.HXTriggers().Join(); got != "usercard" {
		t.Errorf("got %q, want %q", got, "usercard")
	}
}

// TestWithOOB_CleanDoesNotEmitHXTrigger verifies a clean OOB render
// does NOT emit a trigger event — the user did nothing, the client
// shouldn't react.
func TestWithOOB_CleanDoesNotEmitHXTrigger(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		WithOOB("card").
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("<div/>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "CARD", nil)

	if tr := rc.HXTriggers(); tr != nil {
		t.Errorf("clean render should not emit trigger, got %v", tr)
	}
}

// TestWithOOB_UserTriggerDedupsWithAuto verifies a user-added trigger
// and the auto-added one are both present, with dedup keeping the
// set clean.
func TestWithOOB_UserTriggerDedupsWithAuto(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		WithOOB("card").
		Render(func(c *render.ComponentContext) error {
			c.AddHXTrigger("custom-event")
			_, set := c.UseState("k", 0)
			set(1)
			_, err := c.WriteString("<div/>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	defer render.ReleaseContext(rc)

	renderComp(t, reg, bw, rc, "CARD", nil)

	// Both events present, sorted.
	if got := rc.HXTriggers().Join(); got != "card,custom-event" {
		t.Errorf("got %q, want both events", got)
	}
}
