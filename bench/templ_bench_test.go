package bench

import (
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// buildTemplComponent returns a templ.Component that renders the same
// 3KB document structure as makeDoc(). It is a hand-written proxy for
// what `templ generate` emits: direct io.WriteString calls, no
// reflection, no per-render re-parse. The dynamic values (title, item
// title/body) are written directly, as templ does for clean data.
func buildTemplComponent(data map[string]any) templ.Component {
	title, _ := data["Title"].(string)
	page, _ := data["Page"].(map[string]any)
	pageTitle, _ := page["Title"].(string)
	pageLead, _ := page["Lead"].(string)
	item, _ := data["Item"].(map[string]any)
	itemTitle, _ := item["Title"].(string)
	itemBody, _ := item["Body"].(string)

	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		io.WriteString(w, `<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>`)
		io.WriteString(w, title)
		io.WriteString(w, `</title>
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
        <h1>`)
		io.WriteString(w, pageTitle)
		io.WriteString(w, `</h1>
        <p class="lead">`)
		io.WriteString(w, pageLead)
		io.WriteString(w, `</p>
`)
		for i := 0; i < 30; i++ {
			io.WriteString(w, `        <section class="card">
            <h2>`)
			io.WriteString(w, itemTitle)
			io.WriteString(w, `</h2>
            <p>`)
			io.WriteString(w, itemBody)
			io.WriteString(w, `</p>
            <a class="btn" href="/item/`)
			io.WriteString(w, itoa(i))
			io.WriteString(w, `">Read more</a>
        </section>
`)
		}
		io.WriteString(w, `    </main>
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
		return nil
	})
}

// BenchmarkTempl_Direct: a raw templ.Component rendered directly —
// the cost of templ's runtime in isolation.
func BenchmarkTempl_Direct(b *testing.B) {
	data := sampleData()
	comp := buildTemplComponent(data)
	bw := render.AcquireWriter(io.Discard)
	defer render.ReleaseWriter(bw)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := comp.Render(context.Background(), bw); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTempl_Engine: the templ component rendered through the
// nanite-render Templ engine adapter (Register + Render).
func BenchmarkTempl_Engine(b *testing.B) {
	eng := engine.NewTempl()
	eng.Register("index", func(data any) templ.Component {
		return buildTemplComponent(data.(map[string]any))
	})
	data := sampleData()
	bw := render.AcquireWriter(io.Discard)
	defer render.ReleaseWriter(bw)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := eng.Render("index", bw, data); err != nil {
			b.Fatal(err)
		}
	}
}
