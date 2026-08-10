package render

import (
	"strings"
	"testing"
)

// TestEngineName_Constants verifies the constants map to the engine
// names used for cache keys and lookups.
func TestEngineName_Constants(t *testing.T) {
	tests := []struct {
		name EngineName
		want string
	}{
		{EngineJade, "jade"},
		{EngineHTML, "html"},
		{EngineHTMLTemplate, "html-template"},
		{EngineTempl, "templ"},
	}
	for _, tt := range tests {
		if tt.name.String() != tt.want {
			t.Errorf("EngineName(%q).String() = %q, want %q", tt.name, tt.name.String(), tt.want)
		}
		if string(tt.name) != tt.want {
			t.Errorf("string(EngineName) = %q, want %q", tt.name, tt.want)
		}
	}
}

// TestCustomEngine verifies a custom engine name round-trips.
func TestCustomEngine(t *testing.T) {
	got := CustomEngine("my-engine")
	if got.String() != "my-engine" {
		t.Errorf("CustomEngine = %q, want %q", got, "my-engine")
	}
}

// TestRenderNamed_UnknownTyped verifies an unknown typed engine
// returns ErrEngineNotFound (the same as the string path).
func TestRenderNamed_UnknownTyped(t *testing.T) {
	reg := NewRegistry()
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)

	err := reg.RenderNamed(rc, CustomEngine("nope"), "view", nil)
	if err == nil {
		t.Fatal("expected error for unknown engine")
	}
	if !strings.Contains(err.Error(), "engine not found") {
		t.Errorf("expected ErrEngineNotFound, got: %v", err)
	}
}

// stubCustomEngine is a minimal Engine with a custom name.
type stubCustomEngine struct {
	name string
}

func (s *stubCustomEngine) Name() string { return s.name }
func (s *stubCustomEngine) Compile([]byte, string) (*Program, error) {
	return &Program{Engine: s.name}, nil
}
func (s *stubCustomEngine) Execute(*Program, ByteWriter, *RenderContext, any) error {
	return nil
}

// TestRenderBuilder_CustomEngineViaValue verifies a custom engine
// is usable via EngineInstance.
func TestRenderBuilder_CustomEngineViaValue(t *testing.T) {
	reg := NewRegistry(&stubCustomEngine{name: "my-engine"})
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)
	rc.Loader = func(name string) ([]byte, error) { return []byte("x"), nil }

	err := reg.Page(rc).
		EngineInstance(&stubCustomEngine{name: "my-engine"}).
		View("view").
		Render()
	if err != nil {
		t.Fatalf("custom engine render: %v", err)
	}
}

// TestRenderBuilder_EngineTypedNilContext renders with a typed
// constant against a nil context — errors cleanly, no panic.
func TestRenderBuilder_EngineTypedNilContext(t *testing.T) {
	reg := NewRegistry()
	err := reg.Page(nil).Engine(EngineHTML).View("view").Render()
	if err == nil {
		t.Fatal("expected error for nil RenderContext")
	}
}
