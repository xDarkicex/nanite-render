package render

import (
	"testing"
)

type NavbarProps struct {
	Theme string `nanite:"theme"`
	Size  int    `nanite:"size"`
	User  string `nanite:"user"`
	Label string // defaults to lowercased name
}

type NestedProps struct {
	Inner *InnerProps
}

type InnerProps struct {
	Value int `nanite:"value"`
}

func TestBindProps_Basic(t *testing.T) {
	data := map[string]any{
		"theme": "dark",
		"size":  42,
		"user":  "alice",
		"label": "primary",
	}
	props := BindProps[NavbarProps](data)
	if props.Theme != "dark" {
		t.Errorf("Theme: got %q", props.Theme)
	}
	if props.Size != 42 {
		t.Errorf("Size: got %d", props.Size)
	}
	if props.User != "alice" {
		t.Errorf("User: got %q", props.User)
	}
	if props.Label != "primary" {
		t.Errorf("Label: got %q", props.Label)
	}
}

func TestBindProps_Defaults(t *testing.T) {
	// Missing keys: zero value.
	props := BindProps[NavbarProps](map[string]any{
		"theme": "dark",
	})
	if props.Theme != "dark" {
		t.Errorf("Theme: got %q", props.Theme)
	}
	if props.Size != 0 {
		t.Errorf("Size (default 0): got %d", props.Size)
	}
	if props.User != "" {
		t.Errorf("User (default empty): got %q", props.User)
	}
	if props.Label != "" {
		t.Errorf("Label (default empty): got %q", props.Label)
	}
}

func TestBindProps_DefaultKey(t *testing.T) {
	// No tag → use lowercased field name.
	props := BindProps[NavbarProps](map[string]any{
		"label": "auto-named",
	})
	if props.Label != "auto-named" {
		t.Errorf("default key: got %q", props.Label)
	}
}

func TestBindProps_NestedPointer(t *testing.T) {
	data := map[string]any{
		"inner": map[string]any{
			"value": 99,
		},
	}
	props := BindProps[NestedProps](data)
	if props.Inner == nil {
		t.Fatal("Inner is nil")
	}
	if props.Inner.Value != 99 {
		t.Errorf("Inner.Value: got %d", props.Inner)
	}
}

func TestBindProps_NilData(t *testing.T) {
	props := BindProps[NavbarProps](nil)
	if props.Theme != "" {
		t.Errorf("nil Theme: got %q", props.Theme)
	}
	if props.Size != 0 {
		t.Errorf("nil Size: got %d", props.Size)
	}
}

func TestBindProps_TypeMismatch(t *testing.T) {
	// Wrong type — field stays at zero.
	props := BindProps[NavbarProps](map[string]any{
		"theme": 42, // int, but field is string
	})
	if props.Theme != "" {
		t.Errorf("type mismatch: got %q want empty", props.Theme)
	}
}
