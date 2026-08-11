package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// DefaultActionPrefix is the default URL prefix for colocated
// server actions. Actions are mounted at
// {prefix}/{COMPONENT}/{action} — e.g.
// /_nano/action/LIKE_BUTTON/toggle. Override with WithActionPrefix.
const DefaultActionPrefix = "/_nano/action/"

// ActionFunc is a colocated server action: mutation logic that
// lives next to the component that triggers it. Invoked by
// Registry.HandleAction when the action's URL is POSTed.
//
// props is the parsed request body — JSON bodies decode into a
// map[string]any with real types (BindProps-compatible); form
// bodies get best-effort conversion ("42" → int, "true" → bool,
// else string).
//
// The state contract: this framework does NOT persist state
// across requests. The action mutates the user's own storage
// (DB, session, cookie), and the subsequent re-render of the
// component is a pure function of (props, request). Per-request
// state set here (rc.SetState) IS visible to the re-render that
// follows in the same request.
type ActionFunc func(rc *RenderContext, props map[string]any) error

// ActionProvider is an optional interface a Component implements
// to expose colocated server actions. Engines that pre-compile
// components can implement it directly; the fluent builder
// exposes it via Definition.Action.
type ActionProvider interface {
	Component
	LookupAction(name string) (ActionFunc, bool)
}

// ActionPrefix returns the URL prefix for action endpoints.
// Defaults to DefaultActionPrefix.
func (r *Registry) ActionPrefix() string {
	if r.actionPrefix == "" {
		return DefaultActionPrefix
	}
	return r.actionPrefix
}

// SetActionPrefix changes the URL prefix for action endpoints.
// The prefix must end with "/" — ActionURL appends
// {COMPONENT}/{action} directly.
func (r *Registry) SetActionPrefix(prefix string) {
	r.actionPrefix = prefix
}

