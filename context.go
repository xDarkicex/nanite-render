package render

import (
	"html/template"
	"io"
	"maps"
	"net/http"
	"sync"
	"time"
)

// ContextKey is the key used to store the *RenderContext on the
// nanite.Context's Values map (or any compat router's context map).
// Re-exported as a public constant so external callers can read the
// context back without stringly-typed map access.
const ContextKey = "render"

// SourceFunc returns the raw template bytes for a given template name.
// The Registry calls this on cache miss. Implementations are typically
// fs.FS, embed.FS, or a path-based loader.
type SourceFunc func(name string) ([]byte, error)

// RenderContext is the per-request context the Registry writes through.
// It owns the ByteWriter, the SourceFunc, the per-request FuncMap, and
// the optional Times map. Instances are pooled — use AcquireContext
// and ReleaseContext.
//
// RenderContext is router-agnostic. The core render package has no
// dependency on any specific router (nanite, chi, gin, stdlib http).
// Adapters live in sub-packages (e.g. nano/, chi/, gin/).
type RenderContext struct {
	// Writer is the ByteWriter render writes into. The underlying
	// io.Writer is typically the response writer wrapped by a router.
	Writer ByteWriter

	// Request is the live HTTP request. Headers are written by the
	// caller before any render invocation so the buffer never holds
	// head-of-line bytes.
	Request *http.Request

	// Loader resolves a template name to bytes. Typically set by the
	// router middleware when the request starts.
	Loader SourceFunc

	// FuncMap is the per-request template function map. Engines
	// apply it on top of (or instead of) the Program's default
	// FuncMap. Frozen on the Program for compile-time defaults,
	// overridden by the request at render time.
	//
	// This mirrors GO-Portfolio's per-render funcMap (render.go:83-106),
	// which lets each handler inject closure-bound helpers.
	FuncMap template.FuncMap

	// Times is populated when the Debug stage is enabled. The map
	// is keyed by stage name and stores the wall-clock duration of
	// each stage. nil when Debug is off — opt-in only.
	Times map[string]time.Duration

	// Headers is the set of response headers to write before the body.
	// Pre-set by the middleware (Content-Type, Cache-Control, etc.).
	Headers http.Header

	// Layout is the optional layout template name. When set, the
	// Renderer renders the inner view then wraps it in the layout.
	// This mirrors GO-Portfolio's two-pass layout pattern.
	Layout string

	// Variants are the per-request variant dimensions, e.g.
	// {"locale": "fr", "theme": "dark"}. Resolved at Part() lookup
	// to compose the cache key. Composed Layout = engine/name@locale.fr@theme.dark.
	Variants map[string]string

	// varKeyBuf is a scratch buffer for variantKey / variantName
	// outputs. The returned strings are views of this buffer via
	// unsafe.String; the caller must use them before the
	// RenderContext is released. The buffer is part of the
	// RenderContext so it's persistent across Get on the render
	// hot path.
	varKeyBuf []byte

	// userData is the side-channel used when injectors wrap a
	// non-map data value. Engines retrieve it via UserData().
	userData any

	// ViewBytes is the pre-rendered view HTML used by the yield()
	// superpower. Set by RenderPage before the layout is executed.
	// Engines that defer to html/template read this via the FuncMap.
	ViewBytes []byte

	// ComponentFuncMap is the per-component FuncMap set by the
	// component dispatcher when rendering a component that
	// implements ComponentWithFuncs. Components that compose via
	// html/template can read this to expose component-specific
	// helpers during their render.
	ComponentFuncMap template.FuncMap

	// state is the per-render state. Components access it via
	// the FuncMap (useState, get, set) or directly via
	// rc.ComponentState().
	state *State

	// componentRegistry is the active ComponentRegistry. Set by
	// the engine adapter or middleware. The executor reads this
	// to dispatch component nodes.
	componentRegistry *ComponentRegistry

	// oobSink is the destination for HTMX Out-of-Band swap payloads
	// when components with WithOOB mutate state during render. nil
	// means OOB swaps write to Writer instead. Set via SetOOBSink.
	// The router can target a separate stream (e.g. a trailing
	// HTTP/2 frame) by pointing OOBSink elsewhere.
	oobSink ByteWriter

	// hxTriggers accumulates HTMX client-side event names during a
	// render. Components call AddHXTrigger; the router reads
	// HXTriggers() at the end of the handler to populate the
	// HX-Trigger response header. nil until the first Add.
	hxTriggers HXTriggers

	// hxTriggersAfterSwap mirrors hxTriggers for events fired after
	// the swap step. Populates the HX-Trigger-After-Swap header.
	hxTriggersAfterSwap HXTriggers

	// hxTriggersAfterSettle mirrors hxTriggers for events fired
	// after the settle step. Populates the HX-Trigger-After-Settle
	// header.
	hxTriggersAfterSettle HXTriggers

	// hmx holds server-driven HTMX response decisions (retarget,
	// reswap, push-url, redirect, refresh, replace-url, location).
	// Set via methods like SetHXRetarget; WriteHTMXHeaders applies
	// the full set to the response writer at the end of the handler.
	hmx HTMXResponse
}

