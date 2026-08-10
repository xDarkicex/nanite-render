package render

import (
	"errors"
	"testing"
)

// Test sentinel errors are wrapped correctly via errors.Is.
func TestSentinelErrors_Is(t *testing.T) {
	// Setup a registry with no engines.
	r := NewRegistry()
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)
	rc.Loader = nil // force loader-missing path

	tests := []struct {
		name    string
		gotErr  error
		wantErr error
	}{
		{
			name:    "engine not found on Part",
			gotErr:  mustErr(func() error { _, e := r.Part(rc, "missing", "x"); return e }()),
			wantErr: ErrEngineNotFound,
		},
		{
			name:    "engine not found on Render",
			gotErr:  mustErr(func() error { return r.Render(rc, "missing", "x", nil) }()),
			wantErr: ErrEngineNotFound,
		},
		{
			name: "loader missing on Part",
			gotErr: mustErr(func() error {
				r.AddEngine(newStubEngine("stub"))
				rc.Loader = nil
				_, e := r.Part(rc, "stub", "y")
				return e
			}()),
			wantErr: ErrLoaderMissing,
		},
		{
			name:    "template not found on Part",
			gotErr:  mustErr(func() error {
				eng := newStubEngine("stub")
				r.AddEngine(eng)
				rc.Loader = func(name string) ([]byte, error) { return nil, errors.New("not on disk") }
				_, e := r.Part(rc, "stub", "x")
				return e
			}()),
			wantErr: ErrTemplateNotFound,
		},
		{
			name:    "layout missing on RenderPage",
			gotErr:  mustErr(func() error { return r.RenderPage(rc, "", "view", nil) }()),
			wantErr: ErrLayoutMissing,
		},
		{
			name:    "invalid RenderPage (no view)",
			gotErr:  mustErr(func() error { return r.RenderPage(rc, "layout", "", nil) }()),
			wantErr: ErrRenderPageInvalid,
		},
		{
			name:    "invalid RenderComposition (no engine)",
			gotErr:  mustErr(func() error { return r.RenderComposition(rc, Composition{}, nil) }()),
			wantErr: ErrRenderPageInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gotErr == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(tc.gotErr, tc.wantErr) {
				t.Errorf("errors.Is(%q, %q) = false; want true",
					tc.gotErr.Error(), tc.wantErr.Error())
			}
		})
	}
}

// Test ParseError is detectable via errors.As.
func TestParseError_As(t *testing.T) {
	src := []byte("<<")
	_, err := ParseHTML(src, "test")
	if err == nil {
		t.Fatal("expected parse error")
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As did not match *ParseError: %T", err)
	}
	if pe.Name != "test" {
		t.Errorf("Name: got %q want %q", pe.Name, "test")
	}
}

// Test MustPreloadParts panics on engine-not-found and the panic
// value is the wrapped error, recoverable and inspectable via
// errors.Is.
func TestMustPreloadParts_PanicsOnEngineNotFound(t *testing.T) {
	r := NewRegistry() // no engines
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("panic value is not error: %T", r)
		}
		if !errors.Is(err, ErrEngineNotFound) {
			t.Errorf("errors.Is did not match ErrEngineNotFound: %v", err)
		}
	}()

	r.MustPreloadParts(rc, "missing", "x")
}

// Test MustPreloadParts panics on template-not-found.
func TestMustPreloadParts_PanicsOnTemplateNotFound(t *testing.T) {
	r := NewRegistry(newStubEngine("stub"))
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)
	rc.Loader = func(name string) ([]byte, error) {
		return nil, errors.New("not on disk")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("panic value is not error: %T", r)
		}
		if !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("errors.Is did not match ErrTemplateNotFound: %v", err)
		}
	}()

	r.MustPreloadParts(rc, "stub", "missing")
}

// Test MustPreloadParts does not panic on success.
func TestMustPreloadParts_OK(t *testing.T) {
	r := NewRegistry(newStubEngine("stub"))
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)
	rc.Loader = func(name string) ([]byte, error) {
		return []byte("x"), nil
	}

	// Should not panic.
	r.MustPreloadParts(rc, "stub", "a", "b", "c")

	// All three should now be in the cache.
	cache := r.Cache()
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := cache.Get("stub", name); !ok {
			t.Errorf("expected %q in cache after MustPreloadParts", name)
		}
	}
}

// Test MustPreloadParts is recoverable with errors.Is. Demonstrates
// the typical pattern for boot-time validation with error dispatch.
func TestMustPreloadParts_RecoveryPattern(t *testing.T) {
	r := NewRegistry() // no engines
	rc := AcquireContext(nil, nil)
	defer ReleaseContext(rc)

	var caughtErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					caughtErr = e
				}
			}
		}()
		r.MustPreloadParts(rc, "missing", "x")
	}()

	if caughtErr == nil {
		t.Fatal("expected recovered error")
	}
	if !errors.Is(caughtErr, ErrEngineNotFound) {
		t.Errorf("recovered error is not ErrEngineNotFound: %v", caughtErr)
	}
}

// mustErr is a small helper to extract an error from a function
// call expression in a table-driven test.
func mustErr(e error) error {
	if e == nil {
		panic("mustErr: nil error")
	}
	return e
}

func newStubEngine(name string) *stubEngine { return &stubEngine{name: name} }
