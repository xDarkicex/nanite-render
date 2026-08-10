package render

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// flushRecorder is an io.Writer + http.Flusher that records every
// Flush call so tests can assert flush-on-write behavior.
type flushRecorder struct {
	buf    bytes.Buffer
	flushN int
}

func (f *flushRecorder) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *flushRecorder) Flush()                      { f.flushN++ }

// plainRecorder is an io.Writer WITHOUT a Flush method, so the
// AcquireWriter type assertion fails and no flusher is captured.
type plainRecorder struct {
	buf bytes.Buffer
}

func (p *plainRecorder) Write(b []byte) (int, error) { return p.buf.Write(b) }

func TestByteWriter_FlushCapturesFlusher(t *testing.T) {
	rec := &flushRecorder{}
	// *flushRecorder satisfies http.Flusher via its Flush() method,
	// so AcquireWriter's type assertion captures it automatically.
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	bw.WriteString("hello")
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.flushN != 1 {
		t.Errorf("flusher not called: flushN=%d", rec.flushN)
	}
	if rec.buf.String() != "hello" {
		t.Errorf("buffer mismatch: %q", rec.buf.String())
	}
}

func TestByteWriter_SetFlusherExplicit(t *testing.T) {
	// plainRecorder has no Flush method → AcquireWriter captures
	// nothing. SetFlusher attaches one explicitly (the middleware
	// case: writer was wrapped AFTER acquisition).
	p := &plainRecorder{}
	bw := AcquireWriter(p)
	defer ReleaseWriter(bw)

	rec := &flushRecorder{}
	SetFlusher(bw, http.Flusher(rec))

	bw.WriteString("x")
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.flushN != 1 {
		t.Errorf("explicit flusher not signaled: flushN=%d", rec.flushN)
	}
}

func TestByteWriter_AutoFlushOnCap(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	// Fill beyond the 8 KiB starting cap.
	big := bytes.Repeat([]byte{'x'}, 80*1024)
	if _, err := bw.Write(big); err != nil {
		t.Fatal(err)
	}
	if rec.flushN == 0 {
		t.Errorf("auto-flush on cap did not signal flusher")
	}
	if rec.buf.Len() != 80*1024 {
		t.Errorf("data lost on auto-flush: got %d bytes", rec.buf.Len())
	}
}

func TestByteWriter_AutoFlushWriteString(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	big := strings.Repeat("y", 80*1024)
	if _, err := bw.WriteString(big); err != nil {
		t.Fatal(err)
	}
	if rec.flushN == 0 {
		t.Errorf("WriteString auto-flush did not signal flusher")
	}
}

func TestByteWriter_FlushEmptyNoOp(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	// Empty flush: should NOT signal the flusher (nothing to push).
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.flushN != 0 {
		t.Errorf("empty flush signaled flusher: flushN=%d", rec.flushN)
	}
}

func TestByteWriter_NoFlusherNoFlush(t *testing.T) {
	// Plain writer (no Flusher). Flush should still drain the buffer
	// but not panic.
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)

	bw.WriteString("hello")
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Errorf("buffer mismatch: %q", buf.String())
	}
}

func TestByteWriter_ResetRecapturesFlusher(t *testing.T) {
	p := &plainRecorder{}
	bw := AcquireWriter(p)
	defer ReleaseWriter(bw)

	// First writer has no flusher (plainRecorder has no Flush method).
	bw.WriteString("a")
	bw.Flush()
	// No way to assert against plainRecorder, but it must not panic.
	if p.buf.String() != "a" {
		t.Errorf("first writer buffer: %q", p.buf.String())
	}

	// Reset to a writer with a flusher; Reset re-captures it.
	rec2 := &flushRecorder{}
	bw.Reset(io.Writer(rec2))
	bw.WriteString("b")
	bw.Flush()
	if rec2.flushN != 1 {
		t.Errorf("Reset flusher not recaptured: flushN=%d", rec2.flushN)
	}
}

// ---------------------------------------------------------------------------
// perFlushWriter tests — the streaming primitive for RenderStream.
// ---------------------------------------------------------------------------

func TestPerFlushWriter_PassesThroughThresholdZero(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	pw := AcquirePerFlushWriter(bw, 0)
	defer ReleasePerFlushWriter(pw)

	pw.WriteString("hello")
	// threshold=0 → no auto-flush yet; data sits in the wrapper's
	// tiny buffer until Flush.
	if rec.buf.Len() != 0 {
		t.Errorf("threshold=0 should not auto-flush: got %d bytes", rec.buf.Len())
	}
	pw.Flush()
	if rec.buf.String() != "hello" {
		t.Errorf("post-flush mismatch: %q", rec.buf.String())
	}
}

func TestPerFlushWriter_AutoFlushesAtThreshold(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	pw := AcquirePerFlushWriter(bw, 8) // 8-byte chunks
	defer ReleasePerFlushWriter(pw)

	pw.WriteString("12345") // 5 bytes — under threshold
	if rec.buf.Len() != 0 {
		t.Errorf("under threshold: got %d bytes", rec.buf.Len())
	}
	pw.WriteString("678") // appending would total 8; still under
	if rec.buf.Len() != 0 {
		t.Errorf("exactly at threshold should not flush yet: got %d bytes", rec.buf.Len())
	}
	pw.WriteString("9") // now 9 → exceeds 8 → flush before append
	if rec.buf.Len() == 0 || rec.buf.String() != "12345678" {
		t.Errorf("auto-flush emitted %q", rec.buf.String())
	}
	if rec.flushN == 0 {
		t.Errorf("auto-flush did not signal http.Flusher")
	}
}

func TestPerFlushWriter_LargeChunkPassesThrough(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	pw := AcquirePerFlushWriter(bw, 8)
	defer ReleasePerFlushWriter(pw)

	// Chunk larger than threshold: bypasses the per-flush buffer.
	big := strings.Repeat("z", 1024)
	pw.WriteString(big)
	if rec.buf.String() != big {
		t.Errorf("large chunk lost: got %d bytes", rec.buf.Len())
	}
	if rec.flushN == 0 {
		t.Errorf("large chunk did not flush")
	}
}

func TestPerFlushWriter_BytesDelegates(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	pw := AcquirePerFlushWriter(bw, 1024)
	defer ReleasePerFlushWriter(pw)

	pw.WriteString("xyz")
	if got := string(pw.Bytes()); got != "xyz" {
		t.Errorf("Bytes() = %q", got)
	}
}

func TestPerFlushWriter_ReleaseFlushesRemainder(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	pw := AcquirePerFlushWriter(bw, 1024)
	pw.WriteString("trailing bytes")
	// Don't call Flush; Release must drain the buffer.
	ReleasePerFlushWriter(pw)
	if rec.buf.String() != "trailing bytes" {
		t.Errorf("Release lost trailing bytes: got %q", rec.buf.String())
	}
}

func TestPerFlushWriter_NegativeThresholdTreatedAsZero(t *testing.T) {
	rec := &flushRecorder{}
	bw := AcquireWriter(io.Writer(rec))
	defer ReleaseWriter(bw)

	pw := AcquirePerFlushWriter(bw, -1) // invalid → 0
	defer ReleasePerFlushWriter(pw)

	pw.WriteString("x")
	if rec.buf.Len() != 0 {
		t.Errorf("negative threshold should disable auto-flush: got %d bytes", rec.buf.Len())
	}
}
