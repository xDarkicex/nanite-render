package render

import (
	"testing"
)

// Benchmark variantKey with several variants.
func BenchmarkVariantKey(b *testing.B) {
	r := NewRegistry()
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)
	rc.SetVariant("locale", "fr")
	rc.SetVariant("theme", "dark")
	rc.SetVariant("tenant", "acme")
	for i := 0; i < b.N; i++ {
		_ = r.variantKey(rc)
	}
}

// Benchmark variantName with the same variants.
func BenchmarkVariantName(b *testing.B) {
	r := NewRegistry()
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)
	rc.SetVariant("locale", "fr")
	rc.SetVariant("theme", "dark")
	rc.SetVariant("tenant", "acme")
	for i := 0; i < b.N; i++ {
		_ = r.variantName("layouts/application", rc)
	}
}
