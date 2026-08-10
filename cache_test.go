package render

import (
	"testing"
)

func TestCache_PutGet(t *testing.T) {
	c := NewCache(64)
	p := &Program{Name: "test", Engine: "test"}
	c.Put("test", "test", p)

	got, ok := c.Get("test", "test")
	if !ok {
		t.Fatal("cache miss after put")
	}
	if got != p {
		t.Errorf("got %p want %p", got, p)
	}
}

func TestCache_Miss(t *testing.T) {
	c := NewCache(64)
	if _, ok := c.Get("missing", "missing"); ok {
		t.Error("expected miss on empty cache")
	}
}

func TestCache_Overwrite(t *testing.T) {
	c := NewCache(64)
	p1 := &Program{Name: "v1"}
	p2 := &Program{Name: "v2"}
	c.Put("k", "v1", p1)
	c.Put("k", "v2", p2)
	got, ok := c.Get("k", "v2")
	if !ok {
		t.Fatal("expected hit")
	}
	if got != p2 {
		t.Errorf("got %p want %p", got, p2)
	}
}

func TestCache_Delete(t *testing.T) {
	c := NewCache(64)
	p := &Program{Name: "x"}
	c.Put("k", "x", p)
	c.Delete("k", "x")
	if _, ok := c.Get("k", "x"); ok {
		t.Error("expected miss after delete")
	}
}

func TestCache_Tags(t *testing.T) {
	r := NewRegistry()
	c := r.Cache()
	c.Put("layout", "x", &Program{Name: "layout"})
	c.Put("view", "x", &Program{Name: "view"})
	r.SetTag("layout", "x", "chrome")
	r.SetTag("view", "x", "content")

	if n := r.InvalidateTag("chrome"); n != 1 {
		t.Errorf("invalidated %d, want 1", n)
	}
	if _, ok := c.Get("layout", "x"); ok {
		t.Error("expected layout miss after tag invalidation")
	}
	if _, ok := c.Get("view", "x"); !ok {
		t.Error("expected view hit")
	}
}

func TestCache_Stats(t *testing.T) {
	c := NewCache(64)
	p := &Program{Name: "x"}
	c.Put("k", "x", p)
	c.Get("k", "x")
	c.Get("missing", "missing")
	stats := c.Stats()
	if stats.Hits+stats.Misses == 0 {
		t.Error("expected non-zero stats")
	}
}

func TestCache_Close(t *testing.T) {
	c := NewCache(64)
	c.Close()
	c.Put("k", "x", &Program{})
	if _, ok := c.Get("k", "x"); ok {
		t.Error("Get after Close should miss")
	}
}

func TestCache_PowerOfTwo(t *testing.T) {
	c := NewCache(100)
	if c.capacity != 128 {
		t.Errorf("capacity: got %d want 128", c.capacity)
	}
}

func TestFNV1a64(t *testing.T) {
	if got := fnv1a64(""); got != 0xcbf29ce484222325 {
		t.Errorf("empty: got %x want cbf29ce484222325", got)
	}
	if got := fnv1a64("a"); got != 0xaf63dc4c8601ec8c {
		t.Errorf("a: got %x want af63dc4c8601ec8c", got)
	}
	if got := fnv1a64("foobar"); got != 0x85944171f73967e8 {
		t.Errorf("foobar: got %x want 85944171f73967e8", got)
	}
}

func TestFNVPair(t *testing.T) {
	// Same pair → same hash.
	h1 := fnvPair("jade", "x")
	h2 := fnvPair("jade", "x")
	if h1 != h2 {
		t.Error("FNV pair not deterministic")
	}
	// Different name → different hash.
	if fnvPair("jade", "x") == fnvPair("jade", "y") {
		t.Error("FNV pair collided on different names")
	}
}

func TestSplitKey(t *testing.T) {
	// splitKey was removed when the tag map switched to typed pairs.
	// The cache now stores (engine, name) directly with no string
	// round-trip; this test is intentionally a no-op marker.
}
