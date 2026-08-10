package render

import (
	"testing"
)

func TestNodeStreamBuilder_Append(t *testing.T) {
	b := NewBuilder()
	root := b.AppendNode("#document", FlagFragment)
	if root != 0 {
		t.Fatalf("root index: got %d want 0", root)
	}

	div := b.AppendNode("div", 0)
	if div != 1 {
		t.Fatalf("div index: got %d want 1", div)
	}

	text := b.AppendText([]byte("hello"), false)
	if text != 2 {
		t.Fatalf("text index: got %d want 2", text)
	}

	for i := 0; i < b.stream.Count; i++ {
		if i >= len(b.stream.Tag) {
			t.Fatalf("Tag slot %d missing", i)
		}
		if i >= len(b.stream.Flags) {
			t.Fatalf("Flags slot %d missing", i)
		}
	}
}

func TestNodeStreamBuilder_SetChildren(t *testing.T) {
	b := NewBuilder()
	root := b.AppendNode("#document", FlagFragment)
	div := b.AppendNode("div", 0)
	txt := b.AppendText([]byte("hi"), false)

	b.SetChildren(root, 1, 3) // div, txt
	b.SetChildren(div, uint32(txt), uint32(txt+1))

	if b.stream.Flags[root]&FlagHasChildren == 0 {
		t.Errorf("root should have FlagHasChildren")
	}
	if b.stream.Flags[div]&FlagHasChildren == 0 {
		t.Errorf("div should have FlagHasChildren")
	}
}

func TestNodeStreamBuilder_SetAttrs(t *testing.T) {
	b := NewBuilder()
	div := b.AppendNode("div", 0)
	b.SetAttrs(div, []string{"id", "class"}, []any{"main", "container"})

	if got := b.stream.NumAttr(div); got != 2 {
		t.Errorf("attr count: got %d want 2", got)
	}
	if got := b.stream.AttrKey(div, 0); got != "id" {
		t.Errorf("attr key 0: got %q want %q", got, "id")
	}
	if got := b.stream.AttrVal(div, 1); got != "container" {
		t.Errorf("attr val 1: got %v want %v", got, "container")
	}
}

func TestNodeStreamBuilder_VoidFlag(t *testing.T) {
	b := NewBuilder()
	br := b.AppendNode("br", 0)
	if !voidTags["br"] {
		t.Fatal("br is a void tag")
	}
	_ = br
}

func TestNodeStream_Reset(t *testing.T) {
	b := NewBuilder()
	b.AppendNode("div", 0)
	b.AppendText([]byte("x"), false)
	b.SetChildren(0, 1, 2)

	s := b.Stream()
	if s.Count != 2 {
		t.Fatalf("count: got %d want 2", s.Count)
	}
	s.Reset()
	if s.Count != 0 {
		t.Errorf("after reset count: got %d want 0", s.Count)
	}
	if len(s.Tag) != 0 {
		t.Errorf("after reset Tag: got %d want 0", len(s.Tag))
	}
}
