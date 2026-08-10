package engine

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/xDarkicex/nanite-render"
)

// TestTempl_ThroughRegistry verifies a templ component rendered via
// Registry.Render flows through the full pipeline: it is cached, runs
// through injectors + FuncMap, and receives the framework's
// *RenderContext through the templ context so it can use state and
// superpowers.
func TestTempl_ThroughRegistry(t *testing.T) {
	eng := NewTempl()
	eng.Register("counter", func(data any) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			// Recover the framework runtime from the templ context.
			rc := render.FromContext(ctx)
			if rc == nil {
				return nil
			}
			// useState-style state, injected via the FuncMap factory.
			count, setCount := render.UseState(rc.ComponentState(), "count", 0)
			setCount(count + 1)

			// Superpowers: read the per-request FuncMap.
			shout, _ := rc.FuncMap["shout"].(func(string) string)
			label := "hi"
			if shout != nil {
				label = shout(label)
			}

			// Injected data key.
			theme, _ := data.(map[string]any)["theme"].(string)

			_, err := io.WriteString(w, "<span data-count='"+itoaT(count)+"' data-theme='"+theme+"'>"+label+"</span>")
			return err
		})
	})

	reg := render.New(render.WithEngine(eng))
	// Per-request FuncMap factory (closure-based).
	reg.FuncMap(func(rc *render.RenderContext) template.FuncMap {
		return template.FuncMap{"shout": func(s string) string { return strings.ToUpper(s) + "!" }}
	})
	// Data injector — mutates the data map before render.
	reg.Inject(func(rc *render.RenderContext, data map[string]any) {
		if data["theme"] == nil {
			data["theme"] = "dark"
		}
	})

	var out strings.Builder
	bw := render.AcquireWriter(&out)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	defer render.ReleaseContext(rc)
	// No loader needed — templ is a Sourceless engine.

	if err := reg.RenderNamed(rc, render.EngineTempl, "counter", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	first := out.String()
	out.Reset()

	// Render again on the same context: state persists, so the setter
	// from render 1 (count: 0→1) is visible now.
	if err := reg.RenderNamed(rc, render.EngineTempl, "counter", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	second := out.String()

	if !strings.Contains(first, "data-count='0'") {
		t.Errorf("first render should show initial 0: %q", first)
	}
	if !strings.Contains(second, "data-count='1'") {
		t.Errorf("state did not persist across renders: %q", second)
	}
	if !strings.Contains(first, "data-theme='dark'") {
		t.Errorf("injector not run: %q", first)
	}
	if !strings.Contains(first, "HI!") {
		t.Errorf("FuncMap superpower not applied: %q", first)
	}
}

// TestTempl_CachedThroughRegistry verifies the templ program is
// cached by (engine, name) — a second render is a cache hit, not a
// recompile.
func TestTempl_CachedThroughRegistry(t *testing.T) {
	eng := NewTempl()
	eng.Register("x", func(data any) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "x")
			return err
		})
	})
	reg := render.New(render.WithEngine(eng))

	var out strings.Builder
	bw := render.AcquireWriter(&out)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineTempl, "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Cache().Get(render.EngineTempl.String(), "x"); !ok {
		t.Error("templ program not cached after first render")
	}
}

// itoaT is a tiny int→string helper for the test component.
func itoaT(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
