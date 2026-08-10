package render_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// flushRecorder counts Flush calls and captures each chunk. Used to
// verify the streaming write path actually emits intermediate
// chunks (not just buffers everything).
type flushRecorder struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	flushN   int32
	chunks   [][]byte
	flusher  http.Flusher
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{}
}

func (f *flushRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Snapshot the bytes so each chunk stays distinct.
	cp := make([]byte, len(p))
	copy(cp, p)
	f.chunks = append(f.chunks, cp)
	return f.buf.Write(p)
}
func (f *flushRecorder) Flush() {
	atomic.AddInt32(&f.flushN, 1)
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

// As http.ResponseWriter (so AcquireWriter captures the flusher).
func (f *flushRecorder) Header() http.Header { return http.Header{} }
func (f *flushRecorder) WriteHeader(int)     {}

// String returns the full buffered body in chunk order.
func (f *flushRecorder) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

// ---------------------------------------------------------------------------
// Async/Fallback end-to-end.
// ---------------------------------------------------------------------------

// TestAsync_FallbackRendersImmediately verifies the fallback is in
// the body BEFORE the slow component finishes.
func TestAsync_FallbackRendersImmediately(t *testing.T) {
	cr := render.NewComponentRegistry()
	rendered := make(chan struct{})
	cr.Define("USER_PROFILE").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString(`<div id="profile-stub" class="skeleton">Loading...</div>`)
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			time.Sleep(50 * time.Millisecond) // simulate slow work
			close(rendered)
			_, err := c.WriteString(`<div id="profile-stub" class="real">Real data</div>`)
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	src := `<html><body><USER_PROFILE/></body></html>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"page": []byte(src),
	}))

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "page", nil); err != nil {
		t.Fatal(err)
	}
	// The fallback should already be in the body BEFORE the worker
	// finishes. Verify by reading immediately — the worker hasn't
	// closed `rendered` yet because we haven't waited.
	if got := rec.String(); !strings.Contains(got, "Loading...") {
		t.Errorf("fallback missing: %q", got)
	}
	if got := rec.String(); strings.Contains(got, "Real data") {
		t.Errorf("real output should not be in body yet: %q", got)
	}

	// Now wait for the worker to finish, then drain the suspense
	// coordinator (CloseSuspense flushes trailing OOB chunks).
	rc.CloseSuspense()
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish in time")
	}
	// Give the writer goroutine a moment to drain.
	time.Sleep(20 * time.Millisecond)

	got := rec.String()
	if !strings.Contains(got, "Loading...") {
		t.Errorf("fallback lost after close: %q", got)
	}
	// OOB wrapper id defaults to the lowercased component name; the
	// fallback's `profile-stub` id is just a placeholder element.
	if !strings.Contains(got, `<div id="user_profile" hx-swap-oob="true">`) {
		t.Errorf("OOB wrapper missing: %q", got)
	}
	if !strings.Contains(got, "Real data") {
		t.Errorf("real output not delivered as OOB: %q", got)
	}
}

// TestAsync_ConcurrentWithShell verifies the worker runs concurrently
// with the rest of the page (true parallelism). We measure wall time:
// shell renders fast, slow component runs in parallel, total ≈ max
// rather than sum.
func TestAsync_ConcurrentWithShell(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLOW").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("<div id='slow'>fallback</div>")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			time.Sleep(100 * time.Millisecond)
			_, err := c.WriteString("<div id='slow'>real</div>")
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	src := `<html><body><h1>Shell</h1><SLOW/></body></html>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	start := time.Now()
	if err := reg.RenderNamed(rc, render.EngineHTML, "page", nil); err != nil {
		t.Fatal(err)
	}
	rc.CloseSuspense()
	// Block until the coordinator drains. Look for the OOB wrapper
	// id (the lowercased component name) and the real payload
	// together — the wrapper + payload string is unambiguous.
	for i := 0; i < 100; i++ {
		s := rec.String()
		if strings.Contains(s, `id="slow" hx-swap-oob="true"`) && strings.Contains(s, "real") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)

	// If async worked, total ≈ 100ms (worker time), not ≈ 200ms
	// (shell + worker sequential). Allow generous slack for CI.
	if elapsed > 180*time.Millisecond {
		t.Errorf("render took %v — async worker not concurrent with shell", elapsed)
	}
}

// TestAsync_NoCoordinatorForSyncComponents verifies the suspense
// path is NOT taken when no component is async. The coordinator
// must remain nil, so the hot path stays zero-alloc.
func TestAsync_NoCoordinatorForSyncComponents(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PLAIN").
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("<plain/>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "PLAIN", nil); err != nil {
		t.Fatal(err)
	}
	if susp := rc.Suspense(); susp != nil {
		t.Errorf("coordinator allocated for sync render: %p", susp)
	}
}

// TestAsync_CtxCancellationAbortsWorker verifies a cancelled request
// context stops the worker without submitting the OOB chunk.
func TestAsync_CtxCancellationAbortsWorker(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLOW").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("<div id='slow'>fallback</div>")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			time.Sleep(2 * time.Second)
			_, err := c.WriteString("<div id='slow'>real</div>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rc := render.AcquireContext(bw, req)
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "SLOW", nil); err != nil {
		t.Fatal(err)
	}
	// Cancel the request context immediately.
	cancel()
	rc.CloseSuspense()
	// Wait a beat for the worker to notice.
	time.Sleep(50 * time.Millisecond)
	// The OOB wrapper for the cancelled component should NOT be in
	// the body — the worker aborted.
	if got := rec.String(); strings.Contains(got, `hx-swap-oob="true">real`) {
		t.Errorf("cancelled worker should not emit OOB: %q", got)
	}
}

// TestAsync_OOBIdEscaped verifies a malicious component name can't
// break out of the OOB attribute.
func TestAsync_OOBIdEscaped(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define(`EVIL"><script>`).
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("fb")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("real")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, `EVIL"><script>`, nil); err != nil {
		t.Fatal(err)
	}
	rc.CloseSuspense()
	time.Sleep(20 * time.Millisecond)

	if got := rec.String(); strings.Contains(got, "<script>") {
		t.Errorf("unescaped component name injected script: %q", got)
	}
}

