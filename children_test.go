package render

import (
	"bytes"
	"testing"
)

// TestComponentWithChildren_RenderWithChildren verifies that a
// component implementing ComponentWithChildren receives the
// pre-rendered children list and can iterate it.
func TestComponentWithChildren_RenderWithChildren(t *testing.T) {
	cr := NewComponentRegistry()
	cr.Register("WRAP", ComponentWithChildrenFunc(func(w ByteWriter, rc *RenderContext, data any, children Children) error {
		_, err := w.WriteString("<wrap>")
		if err != nil {
			return err
		}
		for _, c := range children {
			if _, err := w.WriteString(c); err != nil {
				return err
			}
		}
		_, err = w.WriteString("</wrap>")
		return err
	}))

	// Build a node stream with a WRAP component that has two
	// child text nodes.
	b := NewBuilder()
	root := b.AppendNode("#document", FlagFragment)
	wrap := b.AppendNode("WRAP", FlagComponent)
	b.stream.ComponentName[wrap] = "WRAP"
	t1 := b.AppendText([]byte("hello "), false)
	t2 := b.AppendText([]byte("world"), false)
	b.SetParent(wrap, root)
	b.SetParent(t1, wrap)
	b.SetParent(t2, wrap)
	b.SetChildren(root, 1, 2) // [wrap, t1, t2)
	b.SetChildren(wrap, 2, 4)  // [t1, t2)

	c, _ := cr.Lookup("WRAP")
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	rc := AcquireContext(bw, nil)
	defer ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	// Test renderComponent directly with children
	children := Children{"hello ", "world"}
	if err := renderComponent(c, bw, rc, nil, children); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if out != "<wrap>hello world</wrap>" {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestComponentWithChildren_DataMap verifies that components NOT
// implementing ComponentWithChildren receive children via the
// data map (data[ChildrenDataKey]).
func TestComponentWithChildren_DataMap(t *testing.T) {
	cr := NewComponentRegistry()
	cr.Register("PLAIN", ComponentFunc(func(w ByteWriter, rc *RenderContext, data any) error {
		m, _ := data.(map[string]any)
		children, _ := m[ChildrenDataKey].(Children)
		for _, c := range children {
			if _, err := w.WriteString(c); err != nil {
				return err
			}
		}
		return nil
	}))

	c, _ := cr.Lookup("PLAIN")
	var buf bytes.Buffer
	bw := AcquireWriter(&buf)
	defer ReleaseWriter(bw)
	rc := AcquireContext(bw, nil)
	defer ReleaseContext(rc)
	rc.SetComponentRegistry(cr)

	children := Children{"a", "b"}
	if err := renderComponent(c, bw, rc, nil, children); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "ab" {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

// ComponentWithChildrenFunc is a function adapter for
// ComponentWithChildren.
type ComponentWithChildrenFunc func(w ByteWriter, rc *RenderContext, data any, children Children) error

// Render implements Component.
func (f ComponentWithChildrenFunc) Render(w ByteWriter, rc *RenderContext, data any) error {
	return f(w, rc, data, nil)
}

// RenderWithChildren implements ComponentWithChildren.
func (f ComponentWithChildrenFunc) RenderWithChildren(w ByteWriter, rc *RenderContext, data any, children Children) error {
	return f(w, rc, data, children)
}
