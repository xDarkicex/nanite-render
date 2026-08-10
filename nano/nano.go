// Package nano provides a thin adapter between the nanite router and
// the nanite-render package. The core render package has no nanite
// dependency; this package is the only place that imports it.
//
// Design: direct-call, not middleware. The handler explicitly calls
// Render (or the fluent Page builder) at the end of the request.
// The original GO-Portfolio did it this way because:
//
//  1. The handler is the one that knows the view name and data.
//     A middleware can't infer this without route introspection.
//  2. Render is a terminal operation — it writes headers + body.
//     There's no "next" handler to defer to.
//  3. Skipping the middleware saves a function call and a map
//     lookup on every request. At 190k req/sec, that's measurable.
package nano

import (
	"net/http"

	"github.com/xDarkicex/nanite"

	"github.com/xDarkicex/nanite-render"
)

// Render is the high-level helper for handlers. It acquires a pooled
// ByteWriter wrapping the response writer, a pooled RenderContext,
// runs the engine, and releases both. The function is the entire
// render lifecycle — no middleware, no setup, no teardown outside
// this call.
//
// Usage:
//
//	api.Get("/posts/:slug", func(c *nanite.Context) {
//	    post := loadPost(c.Param("slug"))
//	    if err := nano.Render(c, reg, "html", "posts/show", post); err != nil {
//	        c.Error(http.StatusInternalServerError, err)
//	    }
//	})
func Render(c *nanite.Context, reg *render.Registry, engine, view string, data any) error {
	bw := render.AcquireWriter(c.Writer)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, c.Request)
	rc.Loader = reg.DefaultLoader()
	defer render.ReleaseContext(rc)
	return reg.Render(rc, engine, view, data)
}

// RenderWithLoader is Render with an explicit loader. Use this when
// the loader depends on the request (e.g. tenant-scoped template
// roots) or when the registry has no default loader.
func RenderWithLoader(c *nanite.Context, reg *render.Registry, engine, view string, data any, loader render.SourceFunc) error {
	bw := render.AcquireWriter(c.Writer)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, c.Request)
	rc.Loader = loader
	defer render.ReleaseContext(rc)
	return reg.Render(rc, engine, view, data)
}

// RenderPage composes a layout + view and renders it into the
// response. It acquires a pooled ByteWriter and RenderContext, runs
// RenderPage, and releases both.
//
// Usage:
//
//	api.Get("/posts/:slug", func(c *nanite.Context) {
//	    post := loadPost(c.Param("slug"))
//	    if err := nano.RenderPage(c, reg, "layouts/app", "posts/show", post); err != nil {
//	        c.Error(http.StatusInternalServerError, err)
//	    }
//	})
func RenderPage(c *nanite.Context, reg *render.Registry, layout, view string, data any) error {
	return Page(reg, c).
		Layout(layout).
		View(view).
		With(data).
		Render()
}

// Page returns a fluent render builder bound to the nanite context.
// The builder acquires a pooled ByteWriter and RenderContext,
// wires the registry loader, and renders (and releases) when
// .Render() is called.
//
// Usage:
//
//	api.Get("/posts/:slug", func(c *nanite.Context) {
//	    post := loadPost(c.Param("slug"))
//	    if err := nano.Page(reg, c).
//	        Engine(render.EngineJade).
//	        Layout("layouts/app").
//	        View("posts/show").
//	        With(post).
//	        Render(); err != nil {
//	        c.Error(http.StatusInternalServerError, err)
//	    }
//	})
func Page(reg *render.Registry, c *nanite.Context) *render.RenderBuilder {
	bw := render.AcquireWriter(c.Writer)
	rc := render.AcquireContext(bw, c.Request)
	rc.Loader = reg.DefaultLoader()
	// Release the pooled writer and context when Render completes.
	return reg.Page(rc).WithDone(func() {
		_ = bw.Flush()
		render.ReleaseContext(rc)
		render.ReleaseWriter(bw)
	})
}

// SetHeader sets an HTTP header on the response writer.
func SetHeader(c *nanite.Context, key, value string) {
	c.Writer.Header().Set(key, value)
}

// Status writes the HTTP status code to the response.
func Status(c *nanite.Context, code int) {
	c.Writer.WriteHeader(code)
}

// HTTPErrorHandler renders an error as an HTML response with status 500.
func HTTPErrorHandler(c *nanite.Context, err error) {
	http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
}
