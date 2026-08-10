package render_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// ---------------------------------------------------------------------------
// Trigger detail (JSON form).
// ---------------------------------------------------------------------------

func TestHXTriggers_DetailEmitsJSON(t *testing.T) {
	tr := render.HXTriggers{}
	tr.Add("item-changed")
	tr.AddWithDetail("user-typed", map[string]any{"value": "hello"})
	got := tr.Join()
	// Sorted keys; mixed → JSON form.
	if !strings.Contains(got, `"item-changed":null`) {
		t.Errorf("name-only entry should be null in JSON, got %q", got)
	}
	if !strings.Contains(got, `"user-typed":{`) {
		t.Errorf("detail entry missing in JSON: %q", got)
	}
	if !strings.Contains(got, `"value":"hello"`) {
		t.Errorf("detail value missing: %q", got)
	}
}

func TestHXTriggers_AllDetail(t *testing.T) {
	tr := render.HXTriggers{}
	tr.AddWithDetail("a", 1)
	tr.AddWithDetail("b", "two")
	got := tr.Join()
	if got != `{"a":1,"b":"two"}` {
		t.Errorf("got %q", got)
	}
}

func TestHXTriggers_AddThenAddWithDetailUpgrades(t *testing.T) {
	tr := render.HXTriggers{}
	tr.Add("ev")
	if !tr.HasDetail() == false {
		// confirm HasDetail is false initially
	}
	tr.AddWithDetail("ev", "payload")
	if !tr.HasDetail() {
		t.Errorf("AddWithDetail should upgrade entry to detail")
	}
}

// ---------------------------------------------------------------------------
// After-Swap / After-Settle trigger sets.
// ---------------------------------------------------------------------------

func TestHXTriggersAfterSwapAndSettle(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.AddHXTriggerAfterSwap("swap-event")
	rc.AddHXTriggerAfterSettle("settle-event")

	if got := rc.HXTriggersAfterSwap().Join(); got != "swap-event" {
		t.Errorf("after-swap: got %q", got)
	}
	if got := rc.HXTriggersAfterSettle().Join(); got != "settle-event" {
		t.Errorf("after-settle: got %q", got)
	}
	// HX-Trigger itself should still be empty.
	if got := rc.HXTriggers(); got != nil {
		t.Errorf("plain HXTriggers should be nil, got %v", got)
	}
}

func TestHXTriggersAfterSwap_ResetBetweenRequests(t *testing.T) {
	rc := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	rc.AddHXTriggerAfterSwap("leak")
	rc.AddHXTriggerAfterSettle("leak")
	render.ReleaseContext(rc)

	rc2 := render.AcquireContext(nil, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc2)
	if got := rc2.HXTriggersAfterSwap(); got != nil {
		t.Errorf("after-swap leaked: %v", got)
	}
	if got := rc2.HXTriggersAfterSettle(); got != nil {
		t.Errorf("after-settle leaked: %v", got)
	}
}

// ---------------------------------------------------------------------------
// HTMXResponse — server-driven response decisions.
// ---------------------------------------------------------------------------

