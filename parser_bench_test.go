package render

import (
	"strings"
	"testing"
)

// makeDoc builds a synthetic HTML document of roughly the requested
// size. The body alternates text runs and tags so the parser has to
// do real work.
func makeDoc(sizeKB int) []byte {
	const chunk = `<div class="row" id="x"><p>Lorem ipsum dolor sit amet, consectetur adipiscing elit.</p><span class="hl">sed do eiusmod tempor</span><a href="/foo" target="_blank">incididunt</a></div>` + "\n"
	repeat := (sizeKB * 1024) / len(chunk)
	if repeat < 1 {
		repeat = 1
	}
	var b strings.Builder
	for i := 0; i < repeat; i++ {
		b.WriteString(chunk)
	}
	return []byte(b.String())
}

func BenchmarkParse_1KB(b *testing.B) {
	src := makeDoc(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseHTML(src, "test")
	}
}

func BenchmarkParse_10KB(b *testing.B) {
	src := makeDoc(10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseHTML(src, "test")
	}
}

func BenchmarkParse_100KB(b *testing.B) {
	src := makeDoc(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseHTML(src, "test")
	}
}
