package render_test

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
	"github.com/xDarkicex/nanite-render/engine"
)

// TestHead_TitleMetaRoundTrip verifies SetTitle/AddMeta + Title
// getter.
func TestHead_TitleMetaRoundTrip(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.SetTitle("Hello")
	if got := rc.Title(); got != "Hello" {
		t.Errorf("Title() = %q, want %q", got, "Hello")
	}
	rc.AddMeta("description", "A page")
	rc.AddMeta("description", "Updated") // last write wins
	if got := rc.Title(); got != "Hello" {
		t.Errorf("Title() = %q", got)
	}
}

// TestHead_NANOHEADComponent verifies <NANO_HEAD/> emits the
// collected title and meta tags with escaping.
func TestHead_NANOHEADComponent(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PAGE").
		Render(func(c *render.ComponentContext) error {
			c.SetTitle(`Alice & Bob`)
			c.AddMeta("description", `A <b>page</b>`)
			return nil
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	// Render the view component (sets head state), then a layout
	// with <NANO_HEAD/> in the head.
	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "PAGE", nil); err != nil {
		t.Fatal(err)
	}
	// Simulate the layout's <NANO_HEAD/> emitting after the view.
	if err := reg.RenderComponent(bw, rc, "NANO_HEAD", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	out := buf.String()
	if !strings.Contains(out, `<title>Alice &amp; Bob</title>`) {
		t.Errorf("escaped title missing: %q", out)
	}
	if !strings.Contains(out, `<meta name="description" content="A &lt;b&gt;page&lt;/b&gt;">`) {
		t.Errorf("escaped meta missing: %q", out)
	}
}

// TestHead_FullPageLoad verifies the full two-pass flow: a
// component in the view sets title/meta, the layout's
// <NANO_HEAD/> emits it, and the body renders.
func TestHead_FullPageLoad(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PROFILE").
		Render(func(c *render.ComponentContext) error {
			c.SetTitle("Alice - Profile")
			c.AddMeta("description", "Profile page for Alice")
			_, err := c.WriteString(`<div>alice's profile</div>`)
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	layout := `<html><head><NANO_HEAD/></head><body><YIELD/></body></html>`
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
	if !strings.Contains(out, `<title>Alice - Profile</title>`) {
		t.Errorf("title missing from head: %q", out)
	}
	if !strings.Contains(out, `<meta name="description" content="Profile page for Alice">`) {
		t.Errorf("meta missing from head: %q", out)
	}
	if !strings.Contains(out, `alice's profile`) {
		t.Errorf("body missing: %q", out)
	}
}

// TestHead_MetadataPreflight verifies the Metadata closure runs
// before the view renders and its head state lands in the layout.
// The view name equals the component name — the pattern the
// pre-flight matches on.
func TestHead_MetadataPreflight(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("PROFILE").
		Metadata(func(rc *render.RenderContext, data any) error {
			user := data.(string)
			rc.SetTitle(user + " - Profile")
			rc.AddMeta("description", "Profile for "+user)
			return nil
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("body")
			return err
		}).
		Register(cr)

	reg := render.New(render.WithEngines(engine.NewHTML()))
	reg.AttachComponents(cr)

	layout := `<html><head><NANO_HEAD/></head><body><YIELD/></body></html>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{
		"layout":  []byte(layout),
		"PROFILE": []byte(`<PROFILE/>`),
	}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)

	if err := reg.RenderPageEngines(rc, render.EngineHTML, "layout",
		render.EngineHTML, "PROFILE", "Bob"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `<title>Bob - Profile</title>`) {
		t.Errorf("metadata title missing: %q", out)
	}
	if !strings.Contains(out, `<meta name="description" content="Profile for Bob">`) {
		t.Errorf("metadata meta missing: %q", out)
	}
}

// TestUseId_Sequence verifies ids are unique and sequential.
func TestUseId_Sequence(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	first := rc.UseId()
	second := rc.UseId()
	third := rc.UseId()
	if first != "nano-1" || second != "nano-2" || third != "nano-3" {
		t.Errorf("ids = %q %q %q, want nano-1 nano-2 nano-3", first, second, third)
	}
}

// TestUseId_OverflowBeyond256 verifies the slow path past the
// precomputed array still yields unique ids.
func TestUseId_OverflowBeyond256(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	seen := make(map[string]bool)
	for i := 0; i < 300; i++ {
		id := rc.UseId()
		if seen[id] {
			t.Fatalf("duplicate id %q at iteration %d", id, i)
		}
		seen[id] = true
	}
	if rc.UseId() != "nano-301" {
		t.Errorf("overflow id wrong after 300 calls")
	}
}

// TestUseId_PoolReset verifies ids reset per request.
func TestUseId_PoolReset(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	_ = rc.UseId()
	_ = rc.UseId()
	render.ReleaseContext(rc)

	rc2 := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc2)
	if got := rc2.UseId(); got != "nano-1" {
		t.Errorf("first id after pool reuse = %q, want nano-1", got)
	}
}

// TestUseId_ComponentContext verifies c.UseId works in a
// component and pairs label with input.
func TestUseId_ComponentContext(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("FIELD").
		Render(func(c *render.ComponentContext) error {
			id := c.UseId()
			_, err := c.WriteString(`<label for="` + id + `">Name</label>`)
			if err != nil {
				return err
			}
			_, err = c.WriteString(`<input id="` + id + `" type="text">`)
			return err
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "FIELD", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	out := buf.String()
	if !strings.Contains(out, `<label for="nano-1">Name</label><input id="nano-1" type="text">`) {
		t.Errorf("label/input not paired: %q", out)
	}
}

// TestHead_HTMXPartialTitle verifies the HTMX path: a component
// writes <title> directly and it passes through (HTMX hoists it
// from the response body on partial swaps).
func TestHead_HTMXPartialTitle(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("EDITOR").
		Render(func(c *render.ComponentContext) error {
			if render.IsHTMXRequest(c.Context.Request) {
				_, err := c.WriteString(`<title>Saved!</title>`)
				return err
			}
			_, err := c.WriteString(`<form>...</form>`)
			return err
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("HX-Request", "true")
	rc := render.AcquireContext(bw, req)
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "EDITOR", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if !strings.Contains(buf.String(), `<title>Saved!</title>`) {
		t.Errorf("HTMX title not emitted: %q", buf.String())
	}
}

// TestHead_PoolReset verifies title/meta don't leak across pool
// reuse.
func TestHead_PoolReset(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	rc.SetTitle("leaked")
	rc.AddMeta("description", "leaked")
	render.ReleaseContext(rc)

	rc2 := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc2)
	if got := rc2.Title(); got != "" {
		t.Errorf("title leaked across pool reuse: %q", got)
	}
}
