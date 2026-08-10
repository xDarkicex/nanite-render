package render

import (
	"compress/gzip"
	"io"
	"strings"
)

// CompressConfig controls automatic gzip compression of rendered
// output. Mirrors the original GO-Portfolio's hint at compression
// (`Transfer-Encoding: gzip, chunked`) but actually implements it.
type CompressConfig struct {
	Level   int
	MinSize int
	Types   []string
}

// acceptsGzip reports whether the request accepts gzip encoding.
func acceptsGzip(rc *RenderContext) bool {
	if rc == nil || rc.Request == nil {
		return false
	}
	ae := rc.Request.Header.Get("Accept-Encoding")
	if ae == "" {
		return false
	}
	for part := range strings.SplitSeq(ae, ",") {
		if strings.Contains(strings.TrimSpace(part), "gzip") {
			return true
		}
	}
	return false
}

// CompressWriter wraps w in a gzip writer when compression is
// enabled and the request accepts it.
func (r *Registry) CompressWriter(rc *RenderContext, w ByteWriter) (ByteWriter, func() error) {
	cfg := r.compress.Load()
	if cfg == nil || !acceptsGzip(rc) {
		return w, func() error { return nil }
	}
	rc.WriteHeader("Content-Encoding", "gzip")
	rc.WriteHeader("Vary", "Accept-Encoding")
	return &gzipByteWriter{w: w, level: cfg.Level}, nil
}

// gzipByteWriter wraps a ByteWriter in a gzip encoder.
type gzipByteWriter struct {
	w     ByteWriter
	gz    *gzip.Writer
	level int
}

// Write writes p through the gzip writer to the underlying writer.
func (g *gzipByteWriter) Write(p []byte) (int, error) {
	if g.gz == nil {
		gw, err := gzip.NewWriterLevel(g.w, g.level)
		if err != nil {
			return 0, err
		}
		g.gz = gw
	}
	return g.gz.Write(p)
}

// WriteByte writes a single byte through the underlying writer.
func (g *gzipByteWriter) WriteByte(c byte) error {
	if g.gz == nil {
		gw, err := gzip.NewWriterLevel(g.w, g.level)
		if err != nil {
			return err
		}
		g.gz = gw
	}
	b := [1]byte{c}
	_, err := g.gz.Write(b[:])
	return err
}

// WriteString writes a string through gzip.
func (g *gzipByteWriter) WriteString(s string) (int, error) {
	if g.gz == nil {
		gw, err := gzip.NewWriterLevel(g.w, g.level)
		if err != nil {
			return 0, err
		}
		g.gz = gw
	}
	return g.gz.Write([]byte(s))
}

// Flush flushes the gzip stream to the underlying writer.
func (g *gzipByteWriter) Flush() error {
	if g.gz == nil {
		return nil
	}
	if err := g.gz.Flush(); err != nil {
		return err
	}
	return g.w.Flush()
}

// Reset re-targets the encoder at a new underlying writer.
func (g *gzipByteWriter) Reset(w io.Writer) {
	bw, ok := w.(ByteWriter)
	if !ok {
		return
	}
	g.w = bw
	if g.gz != nil {
		g.gz.Reset(w)
	}
}

// Bytes returns the underlying writer's buffer.
func (g *gzipByteWriter) Bytes() []byte { return g.w.Bytes() }
