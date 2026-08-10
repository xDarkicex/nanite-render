package render_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// ---------------------------------------------------------------------------
// HTMX request detection helpers.
// ---------------------------------------------------------------------------

func TestIsHTMXRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"plain GET", "", false},
		{"hx request", "true", true},
		{"hx but wrong value", "yes", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				r.Header.Set(render.HXRequest, tt.header)
			}
			if got := render.IsHTMXRequest(r); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsHTMXRequest_NilSafe(t *testing.T) {
	if render.IsHTMXRequest(nil) {
		t.Error("nil request should not be HTMX")
	}
}

func TestIsHTMXBoosted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if render.IsHTMXBoosted(r) {
		t.Error("plain GET not boosted")
	}
	r.Header.Set(render.HXBoosted, "true")
	if !render.IsHTMXBoosted(r) {
		t.Error("boosted header set but not detected")
	}
}

func TestIsHTMXHistoryRestore(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if render.IsHTMXHistoryRestore(r) {
		t.Error("plain GET not history-restore")
	}
	r.Header.Set(render.HXHistoryRestoreRequest, "true")
	if !render.IsHTMXHistoryRestore(r) {
		t.Error("history-restore header set but not detected")
	}
}

func TestHXTargetID(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(render.HXTarget, "main")
	if got := render.HXTargetID(r); got != "main" {
		t.Errorf("got %q, want %q", got, "main")
	}
	if got := render.HXTargetID(nil); got != "" {
		t.Errorf("nil request: got %q", got)
	}
}

func TestHXTriggerID(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(render.HXTrigger, "btn-1")
	if got := render.HXTriggerID(r); got != "btn-1" {
		t.Errorf("got %q, want %q", got, "btn-1")
	}
}

func TestTriggerName(t *testing.T) {
	r := httptest.NewRequest("POST", "/form", nil)
	r.Header.Set(render.HXTriggerName, "email-input")
	if got := render.TriggerName(r); got != "email-input" {
		t.Errorf("got %q, want %q", got, "email-input")
	}
}

func TestCurrentURL(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(render.HXCurrentURL, "https://example.com/page")
	if got := render.CurrentURL(r); got != "https://example.com/page" {
		t.Errorf("got %q", got)
	}
}

func TestHXPromptResponse(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set(render.HXPrompt, "yes")
	if got := render.HXPromptResponse(r); got != "yes" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// HXTriggers set + render-context integration.
// ---------------------------------------------------------------------------

func TestHXTriggers_DeduplicatesAndJoins(t *testing.T) {
	tr := render.HXTriggers{}
	tr.Add("card-updated")
	tr.Add("nav-refresh")
	tr.Add("card-updated") // duplicate
	if got := tr.Join(); got != "card-updated,nav-refresh" {
		t.Errorf("got %q, want sorted dedup", got)
	}
	// Sorted for stability.
	tr.Add("a-first")
	if got := tr.Join(); !strings.HasPrefix(got, "a-first") {
		t.Errorf("insert sort broken: %q", got)
	}
}

func TestHXTriggers_EmptyIsEmptyString(t *testing.T) {
	tr := render.HXTriggers{}
	if got := tr.Join(); got != "" {
		t.Errorf("empty set should join to empty string, got %q", got)
	}
}

func TestHXTriggers_NilSafe(t *testing.T) {
	var tr render.HXTriggers
	tr.Add("x") // must not panic on nil receiver
	if got := tr.Join(); got != "" {
		t.Errorf("nil set should not retain names, got %q", got)
	}
}

func TestRenderContext_AddAndReadTriggers(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.AddHXTrigger("event-a")
	rc.AddHXTrigger("event-b")
	rc.AddHXTrigger("event-a") // dup

	tr := rc.HXTriggers()
	if got := tr.Join(); got != "event-a,event-b" {
		t.Errorf("got %q", got)
	}
}

func TestRenderContext_AddHXTriggerEmptyIgnored(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.AddHXTrigger("")
	if tr := rc.HXTriggers(); tr != nil {
		t.Errorf("empty trigger should not allocate the set, got %v", tr)
	}
}

func TestComponentContext_AddHXTrigger(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		Render(func(c *render.ComponentContext) error {
			c.AddHXTrigger("card-clicked")
			_, err := c.WriteString("<div/>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	if err := reg.RenderComponent(bw, rc, "CARD", nil); err != nil {
		t.Fatal(err)
	}
	if got := rc.HXTriggers().Join(); got != "card-clicked" {
		t.Errorf("got %q", got)
	}
}

// TestComponentContext_ResetBetweenRequests verifies the trigger set
// is cleared on AcquireContext so handlers don't leak triggers across
// the pooled RenderContext.
func TestComponentContext_ResetBetweenRequests(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	rc.AddHXTrigger("leak-test")
	render.ReleaseContext(rc)

	rc2 := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc2)
	if tr := rc2.HXTriggers(); tr != nil {
		t.Errorf("pooled rc carried triggers: %v", tr)
	}
}

// ---------------------------------------------------------------------------
// Standard request header values — sanity check the constants are
// what HTMX actually sends.
// ---------------------------------------------------------------------------

func TestHTMXHeaderConstants(t *testing.T) {
	cases := map[string]string{
		render.HXRequest:                "HX-Request",
		render.HXBoosted:                "HX-Boosted",
		render.HXHistoryRestoreRequest:  "HX-History-Restore-Request",
		render.HXTarget:                 "HX-Target",
		render.HXTrigger:                "HX-Trigger",
		render.HXTriggerName:            "HX-Trigger-Name",
		render.HXCurrentURL:             "HX-Current-URL",
		render.HXPrompt:                 "HX-Prompt",
		render.HXTriggerAfterSwap:       "HX-Trigger-After-Swap",
		render.HXTriggerAfterSettle:     "HX-Trigger-After-Settle",
		render.HXRetarget:               "HX-Retarget",
		render.HXReswapHeader:           "HX-Reswap",
		render.HXPushURL:                "HX-Push-Url",
		render.HXRedirect:               "HX-Redirect",
		render.HXRefresh:                "HX-Refresh",
		render.HXReplaceURL:             "HX-Replace-Url",
		render.HXLocation:               "HX-Location",
		render.HXReselect:               "HX-Reselect",
		render.HXResettle:               "HX-Resettle",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("HTMX header drift: got %q, want %q", got, want)
		}
	}
	// HXTrigger and HXTriggerHeader share the wire name "HX-Trigger"
	// (one is the request constant, the other the response alias).
	if render.HXTrigger != render.HXTriggerHeader {
		t.Errorf("request/response HX-Trigger names diverged")
	}
	_ = http.MethodGet // keep net/http import used in case future tests need it
}
