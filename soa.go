package render

// NodeStream is the SoA representation of a parsed template. Each "node"
// is just an index into the parallel slices. Walking children is walking
// (start, end) ranges into the same stream, which keeps the working set
// inside L1 cache (target: cc < 10 on the hot loop).
//
// Field shapes (bytes per node, approximate):
//
//	Tag          16    string header
//	Flags         1    bitmap
//	NS            2    namespace id
//	ChildStart    4    first child index
//	ChildEnd      4    one-past-last child index
//	AttrStart     4    first attr pair index
//	AttrEnd       4    one-past-last attr pair index
//	TextOff       4    [start, end) into the shared textBuf
//	ComponentName 16   name key for component dispatch (when flag set)
type NodeStream struct {
	Tag        []string
	Flags      []uint8
	NS         []uint16
	ChildStart []uint32
	ChildEnd   []uint32
	AttrStart  []uint32
	AttrEnd    []uint32
	TextOff    []uint32

	// Parent[i] is the parent node index of node i. -1 means root.
	Parent []int32

	// FirstChild[i] is the first direct child index of node i, or -1
	// if i is a leaf. NextSibling[i] is the next sibling of i (same
	// parent), or -1. These sibling links let the executor walk a tree
	// in O(n) — direct children are NOT contiguous in the stream
	// (grandchildren interleave), so a flat child range can't
	// represent them. Built once at compile time by
	// NodeStreamBuilder.Stream().
	FirstChild  []int32
	NextSibling []int32

	// ComponentName[i] is the name of a registered component when
	// Flags[i]&FlagComponent != 0. The executor dispatches to
	// ComponentRegistry.Lookup(ComponentName[i]).
	ComponentName []string

	// Shared attribute key/value tables. attrKeys/attrVals are indexed
	// by the (AttrStart, AttrEnd) ranges above. Component props can be
	// a *Program reference.
	AttrKeys []string
	AttrVals []any

	// Shared text buffer backing every TextOff range.
	textBuf []byte

	// Count is the number of nodes currently in the stream. Equal to
	// len(Tag) == len(Flags) == … when the stream is well-formed.
	Count int
}

// Node flags. Stuffed in a uint8 so the Flags slice is one byte per node.
const (
	FlagVoid        uint8 = 1 << 0 // <br>, <img>, <input>
	FlagSelfClosing uint8 = 1 << 1 // <MyComponent/>
	FlagComponent   uint8 = 1 << 2 // templ component
	FlagFragment    uint8 = 1 << 3 // <template> / no parent
	FlagRaw         uint8 = 1 << 4 // text not escaped
	FlagText        uint8 = 1 << 5 // synthetic text node
	FlagDynamicTag  uint8 = 1 << 6 // <{tag}>, tag chosen at render
	FlagHasChildren uint8 = 1 << 7 // hot-loop guard for child walk
)

// Slots is a map of slot name to children content. Used for
// multi-slot composition (e.g. Card with header, body, footer).
type Slots map[string]string

// SlotsDataKey is the data-map key where the dispatcher stores
// the slots map. Components access this via data[SlotsDataKey].
const SlotsDataKey = "__slots__"

// Slot is the flag we add to a node that's a slot marker. We use
// a separate bit from FlagVoid so slot nodes can be distinguished
// in walkRender. (A slot is a container, not a void element.)
// Note: we use FlagSlot for clarity; bit-position is 1 << 0
// but the dispatch path checks for it explicitly.

// To avoid the bit overlap, slots are stored via a different
// convention: a node with a non-empty ComponentName but not FlagComponent
// is treated as a slot. The walkRender skips over them; collectSlots
// picks them up by scanning the children.

// Reset clears the stream without releasing backing storage. After Reset
// the stream is reusable for a new program.
func (s *NodeStream) Reset() {
	if s == nil {
		return
	}
	s.Tag = s.Tag[:0]
	s.Flags = s.Flags[:0]
	s.NS = s.NS[:0]
	s.ChildStart = s.ChildStart[:0]
	s.ChildEnd = s.ChildEnd[:0]
	s.AttrStart = s.AttrStart[:0]
	s.AttrEnd = s.AttrEnd[:0]
	s.TextOff = s.TextOff[:0]
	s.Parent = s.Parent[:0]
	s.FirstChild = s.FirstChild[:0]
	s.NextSibling = s.NextSibling[:0]
	s.ComponentName = s.ComponentName[:0]
	s.AttrKeys = s.AttrKeys[:0]
	s.AttrVals = s.AttrVals[:0]
	s.textBuf = s.textBuf[:0]
	s.Count = 0
}

// IsVoid reports whether the node is a known void element that has no
// closing tag.
func (s *NodeStream) IsVoid(i int) bool {
	return s.Flags[i]&FlagVoid != 0
}

// IsComponent reports whether the node is a templ component.
func (s *NodeStream) IsComponent(i int) bool {
	return s.Flags[i]&FlagComponent != 0
}

