package render

import (
	"bytes"
	"html"
)

// assetInlineCap is the size of the zero-allocation inline asset
// arrays (one per kind: CSS, JS). 16 of each covers any
// realistic page; deeper sets spill to heap slices (same pattern
// as the cascading-context stack, meta tags, and form errors).
const assetInlineCap = 16

// nanoAssetsComponent is the built-in <NANO_ASSETS/> component
// (and the {{ nanoAssets }} FuncMap helper's writer). It emits
// the deduplicated CSS/JS dependencies collected during the
// render pass:
//
//	<link rel="stylesheet" href="...">
//	<script defer src="...">
//
// Layouts that want component-declared assets must include
// <NANO_ASSETS/> (or {{ nanoAssets }}) — typically next to
// <NANO_HEAD/> inside <head>. Explicit placement, no magic.
type nanoAssetsComponent struct{}

// Render implements Component.
func (n *nanoAssetsComponent) Render(w ByteWriter, rc *RenderContext, _ any) error {
	return writeAssetTags(w, rc)
}

// writeAssetTags emits the collected CSS and JS dependencies to
// w. Hrefs are HTML-escaped (paths can come from user data).
func writeAssetTags(w ByteWriter, rc *RenderContext) error {
	if rc == nil {
		return nil
	}
	inlineN := rc.cssN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if _, err := w.WriteString(`<link rel="stylesheet" href="` + html.EscapeString(rc.cssAssets[i]) + `">`); err != nil {
			return err
		}
	}
	for _, href := range rc.cssOverflow {
		if _, err := w.WriteString(`<link rel="stylesheet" href="` + html.EscapeString(href) + `">`); err != nil {
			return err
		}
	}
	inlineN = rc.jsN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if _, err := w.WriteString(`<script defer src="` + html.EscapeString(rc.jsAssets[i]) + `"></script>`); err != nil {
			return err
		}
	}
	for _, src := range rc.jsOverflow {
		if _, err := w.WriteString(`<script defer src="` + html.EscapeString(src) + `"></script>`); err != nil {
			return err
		}
	}
	return nil
}

// RequiresCSS registers a CSS dependency for this request. The
// path is emitted by <NANO_ASSETS/> as a <link rel="stylesheet">
// tag. Deduplicated: registering the same path twice emits it
// once, keeping the FIRST occurrence so emission order = first
// render order (deterministic — the executor walks the tree in
// order).
//
// Components rendered in loops (a card repeated 50 times)
// register their dependency once; the head gets one tag, and the
// browser downloads the asset once.
func (rc *RenderContext) RequiresCSS(href string) {
	if rc == nil || href == "" {
		return
	}
	// Linear scan — at ≤16 inline slots this is already optimal;
	// no bitset needed for string paths.
	inlineN := rc.cssN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if rc.cssAssets[i] == href {
			return
		}
	}
	for _, h := range rc.cssOverflow {
		if h == href {
			return
		}
	}
	if rc.cssN < assetInlineCap {
		rc.cssAssets[rc.cssN] = href
		rc.cssN++
		return
	}
	rc.cssOverflow = append(rc.cssOverflow, href)
	rc.cssN++
}

// RequiresJS registers a JS dependency for this request. The
// path is emitted by <NANO_ASSETS/> as a <script defer> tag.
// Deduplicated, first occurrence wins.
func (rc *RenderContext) RequiresJS(src string) {
	if rc == nil || src == "" {
		return
	}
	inlineN := rc.jsN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if rc.jsAssets[i] == src {
			return
		}
	}
	for _, s := range rc.jsOverflow {
		if s == src {
			return
		}
	}
	if rc.jsN < assetInlineCap {
		rc.jsAssets[rc.jsN] = src
		rc.jsN++
		return
	}
	rc.jsOverflow = append(rc.jsOverflow, src)
	rc.jsN++
}

// assetTagsString renders the collected asset tags into a
// string. Used by the {{ nanoAssets }} FuncMap helper (template
// engines), which must return a value — the plain-HTML
// <NANO_ASSETS/> path is the zero-alloc one.
func assetTagsString(rc *RenderContext) string {
	if rc == nil {
		return ""
	}
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	_ = writeAssetTags(bw, rc)
	_ = bw.Flush()
	return buf.String()
}

// ClearAssets resets the collected CSS/JS dependencies. Called
// on pool reuse; callers generally don't need it.
func (rc *RenderContext) ClearAssets() {
	if rc == nil {
		return
	}
	inlineN := rc.cssN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.cssAssets[i] = ""
	}
	rc.cssOverflow = rc.cssOverflow[:0]
	rc.cssN = 0
	inlineN = rc.jsN
	if inlineN > assetInlineCap {
		inlineN = assetInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.jsAssets[i] = ""
	}
	rc.jsOverflow = rc.jsOverflow[:0]
	rc.jsN = 0
}
