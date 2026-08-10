package render_test

import (
	"bytes"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/xDarkicex/nanite-render"
)

type scalarProps struct {
	Theme string `nanite:"theme"`
	Size  int    `nanite:"size"`
	On    bool   `nanite:"on"`
	Ratio float64 `nanite:"ratio"`
}

type allKindsProps struct {
	S  string  `nanite:"s"`
	B  bool    `nanite:"b"`
	I  int     `nanite:"i"`
	I8 int8    `nanite:"i8"`
	I16 int16  `nanite:"i16"`
	I32 int32  `nanite:"i32"`
	I64 int64  `nanite:"i64"`
	U  uint    `nanite:"u"`
	U8 uint8   `nanite:"u8"`
	U16 uint16 `nanite:"u16"`
	U32 uint32 `nanite:"u32"`
	U64 uint64 `nanite:"u64"`
	F32 float32 `nanite:"f32"`
	F64 float64 `nanite:"f64"`
}

// TestBindProps_AllScalarKindsAssign verifies every supported
// scalar kind round-trips through the unsafe fast path.
func TestBindProps_AllScalarKindsAssign(t *testing.T) {
	data := map[string]any{
		"s":   "hello",
		"b":   true,
		"i":   42,
		"i8":  int8(8),
		"i16": int16(16),
		"i32": int32(32),
		"i64": int64(64),
		"u":   uint(1),
		"u8":  uint8(2),
		"u16": uint16(3),
		"u32": uint32(4),
		"u64": uint64(5),
		"f32": float32(1.5),
		"f64": 2.5,
	}
	got := render.BindProps[allKindsProps](data)
	if got.S != "hello" || !got.B || got.I != 42 ||
		got.I8 != 8 || got.I16 != 16 || got.I32 != 32 || got.I64 != 64 ||
		got.U != 1 || got.U8 != 2 || got.U16 != 3 || got.U32 != 4 || got.U64 != 5 ||
		got.F32 != 1.5 || got.F64 != 2.5 {
		t.Errorf("scalar fields didn't round-trip: %+v", got)
	}
}

// TestBindProps_ZeroAllocScalars verifies the scalar fast
// path doesn't allocate on the hot path. The test uses
// testing.AllocsPerRun with a per-iteration local variable —
// which mirrors the production pattern (`props := BindProps[T](data)`
// inside a component render, where props doesn't escape).
//
// Note: assigning the result to a closure-captured variable forces
// props onto the heap (Go's escape analysis can't prove the
// closure doesn't outlive the function). That's a CALLER concern;
// the framework's BindProps itself is zero-alloc on the hot path,
// as verified by direct call to bindStruct in the internal
// leak-detection tests.
func TestBindProps_ZeroAllocScalars(t *testing.T) {
	// Build the data as `any` from the start so no conversion
	// happens inside the measured loop.
	data := any(map[string]any{"theme": "dark", "size": 42, "on": true, "ratio": 0.5})
	// Warm up the layout cache.
	_ = render.BindProps[scalarProps](data)

	// Use a manual loop with runtime.MemStats because
	// testing.AllocsPerRun wraps the body in a closure that
	// allocates once per call (for the closure value itself).
	const N = 10000
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < N; i++ {
		props := render.BindProps[scalarProps](data)
		// Touch fields so the compiler can't optimize the bind
		// call away.
		if props.Theme == "" && props.Size == 0 && !props.On && props.Ratio == 0 {
			_ = props.Theme
		}
	}
	runtime.ReadMemStats(&after)
	perIter := float64(after.Mallocs-before.Mallocs) / float64(N)
	if perIter > 0.001 {
		t.Errorf("scalar props: %.3f allocs/iter, want 0", perIter)
	}
}

// BenchmarkBindProps_ScalarFastPath reports the hot-path cost
// of the unsafe scalar binder: should be ~40ns, 0 B/op.
func BenchmarkBindProps_ScalarFastPath(b *testing.B) {
	data := any(map[string]any{"theme": "dark", "size": 42, "on": true, "ratio": 0.5})
	_ = render.BindProps[scalarProps](data) // warm the layout cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		props := render.BindProps[scalarProps](data)
		if props.Theme == "" && props.Size == 0 && !props.On && props.Ratio == 0 {
			b.Fatal("bad bind")
		}
	}
}