// IsText reports whether the node is a synthetic text node.
func (s *NodeStream) IsText(i int) bool {
	return s.Flags[i]&FlagText != 0
}

// Text returns the text slice for a text node. Returns nil if the node
// is not a text node or has no text.
func (s *NodeStream) Text(i int) []byte {
	if i >= len(s.TextOff)/2 {
		return nil
	}
	lo := s.TextOff[2*i]
	hi := s.TextOff[2*i+1]
	return s.textBuf[lo:hi]
}

// NumAttr returns the number of attribute pairs for node i.
func (s *NodeStream) NumAttr(i int) int {
	if s.AttrEnd[i] <= s.AttrStart[i] {
		return 0
	}
	return int(s.AttrEnd[i] - s.AttrStart[i])
}

// AttrKey returns the attribute key for the k-th attribute of node i.
func (s *NodeStream) AttrKey(i, k int) string {
	idx := int(s.AttrStart[i]) + k
	return s.AttrKeys[idx]
}

// AttrVal returns the attribute value for the k-th attribute of node i.
func (s *NodeStream) AttrVal(i, k int) any {
	idx := int(s.AttrStart[i]) + k
	return s.AttrVals[idx]
}

// ChildRange returns the [start, end) child index range for node i.
func (s *NodeStream) ChildRange(i int) (uint32, uint32) {
	return s.ChildStart[i], s.ChildEnd[i]
}

// voidTags is the set of HTML void elements that never have a closing
// tag. Engines populate this on demand.
var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "source": true, "track": true, "wbr": true,
}

// IsVoidTag reports whether the tag is a known void element.
func IsVoidTag(tag string) bool { return voidTags[tag] }

// NodeStreamBuilder appends nodes to a NodeStream with growth on demand.
// The builder is stateful: call Reset to reuse. Slices grow geometrically
// to keep amortised allocation cost constant.
type NodeStreamBuilder struct {
	stream NodeStream
}

// NewBuilder returns an empty builder.
func NewBuilder() *NodeStreamBuilder {
	return &NodeStreamBuilder{}
}

// Reserve pre-allocates the builder's internal slices with the given
// expected node count. Use this before parsing to avoid the
// geometric-growth reallocations as nodes are appended. Returns the
// builder for chaining.
func (b *NodeStreamBuilder) Reserve(approxNodes int) *NodeStreamBuilder {
	if approxNodes < 16 {
		approxNodes = 16
	}
	attrCap := approxNodes + approxNodes/2
	b.stream.Tag = make([]string, 0, approxNodes)
	b.stream.Flags = make([]uint8, 0, approxNodes)
	b.stream.NS = make([]uint16, 0, approxNodes)
	b.stream.ChildStart = make([]uint32, 0, approxNodes)
	b.stream.ChildEnd = make([]uint32, 0, approxNodes)
	b.stream.AttrStart = make([]uint32, 0, approxNodes)
	b.stream.AttrEnd = make([]uint32, 0, approxNodes)
	b.stream.TextOff = make([]uint32, 0, approxNodes*2)
	b.stream.Parent = make([]int32, 0, approxNodes)
	b.stream.FirstChild = make([]int32, 0, approxNodes)
	b.stream.NextSibling = make([]int32, 0, approxNodes)
	b.stream.ComponentName = make([]string, 0, approxNodes)
	b.stream.AttrKeys = make([]string, 0, attrCap)
	b.stream.AttrVals = make([]any, 0, attrCap)
	return b
}

// ReserveText estimates the textBuf capacity based on the source
// size. The text content of an HTML document is typically 40-60% of
// the source bytes. Pre-allocating avoids the growth reallocations
// as text runs are appended.
func (b *NodeStreamBuilder) ReserveText(srcSize int) *NodeStreamBuilder {
	cap := srcSize / 2
	if cap < 64 {
		cap = 64
	}
	b.stream.textBuf = make([]byte, 0, cap)
	return b
}

// Stream returns the underlying stream, finalising the sibling links.
// The builder is consumed after Stream; further appends are invalid.
func (b *NodeStreamBuilder) Stream() NodeStream {
	b.finalizeChildren()
	return b.stream
}

// finalizeChildren builds the FirstChild/NextSibling links from the
// Parent array in O(n). Children appear in document order (increasing
// index) and a parent is always a lower index than its children, so a
// single forward pass links them. Called once when Stream() is invoked.
func (b *NodeStreamBuilder) finalizeChildren() {
	n := b.stream.Count
	if n == 0 {
		return
	}
	// last[p] = the most recently appended child of p, or -1.
	last := make([]int32, n)
	for i := range last {
		last[i] = -1
		b.stream.FirstChild[i] = -1
		b.stream.NextSibling[i] = -1
	}
	for i := 1; i < n; i++ {
		p := b.stream.Parent[i]
		if p < 0 {
			continue
		}
		if last[p] == -1 {
			b.stream.FirstChild[p] = int32(i)
		} else {
			b.stream.NextSibling[last[p]] = int32(i)
		}
		last[p] = int32(i)
	}
}

