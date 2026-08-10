package render

import "errors"

// Sentinel errors. Routers can match these via errors.Is to translate
// framework errors into HTTP responses:
//
//	switch {
//	case errors.Is(err, render.ErrTemplateNotFound):
//	    c.JSON(404, ...)
//	case errors.Is(err, render.ErrEngineNotFound):
//	    c.JSON(500, "engine not registered")
//	default:
//	    c.JSON(500, err.Error())
//	}
var (
	// ErrEngineNotFound is returned when the requested engine is
	// not registered with the Registry. Likely a misconfiguration
	// in the boot path; usually indicates a 500.
	ErrEngineNotFound = errors.New("render: engine not found")

	// ErrTemplateNotFound is returned when the loader cannot find
	// the template. Suitable for 404 responses.
	ErrTemplateNotFound = errors.New("render: template not found")

	// ErrLoaderMissing is returned when no template loader is
	// configured on the RenderContext. Indicates the middleware
	// wasn't installed or the handler is missing setup.
	ErrLoaderMissing = errors.New("render: no loader on context")

	// ErrLayoutMissing is returned when a layout is required but
	// not provided. Misconfiguration; usually 500.
	ErrLayoutMissing = errors.New("render: layout missing")

	// ErrRenderPageInvalid is returned when RenderPage is called
	// with invalid arguments (empty layout or view, missing data).
	ErrRenderPageInvalid = errors.New("render: invalid render page")
)

// ParseError is a structured error with a name and offset. Used by
// the parser and engine adapters to report failures with enough
// context to diagnose the offending template. ComposeError is a
// ParseError and is matched by errors.As.
type ParseError struct {
	Name   string
	Offset uint32
	Msg    string
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	if e.Name == "" {
		return "render: " + e.Msg
	}
	return "render: " + e.Name + ": " + e.Msg
}

// Unwrap supports errors.Is/As to introspect wrapped errors.
func (e *ParseError) Unwrap() error { return nil }
