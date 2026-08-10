package render

import (
	"sync"
	"testing"
)

func TestState_GetSet(t *testing.T) {
	s := NewState()

	if v, ok := s.Get("missing"); ok || v != nil {
		t.Errorf("Get on missing key: got %v ok=%v", v, ok)
	}

	s.Set("hello", "world")
	if v, ok := s.Get("hello"); !ok || v != "world" {
		t.Errorf("Get after Set: got %v ok=%v", v, ok)
	}

	s.Set("hello", "again")
	if v, _ := s.Get("hello"); v != "again" {
		t.Errorf("Get after re-Set: got %v", v)
	}
}

func TestState_UseState(t *testing.T) {
	s := NewState()

	// First call returns initial value and stores it.
	v := s.UseState("count", 0)
	if v != 0 {
		t.Errorf("UseState first call: got %v want 0", v)
	}

	// Second call returns the stored value.
	v = s.UseState("count", 999)
	if v != 0 {
		t.Errorf("UseState second call: got %v want 0 (initial)", v)
	}

	// SetState updates the value.
	s.Set("count", 42)
	v = s.UseState("count", 0)
	if v != 42 {
		t.Errorf("UseState after Set: got %v want 42", v)
	}
}

func TestState_Clear(t *testing.T) {
	s := NewState()
	s.Set("a", 1)
	s.Set("b", 2)

	if _, ok := s.Get("a"); !ok {
		t.Fatal("expected 'a' to be set")
	}

	s.Clear()

	if _, ok := s.Get("a"); ok {
		t.Error("Get after Clear should return ok=false")
	}
}

func TestState_NilSafe(t *testing.T) {
	var s *State

	// All methods should be nil-safe.
	s.Set("key", "val")
	if v, ok := s.Get("key"); ok || v != nil {
		t.Errorf("nil Get: got %v ok=%v", v, ok)
	}
	if v := s.UseState("key", "default"); v != "default" {
		t.Errorf("nil UseState: got %v want default", v)
	}
	s.Clear() // should not panic
}

func TestState_Concurrent(t *testing.T) {
	s := NewState()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.Set("key", i)
		}(i)
		go func() {
			defer wg.Done()
			_ = s.UseState("key", -1)
		}()
	}
	wg.Wait()
}

func TestRenderContext_ComponentState(t *testing.T) {
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)

	s := rc.ComponentState()
	if s == nil {
		t.Fatal("ComponentState returned nil")
	}

	// ComponentState is initialised — no panic.
	s.Set("hello", "world")
	if v, _ := s.Get("hello"); v != "world" {
		t.Errorf("ComponentState Get: got %v", v)
	}
}
