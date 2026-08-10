package render

import (
	"encoding/json"
	"net/http"
	"strings"
)

// HTMX request header constants. HTMX sets these on every request it
// makes; servers use them to differentiate HTMX-driven requests from
// full-page loads.
//
// See https://htmx.org/reference/#request_headers for the full list.
const (
	// HXRequest is set to "true" on every HTMX request. Its presence
	// is the canonical "this is an HTMX request" signal.
	HXRequest = "HX-Request"

	// HXBoosted is set to "true" when the request came from an
	// hx-boosted element (typically an <a> or <form>). Boosted
	// requests render only the response body, not the full page.
	HXBoosted = "HX-Boosted"

	// HXHistoryRestoreRequest is set to "true" when HTMX is taking
	// a snapshot of the page (history restoration on navigation).
	// Servers should render the full page, not a partial swap.
	HXHistoryRestoreRequest = "HX-History-Restore-Request"

	// HXTarget is the id of the element targeted by the request.
	HXTarget = "HX-Target"

	// HXTrigger is the id of the triggered element (when the request
	// was triggered by an event, not a load).
	HXTrigger = "HX-Trigger"

	// HXTriggerName is the name attribute of the triggered element.
	HXTriggerName = "HX-Trigger-Name"

	// HXCurrentURL is the URL of the page the browser was on when
	// the request was made. Useful for server-side history tracking.
	HXCurrentURL = "HX-Current-URL"

	// HXPrompt is the user's response to an hx-prompt dialog.
	HXPrompt = "HX-Prompt"
)

// HTMX response header constants. The server sets these to drive
// HTMX client behavior.
//
// Where the same header is used for both directions (e.g. HX-Trigger)
// the request constant (HXTrigger) and the response constant share
// the same value; the alias HXTriggerHeader is provided for code
// that handles both sides and wants to make the direction explicit.
const (
	// HXTriggerHeader (response) tells HTMX to trigger client-side
	// events after the response is swapped. Comma-separated list of
	// event names. nanite-render accumulates triggered events on
	// rc.hxTriggers during a render; the router copies the joined
	// list into this header.
	HXTriggerHeader = "HX-Trigger"

	// HXTriggerAfterSwap fires after the swap step.
	HXTriggerAfterSwap = "HX-Trigger-After-Swap"

	// HXTriggerAfterSettle fires after the settle step.
	HXTriggerAfterSettle = "HX-Trigger-After-Settle"

	// HXRetarget overrides hx-target on the triggered element.
	HXRetarget = "HX-Retarget"

	// HXReswapHeader overrides hx-swap on the triggered element.
	HXReswapHeader = "HX-Reswap"

	// HXPushURL pushes a new URL into the history stack.
	HXPushURL = "HX-Push-Url"

	// HXRedirect triggers a client-side redirect.
	HXRedirect = "HX-Redirect"

	// HXRefresh makes the client do a full page refresh.
	HXRefresh = "HX-Refresh"

	// HXReplaceURL replaces the current URL without history entry.
	HXReplaceURL = "HX-Replace-Url"

	// HXLocation triggers client-side navigation without history.
	HXLocation = "HX-Location"

	// HXReselect is a CSS selector telling HTMX which part of the
	// response body to extract and swap. Useful when the server
	// returns a wrapper page (e.g. layout) and only an inner region
	// should land in the DOM.
	HXReselect = "HX-Reselect"

	// HXResettle controls the settle algorithm after the swap.
	// "true" forces scroll reset to the top; "false" (default) tries
	// to preserve scroll position across the swap.
	HXResettle = "HX-Resettle"
)

// IsHTMXRequest reports whether the request was made by HTMX. This
// is the canonical check; all other HTMX request modifiers imply
// IsHTMXRequest is also true.
func IsHTMXRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get(HXRequest) == "true"
}

// IsHTMXBoosted reports whether the request came from an hx-boost
// (typically a link or form that the user activated). Boosted
// requests should render the response body only — the layout is
// already on the page.
func IsHTMXBoosted(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get(HXBoosted) == "true"
}

// IsHTMXHistoryRestore reports whether HTMX is restoring a history
// snapshot. Servers should render the full page (no partial swap)
// so the snapshot is faithful.
func IsHTMXHistoryRestore(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get(HXHistoryRestoreRequest) == "true"
}

