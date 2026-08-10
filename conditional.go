package render

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashContent computes a strong ETag for a program. The hash is
// derived from the engine name, template name, and the program's
// content fingerprint. Compile-time; not re-computed on every render.
//
// The computed ETag is stored on the Program.ETag field so the
// render path reads it with no allocation.
func HashContent(p *Program) string {
	if p == nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(p.Engine))
	h.Write([]byte{0})
	h.Write([]byte(p.Name))
	h.Write([]byte{0})
	for _, s := range p.Nodes.Tag {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	for _, f := range p.Nodes.Flags {
		h.Write([]byte{f})
	}
	for _, n := range p.Nodes.NS {
		h.Write([]byte{byte(n), byte(n >> 8)})
	}
	for _, c := range p.Nodes.ChildStart {
		h.Write([]byte{
			byte(c), byte(c >> 8), byte(c >> 16), byte(c >> 24),
		})
	}
	for _, c := range p.Nodes.ChildEnd {
		h.Write([]byte{
			byte(c), byte(c >> 8), byte(c >> 16), byte(c >> 24),
		})
	}
	for _, k := range p.Nodes.AttrKeys {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	for _, v := range p.Nodes.AttrVals {
		h.Write([]byte(stringify(v)))
		h.Write([]byte{0})
	}
	h.Write(p.Nodes.Text(0))
	return `"` + hex.EncodeToString(h.Sum(nil))[:32] + `"`
}

// CheckConditional compares the program's ETag against the
// request's If-None-Match header. If it matches, the function writes
// a 304 response (with appropriate headers) and returns true.
func CheckConditional(rc *RenderContext, p *Program) bool {
	if rc == nil || rc.Request == nil || p == nil {
		return false
	}
	inm := rc.Request.Header.Get("If-None-Match")
	if inm == "" {
		return false
	}
	etag := p.ETag
	if etag == "" {
		etag = HashContent(p)
		p.ETag = etag
	}
	if !etagMatches(inm, etag) {
		return false
	}
	rc.WriteHeader("ETag", etag)
	rc.WriteHeader("Cache-Control", "private, max-age=7776000")
	rc.WriteHeader("Status", "304 Not Modified")
	rc.Request.Header.Set("X-Render-Conditional", "hit")
	return true
}

// etagMatches compares the response ETag against the request's
// If-None-Match list.
func etagMatches(header, etag string) bool {
	if len(etag) > 2 && etag[:2] == "W/" {
		etag = etag[2:]
	}
	for _, candidate := range splitTags(header) {
		if len(candidate) > 2 && candidate[:2] == "W/" {
			candidate = candidate[2:]
		}
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

// splitTags splits a comma-separated If-None-Match header.
func splitTags(h string) []string {
	var out []string
	cur := ""
	for _, c := range h {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
