package render

import (
	"bytes"
	"html"
	"strconv"
)

// metaInlineCap is the size of the zero-allocation inline meta-tag
// array on RenderContext. 16 head meta tags covers any realistic
// page; deeper sets spill to a heap slice (same pattern as the
// cascading-context stack and form errors).
const metaInlineCap = 16

// metaKV is one head <meta> tag: name + content.
type metaKV struct {
	name    string
	content string
}

// MetadataProvider is an optional interface a Component implements
// to contribute document head metadata BEFORE the page streams.
// The Metadata closure runs with the page data before the view
// renders; it sets rc.SetTitle / rc.AddMeta, which the layout's
// <NANO_HEAD/> injector emits.
//
// For the common case this is redundant: any component rendered in
// the view can call c.SetTitle during its render (the view renders
// before the layout in the two-pass page pipeline, and the head
// state is transferred to the layout's context). Metadata exists
// for views that are NOT registered components, or metadata that
// should run even if the render walk is skipped.
type MetadataProvider interface {
	Component
	Metadata() func(rc *RenderContext, data any) error
}

// nanoHeadComponent is the built-in <NANO_HEAD/> component (and
// the {{ nanoHead }} FuncMap helper's writer). It emits the
// collected <title> and <meta> tags to the response.
//
// Layouts that want dynamic metadata must include <NANO_HEAD/>
// (or {{ nanoHead }}) inside <head>. Without it, metadata is
// collected but never emitted — explicit placement, no magic.
type nanoHeadComponent struct{}

// Render implements Component. Emits the head tags collected on
// rc via SetTitle/AddMeta. Values are HTML-escaped (user data in
// head tags is an injection vector).
func (n *nanoHeadComponent) Render(w ByteWriter, rc *RenderContext, _ any) error {
	return writeHeadTags(w, rc)
}

// writeHeadTags emits the title and meta tags on rc to w.
func writeHeadTags(w ByteWriter, rc *RenderContext) error {
	if rc == nil {
		return nil
	}
	if rc.title != "" {
		if _, err := w.WriteString(`<title>` + html.EscapeString(rc.title) + `</title>`); err != nil {
			return err
		}
	}
	inlineN := rc.metaN
	if inlineN > metaInlineCap {
		inlineN = metaInlineCap
	}
	for i := 0; i < inlineN; i++ {
		kv := rc.metaTags[i]
		if _, err := w.WriteString(`<meta name="` + html.EscapeString(kv.name) +
			`" content="` + html.EscapeString(kv.content) + `">`); err != nil {
			return err
		}
	}
	for _, kv := range rc.metaOverflow {
		if _, err := w.WriteString(`<meta name="` + html.EscapeString(kv.name) +
			`" content="` + html.EscapeString(kv.content) + `">`); err != nil {
			return err
		}
	}
	return nil
}

// headTagsString renders the collected head tags into a string.
// Used by the {{ nanoHead }} FuncMap helper (template engines),
// which must return a value — the plain-HTML <NANO_HEAD/> path is
// the zero-alloc one.
func headTagsString(rc *RenderContext) string {
	if rc == nil {
		return ""
	}
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	_ = writeHeadTags(bw, rc)
	_ = bw.Flush()
	return buf.String()
}

// SetTitle sets the document <title> for this request. Visible to
// the layout's <NANO_HEAD/> injector on full page loads (the view
// renders before the layout in the two-pass pipeline, and the
// head state is transferred to the layout's context).
//
// On HTMX partial swaps, HTMX hoists <title> tags from the
// response body automatically — components can write them
// directly to their output instead.
func (rc *RenderContext) SetTitle(title string) {
	if rc == nil {
		return
	}
	rc.title = title
}

// Title returns the current document title ("" if unset). Useful
// for tests and for layouts that want to read it back.
func (rc *RenderContext) Title() string {
	if rc == nil {
		return ""
	}
	return rc.title
}

// AddMeta adds a <meta name="..." content="..."> tag for this
// request. Last write wins per name (no duplicate emission).
// Spills to a heap slice past metaInlineCap.
func (rc *RenderContext) AddMeta(name, content string) {
	if rc == nil || name == "" {
		return
	}
	// Last write wins: overwrite an existing entry for the same
	// name. The inline scan is clamped to the inline capacity —
	// beyond it, entries live in the overflow slice.
	inlineN := rc.metaN
	if inlineN > metaInlineCap {
		inlineN = metaInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if rc.metaTags[i].name == name {
			rc.metaTags[i].content = content
			return
		}
	}
	for i := range rc.metaOverflow {
		if rc.metaOverflow[i].name == name {
			rc.metaOverflow[i].content = content
			return
		}
	}
	if rc.metaN < metaInlineCap {
		rc.metaTags[rc.metaN] = metaKV{name: name, content: content}
		rc.metaN++
		return
	}
	rc.metaOverflow = append(rc.metaOverflow, metaKV{name: name, content: content})
	rc.metaN++
}

// ClearHeadState resets the title, meta tags, and id sequence.
// Called on pool reuse; callers generally don't need it.
func (rc *RenderContext) ClearHeadState() {
	if rc == nil {
		return
	}
	rc.title = ""
	inlineN := rc.metaN
	if inlineN > metaInlineCap {
		inlineN = metaInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.metaTags[i] = metaKV{}
	}
	rc.metaOverflow = rc.metaOverflow[:0]
	rc.metaN = 0
	rc.idSeq = 0
}

// UseId returns a stable, unique identifier for this request,
// suitable for pairing <label for="x"> with <input id="x">.
// Returns "nano-1", "nano-2", … — the first 256 come from a
// precomputed static array (zero allocation); deeper requests
// fall back to strconv formatting.
//
// Note the difference from React: React's useId is stable per
// component instance across renders; ours is a per-request
// sequence — uniqueness (the property the label/input pairing
// needs) is what's guaranteed, and ids never repeat within a
// request. The sequence is reset per request (pool reuse).
func (rc *RenderContext) UseId() string {
	if rc == nil {
		return "nano-0"
	}
	rc.idSeq++
	if rc.idSeq <= len(precomputedIDs) {
		return precomputedIDs[rc.idSeq-1]
	}
	return "nano-" + strconv.Itoa(rc.idSeq)
}

// precomputedIDs holds the first 256 "nano-N" ids as static
// strings, so UseId returns a pointer to static data — no
// allocation on the fast path.
var precomputedIDs [256]string

func init() {
	for i := range precomputedIDs {
		precomputedIDs[i] = "nano-" + strconv.Itoa(i+1)
	}
}
