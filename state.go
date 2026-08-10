package render

import "sync"

// State is the per-render state shared across components in a
// single render. It enables React-style state management: a parent
// can set a value, and a child (rendered later in the same render)
// can read it. State is reset on each new render.
//
// The state object is accessed via the FuncMap (useState, get, set)
// or directly via the RenderContext.
type State struct {
	mu     sync.RWMutex
	values map[string]any
}

// NewState returns an empty State.
func NewState() *State { return &State{} }

// Get returns the value for the given key. Returns nil if the key
// has not been set. The boolean indicates whether the key was
// present.
func (s *State) Get(key string) (any, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	return v, ok
}

// Set sets the value for the given key. Overwrites any previous
// value.
func (s *State) Set(key string, value any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]any)
	}
	s.values[key] = value
}

// UseState returns the value for the given key, or initialValue if
// the key has not been set. The initialValue is stored for
// subsequent calls. This is the React-style useState hook:
//
//	count, _ := state.UseState("count", 0).(int)
//
// On the first call, UseState returns initialValue and stores it.
// On subsequent calls, UseState returns the stored value.
func (s *State) UseState(key string, initialValue any) any {
	if s == nil {
		return initialValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]any)
	}
	if v, ok := s.values[key]; ok {
		return v
	}
	s.values[key] = initialValue
	return initialValue
}

// Clear removes all key-value pairs from the State.
func (s *State) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = nil
}

// UseState is a generic, type-safe wrapper around State.UseState.
// It returns the value (typed T) and a setter. The setter writes the
// new value back to the same key. This is the React-style useState
// hook for Go templates — no type assertions needed.
//
// Usage in a component:
//
//	count, setCount := render.UseState(state, "count", 0)
//	setCount(count + 1)
//
// The first call returns initial and stores it. Subsequent calls
// return the stored value. The setter always writes the typed value.
func UseState[T any](s *State, key string, initial T) (T, func(T)) {
	val, _ := s.UseState(key, initial).(T)
	set := func(v T) {
		s.Set(key, v)
	}
	return val, set
}

// UseStateOrDefault returns the value for the given key if it is
// set and is of type T. The boolean indicates whether both the key
// was present and the type assertion succeeded.
func UseStateOrDefault[T any](s *State, key string) (T, bool) {
	if s == nil {
		var zero T
		return zero, false
	}
	v, ok := s.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	val, ok := v.(T)
	if !ok {
		var zero T
		return zero, false
	}
	return val, true
}

// ComponentState returns the per-render state. The state is reset
// on each new render (via SetRenderState).
func (rc *RenderContext) ComponentState() *State {
	return rc.state
}
