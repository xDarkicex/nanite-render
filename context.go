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

	// suspense is the lazily-allocated coordinator for Async
	// components. Nil until the first Async component is hit during
	// a render walk; see suspense.go.
	suspense     *suspenseCoordinator
	suspenseOnce sync.Once

	// ctxStack is the cascading-context stack. Components call
	// ProvideContext to push (key, val) and UseContext to look up
	// the nearest binding for a key (reverse-scan).
	//
	// The first inlineCtxCap bindings live in inlineCtx — a
	// fixed-size array on the RenderContext struct, persistent
	// across pool reuse, zero allocations on the fast path.
	// RenderContext is pool-allocated so this array is part of
	// the pooled memory; no heap traffic for typical pages (a
	// real page has 3-5 cascading contexts).
	//
	// If a render tree exceeds inlineCtxCap nested bindings
	// (unusual; admin UIs with deeply nested layouts can), pushes
	// spill into overflowCtx — a heap slice that grows as
	// needed. The slow path still works, the framework never
	// panics.
	//
	// The stack pointer is reset in AcquireContext; per-render
	// pushes during the walk are unwound automatically by
	// ComponentContext when each component's render returns.
	inlineCtx   [inlineCtxCap]contextKV
	overflowCtx []contextKV
	ctxPtr      int

	// actionPrefix is the colocated-action URL prefix for this
	// request. Set by the Registry before rendering (renderNamed)
	// so ComponentContext.ActionURL can generate absolute action
	// URLs without a global. "" means the default prefix.
	actionPrefix string

	// formErrors holds flash validation errors for the current
	// request. Set by actions via SetFormError; read by components
	// via GetFormError during the re-render pass. Flash semantics:
	// the errors live for exactly one request and are cleared on
	// pool reuse (AcquireContext).
	//
	// The first formErrInlineCap errors land in the inline array
	// (zero alloc); beyond that they spill into a heap slice.
	formErrInline    [formErrInlineCap]formErrorKV
	formErrOverflow  []formErrorKV
	formErrN         int

	// Head-management state for this request: the document title,
	// collected <meta> tags, and the useId sequence. Set by
	// components during the view render (or by a Metadata
	// closure); emitted by the layout's <NANO_HEAD/> / {{ nanoHead }}
	// injector. Cleared on pool reuse.
	title          string
	metaTags       [metaInlineCap]metaKV
	metaOverflow   []metaKV
	metaN          int
	idSeq          int

	// Asset dependencies collected during the render pass via
	// RequiresCSS / RequiresJS; emitted by <NANO_ASSETS/> /
	// {{ nanoAssets }}. Deduplicated, first occurrence wins.
	// Cleared on pool reuse.
	cssAssets    [assetInlineCap]string
	cssOverflow  []string
	cssN         int
	jsAssets     [assetInlineCap]string
	jsOverflow   []string
	jsN          int
}

// contextKV is one slot on the cascading-context stack. Stored
// inline in RenderContext so pushes/pops are pure memory moves
// (no allocation) up to inlineCtxCap; spills to a heap slice
// beyond that.
type contextKV struct {
	key string
	val any
}

// inlineCtxCap is the size of the zero-allocation inline array.
// 32 × 32 bytes (string + any) = 1 KiB per RenderContext. 32
// nested ambient contexts (theme, user, locale, CSRF, layout,
// …) covers every realistic UI; deeper trees spill to the heap.
const inlineCtxCap = 32

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

// PushContext pushes (key, val) onto the cascading-context stack.
// Nested bindings shadow outer ones (reverse-scan lookup).
//
// The first inlineCtxCap pushes land in the inline array on
// RenderContext itself — zero allocation, persistent across pool
// reuse. Beyond that, pushes spill into overflowCtx (a heap
// slice) so genuinely deep trees don't panic. There is no hard
// cap; the framework never panics on depth.
//
// Prefer ComponentContext.ProvideContext over calling this
// directly — it auto-pops on the component's render scope exit
// and gives you true cascading isolation.
func (rc *RenderContext) PushContext(key string, val any) {
	if rc == nil {
		return
	}
	kv := contextKV{key: key, val: val}
	if rc.ctxPtr < inlineCtxCap {
		rc.inlineCtx[rc.ctxPtr] = kv
	} else {
		// Spillover: heap slice for depth > inlineCtxCap. Index
		// into the slice is offset by inlineCtxCap so logical
		// positions stay contiguous across both storage areas.
		overflowIdx := rc.ctxPtr - inlineCtxCap
		if overflowIdx < len(rc.overflowCtx) {
			rc.overflowCtx[overflowIdx] = kv
		} else {
			rc.overflowCtx = append(rc.overflowCtx, kv)
		}
	}
	rc.ctxPtr++
}

