package render

import "context"

// ctxKey is an unexported key type for carrying a *RenderContext in a
// context.Context. Unexported so no external package can collide with
// or forge the value.
type ctxKey struct{}

// WithContext returns a context carrying the given *RenderContext.
// Engine adapters that render through a context.Context (notably
// templ, whose components receive one) use this so components can
// reach the framework's runtime layer.
//
// A templ component can recover the framework state like this:
//
//	rc := render.FromContext(ctx)
//	count, setCount := render.UseState(rc.ComponentState(), "count", 0)
//	slots, _ := rc.Slots  // or data[SlotsDataKey]
func WithContext(ctx context.Context, rc *RenderContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, rc)
}

// FromContext returns the *RenderContext carried by ctx, or nil if
// none is present.
func FromContext(ctx context.Context) *RenderContext {
	if ctx == nil {
		return nil
	}
	rc, _ := ctx.Value(ctxKey{}).(*RenderContext)
	return rc
}
