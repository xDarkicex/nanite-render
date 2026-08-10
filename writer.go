package render

import (
	"io"
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
type byteWriter struct {
	buf  []byte
	out  io.Writer
	head [128]byte // scratch space for header writes
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
func AcquireWriter(w io.Writer) ByteWriter {
	bw := writerPool.Get().(*byteWriter)
	bw.out = w
	bw.buf = bw.buf[:0]
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
// buffer exceeds the 64 KiB cap.
func (b *byteWriter) Write(p []byte) (int, error) {
	if cap(b.buf) == 0 {
		b.buf = make([]byte, 0, 8*1024)
	}
	if len(b.buf)+len(p) > cap(b.buf) {
		if err := b.Flush(); err != nil {
			return 0, err
		}
		if len(p) > cap(b.buf) {
			// p is larger than the buffer; write directly.
			n, err := b.out.Write(p)
			return n, err
		}
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// WriteByte writes a single byte.
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
// because it avoids the []byte view.
func (b *byteWriter) WriteString(s string) (int, error) {
	if len(b.buf)+len(s) > cap(b.buf) {
		if err := b.Flush(); err != nil {
			return 0, err
		}
		if len(s) > cap(b.buf) {
			return b.out.Write([]byte(s))
		}
	}
	b.buf = append(b.buf, s...)
	return len(s), nil
}

// Flush drains the buffer into the underlying writer.
func (b *byteWriter) Flush() error {
	if len(b.buf) == 0 || b.out == nil {
		return nil
	}
	_, err := b.out.Write(b.buf)
	b.buf = b.buf[:0]
	return err
}

// Reset re-targets the writer at a new underlying io.Writer.
func (b *byteWriter) Reset(w io.Writer) {
	b.out = w
	b.buf = b.buf[:0]
}

// Bytes returns the current buffer contents.
func (b *byteWriter) Bytes() []byte {
	return b.buf
}
