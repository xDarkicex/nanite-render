package render

import "testing"

func TestTagDebug(t *testing.T) {
	r := NewRegistry()
	r.SetTag("layout", "x", "chrome")
	r.SetTag("view", "x", "content")

	m := r.tags.Load()
	if m == nil {
		t.Fatal("tags map is nil")
	}
	for tag, set := range *m {
		t.Logf("tag %q has %d pairs", tag, len(set))
		for k := range set {
			t.Logf("  pair: %+v", k)
		}
	}

	n := r.InvalidateTag("chrome")
	t.Logf("InvalidateTag chrome returned %d", n)
}