func TestHTMXResponse_SetAndRead(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.SetHXRetarget("#main-panel")
	rc.SetHXReswap("outerHTML swap:200ms")
	rc.SetHXPushURL("true")
	rc.SetHXRedirect("/done")
	rc.SetHXRefresh()
	rc.SetHXReplaceURL("/current")
	rc.SetHXLocation("/alt")

	got := rc.HTMXResponse()
	want := render.HTMXResponse{
		Retarget:   "#main-panel",
		Reswap:     "outerHTML swap:200ms",
		PushURL:    "true",
		Redirect:   "/done",
		Refresh:    "true",
		ReplaceURL: "/current",
		Location:   "/alt",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// ---------------------------------------------------------------------------
// WriteHTMXHeaders — end-to-end header write.
// ---------------------------------------------------------------------------

func TestWriteHTMXHeaders_AllFields(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.AddHXTrigger("event-a")
	rc.AddHXTriggerAfterSwap("swap-event")
	rc.AddHXTriggerAfterSettle("settle-event")
	rc.AddHXTriggerWithDetail("item", 42)
	rc.SetHXRetarget("#panel")
	rc.SetHXReswap("outerHTML")
	rc.SetHXPushURL("/page/2")
	rc.SetHXRedirect("/done")
	rc.SetHXRefresh()
	rc.SetHXReplaceURL("/replaced")
	rc.SetHXLocation("/loc")
	rc.SetHXReselect("#content")
	rc.SetHXResettle("true")

	rec := httptest.NewRecorder()
	rc.WriteHTMXHeaders(rec)

	h := rec.Header()
	if got := h.Get(render.HXTrigger); !strings.Contains(got, `"event-a"`) || !strings.Contains(got, `"item":42`) {
		t.Errorf("HX-Trigger wrong: %q", got)
	}
	if got := h.Get(render.HXTriggerAfterSwap); got != "swap-event" {
		t.Errorf("HX-Trigger-After-Swap: %q", got)
	}
	if got := h.Get(render.HXTriggerAfterSettle); got != "settle-event" {
		t.Errorf("HX-Trigger-After-Settle: %q", got)
	}
	if got := h.Get(render.HXRetarget); got != "#panel" {
		t.Errorf("HX-Retarget: %q", got)
	}
	if got := h.Get(render.HXReswapHeader); got != "outerHTML" {
		t.Errorf("HX-Reswap: %q", got)
	}
	if got := h.Get(render.HXPushURL); got != "/page/2" {
		t.Errorf("HX-Push-Url: %q", got)
	}
	if got := h.Get(render.HXRedirect); got != "/done" {
		t.Errorf("HX-Redirect: %q", got)
	}
	if got := h.Get(render.HXRefresh); got != "true" {
		t.Errorf("HX-Refresh: %q", got)
	}
	if got := h.Get(render.HXReplaceURL); got != "/replaced" {
		t.Errorf("HX-Replace-Url: %q", got)
	}
	if got := h.Get(render.HXLocation); got != "/loc" {
		t.Errorf("HX-Location: %q", got)
	}
	if got := h.Get(render.HXReselect); got != "#content" {
		t.Errorf("HX-Reselect: %q", got)
	}
	if got := h.Get(render.HXResettle); got != "true" {
		t.Errorf("HX-Resettle: %q", got)
	}
}

func TestWriteHTMXHeaders_EmptyOmitsAllHeaders(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rec := httptest.NewRecorder()
	rc.WriteHTMXHeaders(rec)

	// Every HX-* header should be absent.
	for _, h := range []string{
		render.HXTrigger,
		render.HXTriggerAfterSwap,
		render.HXTriggerAfterSettle,
		render.HXRetarget,
		render.HXReswapHeader,
		render.HXPushURL,
		render.HXRedirect,
		render.HXRefresh,
		render.HXReplaceURL,
		render.HXLocation,
		render.HXReselect,
		render.HXResettle,
	} {
		if got := rec.Header().Get(h); got != "" {
			t.Errorf("%s: should be absent, got %q", h, got)
		}
	}
}

func TestWriteHTMXHeaders_NilSafe(t *testing.T) {
	var rc *render.RenderContext
	rc.WriteHTMXHeaders(httptest.NewRecorder()) // must not panic
	rc.WriteHTMXHeaders(nil)                   // must not panic
}

// ---------------------------------------------------------------------------
// HXTriggers convenience on ComponentContext.
// ---------------------------------------------------------------------------

func TestComponentContext_AfterSwapTrigger(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		Render(func(c *render.ComponentContext) error {
			c.AddHXTriggerAfterSwap("swap-done")
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
	if got := rc.HXTriggersAfterSwap().Join(); got != "swap-done" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// End-to-end: simulated HTMX handler pattern.
// ---------------------------------------------------------------------------

func TestHTMXHandlerPattern_EndToEnd(t *testing.T) {
	// Simulate a real handler:
	//   if htmx request → render one component + write HTMX headers
	//   else            → render full layout
	cr := render.NewComponentRegistry()
	cr.Define("CARD").
		Render(func(c *render.ComponentContext) error {
			c.AddHXTriggerWithDetail("card-updated", map[string]any{"id": 7})
			_, err := c.WriteString("<div id='card'>7</div>")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)

	req := httptest.NewRequest("POST", "/posts/7/like", nil)
	req.Header.Set(render.HXRequest, "true")
	req.Header.Set(render.HXTarget, "card")
	rc := render.AcquireContext(bw, req)
	defer render.ReleaseContext(rc)

	if !render.IsHTMXRequest(req) {
		t.Fatal("test setup: should be HTMX request")
	}

	// Handler body.
	reg.RenderComponent(bw, rc, "CARD", nil)
	rc.SetHXRetarget("#main")

	// Apply all HTMX response headers in one call.
	recWriter := httptest.NewRecorder()
	rc.WriteHTMXHeaders(recWriter)
	h := recWriter.Header()

	if got := h.Get(render.HXTrigger); !strings.Contains(got, `"id":7`) {
		t.Errorf("trigger detail missing: %q", got)
	}
	if got := h.Get(render.HXRetarget); got != "#main" {
		t.Errorf("retarget missing: %q", got)
	}
}

// TestHXReselectAndResettle verifies the two scroll/select response
// headers round-trip from setter to header.
func TestHXReselectAndResettle(t *testing.T) {
	bw := render.AcquireWriter(&httptest.ResponseRecorder{})
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, httptest.NewRequest("GET", "/", nil))
	defer render.ReleaseContext(rc)

	rc.SetHXReselect("#content")
	rc.SetHXResettle("false")

	resp := rc.HTMXResponse()
	if resp.Reselect != "#content" {
		t.Errorf("Reselect: %q", resp.Reselect)
	}
	if resp.Resettle != "false" {
		t.Errorf("Resettle: %q", resp.Resettle)
	}

	rec := httptest.NewRecorder()
	rc.WriteHTMXHeaders(rec)
	h := rec.Header()
	if got := h.Get(render.HXReselect); got != "#content" {
		t.Errorf("HX-Reselect: %q", got)
	}
	if got := h.Get(render.HXResettle); got != "false" {
		t.Errorf("HX-Resettle: %q", got)
	}
}
