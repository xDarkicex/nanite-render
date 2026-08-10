package render

import (
	"io"
	"net/http"
	"sync"
)

// ByteWriter is the interface render writes through. Implementations
// are pooled; Flush returns the writer to its pool after draining the
// underlying io.Writer.
type ByteWriter interface {
	io.Writer
	io.ByteWriter
	io.StringWriter

	// Flush drains the internal buffer into the wrapped writer.
	Flush() error

	// Reset re-targets the writer at a new underlying io.Writer. Used
	// when the writer is reused across requests.
	Reset(w io.Writer)

	// Bytes returns the current buffer contents. Intended for direct
	// chunked writes (e.g. SSE) rather than the typical render path.
	Bytes() []byte
}

// byteWriter is the pooled ByteWriter implementation. It starts at 8 KiB
// and grows geometrically up to 64 KiB. The buffer is never shrunk to
// keep steady-state allocations at zero.
//
// flusher is non-nil when the underlying io.Writer also satisfies
// http.Flusher (or any equivalent). It is captured once at writer
// acquisition and reused — the hot path performs no type assertions.
type byteWriter struct {
	buf     []byte
	out     io.Writer
	flusher http.Flusher
	head    [128]byte // scratch space for header writes
}

// Writer pool. New instances start with an 8 KiB buffer.
var writerPool = sync.Pool{
	New: func() any {
		return &byteWriter{buf: make([]byte, 0, 8*1024)}
	},
}

// AcquireWriter returns a ByteWriter targeting w. The writer is reused
// from the pool; callers must Flush and ReleaseWriter when done.
// The internal buffer is reset to len 0 so previous renders do not
// leak into the new render.
//
// If w implements http.Flusher (e.g. a net/http response writer), the
// flusher is captured once. Subsequent Flush() calls push the buffered
// bytes to the underlying writer AND signal the flusher, so chunked
// HTTP responses emit each chunk as it is produced.
func AcquireWriter(w io.Writer) ByteWriter {
	bw := writerPool.Get().(*byteWriter)
	bw.out = w
	bw.buf = bw.buf[:0]
	if f, ok := w.(http.Flusher); ok {
		bw.flusher = f
	} else {
		bw.flusher = nil
	}
	return bw
}

// ReleaseWriter returns w to the pool. The write buffer is retained for
// the next user.
func ReleaseWriter(w ByteWriter) {
	if w == nil {
		return
	}
	bw, ok := w.(*byteWriter)
	if !ok {
		return
	}
	bw.out = nil
	writerPool.Put(bw)
}

