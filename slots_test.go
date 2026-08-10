package render

import "testing"

func TestSlots_Build(t *testing.T) {
	// Slots is just a map[string]string; verify construction.
	s := Slots{
		"header": "<h1>Title</h1>",
		"body":   "<p>Body</p>",
		"footer": "<button>Submit</button>",
	}
	if len(s) != 3 {
		t.Errorf("expected 3 slots, got %d", len(s))
	}
	if s["header"] != "<h1>Title</h1>" {
		t.Errorf("header slot wrong: %q", s["header"])
	}
}

func TestSlots_Empty(t *testing.T) {
	s := Slots{}
	if len(s) != 0 {
		t.Errorf("empty slots: got %d", len(s))
	}
}

func TestCollectSlots_GroupsByName(t *testing.T) {
	// Build a node stream with a parent component and two slot
	// children with different names.
	b := NewBuilder()
	b.AppendNode("#document", FlagFragment)
	parent := b.AppendNode("CARD", FlagComponent)
	b.stream.ComponentName[parent] = "CARD"

	// Slot 1: header
	slot1 := b.AppendNode("header", 0)
	b.stream.ComponentName[slot1] = "header"
	h1 := b.AppendText([]byte("<h1>Title</h1>"), true)
	b.SetParent(h1, slot1)
	b.SetChildren(slot1, uint32(h1), uint32(h1+1))
	b.SetParent(slot1, parent)

	// Slot 2: footer
	slot2 := b.AppendNode("footer", 0)
	b.stream.ComponentName[slot2] = "footer"
	f1 := b.AppendText([]byte("<button>OK</button>"), true)
	b.SetParent(f1, slot2)
	b.SetChildren(slot2, uint32(f1), uint32(f1+1))
	b.SetParent(slot2, parent)

	b.SetChildren(parent, uint32(slot1), uint32(slot2+1))

	// Stream() finalises the FirstChild/NextSibling links that
	// collectSlots walks.
	stream := b.Stream()
	slots := collectSlots(nil, stream, parent, nil)
	if len(slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(slots))
	}
	if slots["header"] != "<h1>Title</h1>" {
		t.Errorf("header slot: got %q want %q", slots["header"], "<h1>Title</h1>")
	}
	if slots["footer"] != "<button>OK</button>" {
		t.Errorf("footer slot: got %q want %q", slots["footer"], "<button>OK</button>")
	}
}