// PopContextTo sets the stack pointer back to a saved index.
// Used by ComponentContext to unwind pushes made during its
// render frame so bindings don't leak out of scope. Popped
// slots are zeroed so the GC can reclaim any `any` values the
// user pushed (without this, they'd stay reachable through the
// stack slot until the next push overwrites them).
func (rc *RenderContext) PopContextTo(savedPtr int) {
	if rc == nil {
		return
	}
	// Defensive: never let the pointer go negative or above the
	// current depth.
	if savedPtr < 0 {
		savedPtr = 0
	}
	if savedPtr > rc.ctxPtr {
		savedPtr = rc.ctxPtr
	}
	// Clear the popped slots in both storage areas.
	for i := savedPtr; i < rc.ctxPtr && i < inlineCtxCap; i++ {
		rc.inlineCtx[i] = contextKV{}
	}
	if rc.ctxPtr > inlineCtxCap {
		// Some of the popped slots live in overflowCtx. Clear
		// them; truncate the overflow slice if the pointer has
		// dropped back into the inline range, so the heap slice
		// doesn't grow unbounded across long-lived pool members.
		overflowEnd := rc.ctxPtr - inlineCtxCap
		if savedPtr >= inlineCtxCap {
			for i := savedPtr - inlineCtxCap; i < overflowEnd; i++ {
				rc.overflowCtx[i] = contextKV{}
			}
		} else {
			// savedPtr is in inline range; clear the entire
			// overflow slice (everything above is popped).
			for i := range rc.overflowCtx {
				rc.overflowCtx[i] = contextKV{}
			}
			rc.overflowCtx = rc.overflowCtx[:0]
		}
	}
	rc.ctxPtr = savedPtr
}

// GetContext scans the cascading-context stack backwards (newest
// first) and returns the value bound to the nearest matching
// key, or nil if no binding exists. Returns nil for a nil
// receiver.
//
// O(n) in the stack depth — at inlineCtxCap the loop is fully
// cache-resident. Deeper trees still hit cache but pay an extra
// indirect on the spillover.
func (rc *RenderContext) GetContext(key string) any {
	if rc == nil {
		return nil
	}
	for i := rc.ctxPtr - 1; i >= 0; i-- {
		var kv contextKV
		if i < inlineCtxCap {
			kv = rc.inlineCtx[i]
		} else {
			kv = rc.overflowCtx[i-inlineCtxCap]
		}
		if kv.key == key {
			return kv.val
		}
	}
	return nil
}

// ContextDepth returns the current stack depth. Useful for
// instrumentation / debugging; not used in the hot path.
func (rc *RenderContext) ContextDepth() int {
	if rc == nil {
		return 0
	}
	return rc.ctxPtr
}

// UseContextFunc returns a template.FuncMap entry that resolves
// the cascading-context stack. Register it via WithFuncMap so
// templates can call {{ useContext "theme" }} and get the nearest
// binding. The result is `any` — the template type-asserts.
//
// Usage:
//
//	reg := render.New(render.WithFuncMap(render.UseContextFunc))
//	// or merge into your own builder:
//	fm := template.FuncMap{"useContext": render.UseContextFunc}
//
// Templates that don't go through the per-request FuncMap won't
// see this helper — but fluent components use ComponentContext
// directly.
func UseContextFunc(rc *RenderContext) any {
	return func(key string) any {
		if rc == nil {
			return nil
		}
		return rc.GetContext(key)
	}
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
	rc.suspense = nil
	rc.suspenseOnce = sync.Once{}
	// Reset the cascading-context stack pointer. The array
	// contents are not cleared here because any leftover binding
	// is unreachable (ctxPtr is the live boundary) and will be
	// overwritten by the next push. ComponentContext's auto-pop
	// clears its own scope on exit, so per-render leakage is
	// impossible.
	rc.ctxPtr = 0
	rc.actionPrefix = ""
	rc.ClearFormErrors()
	rc.ClearHeadState()
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
	rc.suspense = nil
	rc.suspenseOnce = sync.Once{}
	rc.ctxPtr = 0
	rc.actionPrefix = ""
	rc.ClearFormErrors()
	rc.ClearHeadState()
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
