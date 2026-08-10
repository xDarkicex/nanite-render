package render

import (
	"bytes"
	"sync"
	"unsafe"
)

// commentEnd is the byte sequence that closes an HTML comment.
var commentEnd = []byte("-->")

// attrsPool reuses the working slice for the current tag's attributes.
// The slice grows geometrically so the pool's capacity stabilises.
var attrsPool = sync.Pool{
	New: func() any {
		s := make([]AttrKV, 0, 8)
		return &s
	},
}

// ParseHTML is the exported HTML parser. Returns a NodeStreamBuilder
// populated from src.
//
// Contract: the returned Program's string fields (Tag, AttrKeys,
// AttrVals, ComponentName) reference bytes inside src. The caller MUST
// keep src alive for as long as the Program is in use. The Go GC
// will keep src alive transitively (the string pointer fields are
// scanned), so this is safe under normal usage patterns — but if
// the caller releases src, the strings become dangling.
//
// Engine adapters that wrap the source in a long-lived cache
// (the typical case) satisfy this contract automatically.
func ParseHTML(src []byte, name string) (*NodeStreamBuilder, error) {
	return parseHTML(src, name)
}

// parseHTML is a minimal HTML parser that builds a NodeStream.
//
// The builder's slices are pre-sized via Reserve so the parse
// produces zero growth-reallocations on typical inputs. All string
// fields reference src via unsafe.String, so the parser itself
// performs no string copies. The textBuf is reserved too; text runs
// append without reallocation.
// It supports:
//   - Opening tags: <tag>
//   - Closing tags: </tag>
//   - Self-closing tags: <tag/> and <br>
//   - Attributes: name="value", name='value', name=val, name
//   - Text content
//   - Component tags: any uppercase tag name (e.g. <NAVBAR data="x"/>)
//     is emitted with FlagComponent + ComponentName set.
//
// The hot loops use bytes.IndexByte / bytes.Index (SWAR-accelerated
// in the stdlib on amd64 and arm64) instead of byte-by-byte scans.
// String fields reference src directly via unsafe.String, avoiding
// the per-string copy that string(src[lo:hi]) would incur.
func parseHTML(src []byte, name string) (*NodeStreamBuilder, error) {
	// Pre-allocate the builder's slices. Empirical ratio for HTML:
	// roughly 1 node per 20 bytes; attrs roughly 1 per 30.
	b := NewBuilder().Reserve(len(src) / 15).ReserveText(len(src))
	root := b.AppendNode("#document", FlagFragment)

	// Pool the per-tag attrs working slice.
	attrsPtr := attrsPool.Get().(*[]AttrKV)
	attrs := (*attrsPtr)[:0]
	defer func() {
		*attrsPtr = attrs
		attrsPool.Put(attrsPtr)
	}()

	// stack tracks open tags. Each frame is the node id.
	stack := []int{root}

	i := 0
	for i < len(src) {
		c := src[i]
		if c == '<' {
			if i+1 >= len(src) {
				return nil, &ParseError{Name: name, Offset: uint32(i), Msg: "unterminated tag"}
			}
			// Comment: <!-- ... -->. SWAR-accelerated via bytes.Index.
			if i+3 < len(src) && src[i+1] == '!' && src[i+2] == '-' && src[i+3] == '-' {
				end := i + 4
				if idx := bytes.Index(src[end:], commentEnd); idx >= 0 {
					end += idx + 3
				} else {
					end = len(src)
				}
				i = end
				continue
			}
			// Tag.
			end := i + 1
			closing := end < len(src) && src[end] == '/'
			if closing {
				end++
			}
			tagStart := end
			for end < len(src) && isTagNameByte(src[end]) {
				end++
			}
			if end == tagStart {
				return nil, &ParseError{Name: name, Offset: uint32(i), Msg: "expected tag name"}
			}
			// Zero-alloc string view of src.
			tag := unsafe.String(&src[tagStart], end-tagStart)

			// Parse attributes.
			attrs = attrs[:0]
			self := false
			for end < len(src) && src[end] != '>' {
				for end < len(src) && isWhitespace(src[end]) {
					end++
				}
				if end >= len(src) {
					break
				}
				if src[end] == '/' {
					self = true
					end++
					break
				}
				if src[end] == '>' {
					break
				}
				nameStart := end
				for end < len(src) && !isAttrTerminator(src[end]) {
					end++
				}
				// Zero-alloc string view of src.
				attrName := unsafe.String(&src[nameStart], end-nameStart)
				attrVal := ""
				if end < len(src) && src[end] == '=' {
					end++
					for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
						end++
					}
					if end < len(src) && (src[end] == '"' || src[end] == '\'') {
						quote := src[end]
						end++
						if idx := bytes.IndexByte(src[end:], quote); idx >= 0 {
							attrVal = unsafe.String(&src[end], idx)
							end += idx + 1
						} else {
							attrVal = unsafe.String(&src[end], len(src)-end)
							end = len(src)
						}
					} else {
						valStart := end
						for end < len(src) && !isUnquotedAttrTerminator(src[end]) {
							end++
						}
						attrVal = unsafe.String(&src[valStart], end-valStart)
					}
				}
				attrs = append(attrs, AttrKV{K: attrName, V: attrVal})
			}
			if end < len(src) && src[end] == '>' {
				end++
			}

			if closing {
				if len(stack) > 1 {
					stack = stack[:len(stack)-1]
				}
			} else {
				flags := uint8(0)
				if self || IsVoidTag(tag) {
					flags |= FlagSelfClosing
				}
				isComponent := isComponentTag(tag)
				if isComponent {
					flags |= FlagComponent
				}
				id := b.AppendNode(tag, flags)
				if isComponent {
					b.stream.ComponentName[id] = tag
				}
				if len(attrs) > 0 {
					b.SetAttrsEach(id, attrs)
				}
				if len(stack) > 0 {
					b.SetParent(id, stack[len(stack)-1])
				}
				if !self && !IsVoidTag(tag) {
					stack = append(stack, id)
				}
			}
			i = end
		} else {
			// Text until next <. SWAR-accelerated via bytes.IndexByte.
			next := bytes.IndexByte(src[i:], '<')
			if next < 0 {
				next = len(src) - i
			}
			txt := src[i : i+next]
			if len(txt) > 0 {
				id := b.AppendText(txt, false)
				if len(stack) > 0 {
					b.SetParent(id, stack[len(stack)-1])
				}
			}
			i += next
		}
	}

	return b, nil
}

// isTagNameByte reports whether b can appear in an HTML tag name.
func isTagNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '-' || b == '_' || b == ':'
}

// isWhitespace reports whether b is an HTML whitespace byte.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// isAttrTerminator reports whether b ends an unquoted attribute name.
func isAttrTerminator(b byte) bool {
	return b == '=' || b == ' ' || b == '\t' || b == '\n' || b == '\r' ||
		b == '>' || b == '/'
}

// isUnquotedAttrTerminator reports whether b ends an unquoted attribute
// value. Same as isAttrTerminator but excluding '='.
func isUnquotedAttrTerminator(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' ||
		b == '>' || b == '/'
}

// isComponentTag returns true if the tag should be treated as a
// component. Convention: any tag whose name starts with an uppercase
// letter is a component.
func isComponentTag(tag string) bool {
	if len(tag) == 0 {
		return false
	}
	return tag[0] >= 'A' && tag[0] <= 'Z'
}
