package render_test

import (
	"bytes"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// TestAssets_CollectAndEmit verifies RequiresCSS/RequiresJS
// collect and NANO_ASSETS emits the tags.
func TestAssets_CollectAndEmit(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CHART").
		Render(func(c *render.ComponentContext) error {
			c.RequiresCSS("/static/css/chart.css")
			c.RequiresJS("/static/js/chart.js")
			return nil
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "CHART", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.RenderComponent(bw, rc, "NANO_ASSETS", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	out := buf.String()
	if !strings.Contains(out, `<link rel="stylesheet" href="/static/css/chart.css">`) {
		t.Errorf("css link missing: %q", out)
	}
	if !strings.Contains(out, `<script defer src="/static/js/chart.js"></script>`) {
		t.Errorf("js script missing: %q", out)
	}
}

// TestAssets_Deduplication verifies the same path registered many
// times emits once (the loop case: 50 renders → 1 tag).
func TestAssets_Deduplication(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		Render(func(c *render.ComponentContext) error {
			c.RequiresCSS("/static/css/card.css")
			c.RequiresJS("/static/js/card.js")
			return nil
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	// Simulate a loop rendering the component 50 times.
	for i := 0; i < 50; i++ {
		if err := reg.RenderComponent(bw, rc, "CARD", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.RenderComponent(bw, rc, "NANO_ASSETS", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	out := buf.String()
	if got := strings.Count(out, `card.css`); got != 1 {
		t.Errorf("css emitted %d times, want 1: %q", got, out)
	}
	if got := strings.Count(out, `card.js`); got != 1 {
		t.Errorf("js emitted %d times, want 1: %q", got, out)
	}
}

// TestAssets_Escaping verifies paths are HTML-escaped.
func TestAssets_Escaping(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("EVIL").
		Render(func(c *render.ComponentContext) error {
			c.RequiresCSS(`/css/a.css"><script>alert(1)</script>`)
			return nil
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "EVIL", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.RenderComponent(bw, rc, "NANO_ASSETS", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if strings.Contains(buf.String(), `<script>alert`) {
		t.Errorf("unescaped path injected script: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `href="/css/a.css&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Errorf("path not escaped: %q", buf.String())
	}
}

// TestAssets_FullPageLoad verifies the two-pass flow: a component
// in the view declares assets, the layout's <NANO_ASSETS/> emits
// them.
func TestAssets_FullPageLoad(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PROFILE").
		Render(func(c *render.ComponentContext) error {
			c.RequiresCSS("/static/css/profile.css")
			c.RequiresJS("/static/js/profile.js")
			_, err := c.WriteString(`<div>profile</div>`)
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	layout := `<html><head><NANO_HEAD/><NANO_ASSETS/></head><body><YIELD/></body></html>`
	view := `<PROFILE/>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"layout": []byte(layout),
		"view":   []byte(view),
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderPageEngines(rc, render.EngineHTML, "layout",
		render.EngineHTML, "view", nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `<link rel="stylesheet" href="/static/css/profile.css">`) {
		t.Errorf("css missing from head: %q", out)
	}
	if !strings.Contains(out, `<script defer src="/static/js/profile.js"></script>`) {
		t.Errorf("js missing from head: %q", out)
	}
	if !strings.Contains(out, `<div>profile</div>`) {
		t.Errorf("body missing: %q", out)
	}
}

// TestAssets_OverflowSpills verifies more than the inline
// capacity (16) spills to the heap slice and still emits.
func TestAssets_OverflowSpills(t *testing.T) {
	reg := render.New()
	cr := reg.Components()

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	// 20 unique CSS paths — 16 inline, 4 in the overflow slice.
	for i := 0; i < 20; i++ {
		rc.RequiresCSS("/css/a" + strconv.Itoa(i) + ".css")
	}
	_ = cr

	// Emit via the built-in component against the SAME context
	// that collected the assets.
	if err := reg.RenderComponent(bw, rc, "NANO_ASSETS", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if got := strings.Count(buf.String(), "stylesheet"); got != 20 {
		t.Errorf("emitted %d css tags, want 20", got)
	}
}
