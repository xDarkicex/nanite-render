package render_test

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

type sliderProps struct {
	Min int    `json:"min"`
	Max int    `json:"max"`
	Tag string `json:"tag"`
}

// TestWriteHydrateProps_Basic verifies the output shape matches
// the proposal: x-data='{"min":0,"max":100}'.
func TestWriteHydrateProps_Basic(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLIDER").
		Render(func(c *render.ComponentContext) error {
			if _, err := c.WriteString(`<div `); err != nil {
				return err
			}
			if err := c.WriteHydrateProps("x-data", sliderProps{Min: 0, Max: 100, Tag: "a"}); err != nil {
				return err
			}
			_, err := c.WriteString(`>`)
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

	if err := reg.RenderComponent(bw, rc, "SLIDER", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	out := buf.String()
	// escapeBytes escapes " to &#34; — browsers entity-decode the
	// attribute value back to the exact JSON, so the client sees
	// {"min":0,"max":100,"tag":"a"}.
	want := `<div x-data='{&#34;min&#34;:0,&#34;max&#34;:100,&#34;tag&#34;:&#34;a&#34;}'>`
	if !strings.Contains(out, want) {
		t.Errorf("hydration output wrong: %q", out)
	}
}

// TestWriteHydrateProps_Escaping verifies quotes and special
// characters in values are escaped and the client sees the exact
// JSON after entity decoding.
func TestWriteHydrateProps_Escaping(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("EVIL").
		Render(func(c *render.ComponentContext) error {
			return c.WriteHydrateProps("x-data", map[string]any{
				"name": `O'Brien "quoted" <script>alert(1)</script> & more`,
			})
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
	_ = bw.Flush()
	out := buf.String()
	// No raw <script> in the output — json.Marshal's HTML-safe
	// encoding turns < into <.
	if strings.Contains(out, `<script>`) {
		t.Errorf("script broke out of attribute: %q", out)
	}
	// No raw apostrophe inside the single-quoted attribute.
	if strings.Contains(out, `'O'Brien`) {
		t.Errorf("apostrophe not escaped: %q", out)
	}
	// The escaped forms are present: &#39; for the apostrophe
	// (escapeBytes) and the literal backslash form < for <
	// (json.Marshal's HTML-safe encoding).
	if !strings.Contains(out, `&#39;`) || !strings.Contains(out, `\u003cscript\u003e`) {
		t.Errorf("escaping missing: %q", out)
	}
}

// TestWriteHydrateProps_MarshalError verifies unmarshalable props
// (channels) return an error and the component can handle it.
func TestWriteHydrateProps_MarshalError(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("BAD").
		Render(func(c *render.ComponentContext) error {
			return c.WriteHydrateProps("x-data", make(chan int))
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	err := reg.RenderComponent(bw, rc, "BAD", nil)
	if err == nil {
		t.Fatal("expected marshal error for channel props")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("unexpected error: %v", err)
	}
	// Nothing should have been written before the marshal failed.
	if buf.Len() != 0 {
		t.Errorf("partial output on marshal error: %q", buf.String())
	}
}

// TestWriteHydrateProps_AlpineIntegration verifies the full
// pattern: BindProps → WriteHydrateProps on x-data.
func TestWriteHydrateProps_AlpineIntegration(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("SLIDER").
		Render(func(c *render.ComponentContext) error {
			props := render.BindProps[sliderProps](c.Data)
			_, err := c.WriteString(`<div `)
			if err != nil {
				return err
			}
			if err := c.WriteHydrateProps("x-data", props); err != nil {
				return err
			}
			_, err = c.WriteString(`></div>`)
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

	if err := reg.RenderComponent(bw, rc, "SLIDER", map[string]any{
		"min": 0, "max": 100, "tag": "x",
	}); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	want := `<div x-data='{&#34;min&#34;:0,&#34;max&#34;:100,&#34;tag&#34;:&#34;x&#34;}'></div>`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("alpine integration wrong: %q", buf.String())
	}
}