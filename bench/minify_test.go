package bench

import (
	"testing"

	"github.com/xDarkicex/nanite-render"
)

// TestMinify_DataSavings reports the byte and percentage savings from
// compile-time minification for the benchmark document. Run with:
//
//	go test ./bench/ -run TestMinify -v
func TestMinify_DataSavings(t *testing.T) {
	src := []byte(makeDoc())

	// Uncompressed: what the source is.
	raw := len(src)

	// Minified via tdewolff at default settings.
	minified, err := render.MinifyHTML(src, render.MinifyConfig{})
	if err != nil {
		t.Fatalf("MinifyHTML: %v", err)
	}

	// Aggressive: also strip comments and default attr values.
	aggressive, err := render.MinifyHTML(src, render.MinifyConfig{
		KeepWhitespace:      false,
		KeepDocumentTags:    true,
		KeepEndTags:         false,
		KeepDefaultAttrVals: false,
	})
	if err != nil {
		t.Fatalf("MinifyHTML aggressive: %v", err)
	}

	t.Logf("raw bytes:            %6d", raw)
	t.Logf("minified bytes:       %6d  (%.1f%% saved)", len(minified), pct(raw, len(minified)))
	t.Logf("aggressive bytes:     %6d  (%.1f%% saved)", len(aggressive), pct(raw, len(aggressive)))
}

func pct(raw, after int) float64 {
	if raw == 0 {
		return 0
	}
	return 100 * float64(raw-after) / float64(raw)
}

// BenchmarkMinify_Source: cost of reading/serializing uncompressed.
func BenchmarkMinify_Source(b *testing.B) {
	src := []byte(makeDoc())
	var sink int
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += len(src)
	}
	_ = sink
}

// BenchmarkMinify_Default: minify the doc with default settings.
func BenchmarkMinify_Default(b *testing.B) {
	src := []byte(makeDoc())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := render.MinifyHTML(src, render.MinifyConfig{}); err != nil {
			b.Fatal(err)
		}
	}
}
