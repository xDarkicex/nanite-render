package render

import (
	"bytes"
	"html"
	"io"
	"unsafe"
)

// Execute renders the program to w. When the program carries a
// compiled Bytecode (see CompileBytecode), the flat instruction
// stream is executed — static HTML is coalesced into a few writes,
// matching templ's compile-to-output speed. Otherwise the NodeStream
// is walked depth-first via the FirstChild/NextSibling links
// (linear-time, zero-alloc for hand-built streams).
func Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error {
	if p == nil {
		return errNilProgram
	}
	if p.Bytecode != nil {
		return p.Bytecode.Exec(w, rc, data)
	}
	if p.Nodes.Count == 0 {
		return nil
	}
	emitNode(p, p.Nodes, 0, w, rc, data)
	return nil
}

// walkChildren visits every direct child of node i via sibling links.
func walkChildren(p *Program, nodes NodeStream, i int, w ByteWriter, rc *RenderContext, data any) {
	for c := nodes.FirstChildOf(i); c != -1; c = nodes.NextSiblingOf(c) {
		emitNode(p, nodes, c, w, rc, data)
	}
}

// emitNode writes a single node to w, recursing into its children via
// the sibling links. O(subtree).
func emitNode(p *Program, nodes NodeStream, i int, w ByteWriter, rc *RenderContext, data any) {
	flags := nodes.Flags[i]
	// Text node.
	if flags&FlagText != 0 {
		txt := nodes.Text(i)
		if len(txt) == 0 {
			return
		}
		if flags&FlagRaw != 0 {
			_, _ = w.Write(txt)
		} else {
			_ = escapeBytes(w, txt)
		}
		return
	}
	// Component dispatch.
	if flags&FlagComponent != 0 {
		_ = emitComponent(w, rc, nodes, i, data)
		return
	}
	tag := nodes.Tag[i]
	if flags&FlagVoid != 0 || flags&FlagSelfClosing != 0 {
		_ = emitOpen(w, tag, nodes, i, true)
		return
	}
	// Fragment nodes emit no tag — just walk children.
	if flags&FlagFragment != 0 {
		walkChildren(p, nodes, i, w, rc, data)
		return
	}
	// Regular open/close.
	_ = emitOpen(w, tag, nodes, i, false)
	walkChildren(p, nodes, i, w, rc, data)
	_ = writeTag(w, "</", tag, ">")
}

// writeTag batches pre+tag+post into a single write using the pooled
// writer's own head scratch buffer (already heap-resident, so no new
// allocation) and unsafe.String. For non-byteWriter writers, or tags
// wider than the scratch, it falls back to three writes.
func writeTag(w ByteWriter, pre, tag, post string) error {
	n := len(pre) + len(tag) + len(post)
	if bw, ok := w.(*byteWriter); ok && n <= len(bw.head) {
		h := bw.head[:0]
		h = append(h, pre...)
		h = append(h, tag...)
		h = append(h, post...)
		_, err := bw.WriteString(unsafe.String(&bw.head[0], n))
		return err
	}
	if _, err := w.WriteString(pre); err != nil {
		return err
	}
	if _, err := w.WriteString(tag); err != nil {
		return err
	}
	_, err := w.WriteString(post)
	return err
}

// emitOpen writes the opening tag (with attributes) for node i.
func emitOpen(w ByteWriter, tag string, nodes NodeStream, i int, self bool) error {
	// Fast path: no attributes → single batched write.
	if nodes.NumAttr(i) == 0 {
		if self {
			return writeTag(w, "<", tag, "/>")
		}
		return writeTag(w, "<", tag, ">")
	}
	// Slow path: attributes. Write per-piece (values need escaping).
	if _, err := w.WriteString("<"); err != nil {
		return err
	}
	if _, err := w.WriteString(tag); err != nil {
		return err
	}
	n := nodes.NumAttr(i)
	for k := range n {
		key := nodes.AttrKey(i, k)
		if _, err := w.WriteString(" "); err != nil {
			return err
		}
		if _, err := w.WriteString(key); err != nil {
			return err
		}
		val := nodes.AttrVal(i, k)
		if val == nil {
			continue
		}
		if _, err := w.WriteString("=\""); err != nil {
			return err
		}
		if s, ok := val.(string); ok {
			if _, err := w.WriteString(html.EscapeString(s)); err != nil {
				return err
			}
		} else {
			if _, err := w.WriteString(html.EscapeString(stringify(val))); err != nil {
				return err
			}
		}
		if _, err := w.WriteString("\""); err != nil {
			return err
		}
	}
	if self {
		_, err := w.WriteString("/>")
		return err
	}
	_, err := w.WriteString(">")
	return err
}

// emitComponent dispatches a component node to the registered
// Component. The component receives pre-rendered children (nested
// nodes from the same parent). If the component implements
// ComponentWithChildren, it's called directly; otherwise the
// children are passed via the data map.
func emitComponent(w ByteWriter, rc *RenderContext, nodes NodeStream, i int, data any) error {
	name := nodes.ComponentName[i]
	if name == "" {
		return nil
	}
	cr := rc.ComponentRegistry()
	if cr == nil {
		return nil
	}
	c, ok := cr.Lookup(name)
	if !ok {
		_, err := io.WriteString(w, "<!-- missing component: "+name+" -->")
		return err
	}
	// Collect children (nodes whose Parent == i) and pre-render them.
	children := collectChildHTML(rc, nodes, i, data)
	// Collect slots (children with FlagSlot set) and stash on the
	// data map. Components that need slots read data[SlotsDataKey].
	slots := collectSlots(rc, nodes, i, data)
	if len(slots) > 0 {
		if data == nil {
			data = make(map[string]any)
		}
		if m, ok := data.(map[string]any); ok {
			m[SlotsDataKey] = slots
		} else {
			// Wrap non-map data.
			wrapped := map[string]any{"_data": data, SlotsDataKey: slots}
			data = wrapped
		}
	}
	return renderComponent(c, w, rc, data, children)
}