// HTMXResponse bundles server-driven HTMX response decisions. The
// router calls one method on RenderContext (e.g. SetHXRetarget) per
// decision during the handler, then calls WriteHTMXHeaders once at
// the end to apply everything to the response. Each field's zero
// value means "leave the client-side default in place."
//
// Field reference (HTMX 2.x):
//
//	Retarget     — overrides hx-target on the triggered element
//	Reswap       — overrides hx-swap on the triggered element
//	PushURL      — pushes a new URL into history (true → use current, or a URL)
//	Redirect     — triggers a client-side redirect (full URL required)
//	Refresh      — triggers a full page refresh
//	ReplaceURL   — replaces current URL without history entry (true or URL)
//	Location     — triggers client-side navigation without history (full URL)
//	Reselect     — CSS selector picking which part of the response to swap
//	Resettle     — "true" forces scroll reset; "false" preserves scroll
type HTMXResponse struct {
	// Retarget overrides hx-target. CSS selector string.
	Retarget string
	// Reswap overrides hx-swap. e.g. "outerHTML", "innerHTML", "none".
	Reswap string
	// PushURL controls HX-Push-Url. Empty = unset. "true" pushes the
	// current URL; any other value is treated as a URL to push.
	PushURL string
	// Redirect triggers a client-side redirect to the given URL.
	Redirect string
	// Refresh triggers a full page refresh when "true".
	Refresh string
	// ReplaceURL replaces the current URL without a history entry.
	// Empty = unset. "true" replaces with the current URL; any
	// other value is treated as the replacement URL.
	ReplaceURL string
	// Location triggers client-side navigation without history.
	// Full URL required.
	Location string
	// Reselect is a CSS selector for the part of the response body
	// HTMX should swap in. Useful when the server returns a wrapper
	// and only an inner region should land.
	Reselect string
	// Resettle controls the post-swap settle algorithm. "true"
	// forces scroll reset to the top; "false" preserves scroll.
	Resettle string
}

// ComponentRegistry returns the active ComponentRegistry, if any.
func (rc *RenderContext) ComponentRegistry() *ComponentRegistry {
	return rc.componentRegistry
}

// SetComponentRegistry binds a ComponentRegistry to this context.
func (rc *RenderContext) SetComponentRegistry(c *ComponentRegistry) {
	rc.componentRegistry = c
}

// SetOOBSink redirects HTMX Out-of-Band swap output for components
// with WithOOB enabled. Pass nil to clear; OOBSink() then falls back
// to Writer.
//
// Most handlers leave this nil — the main response is the swap —
// but routers that want to interleave OOB updates with a different
// stream (e.g. an SSE trailing chunk) can set it here.
func (rc *RenderContext) SetOOBSink(w ByteWriter) {
	rc.oobSink = w
}

// OOBSink returns the destination for OOB swap output. Falls back to
// Writer when no explicit sink is configured.
func (rc *RenderContext) OOBSink() ByteWriter {
	if rc.oobSink != nil {
		return rc.oobSink
	}
	return rc.Writer
}

// UserData returns the (possibly wrapped) data the injectors last
// saw. The Registry uses this when the original data was not a map.
func (rc *RenderContext) UserData() any { return rc.userData }

// SetUserData stores the wrapped data so engines can read it back.
func (rc *RenderContext) SetUserData(d any) { rc.userData = d }

// SetVariant sets a single variant dimension. Used to drive
// locale/theme resolution at Part() lookup.
func (rc *RenderContext) SetVariant(dim, value string) {
	if rc.Variants == nil {
		rc.Variants = make(map[string]string, 2)
	}
	rc.Variants[dim] = value
}

