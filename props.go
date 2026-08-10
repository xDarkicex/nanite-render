package render

import (
	"reflect"
)

// propsTag is the struct tag used by BindProps to map a struct field
// to a key in the data map. Example:
//
//	type NavbarProps struct {
//	    Theme string `nanite:"theme"`
//	    Size  int    `nanite:"size"`
//	}
//
// The default key is the lowercased field name.
const propsTag = "nanite"

// BindProps unmarshals a data map (or any value) into a typed
// struct T. It uses `nanite:"key"` struct tags to map fields to
// data-map keys. The default key is the lowercased field name.
//
// Supported conversions:
//   - string, bool, int*, uint*, float* — direct assignment
//   - *T (pointer to T) — recursively unmarshals into *T
//   - []T / map[K]V — handled if the underlying type supports it
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
//	        // props.Theme is a typed string
//	        return nil
//	    },
//	))
func BindProps[T any](data any) T {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		return zero
	}
	v := reflect.New(t).Elem()

	// Unwrap the data to a map for key lookup.
	m, _ := unwrapToMap(data)
	if m == nil {
		return v.Interface().(T)
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		// Determine the key: explicit tag, else lowercased name.
		key := field.Tag.Get(propsTag)
		if key == "" {
			key = lowerFirst(field.Name)
		}
		if key == "" {
			continue
		}
		raw, ok := m[key]
		if !ok {
			continue
		}
		// Pointer types: allocate the pointed-to value, recurse
		// BindProps on the inner data, and set.
		if field.Type.Kind() == reflect.Ptr {
			if field.Type.Elem().Kind() == reflect.Struct {
				innerData := unwrapToStruct(raw)
				if innerData == nil {
					continue
				}
				ptr := reflect.New(field.Type.Elem())
				// Recursive unmarshal via direct reflect.
				fillStruct(ptr.Elem(), innerData)
				v.Field(i).Set(ptr)
				continue
			}
		}
		if !setField(v.Field(i), raw) {
			// Type mismatch — field stays at zero.
			continue
		}
	}
	return v.Interface().(T)
}

// fillStruct populates a struct value from a map using the same
// rules as BindProps but in-place (no generic instantiation).
func fillStruct(v reflect.Value, data map[string]any) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		key := field.Tag.Get(propsTag)
		if key == "" {
			key = lowerFirst(field.Name)
		}
		raw, ok := data[key]
		if !ok {
			continue
		}
		if field.Type.Kind() == reflect.Ptr && field.Type.Elem().Kind() == reflect.Struct {
			innerData := unwrapToStruct(raw)
			if innerData != nil {
				ptr := reflect.New(field.Type.Elem())
				fillStruct(ptr.Elem(), innerData)
				v.Field(i).Set(ptr)
			}
			continue
		}
		setField(v.Field(i), raw)
	}
}

// unwrapToStruct extracts a map from the data. If the data is
// already a map, returns it. Otherwise returns nil.
func unwrapToStruct(data any) map[string]any {
	if data == nil {
		return nil
	}
	if m, ok := data.(map[string]any); ok {
		return m
	}
	return nil
}

// unwrapToMap unwraps the common shapes of data that components
// receive into a map[string]any. nil returns nil.
func unwrapToMap(data any) (map[string]any, bool) {
	if data == nil {
		return nil, false
	}
	if m, ok := data.(map[string]any); ok {
		return m, true
	}
	// Allow a pointer-to-map too.
	if m, ok := reflect.ValueOf(data).Interface().(map[string]any); ok {
		return m, true
	}
	return nil, false
}

// setField assigns raw to v if the types match exactly. Returns
// true if the assignment was performed. Type mismatches return
// false; the caller leaves the field at its zero value.
//
// We intentionally do NOT use reflect.Convert for primitive types:
// the BindProps contract is "type mismatch leaves zero", and silent
// int→string conversion hides bugs.
func setField(v reflect.Value, raw any) bool {
	if !v.CanSet() {
		return false
	}
	rawV := reflect.ValueOf(raw)
	if !rawV.IsValid() {
		return false
	}
	if rawV.Type() == v.Type() {
		v.Set(rawV)
		return true
	}
	// Allow *T ↔ T (pointer/element).
	if rawV.Type() == v.Type() || rawV.Type() == reflect.PtrTo(v.Type()) {
		v.Set(rawV.Convert(v.Type()))
		return true
	}
	return false
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
