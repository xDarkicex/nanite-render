package render

import (
	"html"
	"io"
)

// PreloadHint describes a resource the browser should fetch ahead of
// time. The hint is emitted as a <link rel="preload"> tag when the
// <PRELOADS/> component is rendered in the layout's <head>.
//
// Empty fields are omitted from the emitted tag. Common values:
//
//	href: "/static/app.css"     the resource URL
//	as:   "style"               the content type the browser expects
//	                               (style, script, image, font, fetch)
//	type: "text/css"            MIME type — used by the browser to
//	                               validate support before fetching
type PreloadHint struct {
	Href string
	As   string
	Type string
}

// PreloadHints returns the active set of preload hints registered
// against the registry. Returned for read-only inspection; mutations
// must go through Registry.Preload / Unpreload.
type PreloadHints struct {
	hints []PreloadHint
}

// Preload registers a preload hint at the registry level. The hint is
// emitted (in registration order) wherever a <PRELOADS/> component tag
// appears in a layout — typically inside <head>. Hints are deduplicated
// by (Href, As, Type); registering the same triple twice is a no-op.
//
//	// Boot time:
//	reg := render.New(render.WithEngines(engine.NewHTML()))
//	reg.Preload(render.PreloadHint{Href: "/static/app.css", As: "style", Type: "text/css"})
//	reg.Preload(render.PreloadHint{Href: "/static/app.js", As: "script"})
//
//	// In the layout:
//	// <html><head><PRELOADS/></head><body>...</body></html>
func (r *Registry) Preload(h PreloadHint) {
	for {
		old := r.preloads.Load()
		// Copy-on-write. Skip if already present (dedupe).
		var next *[]PreloadHint
		if old != nil {
			for _, existing := range *old {
				if existing == h {
					return
				}
			}
			cp := make([]PreloadHint, len(*old)+1)
			copy(cp, *old)
			next = &cp
		} else {
			cp := make([]PreloadHint, 1)
			next = &cp
		}
		(*next)[len(*next)-1] = h
		if r.preloads.CompareAndSwap(old, next) {
			return
		}
	}
}

// Unpreload removes every hint matching the given (As, Type) pair.
// Use this when a resource becomes unreachable (e.g. CDN purge).
// Matching is by As+Type only — Href is ignored because a single
// resource type often spans many URLs.
func (r *Registry) Unpreload(as, typeStr string) {
	for {
		old := r.preloads.Load()
		if old == nil || len(*old) == 0 {
			return
		}
		cp := make([]PreloadHint, 0, len(*old))
		for _, h := range *old {
			if h.As == as && h.Type == typeStr {
				continue
			}
			cp = append(cp, h)
		}
		if len(cp) == len(*old) {
			return
		}
		if r.preloads.CompareAndSwap(old, &cp) {
			return
		}
	}
}

// Preloads returns a snapshot of the registered hints. The returned
// slice is a copy; callers may mutate it freely.
func (r *Registry) Preloads() []PreloadHint {
	p := r.preloads.Load()
	if p == nil {
		return nil
	}
	cp := make([]PreloadHint, len(*p))
	copy(cp, *p)
	return cp
}

// emitPreloads writes the registered hints as <link rel="preload">
// tags. Called by the PRELOADS component. The format is exactly:
//
//	<link rel="preload" href="..." [as="..."] [type="..."]>
//
// Empty optional fields are omitted. Attribute values are HTML-escaped
// so a malicious href cannot break out of the attribute.
func emitPreloads(w ByteWriter, r *Registry) error {
	hints := r.Preloads()
	for _, h := range hints {
		if _, err := w.WriteString(`<link rel="preload" href="`); err != nil {
			return err
		}
		if _, err := w.WriteString(html.EscapeString(h.Href)); err != nil {
			return err
		}
		if _, err := w.WriteString(`"`); err != nil {
			return err
		}
		if h.As != "" {
			if _, err := w.WriteString(` as="`); err != nil {
				return err
			}
			if _, err := w.WriteString(html.EscapeString(h.As)); err != nil {
				return err
			}
			if _, err := w.WriteString(`"`); err != nil {
				return err
			}
		}
		if h.Type != "" {
			if _, err := w.WriteString(` type="`); err != nil {
				return err
			}
			if _, err := w.WriteString(html.EscapeString(h.Type)); err != nil {
				return err
			}
			if _, err := w.WriteString(`"`); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, `>`); err != nil {
			return err
		}
	}
	return nil
}

// preloadsComponent is the built-in component rendered by <PRELOADS/>.
// It emits the registry-level preload hints as <link rel="preload">
// tags. Holds a back-reference to the owning registry so hints
// registered after the component was installed are still visible
// (the registry uses atomic.Pointer for lock-free reads).
type preloadsComponent struct {
	r *Registry
}

// Render implements Component.
func (p *preloadsComponent) Render(w ByteWriter, rc *RenderContext, data any) error {
	if p.r == nil {
		return nil
	}
	return emitPreloads(w, p.r)
}
