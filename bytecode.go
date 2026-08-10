package render

import (
	"bytes"
	"io"
)

// Bytecode is a flat, zero-allocation instruction stream compiled
// from a NodeStream at compile time.
//
// Static HTML (tags, attributes, text, and HTML escaping) is coalesced
// into a single output buffer at compile time — exactly what templ's
// compiler does — so a static-heavy template renders as a handful of
// OP_STATIC writes instead of a per-node tree walk. The render loop is
// a tight switch over a []uint64, maximising cache-line efficiency and
// branch predictability.
//
// Dynamic points (components, slots) remain explicit instructions, so
// the bytecode supports the full composition layer (state, children,
// slots, memoization) on top of the same ComponentContext API.
type Bytecode struct {
	// code is the flat instruction stream. Each uint64 packs:
	//   bits 0-7   op
	//   bits 8-39  arg1 (uint32)
	//   bits 40-63 arg2 (uint32)
	code []uint64

	// static is the compile-time-coalesced output buffer. Escaping is
	// applied here, so the runtime writes raw bytes.
	static []byte

	// comps holds component names and slot names, indexed by arg1.
	comps []string

	// pending static run: consecutive static appends coalesce into a
	// single opStatic instruction, flushed before any dynamic op.
	stStart uint32
	stLen   uint32
}

// bytecode opcodes.
const (
	opStatic uint8 = iota // write static[arg1 : arg1+arg2]
	opComponent           // dispatch component comps[arg1]; children in (i, arg2)
	opSlotBegin           // begin slot comps[arg1] (inside a component's children)
	opSlotEnd             // end slot
)

// inst packs op, a, b into one uint64.
func inst(op uint8, a, b uint32) uint64 {
	return uint64(op) | (uint64(a) << 8) | (uint64(b) << 40)
}

// CompileBytecode compiles a NodeStream into a flat instruction
// stream. Static content is escaped and coalesced into a single
// output buffer; consecutive static runs emit one opStatic each.
// Components and slots remain dynamic instructions.
func CompileBytecode(nodes NodeStream) *Bytecode {
	bc := &Bytecode{}
	if nodes.Count > 0 {
		bc.emit(nodes, 0)
	}
	bc.flushStatic()
	return bc
}

// addComp interns a component/slot name, returning its index.
func (bc *Bytecode) addComp(name string) uint32 {
	for i, c := range bc.comps {
		if c == name {
			return uint32(i)
		}
	}
	bc.comps = append(bc.comps, name)
	return uint32(len(bc.comps) - 1)
}

// appendStatic appends to the pending static run (coalesced).
func (bc *Bytecode) appendStatic(b []byte) {
	if len(b) == 0 {
		return
	}
	bc.static = append(bc.static, b...)
	bc.stLen += uint32(len(b))
}

// flushStatic emits one opStatic for the pending run.
func (bc *Bytecode) flushStatic() {
	if bc.stLen == 0 {
		return
	}
	bc.code = append(bc.code, inst(opStatic, bc.stStart, bc.stLen))
	bc.stStart += bc.stLen
	bc.stLen = 0
}

// dynamic flushes any pending static run, then appends a dynamic op.
func (bc *Bytecode) dynamic(op uint8, a, b uint32) {
	bc.flushStatic()
	bc.code = append(bc.code, inst(op, a, b))
}

// emit appends instructions for node i's subtree.
func (bc *Bytecode) emit(nodes NodeStream, i int) {
	flags := nodes.Flags[i]
	switch {
	case flags&FlagText != 0:
		txt := nodes.Text(i)
		if len(txt) == 0 {
			return
		}
		if flags&FlagRaw != 0 {
			bc.appendStatic(txt)
		} else {
			before := len(bc.static)
			bc.static = escapeAppend(bc.static, txt)
			bc.stLen += uint32(len(bc.static) - before)
		}
	case flags&FlagComponent != 0:
		ci := bc.addComp(nodes.ComponentName[i])
		// dynamic may flush a pending static run FIRST, so the
		// component instruction is the one just appended — record
		// its index AFTER the flush.
		bc.dynamic(opComponent, ci, 0)
		start := len(bc.code) - 1
		bc.emitChildren(nodes, i)
		// Backpatch the children end index (exclusive).
		bc.code[start] = inst(opComponent, ci, uint32(len(bc.code)))
	case flags&(FlagVoid|FlagSelfClosing) != 0:
		bc.appendOpenTag(nodes, i, true)
	case flags&FlagFragment != 0:
		bc.emitChildren(nodes, i)
	default:
		bc.appendOpenTag(nodes, i, false)
		bc.emitChildren(nodes, i)
		before := len(bc.static)
		bc.static = append(bc.static, "</"...)
		bc.static = append(bc.static, nodes.Tag[i]...)
		bc.static = append(bc.static, '>')
		bc.stLen += uint32(len(bc.static) - before)
	}
}

// emitChildren appends instructions for node i's direct children.
// Slot children are wrapped in opSlotBegin/opSlotEnd markers.
func (bc *Bytecode) emitChildren(nodes NodeStream, i int) {
	for c := nodes.FirstChildOf(i); c != -1; c = nodes.NextSiblingOf(c) {
		if nodes.ComponentName[c] != "" && nodes.Flags[c]&FlagComponent == 0 {
			// Slot wrapper: mark the region with its name.
			si := bc.addComp(nodes.ComponentName[c])
			bc.dynamic(opSlotBegin, si, 0)
			for gc := nodes.FirstChildOf(c); gc != -1; gc = nodes.NextSiblingOf(gc) {
				bc.emit(nodes, gc)
			}
			bc.dynamic(opSlotEnd, 0, 0)
			continue
		}
		bc.emit(nodes, c)
	}
}

