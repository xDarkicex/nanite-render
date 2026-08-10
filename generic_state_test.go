package render

import (
	"testing"
)

func TestUseState_Generic(t *testing.T) {
	s := NewState()

	// Int state.
	count, setCount := UseState(s, "count", 0)
	if count != 0 {
		t.Errorf("initial count: got %d want 0", count)
	}
	setCount(42)
	count2, _ := UseState(s, "count", 0)
	if count2 != 42 {
		t.Errorf("after set: got %d want 42", count2)
	}

	// String state.
	name, setName := UseState(s, "name", "alice")
	if name != "alice" {
		t.Errorf("initial name: got %q want alice", name)
	}
	setName("bob")
	name2, _ := UseState(s, "name", "alice")
	if name2 != "bob" {
		t.Errorf("after set: got %q want bob", name2)
	}

	// Struct state.
	type Counter struct {
		Value int
		Label string
	}
	c, setC := UseState(s, "counter", Counter{Value: 1, Label: "first"})
	if c.Value != 1 || c.Label != "first" {
		t.Errorf("initial struct: got %+v", c)
	}
	setC(Counter{Value: 99, Label: "ninety-nine"})
	c2, _ := UseState(s, "counter", Counter{})
	if c2.Value != 99 || c2.Label != "ninety-nine" {
		t.Errorf("after set struct: got %+v", c2)
	}

	// Different keys are independent.
	x, _ := UseState(s, "x", 100)
	y, _ := UseState(s, "y", 200)
	if x != 100 || y != 200 {
		t.Errorf("independent keys: x=%d y=%d", x, y)
	}
}

func TestUseState_NilSafe(t *testing.T) {
	var s *State
	v, set := UseState(s, "key", 42)
	if v != 42 {
		t.Errorf("nil state: got %d want 42", v)
	}
	set(100) // should not panic
}

func TestUseStateOrDefault(t *testing.T) {
	s := NewState()

	v, ok := UseStateOrDefault[int](s, "missing")
	if ok {
		t.Errorf("missing key: got ok=true")
	}
	if v != 0 {
		t.Errorf("missing key value: got %d want 0", v)
	}

	s.Set("count", 42)
	v, ok = UseStateOrDefault[int](s, "count")
	if !ok || v != 42 {
		t.Errorf("present: got v=%d ok=%v", v, ok)
	}

	// Type mismatch.
	s.Set("text", "hello")
	v, ok = UseStateOrDefault[int](s, "text")
	if ok {
		t.Error("type mismatch: got ok=true")
	}
	if v != 0 {
		t.Errorf("type mismatch value: got %d want 0", v)
	}
}
