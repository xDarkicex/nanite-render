package render

import "html/template"

// DataInjector is a function that mutates the render data map before
// the engine executes. The injector is the user's seam for whatever
// request-scoped values need to land in the template — there is no
// hardcoded list of keys. Anything the user wants to inject, they
// inject.
//
// Mirrors the original GO-Portfolio pattern (object["current_user"] =
// a.User, object["view"] = view, object["flashes"] =
// a.Session.Flashes()) but generalises it: any key, any value, any
// number of injectors registered by the user.
type DataInjector func(rc *RenderContext, data map[string]any)

// FuncMapBuilder is a per-request FuncMap factory. The original
// GO-Portfolio built a fresh FuncMap on every render — a closure
// capturing the request handler's locals. We honour that exactly:
// the builder is called on every render and returns a FuncMap that
// closures can capture from rc.
type FuncMapBuilder func(rc *RenderContext) template.FuncMap