// appendOpenTag renders `<tag attr="val"...>` (escaped) into the
// pending static run. Length accounting uses before/after deltas so
// escaped attribute values (which can grow) count correctly.
func (bc *Bytecode) appendOpenTag(nodes NodeStream, i int, self bool) {
	before := len(bc.static)
	bc.static = append(bc.static, '<')
	bc.static = append(bc.static, nodes.Tag[i]...)
	n := nodes.NumAttr(i)
	for k := range n {
		bc.static = append(bc.static, ' ')
		bc.static = append(bc.static, nodes.AttrKey(i, k)...)
		val := nodes.AttrVal(i, k)
		if val == nil {
			continue
		}
		bc.static = append(bc.static, '=', '"')
		bc.static = escapeAppend(bc.static, []byte(stringify(val)))
		bc.static = append(bc.static, '"')
	}
	if self {
		bc.static = append(bc.static, '/', '>')
	} else {
		bc.static = append(bc.static, '>')
	}
	bc.stLen += uint32(len(bc.static) - before)
}

// escapeAppend appends src to dst, HTML-escaping &, <, >, ", '.
func escapeAppend(dst, src []byte) []byte {
	for _, c := range src {
		switch c {
		case '&':
			dst = append(dst, "&amp;"...)
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		case '"':
			dst = append(dst, "&#34;"...)
		case '\'':
			dst = append(dst, "&#39;"...)
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

// ---------------------------------------------------------------------------
// Executor
// ---------------------------------------------------------------------------

// Exec runs the bytecode, writing the rendered output to w.
func (bc *Bytecode) Exec(w ByteWriter, rc *RenderContext, data any) error {
	if bc == nil {
		return nil
	}
	vm := bcVM{bc: bc, rc: rc, data: data}
	return vm.run(0, len(bc.code), w)
}

// bcVM executes a Bytecode. One instance per render (stack value).
type bcVM struct {
	bc   *Bytecode
	rc   *RenderContext
	data any
}

// run executes instructions [start, end), writing to w. Slot markers
// are ignored outside a component's children region.
func (vm *bcVM) run(start, end int, w ByteWriter) error {
	bc := vm.bc
	for i := start; i < end; i++ {
		ins := bc.code[i]
		switch uint8(ins) {
		case opStatic:
			a, b := uint32(ins>>8), uint32(ins>>40)
			if _, err := w.Write(bc.static[a : a+b]); err != nil {
				return err
			}
		case opComponent:
			a, b := uint32(ins>>8), uint32(ins>>40)
			// Pre-render children (if any) and dispatch.
			children, slots := vm.collectChildren(i+1, int(b))
			if err := vm.dispatch(bc.comps[a], w, children, slots); err != nil {
				return err
			}
			i = int(b) - 1
		}
	}
	return nil
}

// dispatch looks up a component and renders it, mirroring the tree
// executor's emitComponent. Children and slots are passed through
// the same renderComponent path.
func (vm *bcVM) dispatch(name string, w ByteWriter, children Children, slots Slots) error {
	if vm.rc == nil {
		return nil
	}
	cr := vm.rc.ComponentRegistry()
	if cr == nil {
		return nil
	}
	c, ok := cr.Lookup(name)
	if !ok {
		// Mirror the tree executor: placeholder for unregistered
		// components.
		_, err := io.WriteString(w, "<!-- missing component: "+name+" -->")
		return err
	}
	// Async dispatch: same as the tree executor's emitComponent —
	// write fallback inline and run the render in a worker. The
	// bytecode path is the default for plain HTML, so this is where
	// most Async components will actually be hit.
	if ao, ok := c.(AsyncOptioner); ok {
		id, fn := ao.AsyncFallback()
		if id != "" && fn != nil {
			return emitAsyncComponent(w, vm.rc, c, id, fn, vm.data)
		}
	}
	data := vm.data
	if len(slots) > 0 {
		if data == nil {
			data = make(map[string]any)
		}
		if m, ok := data.(map[string]any); ok {
			m[SlotsDataKey] = slots
		} else {
			data = map[string]any{"_data": data, SlotsDataKey: slots}
		}
	}
	return renderComponent(c, w, vm.rc, data, children)
}

// collectChildren renders instructions [start, end) into per-slot
// buffers, returning plain children and the slots map. Used when a
// component has children; nested components render inline.
func (vm *bcVM) collectChildren(start, end int) (Children, Slots) {
	var children Children
	var slots Slots
	var cur bytes.Buffer
	var curName string
	flush := func() {
		if cur.Len() == 0 {
			curName = ""
			return
		}
		if curName == "" {
			children = append(children, cur.String())
		} else {
			if slots == nil {
				slots = Slots{}
			}
			slots[curName] = slots[curName] + cur.String()
		}
		cur.Reset()
		curName = ""
	}
	bc := vm.bc
	for i := start; i < end; i++ {
		ins := bc.code[i]
		switch uint8(ins) {
		case opStatic:
			a, b := uint32(ins>>8), uint32(ins>>40)
			cur.Write(bc.static[a : a+b])
		case opComponent:
			_, b := uint32(ins>>8), uint32(ins>>40)
			// Nested component inside children: render its subtree
			// into the current buffer.
			bw := AcquireWriter(&cur)
			_ = vm.run(i+1, int(b), bw)
			_ = bw.Flush()
			ReleaseWriter(bw)
			i = int(b) - 1
		case opSlotBegin:
			flush()
			curName = bc.comps[uint32(ins>>8)]
		case opSlotEnd:
			flush()
		}
	}
	flush()
	return children, slots
}