// HandleAction is the universal colocated-action HTTP handler.
// Mount it on any router:
//
//	// chi
//	r.Post("/_nano/action/*", reg.HandleAction)
//	// stdlib
//	mux.HandleFunc("/_nano/action/", reg.HandleAction)
//
// Flow:
//
//  1. Method must be POST (405 otherwise).
//  2. HX-Request header must be "true" (403 otherwise).
//  3. Origin or Referer host must match the request Host (403
//     otherwise). This is the CSRF baseline — HTTPS sites behind
//     proxies may need to relax it by wrapping the handler.
//  4. The path is parsed as {prefix}/{COMPONENT}/{action}. Unknown
//     component or action → 404.
//  5. The body is parsed (JSON → typed map, form → best-effort
//     conversion) and passed as props.
//  6. The action closure runs with a fresh RenderContext. Any
//     HX-Trigger events it records are emitted on the response.
//  7. The component re-renders with the post-action state. If the
//     component declared WithOOB(id), the output is wrapped in an
//     HTMX OOB chunk; otherwise raw HTML is returned so the
//     client's hx-target (or triggering element) swaps it inline.
//
// Action closures are slow-path by definition (they mutate a
// database); the 0 B/op guarantee is unaffected — it applies to
// the render hot path, not to mutation endpoints.
func (r *Registry) HandleAction(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if req.Header.Get("HX-Request") != "true" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !actionOriginAllowed(req) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	component, action, ok := parseActionPath(req.URL.Path, r.ActionPrefix())
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cr := r.Components()
	if cr == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	c, found := cr.Lookup(component)
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ap, isAction := c.(ActionProvider)
	if !isAction {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fn, found := ap.LookupAction(action)
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	props := parseActionBody(req)

	// Invoke the action, then re-render the component, both with
	// the same per-request RenderContext so state set by the
	// action (form errors, state) is visible to the re-render.
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	rc := AcquireContext(bw, req)
	rc.Loader = r.DefaultLoader()
	if err := fn(rc, props); err != nil {
		if !errors.Is(err, ErrValidation) {
			ReleaseWriter(bw)
			ReleaseContext(rc)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		// Validation failure: the action recorded per-field
		// errors via rc.SetFormError and returned ErrValidation.
		// Fall through to the re-render — the errors are in rc
		// and the component reads them via c.GetFormError. We
		// return 200 (NOT 422): HTMX does not swap 4xx/5xx
		// responses by default, it fires htmx:responseError and
		// the form with inline errors would never appear.
	}
	if err := r.RenderComponent(bw, rc, component, props); err != nil {
		ReleaseWriter(bw)
		ReleaseContext(rc)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	_ = bw.Flush()
	out := buf.Bytes()
	ReleaseWriter(bw)
	ReleaseContext(rc)

	// Emit HTMX response headers recorded during the action
	// (HX-Trigger, retarget, etc.), then the body.
	writeHTMXHeaders(w, rc)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Auto-detect swap semantics: components declared WithOOB(id)
	// get an HTMX OOB wrapper; everything else returns raw HTML
	// for the client's hx-target to swap inline. The re-render's
	// own OOB machinery may already have wrapped the output (when
	// the render called SetState on an OOB component) — don't
	// double-wrap in that case.
	oobID := ""
	if oo, ok := c.(OOBOptioner); ok {
		oobID = oo.OOBID()
	}
	if oobID != "" && !bytes.HasPrefix(out, []byte(`<div id="`+html.EscapeString(oobID)+`" hx-swap-oob="true">`)) {
		_, _ = io.WriteString(w, `<div id="`+html.EscapeString(oobID)+`" hx-swap-oob="true">`)
		_, _ = w.Write(out)
		_, _ = io.WriteString(w, `</div>`)
		return
	}
	_, _ = w.Write(out)
}

// writeHTMXHeaders writes the HTMX response decisions recorded on
// rc to w. Set via rc.AddHXTrigger / SetHXRetarget / etc.
func writeHTMXHeaders(w http.ResponseWriter, rc *RenderContext) {
	if rc == nil {
		return
	}
	rc.WriteHTMXHeaders(w)
}

// parseActionPath splits {prefix}/{COMPONENT}/{action} from a
// request path. Zero-alloc: string slicing only, no URL parsing.
func parseActionPath(path, prefix string) (component, action string, ok bool) {
	if prefix == "" {
		prefix = DefaultActionPrefix
	}
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := path[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 || slash == 0 || slash == len(rest)-1 {
		return "", "", false
	}
	component = rest[:slash]
	action = rest[slash+1:]
	if strings.IndexByte(action, '/') >= 0 || component == "" || action == "" {
		return "", "", false
	}
	return component, action, true
}

// actionOriginAllowed implements the CSRF baseline: the request
// must carry an Origin or Referer header whose host matches the
// request's Host. Zero-alloc — plain byte scanning, no net/url
// (url.Parse allocates heavily).
//
// Host comparison strips ports on both sides so a request to
// example.com:8080 accepts an Origin of https://example.com and
// vice versa.
func actionOriginAllowed(req *http.Request) bool {
	ref := req.Header.Get("Origin")
	if ref == "" {
		ref = req.Header.Get("Referer")
	}
	if ref == "" {
		return false
	}
	// Strip scheme: "http://" / "https://".
	i := strings.IndexByte(ref, ':')
	if i < 0 {
		return false
	}
	scheme := ref[:i]
	if scheme != "http" && scheme != "https" {
		return false
	}
	rest := ref[i+1:]
	if !strings.HasPrefix(rest, "//") {
		return false
	}
	host := rest[2:]
	// Strip path and query.
	if j := strings.IndexByte(host, '/'); j >= 0 {
		host = host[:j]
	}
	if j := strings.IndexByte(host, '?'); j >= 0 {
		host = host[:j]
	}
	// Strip port.
	if j := strings.IndexByte(host, ':'); j >= 0 {
		host = host[:j]
	}
	reqHost := req.Host
	if j := strings.IndexByte(reqHost, ':'); j >= 0 {
		reqHost = reqHost[:j]
	}
	return host == reqHost
}

// parseActionBody decodes the request body into props:
//
//   - application/json → map[string]any via encoding/json (real
//     types, BindProps-compatible). Decode errors yield nil.
//   - anything else (forms) → r.PostForm with best-effort
//     conversion per value ("42" → int, "true"/"false" → bool,
//     "3.14" → float64, else the raw string).
//
// Body parsing is inherently the slow path (mutations), so
// allocations here are acceptable.
func parseActionBody(req *http.Request) map[string]any {
	if strings.Contains(req.Header.Get("Content-Type"), "application/json") {
		var m map[string]any
		_ = json.NewDecoder(req.Body).Decode(&m)
		if m == nil {
			// Empty or null body — always hand actions a writable
			// map so `props["k"] = v` never panics.
			return map[string]any{}
		}
		return m
	}
	_ = req.ParseForm()
	m := make(map[string]any, len(req.PostForm))
	for k, vs := range req.PostForm {
		if len(vs) == 0 {
			continue
		}
		m[k] = convertFormValue(vs[0])
	}
	return m
}

// convertFormValue best-effort converts a form string to a typed
// primitive. Order matters (cheapest first): bool → int → float64
// → raw string.
//
// strconv.Atoi (not ParseInt) so the result is `int`, matching
// BindProps' exact-type check for the common `int` field.
func convertFormValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