// FirstChildOf returns the first direct child index of node i, or -1.
func (s *NodeStream) FirstChildOf(i int) int {
	return int(s.FirstChild[i])
}

// NextSiblingOf returns the next sibling index of node i, or -1.
func (s *NodeStream) NextSiblingOf(i int) int {
	return int(s.NextSibling[i])
}

// Reset re-initialises the builder for reuse.
func (b *NodeStreamBuilder) Reset() {
	b.stream.Reset()
}

// AppendNode appends a new node with the given tag and flags. Returns
// the new node's index.
func (b *NodeStreamBuilder) AppendNode(tag string, flags uint8) int {
	i := len(b.stream.Tag)
	b.stream.Tag = append(b.stream.Tag, tag)
	b.stream.Flags = append(b.stream.Flags, flags)
	b.stream.NS = append(b.stream.NS, 0)
	b.stream.ChildStart = append(b.stream.ChildStart, 0)
	b.stream.ChildEnd = append(b.stream.ChildEnd, 0)
	b.stream.AttrStart = append(b.stream.AttrStart, 0)
	b.stream.AttrEnd = append(b.stream.AttrEnd, 0)
	b.stream.TextOff = append(b.stream.TextOff, 0, 0)
	b.stream.Parent = append(b.stream.Parent, -1)
	b.stream.FirstChild = append(b.stream.FirstChild, -1)
	b.stream.NextSibling = append(b.stream.NextSibling, -1)
	b.stream.ComponentName = append(b.stream.ComponentName, "")
	b.stream.Count++
	return i
}

// SetParent sets the parent node index for the given node.
func (b *NodeStreamBuilder) SetParent(child, parent int) {
	b.stream.Parent[child] = int32(parent)
}

// AppendComponent appends a component node that dispatches to the
// named component at render time. The component must be registered
// via Registry.AttachComponents before render.
func (b *NodeStreamBuilder) AppendComponent(name string, attrs []string, vals []any) int {
	i := b.AppendNode(name, FlagComponent)
	if len(attrs) > 0 {
		b.SetAttrs(i, attrs, vals)
	}
	b.stream.ComponentName[i] = name
	return i
}

// AppendText appends a synthetic text node. The text is copied into the
// shared textBuf. Returns the new node's index.
func (b *NodeStreamBuilder) AppendText(text []byte, raw bool) int {
	flags := FlagText
	if raw {
		flags |= FlagRaw
	}
	i := b.AppendNode("#text", flags)
	lo := uint32(len(b.stream.textBuf))
	b.stream.textBuf = append(b.stream.textBuf, text...)
	hi := uint32(len(b.stream.textBuf))
	b.stream.TextOff[2*i] = lo
	b.stream.TextOff[2*i+1] = hi
	return i
}

// SetChildren sets the child range for node i.
func (b *NodeStreamBuilder) SetChildren(i int, start, end uint32) {
	b.stream.ChildStart[i] = start
	b.stream.ChildEnd[i] = end
	if end > start {
		b.stream.Flags[i] |= FlagHasChildren
	}
}

// SetAttrs attaches the attribute key/value pairs to node i. The slices
// passed to this call are appended once (no per-node copying).
func (b *NodeStreamBuilder) SetAttrs(i int, keys []string, vals []any) {
	if len(keys) != len(vals) {
		panic("SetAttrs: keys/vals length mismatch")
	}
	start := uint32(len(b.stream.AttrKeys))
	b.stream.AttrKeys = append(b.stream.AttrKeys, keys...)
	b.stream.AttrVals = append(b.stream.AttrVals, vals...)
	end := uint32(len(b.stream.AttrKeys))
	b.stream.AttrStart[i] = start
	b.stream.AttrEnd[i] = end
}

// SetAttrsEach appends (key, value) pairs to the builder's attr
// tables and records the [start, end) range on node i. It avoids
// the two make() calls that SetAttrs requires; the parser uses
// this with a pooled working slice.
func (b *NodeStreamBuilder) SetAttrsEach(i int, attrs []AttrKV) {
	if len(attrs) == 0 {
		return
	}
	start := uint32(len(b.stream.AttrKeys))
	for _, a := range attrs {
		b.stream.AttrKeys = append(b.stream.AttrKeys, a.K)
		b.stream.AttrVals = append(b.stream.AttrVals, a.V)
	}
	end := uint32(len(b.stream.AttrKeys))
	b.stream.AttrStart[i] = start
	b.stream.AttrEnd[i] = end
}

// AttrKV is a (key, value) pair used during parsing. Exported so
// the parser can pass a slice of these to SetAttrsEach without
// needing intermediate []string / []any slices.
type AttrKV struct {
	K, V string
}
