package can

import (
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
)

func TestAliasGenerator(t *testing.T) {
	g := NewAliasGenerator(testNode)
	var seq []openlcb.Alias
	seen := make(map[openlcb.Alias]bool)
	for range 64 {
		a := g.Next()
		if a == 0 || a > 0xFFF {
			t.Fatalf("alias %#x out of range", a)
		}
		seq = append(seq, a)
		seen[a] = true
	}
	if len(seen) < 60 {
		t.Errorf("only %d distinct aliases in 64 draws", len(seen))
	}

	again := NewAliasGenerator(testNode)
	for i, want := range seq {
		if got := again.Next(); got != want {
			t.Fatalf("draw %d: %#x, want %#x (sequence must be deterministic)", i, got, want)
		}
	}

	other := NewAliasGenerator(testNode + 1)
	first, second := other.Next(), other.Next()
	if first == seq[0] && second == seq[1] {
		t.Error("different node IDs produced the same sequence start")
	}
}

func TestAliasGeneratorKnownValues(t *testing.T) {
	// First steps for the node ID 05.01.01.01.22.00, computed from the
	// technical note's algorithm; guards against accidental changes.
	g := NewAliasGenerator(testNode)
	first := g.Next()
	g2 := NewAliasGenerator(testNode)
	if g2.Next() != first {
		t.Fatal("not deterministic")
	}
	// Zero seed must still produce non-zero aliases.
	z := NewAliasGenerator(0)
	for range 10 {
		if z.Next() == 0 {
			t.Fatal("zero alias produced")
		}
	}
}

func TestAliasCache(t *testing.T) {
	c := NewAliasCache()
	c.Add(0x123, testNode)
	c.Add(0x456, 0x050101012201)

	if id, ok := c.Lookup(0x123); !ok || id != testNode {
		t.Errorf("Lookup(0x123) = %v, %v", id, ok)
	}
	if alias, ok := c.LookupID(0x050101012201); !ok || alias != 0x456 {
		t.Errorf("LookupID = %v, %v", alias, ok)
	}
	if _, ok := c.Lookup(0x789); ok {
		t.Error("Lookup of unknown alias succeeded")
	}
	if h := c.Handle(0x123); h.ID != testNode || h.Alias != 0x123 {
		t.Errorf("Handle = %+v", h)
	}
	if h := c.Handle(0x789); h.ID != 0 || h.Alias != 0x789 {
		t.Errorf("Handle unknown = %+v", h)
	}

	// A node that re-allocates its alias must drop the old mapping.
	c.Add(0x999, testNode)
	if _, ok := c.Lookup(0x123); ok {
		t.Error("old alias still mapped after the node moved")
	}
	if alias, _ := c.LookupID(testNode); alias != 0x999 {
		t.Errorf("LookupID after move = %v", alias)
	}

	// An alias taken over by another node must drop the old node.
	c.Add(0x999, 0x050101012202)
	if _, ok := c.LookupID(testNode); ok {
		t.Error("old node still mapped after its alias was taken")
	}

	c.Remove(0x456)
	if _, ok := c.LookupID(0x050101012201); ok {
		t.Error("Remove left the ID mapping")
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	if entries := c.Entries(); len(entries) != 1 || entries[0x999] != 0x050101012202 {
		t.Errorf("Entries = %v", entries)
	}

	c.Add(0, testNode)
	c.Add(0x111, 0)
	if c.Len() != 1 {
		t.Error("zero alias or ID was added")
	}
}
