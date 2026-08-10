package render

import (
	"bytes"
	"fmt"
)

// RenderPage is the high-level "render a page" helper. It composes
// a layout + view + components in three steps:
//
//  1. Render the view to a buffer.
//  2. Stash the view bytes on the RenderContext (rc.ViewBytes) for
//     the yield() superpower.
//  3. Render the layout. Components (e.g. <NAVBAR/>) are
//     dispatched via the SoA executor; the layout's {{ yield }}
//     reads rc.ViewBytes via the FuncMap superpower.
//
// Layout authors use `{{ yield }}` (FuncMap) or <YIELD/> (SoA
// component) where the body should go:
//
//	<html><body><NAVBAR/>
//	  {{ yield }}
//	  <FOOTER/>
//	</body></html>
//
// View authors write the inner content as a flat HTML template:
//
//	<h1>{{.Title}}</h1>
//	<p>{{.Body}}</p>
//
// RenderPage is the only entry point users typically need.
//
// Errors returned by RenderPage are wrapped sentinels:
//   - ErrRenderPageInvalid for empty layout or view
//   - ErrLayoutMissing if the layout lookup fails
//   - ErrTemplateNotFound for missing view
//   - ErrLoaderMissing if the RenderContext has no loader
//
// Routers can use errors.Is to translate these to HTTP responses.
func (r *Registry) RenderPage(rc *RenderContext, layout, view string, data any) error {
	return r.RenderPageWith(rc, "html", layout, view, data)
}

// RenderPageWith is RenderPage with an explicit engine name. Use
// this to compose a layout + view with an engine other than the
// default plain HTML engine (e.g. "html-template" or "jade").
func (r *Registry) RenderPageWith(rc *RenderContext, engine, layout, view string, data any) error {
	// Engines with native composition (templ) handle this themselves.
	if eng := r.Engine(engine); eng != nil {
		if pc, ok := eng.(PageComposer); ok {
			if rc == nil {
				return fmt.Errorf("%w: nil RenderContext", ErrRenderPageInvalid)
			}
			return pc.RenderPage(rc, rc.Writer, layout, view, data)
		}
	}
	return r.renderPageEngines(rc, engine, layout, engine, view, data)
}

// RenderPageEngines composes a layout and view rendered by DIFFERENT
// engines — the GO-Portfolio dynamic-layout pattern. The handler
// names the view; the layout wraps it; partials are components.
//
// Typical split: the layout is plain HTML (compiled to bytecode,
// ~85 ns) with <YIELD/> where the view goes, and the view is templ
// (or jade, or html-template):
//
//	reg.RenderPageEngines(rc,
//	    render.EngineHTML, "layouts/app",   // bytecode layout
//	    render.EngineTempl, "posts/show",   // templ view
//	    data)
//
// The view is rendered to a buffer, then the layout is rendered with
// a YIELD component injecting it. Components inside the layout
// (partials like <NAVBAR/>) dispatch through the ComponentRegistry.
func (r *Registry) RenderPageEngines(rc *RenderContext, layoutEngine EngineName, layout string, viewEngine EngineName, view string, data any) error {
	return r.renderPageEngines(rc, layoutEngine.String(), layout, viewEngine.String(), view, data)
}

// RenderStream renders a layout + view composition with progressive
// HTTP streaming. The layout's bytes are emitted in chunks of
// approximately chunkSize bytes; each chunk is pushed to the
// underlying response writer and signaled via http.Flusher so it
// hits the wire immediately. The browser starts parsing markup
// before the full template has been rendered — TTFB drops to the
// time the first chunk accumulates.
//
// chunkSize of 0 disables chunking (writes pass through to the
// parent writer unchanged). Negative values are treated as 0.
//
// When the layout engine is a PageComposer (e.g. templ), the layout's
// RenderPage handles streaming directly. For the engine-agnostic
// buffer-injection path, the per-flush writer wraps the response
// writer so the layout's bytecode writes stream incrementally.
//
// RenderStream is the streaming primitive for HTTP/1.1 chunked
// encoding and HTTP/2 frame emission. Use it for slow-to-render
// pages with substantial static content.
func (r *Registry) RenderStream(rc *RenderContext, engine EngineName, layout, view string, data any, chunkSize int) error {
	if rc == nil {
		return fmt.Errorf("%w: nil RenderContext", ErrRenderPageInvalid)
	}
	if rc.Writer == nil {
		return fmt.Errorf("%w: nil writer", ErrRenderPageInvalid)
	}
	// Wrap the per-request writer in a per-flush wrapper so the
	// layout's bytes emit in chunks. Acquire/Release are scoped to
	// this call — the wrapper is returned to its pool when done.
	pw := AcquirePerFlushWriter(rc.Writer, chunkSize)
	defer ReleasePerFlushWriter(pw)

	// Swap rc.Writer for the wrapped writer for the duration of this
	// call. Both paths (PageComposer and buffer-injection) write
	// through rc.Writer, so a single swap covers both.
	savedWriter := rc.Writer
	rc.Writer = pw
	defer func() { rc.Writer = savedWriter }()

	// Engines with native composition (templ) handle this themselves.
	if eng := r.Engine(engine.String()); eng != nil {
		if pc, ok := eng.(PageComposer); ok {
			return pc.RenderPage(rc, rc.Writer, layout, view, data)
		}
	}
	return r.renderPageEngines(rc, engine.String(), layout, engine.String(), view, data)
}