// Context pool. One instance per request, returned to the pool on defer.
var rcPool = sync.Pool{
	New: func() any { return &RenderContext{} },
}

// AcquireContext returns a fresh RenderContext bound to w and req. The
// caller must ReleaseContext when done.
func AcquireContext(w ByteWriter, req *http.Request) *RenderContext {
	rc := rcPool.Get().(*RenderContext)
	rc.Writer = w
	rc.Request = req
	if rc.Headers == nil {
		rc.Headers = make(http.Header, 4)
	} else {
		for k := range rc.Headers {
			delete(rc.Headers, k)
		}
	}
	rc.FuncMap = nil
	rc.Times = nil
	rc.Layout = ""
	// Clear per-request fields that survive pool reuse. The auto-
	// populate in Registry.renderNamed repopulates componentRegistry
	// from the active registry's built-ins; clearing here prevents
	// leaking the previous request's ComponentRegistry across calls.
	rc.componentRegistry = nil
	rc.ComponentFuncMap = nil
	rc.ViewBytes = nil
	rc.oobSink = nil
	rc.hxTriggers = nil
	rc.hxTriggersAfterSwap = nil
	rc.hxTriggersAfterSettle = nil
	rc.hmx = HTMXResponse{}
	if rc.state == nil {
		rc.state = NewState()
	} else {
		rc.state.Clear()
	}
	return rc
}

// ReleaseContext returns the context to the pool. The associated Writer
// is *not* closed by this call; the caller is responsible for flushing.
func ReleaseContext(rc *RenderContext) {
	if rc == nil {
		return
	}
	rc.Writer = nil
	rc.Request = nil
	rc.Loader = nil
	rc.FuncMap = nil
	rc.Times = nil
	rc.Layout = ""
	rc.componentRegistry = nil
	rc.ComponentFuncMap = nil
	rc.ViewBytes = nil
	rc.userData = nil
	rc.oobSink = nil
	rc.hxTriggers = nil
	rc.hxTriggersAfterSwap = nil
	rc.hxTriggersAfterSettle = nil
	rc.hmx = HTMXResponse{}
	rcPool.Put(rc)
}

// WithLoader returns rc with the given Loader attached. The receiver is
// returned unchanged for chaining.
func (rc *RenderContext) WithLoader(loader SourceFunc) *RenderContext {
	rc.Loader = loader
	return rc
}

// WithFuncMap returns rc with the given FuncMap attached. The receiver
// is returned unchanged for chaining.
func (rc *RenderContext) WithFuncMap(fm template.FuncMap) *RenderContext {
	rc.FuncMap = fm
	return rc
}

// WithLayout returns rc with the given layout name attached. The
// receiver is returned unchanged for chaining.
func (rc *RenderContext) WithLayout(name string) *RenderContext {
	rc.Layout = name
	return rc
}

// WithDebug enables the Times map so the Debug stage can populate it.
func (rc *RenderContext) WithDebug() *RenderContext {
	if rc.Times == nil {
		rc.Times = make(map[string]time.Duration, 8)
	}
	return rc
}

// WriteHeader writes h to the underlying writer. Headers are not
// buffered by ByteWriter; they are pushed directly to the wrapped
// io.Writer so the response is flushed before the body.
func (rc *RenderContext) WriteHeader(key, value string) {
	if rc.Headers == nil {
		rc.Headers = make(http.Header, 4)
	}
	rc.Headers.Set(key, value)
}

// WriteHeadersTo writes the queued headers to the underlying writer.
// Called automatically by the ByteWriter the first time bytes are
// written.
func (rc *RenderContext) WriteHeadersTo(w io.Writer) error {
	if len(rc.Headers) == 0 {
		return nil
	}
	for k, vs := range rc.Headers {
		for _, v := range vs {
			line := k + ": " + v + "\r\n"
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// EffectiveFuncMap returns the FuncMap that should be applied to the
// Program. Per-request FuncMap (when set) overrides the Program's
// default. If neither is set, returns nil.
func (rc *RenderContext) EffectiveFuncMap(programDefaults template.FuncMap) template.FuncMap {
	if len(rc.FuncMap) == 0 {
		return programDefaults
	}
	if programDefaults == nil {
		return rc.FuncMap
	}
	// Merge: per-request takes precedence.
	merged := make(template.FuncMap, len(programDefaults)+len(rc.FuncMap))
	maps.Copy(merged, programDefaults)
	maps.Copy(merged, rc.FuncMap)
	return merged
}
