package render

import (
	"bytes"
	"html/template"
	"sync"
)

// renderState is the per-render state read by the superpower closures.
// It carries the ComponentRegistry and the pre-rendered view bytes
// from the current render. The engine's SetRenderState sets this
// before the template executes; ClearRenderState resets it after.
//
// We use a single global state protected by a mutex. The critical
// section is just two pointer/length copies; the lock overhead is
// negligible compared to the template execute. This works correctly
// when renders are serialised per goroutine (the typical case for
// HTTP servers where one goroutine handles one request at a time).
type renderState struct {
	components        *ComponentRegistry
	viewBytes        []byte
	componentFuncMap template.FuncMap
	userState        *State
}

var (
	stateMu sync.RWMutex
	state   renderState
)

// SetRenderState populates the per-render state. Engine adapters
// that use html/template (jade, HTMLTemplate) call this before
// Execute and call ClearRenderState after.
func SetRenderState(rc *RenderContext) {
	stateMu.Lock()
	state.components = rc.ComponentRegistry()
	state.viewBytes = rc.ViewBytes
	stateMu.Unlock()
}

// readState returns a snapshot of the current state. The FuncMap
// closures use this to access the per-request state.
func readState() renderState {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return state
}

// ClearRenderState clears the per-render state. Engine adapters
// call this after Execute completes.
func ClearRenderState() {
	stateMu.Lock()
	state.components = nil
	state.viewBytes = nil
	state.componentFuncMap = nil
	state.userState = nil
	stateMu.Unlock()
}

// SuperpowerFuncs returns a FuncMap that exposes the framework's
// cross-engine superpowers: `component` and `yield`. Engines that
// use html/template (jade, the HTMLTemplate adapter) inject this
// into their template compilation.
func SuperpowerFuncs() template.FuncMap {
	return template.FuncMap{
		"component": componentSuperpower,
		"yield":     yieldSuperpower,
	}
}

// componentSuperpower is the FuncMap value for `{{ component "Name" .Data }}`.
// It dispatches to the registered Component, sets the per-component
// FuncMap on the render state for the duration of the render, then
// flushes the buffer.
func componentSuperpower(name string, data any) (template.HTML, error) {
	s := readState()
	cr := s.components
	if cr == nil {
		return "", nil
	}
	c, ok := cr.Lookup(name)
	if !ok {
		return "", nil
	}
	// Set the per-component FuncMap on the render state. The
	// component's Render reads it from its RenderContext.
	var prev template.FuncMap
	if cf, ok := c.(ComponentWithFuncs); ok {
		prev = state.componentFuncMap
		state.componentFuncMap = cf.ComponentFuncs()
		defer func() { state.componentFuncMap = prev }()
	}
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	if err := c.Render(bw, nil, data); err != nil {
		return "", err
	}
	if err := bw.Flush(); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// yieldSuperpower is the FuncMap value for `{{ yield }}` inside a
// layout. It returns the pre-rendered view HTML that RenderPage
// stored in the per-render state.
func yieldSuperpower() (template.HTML, error) {
	s := readState()
	return template.HTML(s.viewBytes), nil
}
