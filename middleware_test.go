package render_test

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// wrapDiv is a middleware that wraps the component output in a
// div with the given class.
func wrapDiv(class string) render.ComponentMiddleware {
	return func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
		return func(c *render.ComponentContext) error {
			if _, err := c.WriteString(`<div class="` + class + `">`); err != nil {
				return err
			}
			if err := next(c); err != nil {
				return err
			}
			_, err := c.WriteString(`</div>`)
			return err
		}
	}
}

// TestMiddleware_Basic verifies the middleware runs and wraps
// output.
func TestMiddleware_Basic(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Use(wrapDiv("mw")).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("inner")
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
	_ = bw.Flush()
	if !strings.Contains(buf.String(), `<div class="mw">inner</div>`) {
		t.Errorf("middleware output wrong: %q", buf.String())
	}
}

// TestMiddleware_Ordering verifies the first .Use() is the
// OUTERMOST wrapper.
func TestMiddleware_Ordering(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Use(wrapDiv("outer"), wrapDiv("inner")).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("core")
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
	_ = bw.Flush()
	want := `<div class="outer"><div class="inner">core</div></div>`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("ordering wrong: got %q want %q", buf.String(), want)
	}
}

// TestMiddleware_Abort verifies a middleware that doesn't call
// next renders nothing.
func TestMiddleware_Abort(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SECRET").
		Use(func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
			return func(c *render.ComponentContext) error {
				return nil // abort — no next
			}
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("TOP SECRET")
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

	if err := reg.RenderComponent(bw, rc, "SECRET", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if strings.Contains(buf.String(), "TOP SECRET") {
		t.Errorf("aborted middleware leaked content: %q", buf.String())
	}
}

// TestMiddleware_AbortAsync verifies the SECURITY case: an abort
// on an async component prevents the fallback AND the worker.
func TestMiddleware_AbortAsync(t *testing.T) {
	var workerRuns atomic.Int32
	cr := render.NewComponentRegistry()
	cr.Define("BILLING").
		Use(func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
			return func(c *render.ComponentContext) error {
				return nil // abort — no fallback, no worker
			}
		}).
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("<skeleton>")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			workerRuns.Add(1)
			_, err := c.WriteString("real")
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)
	src := `<html><body><BILLING/></body></html>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "page", nil); err != nil {
		t.Fatal(err)
	}
	rc.CloseSuspense()
	time.Sleep(30 * time.Millisecond)

	got := rec.String()
	if strings.Contains(got, "<skeleton>") {
		t.Errorf("aborted async middleware leaked fallback: %q", got)
	}
	if strings.Contains(got, "real") {
		t.Errorf("aborted async middleware leaked real output: %q", got)
	}
	if workerRuns.Load() != 0 {
		t.Errorf("worker ran %d times despite abort", workerRuns.Load())
	}
}

// TestMiddleware_ContextAccess verifies the pivot's big win:
// middleware reads cascading context on the main thread.
func TestMiddleware_ContextAccess(t *testing.T) {
	var roleSeen string
	cr := render.NewComponentRegistry()

	cr.Define("PARENT").
		Render(func(c *render.ComponentContext) error {
			c.ProvideContext("role", "admin")
			return c.Render("CHILD", nil)
		}).
		Register(cr)

	cr.Define("CHILD").
		Use(func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
			return func(c *render.ComponentContext) error {
				if role, ok := c.UseContext("role").(string); ok {
					roleSeen = role
					if role != "admin" {
						return nil // abort
					}
				}
				return next(c)
			}
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("admin-panel")
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

	if err := reg.RenderComponent(bw, rc, "PARENT", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if roleSeen != "admin" {
		t.Errorf("middleware didn't see cascading context: %q", roleSeen)
	}
	if !strings.Contains(buf.String(), "admin-panel") {
		t.Errorf("middleware gated on context but content missing: %q", buf.String())
	}
}

// TestMiddleware_Boundary verifies a middleware panic is caught
// by the component's ErrorBoundary.
func TestMiddleware_Boundary(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Use(func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
			return func(c *render.ComponentContext) error {
				panic("middleware exploded")
			}
		}).
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			_, werr := c.WriteString(`<div class="error">recovered</div>`)
			return werr
		}).
		Render(func(c *render.ComponentContext) error {
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

	if err := reg.RenderComponent(bw, rc, "WIDGET", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if !strings.Contains(buf.String(), `class="error">recovered`) {
		t.Errorf("boundary didn't catch middleware panic: %q", buf.String())
	}
}

// TestMiddleware_OOB verifies middleware writes join the OOB
// buffered output and are wrapped when dirty.
func TestMiddleware_OOB(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PANEL").
		WithOOB("panel").
		Use(func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
			return func(c *render.ComponentContext) error {
				c.SetState("touched", true) // marks dirty → OOB wrap
				return next(c)
			}
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString(`<div id="panel">content</div>`)
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

	if err := reg.RenderComponent(bw, rc, "PANEL", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	out := buf.String()
	if !strings.Contains(out, `<div id="panel" hx-swap-oob="true">`) {
		t.Errorf("middleware dirty didn't trigger OOB wrap: %q", out)
	}
	if !strings.Contains(out, `id="panel">content`) {
		t.Errorf("component content missing from OOB: %q", out)
	}
}

// TestMiddleware_AsyncRunsOnceOnMainThread verifies middleware
// runs exactly once, on the main thread, not in the worker.
func TestMiddleware_AsyncRunsOnceOnMainThread(t *testing.T) {
	var mwRuns atomic.Int32
	cr := render.NewComponentRegistry()
	cr.Define("SLOW").
		Use(func(next render.ComponentRenderFunc) render.ComponentRenderFunc {
			return func(c *render.ComponentContext) error {
				mwRuns.Add(1)
				return next(c)
			}
		}).
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("skeleton")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("real")
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)
	src := `<SLOW/>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "page", nil); err != nil {
		t.Fatal(err)
	}
	rc.CloseSuspense()
	time.Sleep(30 * time.Millisecond)

	if mwRuns.Load() != 1 {
		t.Errorf("middleware ran %d times, want exactly 1 (main thread only)", mwRuns.Load())
	}
	if !strings.Contains(rec.String(), "skeleton") {
		t.Errorf("fallback missing: %q", rec.String())
	}
}

// TestMiddleware_FallbackChildren verifies the topology bonus:
// async fallbacks now receive children.
func TestMiddleware_FallbackChildren(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PANEL").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString(`<div class="fb">`)
			if err != nil {
				return err
			}
			c.WriteChildren()
			_, err = c.WriteString(`</div>`)
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("real")
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)
	src := `<PANEL><span>child</span></PANEL>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "page", nil); err != nil {
		t.Fatal(err)
	}
	rc.CloseSuspense()
	time.Sleep(30 * time.Millisecond)

	if !strings.Contains(rec.String(), `<div class="fb"><span>child</span></div>`) {
		t.Errorf("fallback didn't render children: %q", rec.String())
	}
}
