package render

import (
	"reflect"
	"sync"
	"unsafe"
)

// propsTag is the struct tag used by BindProps to map a struct
// field to a key in the data map. Example:
//
//	type NavbarProps struct {
//	    Theme string `nanite:"theme"`
//	    Size  int    `nanite:"size"`
//	}
//
// The default key is the lowercased field name.
const propsTag = "nanite"

// noescape is the runtime's escape-analysis intrinsic. It
// returns its argument unchanged but tells the compiler the
// pointee does NOT escape, so the object can stay on the stack.
//
// Without it, `unsafe.Pointer(&props)` passed as an argument to
// bindStruct crosses a function-call boundary, and Go's escape
// analysis cannot do interprocedural analysis on unsafe
// pointers — it conservatively heap-allocates props. That's a
// 48-byte allocation per BindProps call on the exact hot path
// this feature exists to keep at 0 B/op.
//
// This is the same pattern used by the Go runtime itself and by
// zero-alloc libraries (zerolog, fasthttp internals, etc.).
//
//go:noescape
//go:linkname noescape runtime.noescape
func noescape(p unsafe.Pointer) unsafe.Pointer

// BindProps unmarshals a data map (or any value) into a typed
// struct T. It uses `nanite:"key"` struct tags to map fields to
// data-map keys. The default key is the lowercased field name.
//
// Implementation: zero heap allocations on the hot path. The
// struct T is declared on the stack; per-type field layouts
// (offsets + reflect.Type) are cached in a sync.Map keyed by
// reflect.Type, so reflection runs once per distinct T.
//
// Scalar fields (string, bool, int*, uint*, float*) are written
// directly via unsafe.Pointer arithmetic — no reflect.Value.Set,
// no boxing, no per-call reflection. Complex fields (slices,
// maps, pointers, nested structs, custom interface types) fall
// back to reflect.Value.Set which can allocate. Typical prop
// structs are mostly scalars; complex fields are the exception.
//
// Zero-value semantics: missing keys leave the field at its zero
// value. Type mismatches leave the field at its zero value.
//
// Usage:
//
//	type NavbarProps struct {
//	    Theme string `nanite:"theme"`
//	    Size  int    `nanite:"size"`
//	}
//
//	cr.Register("NAVBAR", render.ComponentFunc(
//	    func(w, rc, data) error {
//	        props := render.BindProps[NavbarProps](data)
//	        return nil
//	    },
//	))
func BindProps[T any](data any) T {
	var props T
	m, ok := unwrapToMap(data)
	if !ok {
		return props
	}
	layout := getLayout(reflect.TypeFor[T]())
	// noescape: keep props on the stack even though its address
	// crosses the bindStruct call boundary as unsafe.Pointer.
	bindStruct(noescape(unsafe.Pointer(&props)), layout, m)
	return props
}

// fieldMeta is one entry in the cached per-type layout. Built
// once per reflect.Type via reflection, then read on every
// BindProps call. No reflection on the hot path.
type fieldMeta struct {
	offset  uintptr      // offset of the field within T
	fieldTy reflect.Type // exact field type (for the type switch)
	kind    reflect.Kind // Kind() of the field type
	tagKey  string       // resolved key in the data map
}

// layoutCache holds per-type field metadata. Keyed by
// reflect.Type; values are []fieldMeta in declaration order.
var layoutCache sync.Map

// canonicalScalarTypes are the exact built-in types whose unsafe
// writeScalar path is safe. Custom named types with the same
// underlying kind (e.g. `type UserID string`) are NOT in this
// set — they're structurally strings but their identity is
// different, so an unsafe write of a plain string into a UserID
// slot would be a type violation. They fall through to the
// reflect-based writeComplex path, which respects assignability.
var (
	typeString  = reflect.TypeFor[string]()
	typeBool    = reflect.TypeFor[bool]()
	typeInt     = reflect.TypeFor[int]()
	typeInt8    = reflect.TypeFor[int8]()
	typeInt16   = reflect.TypeFor[int16]()
	typeInt32   = reflect.TypeFor[int32]()
	typeInt64   = reflect.TypeFor[int64]()
	typeUint    = reflect.TypeFor[uint]()
	typeUint8   = reflect.TypeFor[uint8]()
	typeUint16  = reflect.TypeFor[uint16]()
	typeUint32  = reflect.TypeFor[uint32]()
	typeUint64  = reflect.TypeFor[uint64]()
	typeUintptr = reflect.TypeFor[uintptr]()
	typeFloat32 = reflect.TypeFor[float32]()
	typeFloat64 = reflect.TypeFor[float64]()
)