// renderPageEngines is the generic buffer+YIELD composition path.
func (r *Registry) renderPageEngines(rc *RenderContext, layoutEngine, layout, viewEngine, view string, data any) error {
	if rc == nil {
		return fmt.Errorf("%w: nil RenderContext", ErrRenderPageInvalid)
	}
	if layout == "" {
		return ErrLayoutMissing
	}
	if view == "" {
		return fmt.Errorf("%w: missing view", ErrRenderPageInvalid)
	}
	if layoutEngine == "" || viewEngine == "" {
		return fmt.Errorf("%w: missing engine", ErrRenderPageInvalid)
	}

	// 1. Render the view (viewEngine) to a buffer.
	var viewBuf bytes.Buffer
	viewBw := AcquireWriter(&viewBuf)
	defer ReleaseWriter(viewBw)
	viewRc := AcquireContext(viewBw, rc.Request)
	defer ReleaseContext(viewRc)
	viewRc.Loader = rc.Loader
	viewRc.Variants = rc.Variants
	viewRc.FuncMap = rc.FuncMap
	viewRc.SetComponentRegistry(rc.ComponentRegistry())

	// Metadata pre-flight: if the view name is a registered
	// component with a Metadata closure, run it before the render
	// walk so head metadata lands before anything streams. For
	// most views this is redundant — components in the view can
	// call SetTitle/AddMeta during their render — but it covers
	// views that aren't registered components or metadata that
	// must run unconditionally.
	//
	// Note: consult r.Components() directly — the render call
	// below is what auto-populates viewRc's registry, so it's
	// still nil here.
	if cr := r.Components(); cr != nil {
		if vc, ok := cr.Lookup(view); ok {
			if mp, ok := vc.(MetadataProvider); ok {
				if fn := mp.Metadata(); fn != nil {
					if err := fn(viewRc, data); err != nil {
						return err
					}
				}
			}
		}
	}

	if err := r.Render(viewRc, viewEngine, view, data); err != nil {
		return err
	}
	if err := viewBw.Flush(); err != nil {
		return err
	}

	// Transfer head state and the id sequence from the view
	// context to the main context: the layout renders with rc, so
	// title/meta/assets set during the view render (pass 1) must
	// be visible to <NANO_HEAD/> / <NANO_ASSETS/> in the layout
	// (pass 2). The id sequence continues so page-wide UseId
	// calls never collide.
	rc.title = viewRc.title
	inlineN := viewRc.metaN
	if inlineN > metaInlineCap {
		inlineN = metaInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.AddMeta(viewRc.metaTags[i].name, viewRc.metaTags[i].content)
	}
	for _, kv := range viewRc.metaOverflow {
		rc.AddMeta(kv.name, kv.content)
	}
	inlineN = viewRc.cssN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.RequiresCSS(viewRc.cssAssets[i])
	}
	for _, href := range viewRc.cssOverflow {
		rc.RequiresCSS(href)
	}
	inlineN = viewRc.jsN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.RequiresJS(viewRc.jsAssets[i])
	}
	for _, src := range viewRc.jsOverflow {
		rc.RequiresJS(src)
	}
	rc.idSeq = viewRc.idSeq

	// 2. Stash the view bytes for the yield() superpower. The layout's
	//    html/template reads this via the FuncMap closure. We also
	//    register a YIELD component for engines that use the SoA
	//    executor / bytecode (plain HTML) where <YIELD/> is a tag.
	rc.ViewBytes = viewBuf.Bytes()
	cr := rc.ComponentRegistry()
	if cr == nil {
		// Use the registry's components (built-ins like PRELOADS /
		// NANO_HEAD plus anything attached). Creating a fresh empty
		// registry here would silently drop them — it would also
		// block renderNamed's auto-population later.
		cr = r.Components()
		if cr == nil {
			cr = NewComponentRegistry()
		}
		rc.SetComponentRegistry(cr)
	}
	viewBytes := viewBuf.Bytes()
	yield := ComponentFunc(func(w ByteWriter, _ *RenderContext, _ any) error {
		_, err := w.Write(viewBytes)
		return err
	})
	cr.Register("YIELD", yield)
	defer cr.Unregister("YIELD")

	// 3. Render the layout (layoutEngine) — the bytecode path for
	//    plain HTML, so a static layout is a handful of writes.
	return r.Render(rc, layoutEngine, layout, data)
}

// Unregister removes a component from the registry. Used internally
// to clean up after RenderPage.
func (r *ComponentRegistry) Unregister(name string) {
	for {
		old := r.components.Load()
		newMap := make(map[string]Component, len(*old))
		for k, v := range *old {
			if k != name {
				newMap[k] = v
			}
		}
		if r.components.CompareAndSwap(old, &newMap) {
			return
		}
	}
}
