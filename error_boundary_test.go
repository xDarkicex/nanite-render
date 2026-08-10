package render_test

import (
	"bytes"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// writeErr is a tiny adapter so test code can write strings via
// c.WriteString (which returns (int, error)) as a single expression
// in closures that need to return just an error.
func writeErr(c *render.ComponentContext, s string) error {
	_, err := c.WriteString(s)
	return err
}

// TestErrorBoundary_PanicReplacedByBoundary verifies that a panic
// in the user's render is replaced by the boundary's output, and
// the rest of the page renders normally.
func TestErrorBoundary_PanicReplacedByBoundary(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			return writeErr(c, `<div class="error">widget failed</div>`)
		}).
		Render(func(c *render.ComponentContext) error {
			panic("database timeout")
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	src := `<html><body>before<WIDGET/>after</body></html>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "page", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "before") {
		t.Errorf("prefix lost: %q", out)
	}
	if !strings.Contains(out, "after") {
		t.Errorf("suffix lost: %q", out)
	}
	if !strings.Contains(out, `class="error"`) {
		t.Errorf("boundary output missing: %q", out)
	}
	if !strings.Contains(out, "widget failed") {
		t.Errorf("boundary message missing: %q", out)
	}
}

// TestErrorBoundary_NoBoundaryRePanics verifies that without a
// boundary registered, a panic in the user's render propagates.
func TestErrorBoundary_NoBoundaryRePanics(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Render(func(c *render.ComponentContext) error {
			panic("kaboom")
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic to propagate")
		}
	}()

	bw := render.AcquireWriter(&bytes.Buffer{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)
	if err := reg.RenderComponent(bw, rc, "WIDGET", nil); err != nil {
		t.Logf("unexpected error: %v", err)
	}
}

// TestErrorBoundary_PanicValueIsRaw verifies the boundary receives
// the raw panic value and can format it however it wants.
func TestErrorBoundary_PanicValueIsRaw(t *testing.T) {
	type customErr struct{ Code int }
	var seen any
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			seen = err
			if e, ok := err.(customErr); ok {
				return writeErr(c, "code="+strconv.Itoa(e.Code))
			}
			return writeErr(c, "generic")
		}).
		Render(func(c *render.ComponentContext) error {
			panic(customErr{Code: 42})
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
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if seen == nil {
		t.Fatal("boundary never invoked")
	}
	if ce, ok := seen.(customErr); !ok || ce.Code != 42 {
		t.Errorf("panic value not propagated: got %#v", seen)
	}
	if !strings.Contains(buf.String(), "code=42") {
		t.Errorf("boundary formatted output missing: %q", buf.String())
	}
}

// TestErrorBoundary_BoundaryPanicSwallowed verifies a panic in the
// boundary itself does NOT crash the page — a generic placeholder
// is emitted instead.
func TestErrorBoundary_BoundaryPanicSwallowed(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			panic("boundary exploded")
		}).
		Render(func(c *render.ComponentContext) error {
			panic("render exploded")
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
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "error boundary failed") {
		t.Errorf("generic placeholder missing on boundary panic: %q", buf.String())
	}
}

// TestErrorBoundary_BoundaryErrorSwallowed verifies that an error
// return from the boundary also produces the generic placeholder.
func TestErrorBoundary_BoundaryErrorSwallowed(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			return errBoom
		}).
		Render(func(c *render.ComponentContext) error {
			panic("trigger")
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
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "error boundary failed") {
		t.Errorf("generic placeholder missing on boundary error: %q", buf.String())
	}
}

var errBoom = &boundaryErr{}

type boundaryErr struct{}

func (b *boundaryErr) Error() string { return "boundary failed" }

// TestErrorBoundary_AsyncPanicReplacedByBoundary verifies the
// async path: a worker panic invokes the boundary and the
// boundary's output arrives as an OOB chunk.
func TestErrorBoundary_AsyncPanicReplacedByBoundary(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLOW").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			return writeErr(c, `<div id="slow">skeleton</div>`)
		}).
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			return writeErr(c, `<div id="slow" class="error">async failed</div>`)
		}).
		Render(func(c *render.ComponentContext) error {
			panic("async boom")
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	src := `<html><body><SLOW/></body></html>`
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
	for i := 0; i < 100; i++ {
		if strings.Contains(rec.String(), `class="error"`) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := rec.String()
	if !strings.Contains(got, "skeleton") {
		t.Errorf("fallback missing: %q", got)
	}
	if !strings.Contains(got, `<div id="slow" hx-swap-oob="true">`) {
		t.Errorf("OOB wrapper missing: %q", got)
	}
	if !strings.Contains(got, "async failed") {
		t.Errorf("boundary output missing from OOB: %q", got)
	}
	if !strings.Contains(got, `class="error"`) {
		t.Errorf("error class missing: %q", got)
	}
}

// TestErrorBoundary_AsyncBoundaryItselfPanics verifies that a
// panic in the async worker's boundary emits the generic
// placeholder OOB instead of crashing.
func TestErrorBoundary_AsyncBoundaryItselfPanics(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLOW").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			return writeErr(c, "skeleton")
		}).
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			panic("boundary exploded in worker")
		}).
		Render(func(c *render.ComponentContext) error {
			panic("render exploded in worker")
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
	for i := 0; i < 100; i++ {
		if strings.Contains(rec.String(), "error boundary failed") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(rec.String(), "error boundary failed") {
		t.Errorf("generic placeholder missing for async worker boundary panic: %q", rec.String())
	}
}

// TestErrorBoundary_NoBoundaryAsyncSilentlyDrops verifies the
// pre-feature behaviour: an async worker panic with no boundary
// just doesn't deliver an OOB chunk. The fallback stays inline.
func TestErrorBoundary_NoBoundaryAsyncSilentlyDrops(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLOW").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			return writeErr(c, "skeleton")
		}).
		Render(func(c *render.ComponentContext) error {
			panic("boom")
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
	got := rec.String()
	if !strings.Contains(got, "skeleton") {
		t.Errorf("fallback missing: %q", got)
	}
	if strings.Contains(got, `hx-swap-oob="true"`) {
		t.Errorf("unexpected OOB chunk emitted for panic-without-boundary: %q", got)
	}
}

// TestErrorBoundary_OOBComponentPanic verifies that an OOB-enabled
// component that panics gets its buffer discarded and the boundary
// output emitted to the OOB sink.
func TestErrorBoundary_OOBComponentPanic(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PANEL").
		WithOOB("panel").
		ErrorBoundary(func(c *render.ComponentContext, err any) error {
			return writeErr(c, "<panel-error/>")
		}).
		Render(func(c *render.ComponentContext) error {
			panic("panel boom")
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
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "before-panic-bytes") {
		t.Errorf("partial pre-panic bytes leaked: %q", got)
	}
	if !strings.Contains(got, "panel-error") {
		t.Errorf("boundary output missing from OOB: %q", got)
	}
}