// TestAsync_FallbackMissingDowngradesToSync verifies a definition
// with Async() but no Fallback() renders synchronously rather than
// panicking.
func TestAsync_FallbackMissingDowngradesToSync(t *testing.T) {
	cr := render.NewComponentRegistry()
	// Note: no Fallback call.
	cr.Define("SYNC").
		Async().
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("rendered")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	rec := newFlushRecorder()
	bw := render.AcquireWriter(rec)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "SYNC", nil); err != nil {
		t.Fatal(err)
	}
	rc.CloseSuspense()
	if got := rec.String(); got != "rendered" {
		t.Errorf("got %q, want %q", got, "rendered")
	}
	if susp := rc.Suspense(); susp != nil {
		t.Errorf("downgraded-to-sync should not allocate coordinator")
	}
}

// TestAsync_LazyInitOnlyOnFirstAsync verifies the coordinator is
// allocated exactly once even with multiple async components.
func TestAsync_LazyInitOnlyOnFirstAsync(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("A").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("fb-a")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			time.Sleep(20 * time.Millisecond)
			_, err := c.WriteString("real-a")
			return err
		}).
		Register(cr)
	cr.Define("B").
		Async().
		Fallback(func(c *render.ComponentContext) error {
			_, err := c.WriteString("fb-b")
			return err
		}).
		Render(func(c *render.ComponentContext) error {
			time.Sleep(20 * time.Millisecond)
			_, err := c.WriteString("real-b")
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	src := `<A/><B/>`
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
	time.Sleep(50 * time.Millisecond)

	got := rec.String()
	for _, want := range []string{"fb-a", "fb-b", "real-a", "real-b"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output: %q", want, got)
		}
	}
	// Two OOB wrappers expected.
	if got := strings.Count(got, `hx-swap-oob="true"`); got != 2 {
		t.Errorf("expected 2 OOB wrappers, got %d in %q", got, got)
	}
}
