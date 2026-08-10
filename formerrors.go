package render

import "errors"

// ErrValidation is the sentinel error an Action returns to signal
// a validation failure. HandleAction intercepts it: instead of
// returning 500, it re-renders the component with the form errors
// already set on the RenderContext (via rc.SetFormError) and
// returns 200 so HTMX swaps the form with the inline error spans.
//
// Match with errors.Is — actions may wrap it:
//
//	return fmt.Errorf("submit: %w", render.ErrValidation)
var ErrValidation = errors.New("render: validation error")

// formErrorKV is one flash form error: a field key and its
// display-ready message.
type formErrorKV struct {
	key string
	msg string
}

// formErrInlineCap is the size of the zero-allocation inline
// form-error array. 8 field errors covers any realistic form;
// deeper sets spill to a heap slice (no panic, same pattern as
// the cascading-context stack).
const formErrInlineCap = 8

// SetFormError records a flash validation error for a form field.
// The error is visible to the component's re-render in the same
// request (flash semantics: cleared on pool reuse). Writing to
// the same key twice — last write wins.
//
// Flash lifecycle: the action calls SetFormError for each invalid
// field, returns ErrValidation, and HandleAction re-renders the
// component at 200 with the errors readable via GetFormError.
// The errors do NOT persist across requests — the component
// re-renders from its own data source; the errors are display
// state for this one pass.
//
// Prefer ComponentContext.SetFormError inside components; this
// method is for action closures (which receive rc directly).
func (rc *RenderContext) SetFormError(key, msg string) {
	if rc == nil || key == "" {
		return
	}
	// Last write wins: overwrite an existing entry for the same
	// key instead of appending a duplicate. The inline scan is
	// clamped to the inline capacity — beyond it, entries live in
	// the overflow slice (counted in formErrN but not inline).
	inlineN := rc.formErrN
	if inlineN > formErrInlineCap {
		inlineN = formErrInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if rc.formErrInline[i].key == key {
			rc.formErrInline[i].msg = msg
			return
		}
	}
	for i := range rc.formErrOverflow {
		if rc.formErrOverflow[i].key == key {
			rc.formErrOverflow[i].msg = msg
			return
		}
	}
	if rc.formErrN < formErrInlineCap {
		rc.formErrInline[rc.formErrN] = formErrorKV{key: key, msg: msg}
		rc.formErrN++
		return
	}
	rc.formErrOverflow = append(rc.formErrOverflow, formErrorKV{key: key, msg: msg})
	rc.formErrN++
}

// GetFormError returns the flash validation error for a form
// field, or "" if none was set for this request.
func (rc *RenderContext) GetFormError(key string) string {
	if rc == nil || key == "" {
		return ""
	}
	inlineN := rc.formErrN
	if inlineN > formErrInlineCap {
		inlineN = formErrInlineCap
	}
	for i := 0; i < inlineN; i++ {
		if rc.formErrInline[i].key == key {
			return rc.formErrInline[i].msg
		}
	}
	for i := range rc.formErrOverflow {
		if rc.formErrOverflow[i].key == key {
			return rc.formErrOverflow[i].msg
		}
	}
	return ""
}

// FormErrors returns all flash validation errors as a
// map[string]string (field → message). Returns nil when there
// are none. Allocates — for display/instrumentation, not the
// hot path.
func (rc *RenderContext) FormErrors() map[string]string {
	if rc == nil || rc.formErrN == 0 {
		return nil
	}
	m := make(map[string]string, rc.formErrN)
	inlineN := rc.formErrN
	if inlineN > formErrInlineCap {
		inlineN = formErrInlineCap
	}
	for i := 0; i < inlineN; i++ {
		kv := rc.formErrInline[i]
		m[kv.key] = kv.msg
	}
	for _, kv := range rc.formErrOverflow {
		m[kv.key] = kv.msg
	}
	return m
}

// ClearFormErrors removes all flash validation errors. Called on
// pool reuse; callers generally don't need to invoke it directly.
func (rc *RenderContext) ClearFormErrors() {
	if rc == nil {
		return
	}
	inlineN := rc.formErrN
	if inlineN > formErrInlineCap {
		inlineN = formErrInlineCap
	}
	for i := 0; i < inlineN; i++ {
		rc.formErrInline[i] = formErrorKV{}
	}
	rc.formErrOverflow = rc.formErrOverflow[:0]
	rc.formErrN = 0
}