// getLayout returns (and lazily builds) the cached field layout
// for t. The first call for a given T pays the reflection cost;
// subsequent calls are a sync.Map load (~50ns).
func getLayout(t reflect.Type) []fieldMeta {
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	if cached, ok := layoutCache.Load(t); ok {
		return cached.([]fieldMeta)
	}
	meta := make([]fieldMeta, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		key := f.Tag.Get(propsTag)
		if key == "" {
			key = lowerFirst(f.Name)
		}
		if key == "" {
			continue
		}
		meta[i] = fieldMeta{
			offset:  f.Offset,
			fieldTy: f.Type,
			kind:    f.Type.Kind(),
			tagKey:  key,
		}
	}
	// LoadOrStore handles the race where two goroutines build
	// the same layout simultaneously — the loser's slice is
	// dropped (still heap-allocated but only once per type).
	if actual, loaded := layoutCache.LoadOrStore(t, meta); loaded {
		return actual.([]fieldMeta)
	}
	return meta
}

// bindStruct writes the entries of m into the struct pointed at
// by ptr, using the cached layout. ptr is treated as a pointer
// to the layout's struct type; unsafe arithmetic walks the
// offsets. The caller is responsible for ptr being a valid
// pointer to a value of the layout's struct type.
//
// GC safety: the pointer arithmetic stays within the caller's
// frame; the slot doesn't outlive the struct return.
func bindStruct(ptr unsafe.Pointer, layout []fieldMeta, m map[string]any) {
	for i := range layout {
		// Use index access (not &layout[i]) to avoid forcing the
		// backing array onto the heap. Taking the address of a
		// slice element makes the array escape, which costs one
		// allocation per BindProps call.
		raw, ok := m[layout[i].tagKey]
		if !ok {
			continue
		}
		fmTy := layout[i].fieldTy
		fmOffset := layout[i].offset
		fmKind := layout[i].kind
		fieldPtr := unsafe.Pointer(uintptr(ptr) + fmOffset)
		if writeScalar(fieldPtr, fmTy, raw) {
			continue
		}
		// Slow path: complex types. This branch allocates when
		// the field is a pointer or nested struct.
		writeComplex(fieldPtr, fmTy, fmKind, raw)
	}
}

// writeScalar writes raw into the typed slot at fieldPtr if the
// field's TYPE is one of the canonical built-in scalar types AND
// raw's dynamic type matches. Returns true on success.
//
// Strict canonical-type check: we only do the unsafe write when
// fieldTy is *exactly* one of the canonical built-ins. Custom
// named types with the same underlying kind (e.g. `type UserID
// string`) return false and fall through to writeComplex, which
// uses reflect and respects assignability. This is the right
// trade-off: ~99% of prop fields are plain built-ins and stay on
// the zero-alloc fast path; the rare named-type field pays for
// the reflect call (one per render, not per template node).
func writeScalar(fieldPtr unsafe.Pointer, fieldTy reflect.Type, raw any) bool {
	switch fieldTy {
	case typeString:
		v, ok := raw.(string)
		if !ok {
			return false
		}
		*(*string)(fieldPtr) = v
		return true
	case typeBool:
		v, ok := raw.(bool)
		if !ok {
			return false
		}
		*(*bool)(fieldPtr) = v
		return true
	case typeInt:
		v, ok := raw.(int)
		if !ok {
			return false
		}
		*(*int)(fieldPtr) = v
		return true
	case typeInt8:
		v, ok := raw.(int8)
		if !ok {
			return false
		}
		*(*int8)(fieldPtr) = v
		return true
	case typeInt16:
		v, ok := raw.(int16)
		if !ok {
			return false
		}
		*(*int16)(fieldPtr) = v
		return true
	case typeInt32:
		v, ok := raw.(int32)
		if !ok {
			return false
		}
		*(*int32)(fieldPtr) = v
		return true
	case typeInt64:
		v, ok := raw.(int64)
		if !ok {
			return false
		}
		*(*int64)(fieldPtr) = v
		return true
	case typeUint:
		v, ok := raw.(uint)
		if !ok {
			return false
		}
		*(*uint)(fieldPtr) = v
		return true
	case typeUint8:
		v, ok := raw.(uint8)
		if !ok {
			return false
		}
		*(*uint8)(fieldPtr) = v
		return true
	case typeUint16:
		v, ok := raw.(uint16)
		if !ok {
			return false
		}
		*(*uint16)(fieldPtr) = v
		return true
	case typeUint32:
		v, ok := raw.(uint32)
		if !ok {
			return false
		}
		*(*uint32)(fieldPtr) = v
		return true
	case typeUint64:
		v, ok := raw.(uint64)
		if !ok {
			return false
		}
		*(*uint64)(fieldPtr) = v
		return true
	case typeUintptr:
		v, ok := raw.(uintptr)
		if !ok {
			return false
		}
		*(*uintptr)(fieldPtr) = v
		return true
	case typeFloat32:
		v, ok := raw.(float32)
		if !ok {
			return false
		}
		*(*float32)(fieldPtr) = v
		return true
	case typeFloat64:
		v, ok := raw.(float64)
		if !ok {
			return false
		}
		*(*float64)(fieldPtr) = v
		return true
	}
	return false
}