// HXTargetID returns the id of the element HTMX is targeting. Empty
// when no hx-target was specified (HTMX defaults to the triggering
// element).
func HXTargetID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(HXTarget)
}

// HXTriggerID returns the id of the element that triggered the
// request. Empty when the request was a load (no specific trigger).
func HXTriggerID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(HXTrigger)
}

// TriggerName returns the name attribute of the triggered element.
// Useful for forms whose submit handler depends on the input name.
func TriggerName(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(HXTriggerName)
}

// CurrentURL returns the URL of the page that initiated the
// request. Useful for server-side redirect logic that needs to know
// where the user came from.
func CurrentURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(HXCurrentURL)
}

// HXPromptResponse returns the user's response to an hx-prompt
// dialog. Empty when no prompt was shown.
func HXPromptResponse(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(HXPrompt)
}

// HXTriggers is a deduplicating set of event names to fire on the
// client via the HX-Trigger response header. Components accumulate
// triggers during a render via RenderContext.AddHXTrigger; the
// router joins them into the response header at the end of the
// handler.
//
// Each entry maps an event name to an optional detail value. When
// all values are empty (the common case), Join() emits the plain
// comma-separated form HTMX uses for simple event firing:
//
//	HX-Trigger: card-updated,nav-refresh
//
// When any entry carries detail, Join() emits JSON so the client
// can read the detail on the event object:
//
//	HX-Trigger: {"item-changed":{"id":42},"card-updated":null}
//
// The router only needs to write the joined string into the header;
// nanite-render handles the format decision.
type HXTriggers map[string]any

// emptySentinel marks a name-only trigger (no detail). The value
// type matters: json.Marshal emits "null" for nil, but HTMX treats
// null as detail-present. Use struct{} (which marshals to nothing)
// to keep the comma-separated form. We use a typed pointer so JSON
// encoding produces nothing for the simple case while still being
// distinct from nil.
var emptySentinel = &struct{}{}

// Add records an event name to fire on the client. Idempotent:
// adding the same name twice is a no-op (a later AddWithDetail for
// the same name upgrades the entry — first detail wins for now).
func (t HXTriggers) Add(name string) {
	if t == nil || name == "" {
		return
	}
	if _, exists := t[name]; !exists {
		t[name] = emptySentinel
	}
}

// AddWithDetail records an event name with a detail payload that
// the client receives on the event object. The detail is anything
// JSON-marshalable (struct, map, slice, primitive). An empty
// detail is equivalent to Add. If the name was previously added
// without detail, the detail upgrades the entry.
func (t HXTriggers) AddWithDetail(name string, detail any) {
	if t == nil || name == "" {
		return
	}
	t[name] = detail
}

// Names returns the trigger names as a sorted slice. Deterministic
// ordering makes tests reproducible and lets the router cache the
// joined header.
func (t HXTriggers) Names() []string {
	if len(t) == 0 {
		return nil
	}
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	// sort.Strings would alloc; insertion-sort is fine for small N.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// HasDetail reports whether any trigger in the set carries a JSON
// detail payload. Determines which Join format to use.
func (t HXTriggers) HasDetail() bool {
	for _, v := range t {
		if v != emptySentinel {
			return true
		}
	}
	return false
}

// Join returns the trigger set in HTMX's wire format. Plain
// comma-separated names when no entry carries detail; JSON
// object otherwise. Returns "" for an empty set so the caller
// can skip setting the header.
func (t HXTriggers) Join() string {
	if len(t) == 0 {
		return ""
	}
	if !t.HasDetail() {
		// Plain comma-separated form.
		names := t.Names()
		var sb strings.Builder
		for i, n := range names {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(n)
		}
		return sb.String()
	}
	// JSON form. Emit each entry; name-only entries become "null".
	names := t.Names()
	var sb strings.Builder
	sb.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('"')
		sb.WriteString(n)
		sb.WriteString(`":`)
		v := t[n]
		if v == emptySentinel {
			sb.WriteString("null")
			continue
		}
		jb, err := json.Marshal(v)
		if err != nil {
			// Fall back to null on marshal failure so we still
			// emit valid JSON. Detail value was user-provided;
			// the failure is theirs to debug.
			sb.WriteString("null")
			continue
		}
		sb.Write(jb)
	}
	sb.WriteByte('}')
	return sb.String()
}

