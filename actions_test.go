package render_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// newActionFixture builds a registry with a LIKE_BUTTON component
// that has a "toggle" action. The action mutates an external
// counter (simulating DB storage) and re-renders the button.
func newActionFixture() (*render.Registry, *int) {
	liked := 0
	cr := render.NewComponentRegistry()
	cr.Define("LIKE_BUTTON").
		Action("toggle", func(rc *render.RenderContext, props map[string]any) error {
			liked++
			return nil
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString(`<button hx-post="` + c.ActionURL("toggle") + `">` +
				strings.Repeat("❤", liked) + `</button>`)
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)
	return reg, &liked
}

// doAction performs a POST to the action endpoint with the HTMX
// headers and origin set. formValues populate the request body;
// pass contentType "application/json" for a JSON body.
func doAction(reg *render.Registry, path string, formValues url.Values, contentType string) *httptest.ResponseRecorder {
	var body string
	switch contentType {
	case "application/json":
		// Minimal JSON: {"k":"v"} for single-value keys.
		var b strings.Builder
		b.WriteString("{")
		first := true
		for k, vs := range formValues {
			if !first {
				b.WriteString(",")
			}
			first = false
			if len(vs) > 0 {
				b.WriteString(`"` + k + `":"` + vs[0] + `"`)
			}
		}
		b.WriteString("}")
		body = b.String()
	default:
		body = formValues.Encode()
		contentType = "application/x-www-form-urlencoded"
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Content-Type", contentType)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	reg.HandleAction(rec, req)
	return rec
}

// TestAction_URLGeneration verifies ActionURL builds the default
// prefixed URL.
func TestAction_URLGeneration(t *testing.T) {
	reg, _ := newActionFixture()
	cr := reg.Components()
	c, _ := cr.Lookup("LIKE_BUTTON")

	// Render the component and capture its ActionURL output.
	var buf strings.Builder
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)
	if err := reg.RenderComponent(bw, rc, "LIKE_BUTTON", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if !strings.Contains(buf.String(), `hx-post="/_nano/action/LIKE_BUTTON/toggle"`) {
		t.Errorf("ActionURL missing from render: %q", buf.String())
	}
	_ = c
}

// TestAction_CustomPrefix verifies WithActionPrefix changes the
// generated URL and the handler accepts the new prefix.
func TestAction_CustomPrefix(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Action("go", func(rc *render.RenderContext, props map[string]any) error { return nil }).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString(`<form action="` + c.ActionURL("go") + `">`)
			return err
		}).
		Register(cr)
	reg := render.New(render.WithActionPrefix("/api/act/"))
	reg.AttachComponents(cr)

	var buf strings.Builder
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)
	if err := reg.RenderComponent(bw, rc, "WIDGET", nil); err != nil {
		t.Fatal(err)
	}
	_ = bw.Flush()
	if !strings.Contains(buf.String(), `action="/api/act/WIDGET/go"`) {
		t.Errorf("custom prefix not applied: %q", buf.String())
	}
}