// writeComplex handles non-scalar field types via reflection.
// This branch CAN allocate (reflect.New for pointer fields,
// nested BindProps recursion). For typical prop structs with
// mostly scalar fields, the cost is paid on a small fraction of
// fields per call.
//
// Supported:
//   - Slices, maps, arrays — assigned if the dynamic type matches
//     exactly. Type mismatch leaves the field at zero.
//   - Pointers to struct — recursively bound against the inner
//     data (which must be a map). Allocates one pointer.
//   - Pointers to scalar — written through if the pointee
//     matches raw. Allocates one pointer.
//   - Nested structs — recursive BindProps against the inner data.
//   - Anything else — falls back to reflect.Value.Set which does
//     assignability-aware conversion.
func writeComplex(fieldPtr unsafe.Pointer, fieldTy reflect.Type, kind reflect.Kind, raw any) {
	fieldVal := reflect.NewAt(fieldTy, fieldPtr).Elem()
	if !fieldVal.CanSet() {
		return
	}
	switch kind {
	case reflect.Slice, reflect.Map, reflect.Array:
		rawV := reflect.ValueOf(raw)
		if rawV.IsValid() && rawV.Type() == fieldTy {
			fieldVal.Set(rawV)
		}
		return
	case reflect.Ptr:
		// Allocate the pointed-to value, then bind/assign.
		ptr := reflect.New(fieldTy.Elem())
		if fieldTy.Elem().Kind() == reflect.Struct {
			if inner, ok := raw.(map[string]any); ok {
				bindStruct(noescape(unsafe.Pointer(ptr.Pointer())), getLayout(fieldTy.Elem()), inner)
			}
		} else {
			// Pointer to scalar — writeScalar on the pointee.
			writeScalar(unsafe.Pointer(ptr.Pointer()), fieldTy.Elem(), raw)
		}
		fieldVal.Set(ptr)
		return
	case reflect.Struct:
		if inner, ok := raw.(map[string]any); ok {
			bindStruct(noescape(unsafe.Pointer(fieldVal.Addr().Pointer())), getLayout(fieldTy), inner)
		}
		return
	}
	// Fallback: type-assignable. Covers custom named types,
	// interface{}-shaped fields, etc.
	rawV := reflect.ValueOf(raw)
	if rawV.IsValid() && rawV.Type().AssignableTo(fieldTy) {
		fieldVal.Set(rawV)
	}
}

// unwrapToMap unwraps the common shapes of data that components
// receive into a map[string]any. nil returns nil, false.
func unwrapToMap(data any) (map[string]any, bool) {
	if data == nil {
		return nil, false
	}
	if m, ok := data.(map[string]any); ok {
		return m, true
	}
	return nil, false
}

// lowerFirst returns s with the first character lowercased.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}
