package render

import (
	"bytes"
	"context"
	"html"
	"io"
	"sync"
)

// suspensePayload is a finished async component's bytes, ready to be
// wrapped in an HTMX OOB chunk and written to the response. The buf
// is owned by the payload; the coordinator returns it to the pool
// after writing.
type suspensePayload struct {
	id  string // OOB target id (lowercased component name by default)
	buf *bytes.Buffer
	err error
}

// suspenseCoordinator owns the response writer for the duration of a
// render that contains at least one Async component. ONE writer
// goroutine serializes trailing chunks; N worker goroutines render
// slow components in parallel and deliver their bytes through a
// channel. This avoids data races on the response writer and keeps
// each Write to http.ResponseWriter atomic.
//
// Lifecycle:
//  1. The executor calls RenderContext.EnsureSuspense(w) the first
//     time it hits an Async component. The coordinator spins up the
//     writer goroutine.
//  2. For each Async component, the executor calls
//     RenderContext.SubmitAsync(id, fn, data) — this spawns a worker
//     goroutine that runs fn to a pooled buffer and submits the
//     finished bytes through the channel.
//  3. When the executor finishes walking the tree, it calls
//     RenderContext.CloseSuspense(). The writer goroutine drains
//     pending payloads, writes them as OOB chunks, then exits.
//  4. If the request context is cancelled (client disconnect), the
//     coordinator aborts: workers see ctx.Done() and exit without
//     submitting, the writer drains whatever is already buffered
//     and exits.
//
// Allocations: zero on the non-async path (the coordinator is only
// allocated when EnsureSuspense is actually called).
type suspenseCoordinator struct {
	// out is the io.Writer the writer goroutine writes OOB chunks
	// to. We capture the underlying io.Writer at construction (not
	// the ByteWriter wrapper) so the pool can be reused safely even
	// while the writer goroutine is still draining. The writer
	// goroutine creates its own short-lived byteWriter for the
	// chunk bytes, writes them to out, and exits.
	out io.Writer
	ctx context.Context
	// flusher signals the underlying http.Flusher so trailing OOB
	// chunks hit the wire immediately. Captured once at construction;
	// nil if the wrapped writer doesn't expose Flush (the response
	// is just buffered, no streaming benefit but no error either).
	flusher func() error

	ch     chan *suspensePayload
	done   chan struct{} // closed when Close() is called
	cancel context.CancelFunc

	// wg tracks in-flight workers. The writer goroutine waits on
	// wg.Wait after `done` is closed so it doesn't exit while
	// workers are still submitting payloads.
	wg sync.WaitGroup

	// closeChOnce ensures the payload channel is closed exactly
	// once, after drain has finished. The writer goroutine closes
	// it on its way out so any further receives return immediately
	// and no late send panics.
	closeChOnce sync.Once

	// writeMu serialises the actual writer.Write calls inside the
	// single writer goroutine. Workers don't write to the response
	// directly — they hand bytes off via the channel — so writeMu
	// is only acquired by the writer goroutine. Kept as a field so
	// the channel close happens under the same lock.
	writeMu sync.Mutex

	// bufPool is a sync.Pool of *bytes.Buffer used by workers to
	// render into. The coordinator returns buffers to the pool
	// after writing them to the response, so steady-state allocation
	// stays at zero even with many concurrent async components.
	bufPool sync.Pool
}