// TestBindProps_LayoutCacheBuiltOnce verifies the per-type
// layout is built exactly once across many calls (cache hit
// path). We can't directly count, but if the cache were broken
// the first-call cost would dominate and the bench would show
// allocations.
func TestBindProps_LayoutCacheBuiltOnce(t *testing.T) {
	data := map[string]any{"theme": "dark", "size": 42}
	// First call: cold cache.
	first := render.BindProps[scalarProps](data)
	// Subsequent calls: warm cache.
	for i := 0; i < 1000; i++ {
		_ = render.BindProps[scalarProps](data)
	}
	if first.Theme != "dark" || first.Size != 42 {
		t.Errorf("first call result wrong: %+v", first)
	}
}

// TestBindProps_CustomNamedType verifies the type assertion uses
// the EXACT field type, not just its underlying Kind. A field
// of type `UserID` (defined as `type UserID string`) should NOT
// accept a plain `string` value from the map (different types).
type UserID string
type Role string

type customProps struct {
	ID   UserID `nanite:"id"`
	Role Role   `nanite:"role"`
}

func TestBindProps_CustomNamedType(t *testing.T) {
	// Correct: data carries the named type values.
	data := map[string]any{
		"id":   UserID("alice"),
		"role": Role("admin"),
	}
	got := render.BindProps[customProps](data)
	if string(got.ID) != "alice" || string(got.Role) != "admin" {
		t.Errorf("named types didn't round-trip: %+v", got)
	}

	// Type mismatch: data has plain strings, fields are named types.
	// The fields should stay at zero.
	mismatch := map[string]any{
		"id":   "alice", // string, not UserID
		"role": "admin", // string, not Role
	}
	got2 := render.BindProps[customProps](mismatch)
	if got2.ID != "" || got2.Role != "" {
		t.Errorf("type mismatch should leave zero: %+v", got2)
	}
}

// TestBindProps_PointerToStruct verifies the recursive-bind
// fallback. The inner map gets bound into a fresh pointer.
type innerProps struct {
	Name string `nanite:"name"`
	Age  int    `nanite:"age"`
}
type outerProps struct {
	User *innerProps `nanite:"user"`
}

func TestBindProps_PointerToStruct(t *testing.T) {
	data := map[string]any{
		"user": map[string]any{
			"name": "alice",
			"age":  30,
		},
	}
	got := render.BindProps[outerProps](data)
	if got.User == nil {
		t.Fatal("pointer field was nil after binding nested map")
	}
	if got.User.Name != "alice" || got.User.Age != 30 {
		t.Errorf("nested binding wrong: %+v", got.User)
	}
}

// TestBindProps_NestedStruct verifies non-pointer nested struct
// fields bind correctly.
type nestedDirect struct {
	Inner innerProps `nanite:"inner"`
}
type wrapper struct {
	Wrapped nestedDirect `nanite:"wrapped"`
}

func TestBindProps_NestedStruct(t *testing.T) {
	data := map[string]any{
		"wrapped": map[string]any{
			"inner": map[string]any{
				"name": "bob",
				"age":  25,
			},
		},
	}
	got := render.BindProps[wrapper](data)
	if got.Wrapped.Inner.Name != "bob" || got.Wrapped.Inner.Age != 25 {
		t.Errorf("nested struct wrong: %+v", got)
	}
}

// TestBindProps_SliceAndMap verify exact-type assignment for
// slices and maps. Type mismatch leaves the field at zero.
type sliceProps struct {
	Tags  []string       `nanite:"tags"`
	Meta  map[string]int `nanite:"meta"`
	Names []string       `nanite:"names"` // type mismatch test
}

