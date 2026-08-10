package bench

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// makeDoc builds a realistic HTML template (~3KB) with interpolation,
// a component, and enough structure for the SoA walk and minifier to
// chew on. The same doc is used across all benchmarks so numbers are
// comparable.
func makeDoc() string {
	var b strings.Builder
	// NOTE: no <!DOCTYPE> — the plain-HTML ParseHTML parser doesn't
	// handle doctype declarations, and this doc feeds both it and
	// html/template in the benchmarks.
	b.WriteString(`<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{.Title}}</title>
    <link rel="stylesheet" href="/assets/site.css">
</head>
<body>
    <nav class="site-nav">
        <a href="/">Home</a>
        <a href="/about">About</a>
        <a href="/blog">Blog</a>
        <a href="/contact">Contact</a>
    </nav>
    <main>
        <h1>{{.Page.Title}}</h1>
        <p class="lead">{{.Page.Lead}}</p>
`)
	for i := 0; i < 30; i++ {
		b.WriteString(`        <section class="card">
            <h2>{{.Item.Title}}</h2>
            <p>{{.Item.Body}}</p>
            <a class="btn" href="/item/` + itoa(i) + `">Read more</a>
        </section>
`)
	}
	b.WriteString(`    </main>
    <footer class="site-footer">
        <p>&copy; 2026 Acme. All rights reserved.</p>
        <ul>
            <li><a href="/privacy">Privacy</a></li>
            <li><a href="/terms">Terms</a></li>
        </ul>
    </footer>
</body>
</html>
`)
	return b.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// sampleData is the data passed to renders.
func sampleData() map[string]any {
	item := map[string]any{"Title": "A card", "Body": "Body text " + strings.Repeat("lorem ipsum dolor ", 4)}
	return map[string]any{
		"Title": "Acme Site",
		"Page":  map[string]any{"Title": "Welcome", "Lead": "A lead paragraph"},
		"Item":  item,
	}
}

// newRegistry builds a Registry with the HTMLTemplate engine, a map
// loader, and a default loader wired to it.
func newRegistry() *render.Registry {
	doc := makeDoc()
	reg := render.New(
		render.WithEngine(engine.NewHTMLTemplate()),
		render.WithDefaultLoader(render.NewMapLoader(map[string][]byte{
			"index": []byte(doc),
		})),
		render.WithCacheSize(1024),
	)
	return reg
}

// freshCache replaces the registry's cache with a fresh one so
// cold-path benchmarks compile-on-miss every iteration.
func freshCache(reg *render.Registry) {
	reg.SetCache(render.NewCache(1024))
}

// newContext returns a pooled writer + RenderContext wired to a
// discard sink and the registry's default loader.
func newContext() (render.ByteWriter, *render.RenderContext) {
	bw := render.AcquireWriter(io.Discard)
	rc := render.AcquireContext(bw, &http.Request{Header: http.Header{}})
	rc.Loader = newRegistry().DefaultLoader()
	return bw, rc
}

// executeSoA parses the doc once and walks the NodeStream via the
// plain HTML engine's executor — the SoA executor cost in isolation.
func executeSoA(bw render.ByteWriter, rc *render.RenderContext) error {
	b, err := render.ParseHTML([]byte(makeDoc()), "index")
	if err != nil {
		return err
	}
	p := &render.Program{Engine: "html", Name: "index", Nodes: b.Stream()}
	return render.Execute(p, bw, rc, sampleData())
}

// renderToBuffer is a helper for savings benchmarks: render into a
// reusable buffer (mimics an actual HTTP write).
func renderToBuffer(reg *render.Registry, rc *render.RenderContext, data any) error {
	// rc.Writer targets io.Discard already; for buffer-based benches
	// the caller supplies a buffer-backed writer.
	return reg.RenderNamed(rc, render.EngineHTMLTemplate, "index", data)
}

var _ = bytes.NewBuffer
