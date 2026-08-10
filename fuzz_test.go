package render

import (
	"testing"
	"testing/quick"
)

// TestFuzz_Cache_RoundTrip: Put then Get returns the same program.
func TestFuzz_Cache_RoundTrip(t *testing.T) {
	f := func(engine, name string) bool {
		c := NewCache(64)
		p := &Program{Name: name, Engine: engine}
		c.Put(engine, name, p)
		got, ok := c.Get(engine, name)
		return ok && got == p
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Cache_Delete: Delete removes the entry.
func TestFuzz_Cache_Delete(t *testing.T) {
	f := func(engine, name string) bool {
		c := NewCache(64)
		c.Put(engine, name, &Program{})
		c.Delete(engine, name)
		_, ok := c.Get(engine, name)
		return !ok
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Cache_Missing: Get on missing key returns false, nil.
func TestFuzz_Cache_Missing(t *testing.T) {
	f := func(engine, name string) bool {
		c := NewCache(64)
		got, ok := c.Get(engine, name)
		return !ok && got == nil
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Cache_Stats: Stats reflect hits.
func TestFuzz_Cache_Stats(t *testing.T) {
	f := func(engine, name string, lookups int) bool {
		if lookups < 0 || lookups > 100 {
			return true
		}
		c := NewCache(64)
		c.Put(engine, name, &Program{})
		for i := 0; i < lookups; i++ {
			c.Get(engine, name)
		}
		s := c.Stats()
		return s.Hits == int64(lookups)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Cache_Overwrite: overwrite returns the new program.
func TestFuzz_Cache_Overwrite(t *testing.T) {
	f := func(engine, name string, v1, v2 int) bool {
		c := NewCache(64)
		p1 := &Program{Engine: engine, Name: name, ETag: string(rune(v1))}
		p2 := &Program{Engine: engine, Name: name, ETag: string(rune(v2))}
		c.Put(engine, name, p1)
		c.Put(engine, name, p2)
		got, ok := c.Get(engine, name)
		return ok && got == p2
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_State_UseState: First call returns initial; subsequent
// return stored.
func TestFuzz_State_UseState(t *testing.T) {
	f := func(key string, initial, newVal int) bool {
		s := NewState()
		v1 := s.UseState(key, initial)
		if v1 != initial {
			return false
		}
		s.Set(key, newVal)
		v2 := s.UseState(key, initial)
		return v2 == newVal
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_State_Clear: Clear empties all keys.
func TestFuzz_State_Clear(t *testing.T) {
	f := func(key string, val int) bool {
		s := NewState()
		s.Set(key, val)
		s.Clear()
		_, ok := s.Get(key)
		return !ok
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_State_NilSafe: nil State never panics.
func TestFuzz_State_NilSafe(t *testing.T) {
	f := func(key string, val int) bool {
		var s *State
		s.Set(key, val)
		v, ok := s.Get(key)
		if ok || v != nil {
			return false
		}
		_ = s.UseState(key, 0)
		s.Clear()
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_GenericUseState: Generic UseState returns typed value.
func TestFuzz_GenericUseState(t *testing.T) {
	f := func(key string, initial int) bool {
		s := NewState()
		v, set := UseState(s, key, initial)
		if v != initial {
			return false
		}
		set(initial + 1)
		v2, _ := UseState(s, key, initial)
		return v2 == initial+1
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_BindProps_NilData: nil data → all zero values.
func TestFuzz_BindProps_NilData(t *testing.T) {
	type S struct {
		A string
		B int
	}
	f := func() bool {
		s := BindProps[S](nil)
		return s.A == "" && s.B == 0
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_BindProps_TypedMap: explicit tags map to data keys.
func TestFuzz_BindProps_TypedMap(t *testing.T) {
	type S struct {
		A string `nanite:"a"`
		B int    `nanite:"b"`
	}
	f := func(a string, b int) bool {
		data := map[string]any{
			"a": a,
			"b": b,
		}
		s := BindProps[S](data)
		return s.A == a && s.B == b
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_BindProps_TypeMismatch: wrong type → zero value.
func TestFuzz_BindProps_TypeMismatch(t *testing.T) {
	type S struct {
		A string
	}
	f := func(v string) bool {
		data := map[string]any{"A": len(v)} // int into string field
		out := BindProps[S](data)
		return out.A == ""
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_BindProps_DefaultKey: no tag → lowercased name.
func TestFuzz_BindProps_DefaultKey(t *testing.T) {
	type S struct {
		Foo string
	}
	f := func(v string) bool {
		data := map[string]any{"foo": v}
		s := BindProps[S](data)
		return s.Foo == v
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_ParseHTML_NeverPanics: random inputs never panic.
func TestFuzz_ParseHTML_NeverPanics(t *testing.T) {
	f := func(s string) bool {
		defer func() { recover() }()
		_, _ = ParseHTML([]byte(s), "test")
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_ParseHTML_EdgeCases: known weird inputs don't panic.
func TestFuzz_ParseHTML_EdgeCases(t *testing.T) {
	long := make([]byte, 1000)
	for i := range long {
		long[i] = '<'
	}
	cases := []string{
		"",
		"<<<>>>",
		"<unclosed",
		"</>",
		"<>",
		string(long),
		"<!-- unterminated comment",
		"<!DOCTYPE html>",
	}
	for _, c := range cases {
		t.Run(c[:min(len(c), 20)], func(t *testing.T) {
			_, err := ParseHTML([]byte(c), "test")
			_ = err
		})
	}
}

// TestFuzz_EngineData: Program.EngineData round-trips through a
// type assertion.
func TestFuzz_EngineData(t *testing.T) {
	f := func(v int) bool {
		p := &Program{EngineData: v}
		got, ok := p.EngineData.(int)
		return ok && got == v
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_RenderContext_NeverPanics: random headers don't panic.
func TestFuzz_RenderContext_NeverPanics(t *testing.T) {
	f := func(k, v string) bool {
		defer func() { recover() }()
		rc := AcquireContext(nil, nil)
		defer ReleaseContext(rc)
		rc.WriteHeader(k, v)
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// noopWriter implements io.Writer for writer tests.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestFuzz_AcquireWriter_NeverPanics: random writers don't panic.
func TestFuzz_AcquireWriter_NeverPanics(t *testing.T) {
	f := func(data []byte) bool {
		defer func() { recover() }()
		w := AcquireWriter(noopWriter{})
		defer ReleaseWriter(w)
		if len(data) > 0 {
			_, _ = w.Write(data)
		}
		_ = w.WriteByte('x')
		_, _ = w.WriteString("y")
		_ = w.Flush()
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Slots: Slots map operations are nil-safe.
func TestFuzz_Slots(t *testing.T) {
	f := func(name, content string) bool {
		s := Slots{name: content}
		if s[name] != content {
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Children: Children operations are nil-safe.
func TestFuzz_Children(t *testing.T) {
	f := func(parts []string) bool {
		c := Children(parts)
		if len(c) != len(parts) {
			return false
		}
		for i, p := range parts {
			if c[i] != p {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_AcquireContext_Repeated: repeated acquire/release
// doesn't leak or panic.
func TestFuzz_AcquireContext_Repeated(t *testing.T) {
	f := func(n int) bool {
		if n < 0 || n > 100 {
			return true
		}
		for i := 0; i < n; i++ {
			rc := AcquireContext(nil, nil)
			ReleaseContext(rc)
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_PanicRecovery: helpers don't panic on any input.
func TestFuzz_PanicRecovery(t *testing.T) {
	f := func(s string) bool {
		defer func() { recover() }()
		c := NewCache(0)
		c.Put(s, s, &Program{})
		c.Get(s, s)
		c.Delete(s, s)
		c.Stats()

		st := NewState()
		st.Set(s, s)
		_ = st.UseState(s, s)
		st.Clear()

		_, _ = ParseHTML([]byte(s), "fuzz")

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_NodeStream_Accessors_NeverPanics: accessors don't panic
// on random NodeStream values.
func TestFuzz_NodeStream_Accessors_NeverPanics(t *testing.T) {
	f := func(tags []string, flags []uint8, n, k int) bool {
		defer func() { recover() }()
		ns := NodeStream{
			Tag:      tags,
			Flags:    flags,
			TextOff:  make([]uint32, len(tags)*2),
			AttrStart: make([]uint32, len(tags)),
			AttrEnd:   make([]uint32, len(tags)),
		}
		if n >= 0 && n < len(tags) {
			_ = ns.NumAttr(n)
			_ = ns.Text(n)
		}
		_ = k
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Registry_Engine_NeverPanics: engine lookup on random
// names doesn't panic (returns nil).
func TestFuzz_Registry_Engine_NeverPanics(t *testing.T) {
	f := func(name string) bool {
		defer func() { recover() }()
		r := NewRegistry()
		_ = r.Engine(name)
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// TestFuzz_Program_Scratch_NeverPanics: scratch field is usable.
func TestFuzz_Program_Scratch_NeverPanics(t *testing.T) {
	f := func(n int) bool {
		defer func() { recover() }()
		p := &Program{}
		if n > 0 && n < 100 {
			p.scratch = append(p.scratch, byte(n))
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
