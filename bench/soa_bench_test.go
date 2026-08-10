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