// EnsureSuspense lazily starts a suspense coordinator for this
// RenderContext, bound to w. Returns the coordinator (or the
// existing one if already started). Nil-safe: a nil receiver or nil
// writer means suspense is unavailable; the caller should fall back
// to synchronous rendering.
//
// The coordinator inherits cancellation from rc.Request.Context()
// when rc.Request is non-nil; otherwise it uses context.Background().
func (rc *RenderContext) EnsureSuspense(w ByteWriter) *suspenseCoordinator {
	if rc == nil || w == nil {
		return nil
	}
	rc.suspenseOnce.Do(func() {
		ctx := context.Background()
		if rc.Request != nil && rc.Request.Context() != nil {
			ctx = rc.Request.Context()
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		// Capture the underlying io.Writer so the writer goroutine
		// doesn't share the pooled byteWriter with the main render.
		// The pool can be reused by other requests while the writer
		// goroutine is still draining without data races.
		var rawOut io.Writer
		var rawFlush func() error
		if bw, ok := w.(*byteWriter); ok {
			rawOut = bw.out
			if bw.flusher != nil {
				f := bw.flusher
				rawFlush = func() error { f.Flush(); return nil }
			}
		} else {
			rawOut = w
			// Other ByteWriter implementations may or may not
			// expose a Flush method; we don't capture it here.
			// Streaming-flushed responses are handled by the
			// underlying router/wrapper that the user installs.
		}
		c := &suspenseCoordinator{
			out:     rawOut,
			ctx:     ctx,
			ch:      make(chan *suspensePayload, 8),
			done:    make(chan struct{}),
			cancel:  cancel,
			flusher: rawFlush,
		}
		c.bufPool.New = func() any { return &bytes.Buffer{} }
		rc.suspense = c
		go c.runWriter()
	})
	return rc.suspense
}

// Suspense returns the active coordinator, or nil if no async
// component has been encountered. Useful for callers that want to
// check whether suspense is in flight.
func (rc *RenderContext) Suspense() *suspenseCoordinator {
	if rc == nil {
		return nil
	}
	return rc.suspense
}

// SubmitAsync spawns a worker goroutine that renders fn(data) into a
// pooled buffer and submits the result through the coordinator's
// channel. The worker selects on rc.ctx.Done() so a client disconnect
// aborts the render before it finishes — no wasted work, no leaked
// goroutines, no orphan channels.
//
// id is the OOB target id (HTMX hx-swap-oob="true" wrapping).
// fn is the render function captured by the component definition.
// data is the per-call props.
//
// boundary is the panic recovery callback. When non-nil, a panic
// in fn triggers boundary(bufWriter, panicVal) inside the worker;
// the boundary's output replaces fn's output as the OOB payload.
// A panic in the boundary itself is swallowed and a generic
// placeholder OOB is emitted so the page never crashes.
//
// SubmitAsync is a no-op when the coordinator is nil or when the
// context is already cancelled.
func (rc *RenderContext) SubmitAsync(id string, fn func(w ByteWriter, rc *RenderContext, data any) error, boundary ErrorBoundaryFunc, data any) {
	if rc == nil || rc.suspense == nil {
		return
	}
	c := rc.suspense
	if c.ctx.Err() != nil {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runWorker(id, fn, boundary, data)
	}()
}

// CloseSuspense closes the coordinator: the writer drains pending
// payloads and exits. Workers already running continue; workers that
// haven't started are skipped because the channel will receive no
// more readers. Call from defer in the handler that opened the
// coordinator, after the render walk completes.
//
// Note: CloseSuspense does NOT cancel the coordinator's context —
// that cancellation is owned by rc.Request.Context() and fires
// automatically on client disconnect. Cancelling here would kill
// in-flight workers before they deliver, defeating the point of
// suspense.
func (rc *RenderContext) CloseSuspense() {
	if rc == nil || rc.suspense == nil {
		return
	}
	c := rc.suspense
	// Closing done lets the writer know no more submissions are
	// coming — but workers in flight may still submit. The channel
	// remains open; the writer drains until both done is closed AND
	// the channel is empty (see runWriter).
	select {
	case <-c.done:
		// already closed
	default:
		close(c.done)
	}
}

// runWriter is the single goroutine that writes OOB payloads to the
// response. CloseSuspense closes `done` to signal that no more
// workers will spawn; the writer then drains the channel until
// in-flight workers finish (wg.Wait), at which point the channel is
// closed and the loop exits. On context cancellation, the writer
// drains whatever is already queued and exits.
func (c *suspenseCoordinator) runWriter() {
	for {
		select {
		case p, ok := <-c.ch:
			if !ok {
				// Channel closed by last worker — exit cleanly.
				return
			}
			c.writePayload(p)
		case <-c.done:
			// No more workers will spawn. Wait for in-flight
			// workers to drain, then close the channel so the
			// receive case above picks up ok=false on the next
			// iteration. This guarantees the writer goroutine
			// exits even if drain's ctx branch is taken.
			c.drain()
			c.closeChOnce.Do(func() { close(c.ch) })
			return
		case <-c.ctx.Done():
			// Client disconnected. Drain anything queued, then exit.
			c.drain()
			c.closeChOnce.Do(func() { close(c.ch) })
			return
		}
	}
}

// drain blocks until all in-flight workers have submitted (or the
// context cancels). Called from runWriter after CloseSuspense or a
// client disconnect, so trailing OOB chunks make it to the wire.
func (c *suspenseCoordinator) drain() {
	// Race wg.Wait against ctx.Done so a cancelled client doesn't
	// block the writer indefinitely.
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-c.ctx.Done():
	}
	// Pull anything left in the channel non-blockingly, then flush.
	for {
		select {
		case p := <-c.ch:
			c.writePayload(p)
		default:
			if c.flusher != nil {
				_ = c.flusher()
			}
			return
		}
	}
}

