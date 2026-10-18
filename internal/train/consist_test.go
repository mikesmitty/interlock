package train_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// rosterOf adds locomotives at addresses and returns their IDs.
func rosterOf(t *testing.T, b *bench, addresses ...uint16) []string {
	t.Helper()
	ids := make([]string, len(addresses))
	for i, a := range addresses {
		tr, err := b.m.Create(train.Entry{Name: "Unit " + traction.AddressName(a, a > 127), Address: a})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = tr.ID
	}
	return ids
}

func TestConsist(t *testing.T) {
	b := newBench(t)
	ids := rosterOf(t, b, 1001, 1002, 3)
	if _, err := b.m.CreateConsist(train.Consist{Name: "Alone", Members: []train.Member{{Train: ids[0]}}}); err == nil {
		t.Error("a consist of one locomotive should be refused")
	}
	c, err := b.m.CreateConsist(train.Consist{Name: "Big Three", Members: []train.Member{
		{Train: ids[0], Reverse: true, F0: true},
		{Train: ids[1], Reverse: true, F0: true},
		{Train: ids[2], Functions: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "big-three" || c.Kind != train.ConsistLCC || c.MadeUp || c.Members[0] != (train.Member{Train: ids[0]}) {
		t.Errorf("created = %+v", c)
	}

	c, err = b.m.MakeUp(b.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !c.MadeUp || !c.Attached || c.Problem != "" {
		t.Errorf("made up = %+v", c)
	}
	if tr, _ := b.m.Get(ids[1]); tr.Consist != c.ID {
		t.Errorf("member's consist = %q", tr.Consist)
	}

	// The lead's speed reaches the members, the other way for one that
	// faces backward.
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 40, train.Forward); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the members to follow the lead", func() bool {
		return b.station.DecoderState(1002, true).Speed == traction.Speed{Value: 40, Reverse: true} &&
			b.station.DecoderState(3, false).Speed == traction.Speed{Value: 40}
	})
	if tr, _ := b.m.Get(ids[1]); tr.Speed != 40 || tr.Direction != train.Reverse {
		t.Errorf("member's state = %v %s", tr.Speed, tr.Direction)
	}

	// Functions reach only the members that follow them.
	if _, err := b.m.SetFunction(b.ctx, ids[0], 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.SetFunction(b.ctx, ids[0], 2, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "functions to be passed on", func() bool {
		return b.station.DecoderState(1002, true).Functions[0] && b.station.DecoderState(3, false).Functions[2]
	})
	if b.station.DecoderState(1002, true).Functions[2] || b.station.DecoderState(3, false).Functions[0] {
		t.Error("a member got a function it does not follow")
	}

	// A member's speed comes from the lead.
	if _, err := b.m.SetSpeed(b.ctx, ids[1], 10, ""); err == nil || !strings.Contains(err.Error(), "Big Three") {
		t.Errorf("driving a member = %v", err)
	}
	if _, err := b.m.UpdateConsist(c.ID, func(c *train.Consist) error { c.Members = c.Members[:2]; return nil }); err == nil {
		t.Error("changing the locomotives of a made-up consist should be refused")
	}
	if c, err := b.m.UpdateConsist(c.ID, func(c *train.Consist) error { c.Name = "Three of a Kind"; return nil }); err != nil || c.Name != "Three of a Kind" {
		t.Errorf("rename = %+v, %v", c, err)
	}
	if err := b.m.Delete(b.ctx, ids[2]); err == nil {
		t.Error("removing a member of a made-up consist from the roster should be refused")
	}

	// It breaks up only when stopped.
	if _, err := b.m.BreakUp(b.ctx, c.ID); err == nil {
		t.Error("breaking up a moving consist should be refused")
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 0, ""); err != nil {
		t.Fatal(err)
	}
	c, err = b.m.BreakUp(b.ctx, c.ID)
	if err != nil || c.MadeUp || c.Attached {
		t.Fatalf("broken up = %+v, %v", c, err)
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 20, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the lead to move", func() bool { return b.station.DecoderState(1001, true).Speed.Value == 20 })
	if s := b.station.DecoderState(1002, true).Speed.Value; s != 0 {
		t.Errorf("a detached member moved to %v", s)
	}

	// The file keeps the consist; a locomotive leaving the roster leaves it.
	f, err := train.OpenFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Consists(); len(got) != 1 || got[0].Name != "Three of a Kind" || len(got[0].Members) != 3 || got[0].MadeUp {
		t.Errorf("file = %+v", got)
	}
	if err := b.m.Delete(b.ctx, ids[2]); err != nil {
		t.Fatal(err)
	}
	if c, _ := b.m.Consist(c.ID); len(c.Members) != 2 {
		t.Errorf("members after a removal = %+v", c.Members)
	}
	if err := b.m.DeleteConsist(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(b.m.Consists()) != 0 {
		t.Error("the consist was not deleted")
	}
}

// After the command station restarts, the consist is put together again
// before it is next driven.
func TestConsistAgain(t *testing.T) {
	b := newBench(t)
	ids := rosterOf(t, b, 1001, 1002)
	c, err := b.m.CreateConsist(train.Consist{Name: "Pair", Members: []train.Member{{Train: ids[0]}, {Train: ids[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.MakeUp(b.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	lead, _ := b.m.Get(ids[0])
	member, _ := b.m.Get(ids[1])
	// The command station forgets the consist when it restarts.
	if _, err := b.local.TractionCommand(b.ctx, lead.Node, traction.DetachConsist(member.Node), true); err != nil {
		t.Fatal(err)
	}
	b.m.HandleNode(registry.Info{ID: lead.Node, Online: false})
	if c, _ := b.m.Consist(c.ID); !c.MadeUp || c.Attached {
		t.Errorf("after the lead left = %+v", c)
	}
	b.m.HandleNode(registry.Info{ID: lead.Node, Online: true})
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 30, train.Forward); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the member to follow again", func() bool { return b.station.DecoderState(1002, true).Speed.Value == 30 })
	if c, _ := b.m.Consist(c.ID); !c.Attached {
		t.Errorf("after driving = %+v", c)
	}

	// A new manager over the same file finds the consist made up, and
	// puts it together again when it is driven.
	f, err := train.OpenFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	m := train.New(train.Options{Bus: b.local, File: f, SearchWindow: 150 * time.Millisecond})
	if c, _ := m.Consist(c.ID); !c.MadeUp || c.Attached {
		t.Errorf("after a restart = %+v", c)
	}
	if _, err := m.SetSpeed(b.ctx, ids[0], 0, ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := m.Consist(c.ID); !c.Attached {
		t.Errorf("after driving again = %+v", c)
	}
}

// A consist the command station cannot hold is left apart, with nothing
// attached.
func TestConsistTooBig(t *testing.T) {
	b := newBench(t)
	ids := rosterOf(t, b, 1001, 1002, 1003, 1004, 1005, 1006)
	members := make([]train.Member, len(ids))
	for i, id := range ids {
		members[i] = train.Member{Train: id}
	}
	c, err := b.m.CreateConsist(train.Consist{Name: "Long Haul", Members: members})
	if err != nil {
		t.Fatal(err)
	}
	c, err = b.m.MakeUp(b.ctx, c.ID)
	if err == nil || c.MadeUp || !strings.Contains(c.Problem, "would not add Unit 1006") || !errors.Is(err, train.ErrConsistRefused) {
		t.Fatalf("made up = %+v, %v", c, err)
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 25, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the lead to move", func() bool { return b.station.DecoderState(1001, true).Speed.Value == 25 })
	if s := b.station.DecoderState(1002, true).Speed.Value; s != 0 {
		t.Errorf("a member of the failed consist moved to %v", s)
	}
}

// A locomotive runs in one made-up consist at a time, and a DCC consist's
// address must be free.
func TestConsistChecks(t *testing.T) {
	b := newBench(t)
	ids := rosterOf(t, b, 1001, 1002, 1003, 5)
	a, err := b.m.CreateConsist(train.Consist{Name: "A", Members: []train.Member{{Train: ids[0]}, {Train: ids[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.m.CreateConsist(train.Consist{Name: "B", Members: []train.Member{{Train: ids[2]}, {Train: ids[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.MakeUp(b.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.MakeUp(b.ctx, other.ID); err == nil || !strings.Contains(err.Error(), "runs in A") {
		t.Errorf("a locomotive in two consists = %v", err)
	}
	if _, err := b.m.CreateConsist(train.Consist{Name: "D", Kind: train.ConsistDCC, Address: 5, Members: []train.Member{{Train: ids[0]}, {Train: ids[2]}}}); err == nil {
		t.Error("a DCC consist at a locomotive's address should be refused")
	}
	if _, err := b.m.CreateConsist(train.Consist{Name: "D", Kind: train.ConsistDCC, Members: []train.Member{{Train: ids[0]}, {Train: ids[2]}}}); err == nil {
		t.Error("a DCC consist without an address should be refused")
	}
	if _, err := b.m.CreateConsist(train.Consist{Name: "Twins", Members: []train.Member{{Train: ids[0]}, {Train: ids[0]}}}); err == nil {
		t.Error("a locomotive twice in a consist should be refused")
	}
}
