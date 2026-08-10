package render

import (
	"strconv"
	"testing"
)

func benchProgram() *Program {
	return &Program{
		Engine: "bench",
		Name:   "x",
		Nodes: NodeStream{
			Tag:      []string{"div", "p", "span"},
			Flags:    []uint8{0, 0, 0},
			NS:       []uint16{0, 0, 0},
			AttrKeys: []string{"id", "class"},
			AttrVals: []any{"x", "y"},
		},
	}
}

// Benchmark cache hit.
func BenchmarkCache_Get(b *testing.B) {
	c := NewCache(1024)
	for i := range 100 {
		c.Put("k", strconv.Itoa(i), benchProgram())
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("k", "42")
		}
	})
}

// Benchmark cache miss.
func BenchmarkCache_Miss(b *testing.B) {
	c := NewCache(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("missing", "missing")
		}
	})
}

// Benchmark cache put.
func BenchmarkCache_Put(b *testing.B) {
	c := NewCache(1024)
	p := benchProgram()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Put("k", strconv.Itoa(i), p)
			i++
		}
	})
}

// Benchmark cache hot-path Get+Delete (cold cache variance).
func BenchmarkCache_RoundTrip(b *testing.B) {
	c := NewCache(1024)
	p := benchProgram()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := strconv.Itoa(i)
			c.Put("k", key, p)
			c.Get("k", key)
			c.Delete("k", key)
			i++
		}
	})
}

// Benchmark FNV-1a hash.
func BenchmarkFNV1a64(b *testing.B) {
	s := "engine/template_name"
	for b.Loop() {
		fnv1a64(s)
	}
}

// Benchmark FNV pair (engine, name).
func BenchmarkFNVPair(b *testing.B) {
	for b.Loop() {
		fnvPair("jade", "layouts/application")
	}
}

// Benchmark ETag generation (compile-time-ish).
func BenchmarkHashContent(b *testing.B) {
	p := &Program{
		Engine: "jade",
		Name:   "layouts/application",
		Nodes: NodeStream{
			Tag:      []string{"html", "head", "body", "div", "p", "span", "a", "img"},
			Flags:    []uint8{0, 0, 0, 0, 0, 0, 0, 8},
			NS:       []uint16{0, 0, 0, 0, 0, 0, 0, 0},
			AttrKeys: []string{"id", "class", "href", "src", "alt"},
			AttrVals: []any{"main", "container", "/foo", "/bar.png", "text"},
		},
	}
	for b.Loop() {
		HashContent(p)
	}
}