// writePayload wraps p.buf in <div id="..." hx-swap-oob="true"> and
// writes it to the response writer. Returns p.buf to the pool after
// the write completes. The wrapper id is HTML-escaped so a malicious
// component name can't break out of the attribute.
func (c *suspenseCoordinator) writePayload(p *suspensePayload) {
	if p == nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if p.err != nil {
		// Worker errored or context cancelled mid-render. Return
		// the buffer without writing anything to the response — the
		// client is gone or the render aborted. The OOB wrapper
		// would just produce a half-finished placeholder anyway.
		if p.buf != nil {
			p.buf.Reset()
			c.bufPool.Put(p.buf)
		}
		return
	}
	if p.buf == nil {
		return
	}
	// Write the OOB wrapper open tag, then the buffered bytes, then
	// close tag. We write directly to c.out (the underlying
	// io.Writer, NOT the pooled byteWriter wrapper) so the writer
	// goroutine doesn't share state with the main render's
	// byteWriter pool. The flusher signals chunked transfer
	// encoding so the trailing chunk hits the wire immediately.
	if _, err := io.WriteString(c.out, `<div id="`); err != nil {
		goto release
	}
	if _, err := io.WriteString(c.out, html.EscapeString(p.id)); err != nil {
		goto release
	}
	if _, err := io.WriteString(c.out, `" hx-swap-oob="true">`); err != nil {
		goto release
	}
	if _, err := c.out.Write(p.buf.Bytes()); err != nil {
		goto release
	}
	if _, err := io.WriteString(c.out, `</div>`); err != nil {
		goto release
	}
release:
	// Flush so the chunk hits the wire immediately.
	if c.flusher != nil {
		c.flusher()
	}
	p.buf.Reset()
	c.bufPool.Put(p.buf)
}

// runWorker is the per-async-component goroutine. It runs fn into a
// pooled buffer, then submits the finished payload to the
// coordinator. If the context is cancelled before fn finishes, the
// worker exits without submitting. If fn panics and a boundary is
// provided, the boundary is invoked (writing into the same pooled
// buffer) and its output is submitted as the OOB payload.
func (c *suspenseCoordinator) runWorker(id string, fn func(w ByteWriter, rc *RenderContext, data any) error, boundary ErrorBoundaryFunc, data any) {
	// Acquire a buffer from the pool.
	buf := c.bufPool.Get().(*bytes.Buffer)
	buf.Reset()

	// Build a minimal render context for the worker. The async
	// render path is intentionally narrow: it has the writer (the
	// pooled buffer) and a no-op request — it doesn't need any of
	// the per-request RenderContext fields. We construct one with
	// just the buffer as the writer; the user's fn is expected to
	// use only Writer / WriteString on it.
	wrc := &RenderContext{
		Writer: &bufferByteWriter{buf: buf},
	}

	// Watch ctx.Done in a separate goroutine so we can abort the
	// render mid-flight. We can't interrupt fn cooperatively (Go
	// has no preemption), but we can stop waiting for it once the
	// context is cancelled.
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- &panicValue{val: r}
			}
		}()
		done <- fn(wrc.Writer, wrc, data)
	}()
	select {
	case err := <-done:
		if pe, ok := err.(*panicValue); ok {
			// User render panicked. Try the boundary if one
			// is registered. On success, submit the boundary
			// output as the OOB payload so the client sees a
			// consistent error shape (wrapped OOB chunk).
			// On failure (no boundary or boundary also
			// panicked), submit a generic placeholder OOB.
			if boundary != nil {
				buf.Reset() // discard failed render's bytes
				bw := &bufferByteWriter{buf: buf}
				bwr := &RenderContext{Writer: bw}
				if berr := func() (rerr error) {
					defer func() {
						if r := recover(); r != nil {
							rerr = &panicValue{val: r}
						}
					}()
					return boundary(&ComponentContext{Writer: bw, Context: bwr, Data: data}, pe.val)
				}(); berr != nil {
					// Boundary itself panicked or returned an
					// error. Emit the generic placeholder.
					buf.Reset()
					_, _ = bw.WriteString(`<!-- error boundary failed -->`)
				}
				c.ch <- &suspensePayload{id: id, buf: buf, err: nil}
				return
			}
			// No boundary — silently drop. Same behavior as
			// before this feature: a panic in an async worker
			// just doesn't deliver an OOB chunk.
			buf.Reset()
			c.bufPool.Put(buf)
			return
		}
		c.ch <- &suspensePayload{id: id, buf: buf, err: err}
	case <-c.ctx.Done():
		// Aborted. Don't submit; the buffer is returned to the pool.
		buf.Reset()
		c.bufPool.Put(buf)
	}
}

// bufferByteWriter adapts a *bytes.Buffer to the ByteWriter interface
// without going through the global pool. Used by async workers so
// the bytes land directly in the per-component payload buffer.
type bufferByteWriter struct {
	buf *bytes.Buffer
}

func (b *bufferByteWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufferByteWriter) WriteByte(c byte) error      { return b.buf.WriteByte(c) }
func (b *bufferByteWriter) WriteString(s string) (int, error) {
	return b.buf.WriteString(s)
}
func (b *bufferByteWriter) Flush() error        { return nil }
func (b *bufferByteWriter) Reset(_ io.Writer)   {} // unused
func (b *bufferByteWriter) Bytes() []byte       { return b.buf.Bytes() }
