package bench

import (
	"io"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// BenchmarkPipeline_LoadSource: the loader (map lookup + slice copy).
func BenchmarkPipeline_LoadSource(b *testing.B) {
	reg := newRegistry()
	loader := reg.DefaultLoader()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := loader("index")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPipeline_Compile: one-time parse of the doc via the
// HTMLTemplate engine (html/template.Parse under the hood).
func BenchmarkPipeline_Compile(b *testing.B) {
	src := []byte(makeDoc())
	eng := newRegistry().Engine(render.EngineHTMLTemplate.String())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := eng.Compile(src, "index"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPipeline_CacheHit: lock-free cache Get after warm.
func BenchmarkPipeline_CacheHit(b *testing.B) {
	reg := newRegistry()
	cache := reg.Cache()
	cache.Put(render.EngineHTMLTemplate.String(), "index", &render.Program{Name: "index"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(render.EngineHTMLTemplate.String(), "index")
	}
}

// BenchmarkPipeline_ExecuteSoA: the SoA executor walk in isolation.
func BenchmarkPipeline_ExecuteSoA(b *testing.B) {
	bw, rc := newContext()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := executeSoA(bw, rc); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPipeline_RenderCold: full Registry.Render with a fresh
// registry per iteration — compiles every time (cache always misses).
func BenchmarkPipeline_RenderCold(b *testing.B) {
	data := sampleData()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reg := newRegistry()
		bw := render.AcquireWriter(io.Discard)
		rc := render.AcquireContext(bw, nil)
		rc.Loader = reg.DefaultLoader()
		err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data)
		render.ReleaseContext(rc)
		render.ReleaseWriter(bw)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPipeline_RenderHot: full Registry.Render with a warm cache
// — the steady-state path.
func BenchmarkPipeline_RenderHot(b *testing.B) {
	reg := newRegistry()
	data := sampleData()
	// Warm the cache.
	bw := render.AcquireWriter(io.Discard)
	rc := render.AcquireContext(bw, nil)
	rc.Loader = reg.DefaultLoader()
	if err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data); err != nil {
		b.Fatal(err)
	}
	render.ReleaseContext(rc)
	render.ReleaseWriter(bw)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bw := render.AcquireWriter(io.Discard)
		rc := render.AcquireContext(bw, nil)
		rc.Loader = reg.DefaultLoader()
		err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data)
		render.ReleaseContext(rc)
		render.ReleaseWriter(bw)
		if err != nil {
			b.Fatal(err)
		}
	}
}