// TestAction_InlineSwap verifies the re-rendered component is
// returned raw (no OOB wrapper) and the action ran.
func TestAction_InlineSwap(t *testing.T) {
	reg, liked := newActionFixture()
	rec := doAction(reg, "/_nano/action/LIKE_BUTTON/toggle", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if *liked != 1 {
		t.Errorf("action ran %d times, want 1", *liked)
	}
	// Re-render reflects the mutation (❤ now present).
	if !strings.Contains(rec.Body.String(), "❤") {
		t.Errorf("re-render missing mutated output: %q", rec.Body.String())
	}
	// No OOB wrapper — inline swap.
	if strings.Contains(rec.Body.String(), "hx-swap-oob") {
		t.Errorf("unexpected OOB wrapper for non-OOB component: %q", rec.Body.String())
	}
}

// TestAction_OOBSwap verifies WithOOB components get the OOB
// wrapper in the response.
func TestAction_OOBSwap(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("COUNTER").
		WithOOB("counter").
		Action("bump", func(rc *render.RenderContext, props map[string]any) error { return nil }).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString(`<div id="counter">42</div>`)
			return err
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	rec := doAction(reg, "/_nano/action/COUNTER/bump", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="counter" hx-swap-oob="true">`) {
		t.Errorf("OOB wrapper missing: %q", body)
	}
	if !strings.Contains(body, `id="counter">42`) {
		t.Errorf("component output missing inside OOB: %q", body)
	}
}

// TestAction_FormPropsConversion verifies form values get
// best-effort type conversion before reaching the action.
func TestAction_FormPropsConversion(t *testing.T) {
	var got map[string]any
	cr := render.NewComponentRegistry()
	cr.Define("CONFIG").
		Action("save", func(rc *render.RenderContext, props map[string]any) error {
			got = props
			return nil
		}).
		Render(func(c *render.ComponentContext) error { return nil }).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	rec := doAction(reg, "/_nano/action/CONFIG/save",
		url.Values{"count": {"42"}, "on": {"true"}, "ratio": {"3.14"}, "name": {"bob"}}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got == nil {
		t.Fatal("action never ran")
	}
	if got["count"] != 42 {
		t.Errorf("count = %#v (%T), want int 42", got["count"], got["count"])
	}
	if got["on"] != true {
		t.Errorf("on = %#v, want true", got["on"])
	}
	if got["ratio"] != 3.14 {
		t.Errorf("ratio = %#v, want 3.14", got["ratio"])
	}
	if got["name"] != "bob" {
		t.Errorf("name = %#v, want \"bob\"", got["name"])
	}
}

// TestAction_JSONProps verifies JSON bodies keep real types.
func TestAction_JSONProps(t *testing.T) {
	var got map[string]any
	cr := render.NewComponentRegistry()
	cr.Define("API").
		Action("save", func(rc *render.RenderContext, props map[string]any) error {
			got = props
			return nil
		}).
		Render(func(c *render.ComponentContext) error { return nil }).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	rec := doAction(reg, "/_nano/action/API/save",
		url.Values{"count": {"42"}}, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got["count"] != "42" {
		t.Errorf("count = %#v, want string \"42\" (JSON keeps strings as strings)", got["count"])
	}
}

// TestAction_StateFlowsToRerender verifies per-request state set
// by the action is visible in the re-render.
func TestAction_StateFlowsToRerender(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("LAMP").
		Action("flip", func(rc *render.RenderContext, props map[string]any) error {
			rc.ComponentState().Set("lit", true)
			return nil
		}).
		Render(func(c *render.ComponentContext) error {
			lit, _ := c.GetState("lit")
			if lit == true {
				_, err := c.WriteString("<b>ON</b>")
				return err
			}
			_, err := c.WriteString("<b>OFF</b>")
			return err
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	rec := doAction(reg, "/_nano/action/LAMP/flip", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<b>ON</b>") {
		t.Errorf("action state not visible in re-render: %q", rec.Body.String())
	}
}

// TestAction_CSRFRejections verifies the security baseline.
func TestAction_CSRFRejections(t *testing.T) {
	reg, _ := newActionFixture()
	path := "/_nano/action/LIKE_BUTTON/toggle"

	// GET (wrong method) → 405.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Origin", "https://example.com")
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	reg.HandleAction(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", rec.Code)
	}

	// Missing HX-Request → 403.
	req = httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Origin", "https://example.com")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	reg.HandleAction(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no HX-Request = %d, want 403", rec.Code)
	}

	// Origin host mismatch → 403.
	req = httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Origin", "https://evil.example.com")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	reg.HandleAction(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("mismatched origin = %d, want 403", rec.Code)
	}

	// Missing Origin AND Referer → 403.
	req = httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("HX-Request", "true")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	reg.HandleAction(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no origin/referer = %d, want 403", rec.Code)
	}

	// Valid referer (not origin) → 200.
	req = httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Referer", "https://example.com/")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	reg.HandleAction(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid referer = %d, want 200", rec.Code)
	}
}

// TestAction_NotFound verifies unknown component/action → 404.
func TestAction_NotFound(t *testing.T) {
	reg, _ := newActionFixture()

	for _, path := range []string{
		"/_nano/action/NOPE/nope",      // unknown component
		"/_nano/action/LIKE_BUTTON/nope", // unknown action
		"/_nano/action/",               // empty
		"/_nano/action/LIKE_BUTTON",    // no action
	} {
		rec := doAction(reg, path, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}

// TestAction_ActionErrorYields500 verifies a failing action
// produces a 500 and no re-render body.
func TestAction_ActionErrorYields500(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("BROKEN").
		Action("fail", func(rc *render.RenderContext, props map[string]any) error {
			return errBoom
		}).
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("should not render")
			return err
		}).
		Register(cr)
	reg := render.New()
	reg.AttachComponents(cr)

	rec := doAction(reg, "/_nano/action/BROKEN/fail", nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "should not render") {
		t.Errorf("re-render happened despite action error: %q", rec.Body.String())
	}
}
