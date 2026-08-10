package bench

import (
	"io"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// BenchmarkSoA_ExecuteWalk: the plain-HTML SoA executor walking a
// PRE-PARSED NodeStream — our own hot path, no html/template.
func BenchmarkSoA_ExecuteWalk(b *testing.B) {
	bld, err := render.ParseHTML([]byte(makeDoc()), "index")
	if err != nil {
		b.Fatal(err)
	}
	p := &render.Program{Engine: "html", Name: "index", Nodes: bld.Stream()}
	bw := render.AcquireWriter(io.Discard)
	rc := render.AcquireContext(bw, nil)
	// Build the data once so the benchmark isolates the executor, not
	// the caller's data construction.
	data := sampleData()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := render.Execute(p, bw, rc, data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSoA_Bytecode: the flat instruction-stream executor
// (CompileBytecode) on the same pre-parsed doc — static HTML is
// coalesced into a single write at compile time.
func BenchmarkSoA_Bytecode(b *testing.B) {
	bld, err := render.ParseHTML([]byte(makeDoc()), "index")
	if err != nil {
		b.Fatal(err)
	}
	stream := bld.Stream()
	bc := render.CompileBytecode(stream)
	p := &render.Program{Engine: "html", Name: "index", Nodes: stream, Bytecode: bc}
	bw := render.AcquireWriter(io.Discard)
	rc := render.AcquireContext(bw, nil)
	data := sampleData()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := render.Execute(p, bw, rc, data); err != nil {
			b.Fatal(err)
		}
	}
}