// AddHXTrigger records an HTMX client-side event to fire after this
// render completes. Components call this from their render function;
// the router reads the joined list via HXTriggers() at the end of
// the handler and writes it into the HX-Trigger response header.
//
// Multiple events are deduplicated; AddHXTrigger is safe to call
// repeatedly with the same name. An empty name is ignored.
//
// Example:
//
//	cr.Define("CARD").
//	    WithOOB("card-slot").
//	    Render(func(c *render.ComponentContext) error {
//	        c.AddHXTrigger("card-updated")
//	        c.SetState("count", c.GetState("count").(int)+1)
//	        return c.WriteString("<div>...</div>")
//	    }).
//	    Register(cr)
//
// In the handler:
//
//	if isHTMX := render.IsHTMXRequest(r); isHTMX {
//	    reg.RenderComponent(w, rc, "CARD", nil)
//	    rc.WriteHTMXHeaders(w)
//	}
func (rc *RenderContext) AddHXTrigger(name string) {
	if rc == nil || name == "" {
		return
	}
	if rc.hxTriggers == nil {
		rc.hxTriggers = make(HXTriggers, 4)
	}
	rc.hxTriggers.Add(name)
}

// AddHXTriggerWithDetail records an event with a JSON-encodable
// detail payload. The client receives the detail on the event
// object via htmx:event-name.detail. See AddHXTrigger for
// dedup and lifecycle semantics.
func (rc *RenderContext) AddHXTriggerWithDetail(name string, detail any) {
	if rc == nil || name == "" {
		return
	}
	if rc.hxTriggers == nil {
		rc.hxTriggers = make(HXTriggers, 4)
	}
	rc.hxTriggers.AddWithDetail(name, detail)
}

// HXTriggers returns the accumulated trigger set. The router reads
// this after the render completes to populate response headers.
// The returned set is owned by the RenderContext; callers must not
// mutate it after the handler returns.
func (rc *RenderContext) HXTriggers() HXTriggers {
	if rc == nil {
		return nil
	}
	return rc.hxTriggers
}

// AddHXTriggerAfterSwap records an HTMX event to fire after the
// swap step. Populates the HX-Trigger-After-Swap response header.
// See AddHXTrigger for naming and dedup semantics.
func (rc *RenderContext) AddHXTriggerAfterSwap(name string) {
	if rc == nil || name == "" {
		return
	}
	if rc.hxTriggersAfterSwap == nil {
		rc.hxTriggersAfterSwap = make(HXTriggers, 4)
	}
	rc.hxTriggersAfterSwap.Add(name)
}

// HXTriggersAfterSwap returns the after-swap trigger set.
func (rc *RenderContext) HXTriggersAfterSwap() HXTriggers {
	if rc == nil {
		return nil
	}
	return rc.hxTriggersAfterSwap
}

// AddHXTriggerAfterSettle records an HTMX event to fire after the
// settle step. Populates the HX-Trigger-After-Settle response header.
func (rc *RenderContext) AddHXTriggerAfterSettle(name string) {
	if rc == nil || name == "" {
		return
	}
	if rc.hxTriggersAfterSettle == nil {
		rc.hxTriggersAfterSettle = make(HXTriggers, 4)
	}
	rc.hxTriggersAfterSettle.Add(name)
}

// HXTriggersAfterSettle returns the after-settle trigger set.
func (rc *RenderContext) HXTriggersAfterSettle() HXTriggers {
	if rc == nil {
		return nil
	}
	return rc.hxTriggersAfterSettle
}

// ---------------------------------------------------------------------------
// Server-driven HTMX response decisions.
// ---------------------------------------------------------------------------

// SetHXRetarget overrides hx-target on the triggered element. CSS
// selector string. Set during the handler; applied by WriteHTMXHeaders.
func (rc *RenderContext) SetHXRetarget(selector string) {
	if rc == nil {
		return
	}
	rc.hmx.Retarget = selector
}

// SetHXReswap overrides hx-swap on the triggered element.
// Examples: "outerHTML", "innerHTML swap:100ms", "none".
func (rc *RenderContext) SetHXReswap(strategy string) {
	if rc == nil {
		return
	}
	rc.hmx.Reswap = strategy
}

// SetHXPushURL pushes a URL into the history stack. Pass "true" to
// push the response URL; pass any other string as the URL to push.
// Empty clears the decision.
func (rc *RenderContext) SetHXPushURL(url string) {
	if rc == nil {
		return
	}
	rc.hmx.PushURL = url
}

