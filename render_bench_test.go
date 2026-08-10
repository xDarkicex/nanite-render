package render

import (
	"bytes"
	"html/template"
	"io"
	"net/http"
	"testing"
)

// stubEngine is a no-op engine used for benchmarks. The Compile
// returns a Program with a small NodeStream and Execute writes
// nothing — we are benchmarking the cache + registry hot path, not
// the engine's render work.
type stubEngine struct {
	name string
	prog *Program
}

func (s *stubEngine) Name() string { return s.name }
func (s *stubEngine) Compile(src []byte, name string) (*Program, error) {
	if s.prog != nil {
		return s.prog, nil
	}
	return &Program{Engine: s.name, Name: name}, nil
}
func (s *stubEngine) Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error {
	return nil
}

// stubWriter is a no-op ByteWriter for benchmarks.
type stubWriter struct{}

func (stubWriter) Write(p []byte) (int, error)       { return len(p), nil }
func (stubWriter) WriteByte(c byte) error            { return nil }
func (stubWriter) WriteString(s string) (int, error) { return len(s), nil }
func (stubWriter) Flush() error                      { return nil }
func (stubWriter) Reset(w io.Writer)                 {}
func (stubWriter) Bytes() []byte                     { return nil }

// Benchmark full Registry.Render hot path with all features off.
func BenchmarkRegistry_RenderHotPath(b *testing.B) {
	prog := &Program{
		Engine: "stub",
		Name:   "test",
		Nodes: NodeStream{
			Tag:      []string{"div"},
			Flags:    []uint8{0},
			NS:       []uint16{0},
			AttrKeys: []string{"id"},
			AttrVals: []any{"x"},
		},
	}
	eng := &stubEngine{name: "stub", prog: prog}
	reg := NewRegistry(eng)
	// Pre-warm the cache.
	bw := AcquireWriter(stubWriter{})
	defer ReleaseWriter(bw)
	rc := AcquireContext(bw, &http.Request{})
	defer ReleaseContext(rc)
	rc.Loader = func(name string) ([]byte, error) {
		return []byte("test"), nil
	}
	if err := reg.Render(rc, "stub", "test", nil); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := reg.Render(rc, "stub", "test", nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Benchmark with conditional + FuncMap enabled.
func BenchmarkRegistry_RenderWithFeatures(b *testing.B) {
	prog := &Program{
		Engine: "stub",
		Name:   "test",
		ETag:   `"abc123"`,
		Nodes: NodeStream{
			Tag:      []string{"div"},
			Flags:    []uint8{0},
			NS:       []uint16{0},
			AttrKeys: []string{"id"},
			AttrVals: []any{"x"},
		},
	}
	eng := &stubEngine{name: "stub", prog: prog}
	reg := NewRegistry(eng)
	reg.Conditional(true)
	reg.FuncMap(func(rc *RenderContext) template.FuncMap {
		return template.FuncMap{
			"x": func() string { return "" },
		}
	})
	bw := AcquireWriter(stubWriter{})
	defer ReleaseWriter(bw)
	rc := AcquireContext(bw, &http.Request{})
	defer ReleaseContext(rc)
	rc.Loader = func(name string) ([]byte, error) {
		return []byte("test"), nil
	}
	if err := reg.Render(rc, "stub", "test", nil); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := reg.Render(rc, "stub", "test", nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Benchmark the executor hot path walking a small SoA.
func BenchmarkExecute_WalkSmall(b *testing.B) {
	prog := &Program{
		Engine: "test",
		Name:   "small",
		Nodes: NodeStream{
			Tag:         []string{"div", "p", "span"},
			Flags:       []uint8{0, 0, 0},
			NS:          []uint16{0, 0, 0},
			AttrKeys:    []string{"id"},
			AttrVals:    []any{"x"},
			AttrStart:   []uint32{0, 0, 0},
			AttrEnd:     []uint32{0, 0, 0},
			TextOff:     []uint32{0, 0, 0, 0, 0, 0},
			Parent:      []int32{-1, 0, 0},
			FirstChild:  []int32{1, -1, -1},
			NextSibling: []int32{-1, 2, -1},
			Count:       3,
		},
	}
	var buf bytes.Buffer
	w := &bytesWriter{w: &buf}
	for b.Loop() {
		buf.Reset()
		w.Reset(&buf)
		if err := Execute(prog, w, nil, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// bytesWriter is a minimal ByteWriter for benchmarks.
type bytesWriter struct {
	w *bytes.Buffer
}

func (b *bytesWriter) Write(p []byte) (int, error)       { return b.w.Write(p) }
func (b *bytesWriter) WriteByte(c byte) error            { return b.w.WriteByte(c) }
func (b *bytesWriter) WriteString(s string) (int, error) { return b.w.WriteString(s) }
func (b *bytesWriter) Flush() error                      { return nil }
func (b *bytesWriter) Reset(w io.Writer)                 { bw, _ := w.(*bytes.Buffer); b.w = bw }
func (b *bytesWriter) Bytes() []byte                     { return b.w.Bytes() }
