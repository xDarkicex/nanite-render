package bench

import (
	"fmt"
	"html/template"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/xDarkicex/nanite-render"
)

const (
	latencySamples = 20_000
	latencyWarmup  = 1_000
)

// TestLatency_Percentiles measures p50/p99/p99.9 end-to-end render
// latency for three paths:
//
//  1. Nanite hot (cached) — steady-state after first render
//  2. Nanite cold — fresh registry per render (compile every time)
//  3. Raw html/template re-parse — the GO-Portfolio baseline
//
// Run with:
//
//	go test ./bench/ -run TestLatency -v
func TestLatency_Percentiles(t *testing.T) {
	data := sampleData()
	doc := makeDoc()

	// --- 1. Nanite hot (cached) ---
	reg := newRegistry()
	hot := measureLatency(latencySamples, func() error {
		bw := render.AcquireWriter(io.Discard)
		rc := render.AcquireContext(bw, nil)
		rc.Loader = reg.DefaultLoader()
		err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data)
		render.ReleaseContext(rc)
		render.ReleaseWriter(bw)
		return err
	})

	// --- 2. Nanite cold (fresh registry each time) ---
	cold := measureLatency(latencySamples, func() error {
		r := newRegistry()
		bw := render.AcquireWriter(io.Discard)
		rc := render.AcquireContext(bw, nil)
		rc.Loader = r.DefaultLoader()
		err := r.RenderNamed(rc, render.EngineHTMLTemplate, "index", data)
		render.ReleaseContext(rc)
		render.ReleaseWriter(bw)
		return err
	})

	// --- 3. Raw html/template re-parse every render ---
	raw := measureLatency(latencySamples, func() error {
		var sink []byte
		tmpl, err := template.New("index").Parse(doc)
		if err != nil {
			return err
		}
		var buf smallBuf
		if err := tmpl.Execute(&buf, data); err != nil {
			return err
		}
		sink = buf[:0]
		_ = sink
		return nil
	})

	// --- 4. Raw html/template compiled once (cached baseline) ---
	tmpl := template.Must(template.New("index").Parse(doc))
	rawCached := measureLatency(latencySamples, func() error {
		var buf smallBuf
		return tmpl.Execute(&buf, data)
	})

	t.Logf("\n=== End-to-end render latency (%d samples, warm %d) ===", latencySamples, latencyWarmup)
	t.Logf("\n%-30s %10s %10s %10s", "path", "p50", "p99", "p99.9")
	t.Logf("%-30s %10s %10s %10s", "----------------------------", "----------", "----------", "----------")
	report(t, "nanite hot (cached)", hot)
	report(t, "nanite cold (compile)", cold)
	report(t, "raw html/template re-parse", raw)
	report(t, "raw html/template compiled", rawCached)

	// Cache savings: hot vs cold.
	hotP50, coldP50 := hot.p50(), cold.p50()
	t.Logf("\n=== Cache latency savings ===")
	t.Logf("p50 hot  = %v", hotP50)
	t.Logf("p50 cold = %v", coldP50)
	t.Logf("p50 saved by cache = %v (%.1fx faster)", coldP50-hotP50, ratio(coldP50, hotP50))
}

// smallBuf is a tiny reusable sink for html/template execution.
type smallBuf []byte

func (b *smallBuf) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}

type latencySet struct {
	samples []time.Duration
}

// measureLatency runs fn n times and records each duration. Warm-up
// runs happen first so steady-state (not JIT/cache-cold) is measured.
func measureLatency(n int, fn func() error) latencySet {
	// Warm-up.
	for i := 0; i < latencyWarmup; i++ {
		if err := fn(); err != nil {
			panic(err)
		}
	}
	samples := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		if err := fn(); err != nil {
			panic(err)
		}
		samples[i] = time.Since(start)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return latencySet{samples: samples}
}

func (l latencySet) p50() time.Duration    { return l.percentile(0.50) }
func (l latencySet) p99() time.Duration    { return l.percentile(0.99) }
func (l latencySet) p99_9() time.Duration  { return l.percentile(0.999) }
func (l latencySet) percentile(p float64) time.Duration {
	idx := int(float64(len(l.samples)) * p)
	if idx >= len(l.samples) {
		idx = len(l.samples) - 1
	}
	return l.samples[idx]
}

func report(t *testing.T, name string, l latencySet) {
	t.Logf("%-30s %10v %10v %10v", name, l.p50(), l.p99(), l.p99_9())
}

func ratio(a, b time.Duration) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

var _ = fmt.Sprintf