// collectChildHTML walks the child nodes of node i, renders each
// to a string, and returns the list. Used to pass children to
// components for composition.
//
// If the parent has FlagSlot children (slots), they are grouped
// into a Slots map instead. The map is stored at data[SlotsDataKey]
// when the dispatcher's renderComponent is called.
func collectChildHTML(rc *RenderContext, nodes NodeStream, parentIdx int, data any) Children {
	// Count children first (sibling links) to size the slice once.
	childCount := 0
	for c := nodes.FirstChildOf(parentIdx); c != -1; c = nodes.NextSiblingOf(c) {
		childCount++
	}
	if childCount == 0 {
		return nil
	}
	out := make(Children, 0, childCount)
	for c := nodes.FirstChildOf(parentIdx); c != -1; c = nodes.NextSiblingOf(c) {
		// Render this child's subtree to a string.
		var buf bytes.Buffer
		bw := AcquireWriter(&buf)
		if err := walkRender(rc, nodes, c, bw, data); err != nil {
			ReleaseWriter(bw)
			continue
		}
		_ = bw.Flush()
		out = append(out, buf.String())
		ReleaseWriter(bw)
	}
	return out
}

// collectSlots walks the children of a component node and groups
// them by slot name. A child is a slot if it has FlagSlot set (and
// the tag matches a slot marker). Children without a slot are
// stored under the empty-key slot ("default content").
func collectSlots(rc *RenderContext, nodes NodeStream, parentIdx int, data any) Slots {
	slots := Slots{}
	for c := nodes.FirstChildOf(parentIdx); c != -1; c = nodes.NextSiblingOf(c) {
		// The slot name is in ComponentName (we reuse the slot
		// marker field for the slot name). Empty slot name → default.
		name := nodes.ComponentName[c]
		if name == "" {
			name = "default"
		}
		// Render this child's subtree.
		var buf bytes.Buffer
		bw := AcquireWriter(&buf)
		if err := walkRender(rc, nodes, c, bw, data); err != nil {
			ReleaseWriter(bw)
			continue
		}
		_ = bw.Flush()
		// Concatenate with existing content (in case multiple
		// children share a slot name).
		slots[name] = slots[name] + buf.String()
		ReleaseWriter(bw)
	}
	return slots
}

// walkRender walks a node subtree and writes to the given writer.
// Like emitNode but for a buffer-bound writer.
//
// Slot nodes (non-empty ComponentName, not a component) emit only
// their children — the slot wrapper tag is invisible.
func walkRender(rc *RenderContext, nodes NodeStream, rootIdx int, w ByteWriter, data any) error {
	flags := nodes.Flags[rootIdx]
	if flags&FlagText != 0 {
		txt := nodes.Text(rootIdx)
		if len(txt) == 0 {
			return nil
		}
		if flags&FlagRaw != 0 {
			_, err := w.Write(txt)
			return err
		}
		return escapeBytes(w, txt)
	}
	// Component
	if flags&FlagComponent != 0 {
		return emitComponent(w, rc, nodes, rootIdx, data)
	}
	// Slot node: emit only children (no wrapping tag).
	// Identified by a non-empty ComponentName without FlagComponent.
	if nodes.ComponentName[rootIdx] != "" {
		for c := nodes.FirstChildOf(rootIdx); c != -1; c = nodes.NextSiblingOf(c) {
			if err := walkRender(rc, nodes, c, w, data); err != nil {
				return err
			}
		}
		return nil
	}
	// Self-closing or void
	if flags&(FlagVoid|FlagSelfClosing) != 0 {
		tag := nodes.Tag[rootIdx]
		return emitOpen(w, tag, nodes, rootIdx, true)
	}
	// Fragment or regular — emit tag, walk children, close tag
	tag := nodes.Tag[rootIdx]
	isFragment := flags&FlagFragment != 0
	if !isFragment {
		if err := emitOpen(w, tag, nodes, rootIdx, false); err != nil {
			return err
		}
	}
	for c := nodes.FirstChildOf(rootIdx); c != -1; c = nodes.NextSiblingOf(c) {
		if err := walkRender(rc, nodes, c, w, data); err != nil {
			return err
		}
	}
	if !isFragment {
		if _, err := io.WriteString(w, "</"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, tag); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ">"); err != nil {
			return err
		}
	}
	return nil
}

// escapeBytes writes b to w, HTML-escaping &, <, >, ", '. Zero-alloc:
// no []byte→string conversion, no intermediate buffers. Plain runs are
// written directly via w.Write; entities are written inline.
func escapeBytes(w ByteWriter, b []byte) error {
	start := 0
	for i, c := range b {
		var ent string
		switch c {
		case '&':
			ent = "&amp;"
		case '<':
			ent = "&lt;"
		case '>':
			ent = "&gt;"
		case '"':
			ent = "&#34;"
		case '\'':
			ent = "&#39;"
		default:
			continue
		}
		if i > start {
			if _, err := w.Write(b[start:i]); err != nil {
				return err
			}
		}
		if _, err := w.WriteString(ent); err != nil {
			return err
		}
		start = i + 1
	}
	if start < len(b) {
		_, err := w.Write(b[start:])
		return err
	}
	return nil
}

// stringify returns a string representation of an attribute value
// suitable for HTML emission.
func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}

var errNilProgram = &ParseError{Msg: "Execute: nil program"}