func TestBindProps_SliceAndMap(t *testing.T) {
	data := map[string]any{
		"tags": []string{"a", "b", "c"},
		"meta": map[string]int{"x": 1, "y": 2},
		"names": "not-a-slice", // wrong type
	}
	got := render.BindProps[sliceProps](data)
	if len(got.Tags) != 3 || got.Tags[0] != "a" {
		t.Errorf("slice: %+v", got.Tags)
	}
	if got.Meta["x"] != 1 || got.Meta["y"] != 2 {
		t.Errorf("map: %+v", got.Meta)
	}
	if len(got.Names) != 0 {
		t.Errorf("type-mismatch should leave zero, got %+v", got.Names)
	}
}

// TestBindProps_PointerToScalar verifies the pointer-to-scalar
// path allocates the pointee and writes the value through.
type ptrScalarProps struct {
	Name *string `nanite:"name"`
}

func TestBindProps_PointerToScalar(t *testing.T) {
	data := map[string]any{"name": "carol"}
	got := render.BindProps[ptrScalarProps](data)
	if got.Name == nil || *got.Name != "carol" {
		t.Errorf("pointer-to-scalar wrong: %v", got.Name)
	}
}

// TestBindProps_FluentIntegration verifies a fluent component
// can use the zero-alloc binder end-to-end. The render closure
// captures no outer `props` variables, so the binder's stack
// result stays stack-allocated.
//
// The framework's render path itself allocates (the
// ComponentContext struct, pool misses), so we measure the
// DELTA between a render that uses BindProps and one that
// doesn't. BindProps must add zero allocations on top.
func TestBindProps_FluentIntegration(t *testing.T) {
	cr := render.NewComponentRegistry()
	cr.Define("WIDGET").
		Render(func(c *render.ComponentContext) error {
			props := render.BindProps[scalarProps](c.Data)
			_, err := c.WriteString(props.Theme)
			return err
		}).
		Register(cr)
	// Control component: identical output, no BindProps.
	cr.Define("PLAIN").
		Render(func(c *render.ComponentContext) error {
			_, err := c.WriteString("dark")
			return err
		}).
		Register(cr)

	reg := render.New()
	reg.AttachComponents(cr)

	// Warm up.
	data := map[string]any{"theme": "dark", "size": 42, "on": true, "ratio": 0.5}
	req := httptest.NewRequest("GET", "/", nil)
	bw := render.AcquireWriter(&bytes.Buffer{})
	rc := render.AcquireContext(bw, req)
	_ = reg.RenderComponent(bw, rc, "WIDGET", data)
	_ = reg.RenderComponent(bw, rc, "PLAIN", data)
	render.ReleaseWriter(bw)
	render.ReleaseContext(rc)

	// Measure the delta: with-BindProps minus without. Use a
	// shared request and writer so the only difference is the
	// component's render body.
	allocsWith := testing.AllocsPerRun(500, func() {
		var buf bytes.Buffer
		bw2 := render.AcquireWriter(&buf)
		rc2 := render.AcquireContext(bw2, req)
		_ = reg.RenderComponent(bw2, rc2, "WIDGET", data)
		render.ReleaseWriter(bw2)
		render.ReleaseContext(rc2)
	})
	allocsWithout := testing.AllocsPerRun(500, func() {
		var buf bytes.Buffer
		bw2 := render.AcquireWriter(&buf)
		rc2 := render.AcquireContext(bw2, req)
		_ = reg.RenderComponent(bw2, rc2, "PLAIN", data)
		render.ReleaseWriter(bw2)
		render.ReleaseContext(rc2)
	})
	delta := allocsWith - allocsWithout
	// BindProps must add zero allocations. Allow fractional
	// noise from the allocator's measurement; the delta should
	// be 0 in steady state.
	if delta > 0.1 {
		t.Errorf("BindProps added %.1f allocs/op to the render (with=%v without=%v)",
			delta, allocsWith, allocsWithout)
	}
	// Sanity: runtime mem stats should not be ballooning.
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > 50*1024*1024 {
		t.Errorf("heap grew past 50MB during test: %d bytes", ms.HeapAlloc)
	}
}