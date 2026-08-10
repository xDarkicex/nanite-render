package bench

import (
	"bytes"
	"html/template"
	"io"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// BenchmarkSavings_NaniteCached: the nanite-render hot path. The
// template is compiled once, then rendered from cache.
func BenchmarkSavings_NaniteCached(b *testing.B) {
	reg := newRegistry()
	data := sampleData()
	bw := render.AcquireWriter(io.Discard)
	rc := render.AcquireContext(bw, nil)
	rc.Loader = reg.DefaultLoader()
	if err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSavings_RawHTMLTemplateReParse: the GO-Portfolio-style
// baseline — re-parse the template and execute on every render. This
// is what nanite-render's cache eliminates.
func BenchmarkSavings_RawHTMLTemplateReParse(b *testing.B) {
	doc := makeDoc()
	data := sampleData()
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		tmpl, err := template.New("index").Parse(doc)
		if err != nil {
			b.Fatal(err)
		}
		if err := tmpl.Execute(&buf, data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSavings_RawHTMLTemplateCompiled: the "cached baseline" —
// parse once, then execute from the compiled template. This isolates
// the cost nanite-render adds on top of a compiled html/template.
func BenchmarkSavings_RawHTMLTemplateCompiled(b *testing.B) {
	doc := makeDoc()
	data := sampleData()
	tmpl := template.Must(template.New("index").Parse(doc))
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := tmpl.Execute(&buf, data); err != nil {
			b.Fatal(err)
		}
	}
}
