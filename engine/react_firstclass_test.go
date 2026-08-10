package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/xDarkicex/nanite-render"
)

// sharedProps is the typed prop shape the shared component consumes.
type sharedProps struct {
	Name string `nanite:"name"`
}

// sharedDef builds a registry containing ONE shared component written
// against the React-style ComponentContext API: typed props via
// render.Props, state via c.UseState. The same definition is
// dispatched from templ, html/template, and the plain HTML executor
// below — proving the layer is first-class across the stack.
func sharedDef(t *testing.T) *render.ComponentRegistry {
	t.Helper()
	cr := render.NewComponentRegistry()
	cr.Define("SHARED").
		Render(func(c *render.ComponentContext) error {
			p := render.Props[sharedProps](c)
			count, setCount := c.UseState("count", 0)
			setCount(count.(int) + 1)
			_, err := c.WriteString("<div class='shared' data-name='" + p.Name + "' data-count='" + itoaT(count.(int)) + "'></div>")
			return err
		}).
		Register(cr)
	return cr
}

// TestFirstClass_FromTempl renders the shared component from inside a
// templ component via render.RenderComponent.
func TestFirstClass_FromTempl(t *testing.T) {
	cr := sharedDef(t)
	eng := NewTempl()
	eng.Register("page", func(data any) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			// A templ component composes a nanite-render component.
			return render.RenderComponent(ctx, w, "SHARED", map[string]any{"name": "from-templ"})
		})
	})
	reg := render.New(render.WithEngine(eng))
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, &http.Request{})
	rc.SetComponentRegistry(cr)
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineTempl, "page", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	assertShared(t, buf.String())
}

// TestFirstClass_FromHTMLTemplate renders the shared component from an
// html/template engine via the {{ component }} superpower.
func TestFirstClass_FromHTMLTemplate(t *testing.T) {
	cr := sharedDef(t)
	eng := NewHTMLTemplate()
	reg := render.New(render.WithEngine(eng))
	reg.AttachComponents(cr)

	src := `{{ component "SHARED" . }}`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	rc.Loader = reg.DefaultLoader()
	rc.SetComponentRegistry(cr)
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTMLTemplate, "page", map[string]any{"name": "from-htmltmpl"}); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	assertShared(t, buf.String())
}

// TestFirstClass_FromPlainHTML renders the shared component from the
// plain HTML SoA executor via <SHARED/>.
func TestFirstClass_FromPlainHTML(t *testing.T) {
	cr := sharedDef(t)
	eng := NewHTML()
	reg := render.New(render.WithEngine(eng))
	reg.AttachComponents(cr)

	src := `<div><SHARED/></div>`
	reg.SetDefaultLoader(render.NewMapLoader(map[string][]byte{"page": []byte(src)}))

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	rc.Loader = reg.DefaultLoader()
	rc.SetComponentRegistry(cr)
	defer render.ReleaseContext(rc)

	if err := reg.RenderNamed(rc, render.EngineHTML, "page", map[string]any{"name": "from-plain"}); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	assertShared(t, buf.String())
}

// assertShared checks the shared component's output: the typed prop
// name made it through.
func assertShared(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "class='shared'") {
		t.Errorf("missing shared component output: %q", out)
	}
	if !strings.Contains(out, "data-name='from-") {
		t.Errorf("typed props not applied: %q", out)
	}
}

// TestMemoize_SkipsReRender verifies a memoized component renders once
// and serves cached HTML on repeat keys.
func TestMemoize_SkipsReRender(t *testing.T) {
	cr := render.NewComponentRegistry()
	renderCount := 0
	cr.Define("slow").
		Render(func(c *render.ComponentContext) error {
			renderCount++
			_, err := c.WriteString("<b>slow output</b>")
			return err
		}).
		Register(cr)
	// Memoize by a constant key → render once, cache forever.
	cr.Memoize("slow", func(rc *render.RenderContext, data any) string { return "static" })

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	defer render.ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	for i := 0; i < 100; i++ {
		buf.Reset()
		// Dispatch by name with the framework context; the registry
		// resolves to the memo wrapper.
		ctx := render.WithContext(context.Background(), rc)
		if err := render.RenderComponent(ctx, bw, "slow", nil); err != nil {
			t.Fatal(err)
		}
		if err := bw.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if renderCount != 1 {
		t.Errorf("memoized component rendered %d times, want 1", renderCount)
	}
	if !strings.Contains(buf.String(), "<b>slow output</b>") {
		t.Errorf("memoized output wrong: %q", buf.String())
	}
}

// TestTempl_LayoutComposition verifies native templ composition:
// a layout component wraps a view component through
// Registry.RenderPageWith, and the layout can use the framework
// context (state) via the bridge.
func TestTempl_LayoutComposition(t *testing.T) {
	eng := NewTempl()
	eng.Register("view", func(data any) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "<h1>the view</h1>")
			return err
		})
	})
	eng.RegisterLayout("layouts/app", func(child templ.Component) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			// Layout uses the framework layer via the ctx bridge.
			rc := render.FromContext(ctx)
			if rc != nil {
				count, setCount := render.UseState(rc.ComponentState(), "layout-count", 0)
				setCount(count + 1)
				io.WriteString(w, "<html data-layout-count='"+itoaT(count)+"'><body>")
			} else {
				io.WriteString(w, "<html><body>")
			}
			if err := child.Render(ctx, w); err != nil {
				return err
			}
			_, err := io.WriteString(w, "</body></html>")
			return err
		})
	})

	reg := render.New(render.WithEngine(eng))
	cr := render.NewComponentRegistry()
	reg.AttachComponents(cr)

	var buf bytes.Buffer
	bw := render.AcquireWriter(&buf)
	defer render.ReleaseWriter(bw)
	rc := render.AcquireContext(bw, nil)
	rc.SetComponentRegistry(cr)
	defer render.ReleaseContext(rc)

	if err := reg.RenderPageWith(rc, render.EngineTempl.String(), "layouts/app", "view", nil); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<html data-layout-count='0'><body><h1>the view</h1></body></html>") {
		t.Errorf("templ layout composition wrong: %q", out)
	}
}