// SetHXRedirect triggers a client-side redirect to the given URL.
// Full URL required.
func (rc *RenderContext) SetHXRedirect(url string) {
	if rc == nil {
		return
	}
	rc.hmx.Redirect = url
}

// SetHXRefresh triggers a full client-side page refresh when set to
// "true". Any other value is treated as "true" by HTMX.
func (rc *RenderContext) SetHXRefresh() {
	if rc == nil {
		return
	}
	rc.hmx.Refresh = "true"
}

// SetHXReplaceURL replaces the current URL without a history entry.
// Pass "true" to replace with the response URL; pass any other
// string as the URL to replace with.
func (rc *RenderContext) SetHXReplaceURL(url string) {
	if rc == nil {
		return
	}
	rc.hmx.ReplaceURL = url
}

// SetHXLocation triggers client-side navigation without a history
// entry. Full URL required.
func (rc *RenderContext) SetHXLocation(url string) {
	if rc == nil {
		return
	}
	rc.hmx.Location = url
}

// SetHXReselect sets the HX-Reselect response header. selector is a
// CSS selector telling HTMX which part of the response body to swap
// in. Use when the server returns a wrapper page and only an inner
// region should land in the DOM.
func (rc *RenderContext) SetHXReselect(selector string) {
	if rc == nil {
		return
	}
	rc.hmx.Reselect = selector
}

// SetHXResettle sets the HX-Resettle response header. pass "true"
// to force scroll reset to the top after the swap, "false" to
// preserve scroll position across the swap. Empty string clears.
func (rc *RenderContext) SetHXResettle(value string) {
	if rc == nil {
		return
	}
	rc.hmx.Resettle = value
}

// HTMXResponse returns the active response-decision struct. Callers
// may read it for inspection; modifications should go through the
// Set* helpers above.
func (rc *RenderContext) HTMXResponse() HTMXResponse {
	if rc == nil {
		return HTMXResponse{}
	}
	return rc.hmx
}

// WriteHTMXHeaders applies all accumulated HTMX response headers to
// the response writer in one call. The router invokes this at the
// end of the handler. Nil-safe.
//
// Headers written:
//   - HX-Trigger (from hxTriggers)
//   - HX-Trigger-After-Swap (from hxTriggersAfterSwap)
//   - HX-Trigger-After-Settle (from hxTriggersAfterSettle)
//   - HX-Retarget, HX-Reswap, HX-Push-Url, HX-Redirect,
//     HX-Refresh, HX-Replace-Url, HX-Location
//
// w may be nil (no-op). Headers whose source is empty are skipped so
// the response carries only what the handler actually set.
func (rc *RenderContext) WriteHTMXHeaders(w http.ResponseWriter) {
	if rc == nil || w == nil {
		return
	}
	h := w.Header()
	if v := rc.hxTriggers.Join(); v != "" {
		h.Set(HXTriggerHeader, v)
	}
	if v := rc.hxTriggersAfterSwap.Join(); v != "" {
		h.Set(HXTriggerAfterSwap, v)
	}
	if v := rc.hxTriggersAfterSettle.Join(); v != "" {
		h.Set(HXTriggerAfterSettle, v)
	}
	if rc.hmx.Retarget != "" {
		h.Set(HXRetarget, rc.hmx.Retarget)
	}
	if rc.hmx.Reswap != "" {
		h.Set(HXReswapHeader, rc.hmx.Reswap)
	}
	if rc.hmx.PushURL != "" {
		h.Set(HXPushURL, rc.hmx.PushURL)
	}
	if rc.hmx.Redirect != "" {
		h.Set(HXRedirect, rc.hmx.Redirect)
	}
	if rc.hmx.Refresh != "" {
		h.Set(HXRefresh, rc.hmx.Refresh)
	}
	if rc.hmx.ReplaceURL != "" {
		h.Set(HXReplaceURL, rc.hmx.ReplaceURL)
	}
	if rc.hmx.Location != "" {
		h.Set(HXLocation, rc.hmx.Location)
	}
	if rc.hmx.Reselect != "" {
		h.Set(HXReselect, rc.hmx.Reselect)
	}
	if rc.hmx.Resettle != "" {
		h.Set(HXResettle, rc.hmx.Resettle)
	}
}