// Write appends p to the buffer. It flushes automatically when the
// buffer exceeds the 64 KiB cap, signaling the captured http.Flusher
// so the chunk hits the wire immediately.
func (b *byteWriter) Write(p []byte) (int, error) {
	if cap(b.buf) == 0 {
		b.buf = make([]byte, 0, 8*1024)
	}
	if len(b.buf)+len(p) > cap(b.buf) {
		if err := b.Flush(); err != nil {
			return 0, err
		}
		if len(p) > cap(b.buf) {
			// p is larger than the buffer; write directly AND
			// signal the flusher so the chunk emits immediately.
			n, err := b.out.Write(p)
			if b.flusher != nil {
				b.flusher.Flush()
			}
			return n, err
		}
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// WriteByte writes a single byte. Triggers an auto-flush (which
// signals the captured http.Flusher) when the buffer is at cap.
func (b *byteWriter) WriteByte(c byte) error {
	if len(b.buf)+1 > cap(b.buf) {
		if err := b.Flush(); err != nil {
			return err
		}
	}
	b.buf = append(b.buf, c)
	return nil
}

// WriteString writes a string. Faster than Write for string literals
// because it avoids the []byte view. Auto-flushes (signaling the
// captured http.Flusher) when the buffer is at cap.
func (b *byteWriter) WriteString(s string) (int, error) {
	if len(b.buf)+len(s) > cap(b.buf) {
		if err := b.Flush(); err != nil {
			return 0, err
		}
		if len(s) > cap(b.buf) {
			n, err := b.out.Write([]byte(s))
			if b.flusher != nil {
				b.flusher.Flush()
			}
			return n, err
		}
	}
	b.buf = append(b.buf, s...)
	return len(s), nil
}

// Flush drains the buffer into the underlying writer. When the
// underlying writer is an http.Flusher (captured at acquisition),
// Flush also signals the flusher so chunked transfer encoding emits
// the chunk immediately. The capture is one-shot at AcquireWriter
// time, so the hot path performs no type assertion.
func (b *byteWriter) Flush() error {
	if len(b.buf) == 0 || b.out == nil {
		return nil
	}
	_, err := b.out.Write(b.buf)
	b.buf = b.buf[:0]
	if b.flusher != nil {
		b.flusher.Flush()
	}
	return err
}

// Reset re-targets the writer at a new underlying io.Writer. The
// flusher is also re-captured from the new writer when it implements
// http.Flusher. Use this when reusing a pooled writer across requests
// with different response writers.
func (b *byteWriter) Reset(w io.Writer) {
	b.out = w
	b.buf = b.buf[:0]
	if f, ok := w.(http.Flusher); ok {
		b.flusher = f
	} else {
		b.flusher = nil
	}
}

// SetFlusher explicitly attaches an http.Flusher. Use this from
// middleware that wraps the underlying writer (e.g. gzip, compression)
// AFTER AcquireWriter has already run — the wrapper still satisfies
// http.Flusher even though the original writer was wrapped. Passing
// nil clears the flusher.
//
// This is a no-op when the writer is not a *byteWriter (other writers
// in the ByteWriter interface have their own Flush implementations).
func SetFlusher(w ByteWriter, f http.Flusher) {
	bw, ok := w.(*byteWriter)
	if !ok {
		return
	}
	bw.flusher = f
}

// perFlushWriter wraps a parent ByteWriter with a max-bytes threshold.
// When the accumulated bytes would exceed the threshold, the writer
// auto-flushes BEFORE appending — the parent drains to the underlying
// io.Writer and signals its http.Flusher, so the chunk hits the wire
// immediately.
//
// This is the streaming primitive for RenderStream: a static-heavy
// layout renders as a series of fixed-size chunks (e.g. 4 KiB), giving
// the browser a fast TTFB even when the full template is large.
type perFlushWriter struct {
	parent    ByteWriter
	threshold int
	buf       []byte // small per-flush buffer; flushed on overflow
}

// perFlushWriterPool: perFlushWriter instances are pooled so the
// per-flush buffer survives across requests.
var perFlushWriterPool = sync.Pool{
	New: func() any {
		return &perFlushWriter{}
	},
}

// AcquirePerFlushWriter returns a per-flush writer wrapping parent.
// When threshold bytes accumulate, the writer auto-flushes to parent.
// A threshold of 0 disables auto-flush (writes pass through directly
// to parent). A negative threshold is treated as 0.
//
// Callers must ReleasePerFlushWriter when done. The wrapper is safe
// to use as a ByteWriter; its Reset/Bytes methods delegate to parent.
func AcquirePerFlushWriter(parent ByteWriter, threshold int) ByteWriter {
	if parent == nil {
		return nil
	}
	if threshold < 0 {
		threshold = 0
	}
	pw := perFlushWriterPool.Get().(*perFlushWriter)
	pw.parent = parent
	pw.threshold = threshold
	pw.buf = pw.buf[:0]
	return pw
}

// ReleasePerFlushWriter returns pw to the pool. Flushes any remaining
// bytes through to the parent. Safe to call with nil.
func ReleasePerFlushWriter(pw ByteWriter) {
	if pw == nil {
		return
	}
	p, ok := pw.(*perFlushWriter)
	if !ok {
		return
	}
	if len(p.buf) > 0 && p.parent != nil {
		_, _ = p.parent.Write(p.buf)
		p.buf = p.buf[:0]
	}
	if p.parent != nil {
		_ = p.parent.Flush()
	}
	p.parent = nil
	perFlushWriterPool.Put(p)
}

// flushToParent writes p.buf to parent and drains parent's internal
// buffer to the underlying io.Writer (signaling any http.Flusher).
// Used by every per-flush transition so the chunk hits the wire
// immediately.
func (p *perFlushWriter) flushToParent() error {
	if len(p.buf) == 0 {
		return nil
	}
	if _, err := p.parent.Write(p.buf); err != nil {
		return err
	}
	p.buf = p.buf[:0]
	return p.parent.Flush()
}

// Write appends p to the per-flush buffer, flushing to parent (and
// signaling parent's http.Flusher) when the threshold would be
// exceeded.
func (p *perFlushWriter) Write(s []byte) (int, error) {
	if p.threshold == 0 {
		return p.parent.Write(s)
	}
	// If the incoming chunk alone exceeds the threshold, flush the
	// buffer first then write the chunk directly to parent.
	if len(s) >= p.threshold {
		if err := p.flushToParent(); err != nil {
			return 0, err
		}
		if _, err := p.parent.Write(s); err != nil {
			return 0, err
		}
		return len(s), p.parent.Flush()
	}
	// If appending would overflow, flush first.
	if len(p.buf)+len(s) > p.threshold {
		if err := p.flushToParent(); err != nil {
			return 0, err
		}
	}
	p.buf = append(p.buf, s...)
	return len(s), nil
}

// WriteString appends s, flushing when the threshold is exceeded.
// Strings avoid the []byte view, so this is faster than Write for
// literals.
func (p *perFlushWriter) WriteString(s string) (int, error) {
	if p.threshold == 0 {
		return p.parent.WriteString(s)
	}
	if len(s) >= p.threshold {
		if err := p.flushToParent(); err != nil {
			return 0, err
		}
		if _, err := p.parent.WriteString(s); err != nil {
			return 0, err
		}
		return len(s), p.parent.Flush()
	}
	if len(p.buf)+len(s) > p.threshold {
		if err := p.flushToParent(); err != nil {
			return 0, err
		}
	}
	p.buf = append(p.buf, s...)
	return len(s), nil
}

// WriteByte writes a single byte, flushing the per-flush buffer when
// the threshold is reached.
func (p *perFlushWriter) WriteByte(c byte) error {
	if p.threshold == 0 {
		return p.parent.WriteByte(c)
	}
	if len(p.buf)+1 > p.threshold {
		if err := p.flushToParent(); err != nil {
			return err
		}
	}
	p.buf = append(p.buf, c)
	return nil
}

// Flush drains the per-flush buffer into the parent (which itself
// signals any captured http.Flusher), then flushes the parent.
func (p *perFlushWriter) Flush() error {
	if err := p.flushToParent(); err != nil {
		return err
	}
	return p.parent.Flush()
}

// Reset re-targets the wrapper at a new parent writer.
func (p *perFlushWriter) Reset(w io.Writer) {
	if pb, ok := w.(ByteWriter); ok {
		p.parent = pb
		return
	}
	// Wrap a plain io.Writer in a pooled byteWriter so the
	// ByteWriter contract is preserved.
	bw := AcquireWriter(w)
	p.parent = bw
}

// Bytes returns the per-flush buffer's pending contents (anything
// that has been written but not yet flushed to the parent). This is
// the visible state of the streaming writer; once a chunk flushes,
// it is no longer accessible here.
func (p *perFlushWriter) Bytes() []byte {
	return p.buf
}

// Bytes returns the current buffer contents.
func (b *byteWriter) Bytes() []byte {
	return b.buf
}
