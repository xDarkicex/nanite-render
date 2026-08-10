package render_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// TestPreload_EmitsLinkTags verifies that <PRELOADS/> in a plain-HTML
// layout emits the registered hints as <link rel="preload"> tags.
func TestPreload_EmitsLinkTags(t *testing.T) {
	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.Preload(render.PreloadHint{Href: "/static/app.css", As: "style", Type: "text/css"})
	reg.Preload(render.PreloadHint{Href: "/static/app.js", As: "script"})

	layout := `<html><head><PRELOADS/></head><body>hi</body></html>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"layout": []byte(layout),
		"view":   nil,
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "layout", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, `<link rel="preload" href="/static/app.css" as="style" type="text/css">`) {
		t.Errorf("css preload missing: %q", out)
	}
	if !strings.Contains(out, `<link rel="preload" href="/static/app.js" as="script">`) {
		t.Errorf("js preload missing: %q", out)
	}
}

// TestPreload_Deduplicates verifies the same hint registered twice is
// only emitted once.
func TestPreload_Deduplicates(t *testing.T) {
	reg := render.New(render.WithEngines(engine.NewHTML()))
	hint := render.PreloadHint{Href: "/x.css", As: "style", Type: "text/css"}
	reg.Preload(hint)
	reg.Preload(hint) // no-op
	reg.Preload(hint) // no-op

	layout := `<PRELOADS/>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"layout": []byte(layout),
		"view":   nil,
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "layout", nil); err != nil {
		t.Fatal(err)
	}
	bw.Flush()

	out := buf.String()
	if got := strings.Count(out, `href="/x.css"`); got != 1 {
		t.Errorf("expected 1 emit, got %d in %q", got, out)
	}
}

// TestPreload_Unpreload verifies Unpreload removes matching hints
// while leaving others intact.
func TestPreload_Unpreload(t *testing.T) {
	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.Preload(render.PreloadHint{Href: "/a.css", As: "style", Type: "text/css"})
	reg.Preload(render.PreloadHint{Href: "/a.js", As: "script"})
	reg.Unpreload("style", "text/css")

	hints := reg.Preloads()
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint after unpreload, got %d", len(hints))
	}
	if hints[0].As != "script" {
		t.Errorf("wrong hint remaining: %+v", hints[0])
	}
}

// TestPreload_EscapesAttributes verifies href and as values are
// HTML-escaped so a malicious value can't break out of the attribute.
func TestPreload_EscapesAttributes(t *testing.T) {
	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.Preload(render.PreloadHint{
		Href: `"><script>alert(1)</script>`,
		As:   `" onerror="x`,
	})

	layout := `<PRELOADS/>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"layout": []byte(layout),
		"view":   nil,
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "layout", nil); err != nil {
		t.Fatal(err)
	}
	bw.Flush()
	out := buf.String()

	if strings.Contains(out, "<script>alert(1)") {
		t.Errorf("unescaped href injected script: %q", out)
	}
	if strings.Contains(out, `onerror="x`) {
		t.Errorf("unescaped as broke out: %q", out)
	}
	// html.EscapeString uses &#34; for double quotes (not &quot;).
	// Assert the escape happened without coupling to the exact form.
	if !strings.Contains(out, `&#34;`) {
		t.Errorf("expected escaped quotes in output: %q", out)
	}
}

// TestPreload_EmptyRegistry verifies <PRELOADS/> emits nothing when
// no hints are registered (rather than erroring).
func TestPreload_EmptyRegistry(t *testing.T) {
	reg := render.New(render.WithEngines(engine.NewHTML()))

	layout := `<PRELOADS/>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"layout": []byte(layout),
		"view":   nil,
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "layout", nil); err != nil {
		t.Fatal(err)
	}
	bw.Flush()
	if got := buf.String(); got != "" {
		t.Errorf("expected empty output, got %q", got)
	}
}
