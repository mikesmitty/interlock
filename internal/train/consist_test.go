package train_test

import (
	"errors"
	"slices"
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

func TestDCCConsist(t *testing.T) {
	b := newBench(t)
	ids := rosterOf(t, b, 1001, 1002, 3)
	c, err := b.m.CreateConsist(train.Consist{Name: "Coal Drag", Kind: train.ConsistDCC, Address: 10, Members: []train.Member{
		{Train: ids[0]},
		{Train: ids[1], Reverse: true, F0: true},
		{Train: ids[2], Functions: true},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// A locomotive that is moving would run off at that speed once the
	// consist is broken up.
	if _, err := b.m.SetSpeed(b.ctx, ids[2], 15, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.MakeUp(b.ctx, c.ID); err == nil || !strings.Contains(err.Error(), "stop Unit 3") {
		t.Fatalf("made up with a locomotive moving: %v", err)
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[2], 0, ""); err != nil {
		t.Fatal(err)
	}

	// Each decoder takes the consist address, the way it faces, and the
	// functions it follows; the lead takes them all.
	c, err = b.m.MakeUp(b.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !c.MadeUp || !c.Attached || c.Problem != "" {
		t.Errorf("made up = %+v", c)
	}
	for _, d := range []struct {
		address uint16
		long    bool
		cv19    byte
		cv21    byte
		cv22    byte
	}{
		{1001, true, 10, 0xFF, 0x3F},
		{1002, true, 0x80 | 10, 0, 0x03},
		{3, false, 10, 0xFF, 0x3C},
	} {
		cvs := b.station.DecoderState(d.address, d.long).CVs
		if cvs[19] != d.cv19 || cvs[21] != d.cv21 || cvs[22] != d.cv22 {
			t.Errorf("decoder %d: CV19 %#x, CV21 %#x, CV22 %#x", d.address, cvs[19], cvs[21], cvs[22])
		}
	}

	// Driving the lead drives the consist address.
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 40, train.Forward); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the decoders to take the speed", func() bool {
		return b.station.DecoderState(1001, true).Speed == traction.Speed{Value: 40} &&
			b.station.DecoderState(1002, true).Speed == traction.Speed{Value: 40, Reverse: true} &&
			b.station.DecoderState(3, false).Speed == traction.Speed{Value: 40}
	})
	if tr, _ := b.m.Get(ids[1]); tr.Speed != 40 || tr.Direction != train.Reverse || tr.Consist != c.ID {
		t.Errorf("member's state = %v %s in %q", tr.Speed, tr.Direction, tr.Consist)
	}
	if tr, _ := b.m.Get("10"); tr.Name != "Coal Drag" || tr.Speed != 40 {
		t.Errorf("consist address = %q at %v", tr.Name, tr.Speed)
	}
	if i := slices.IndexFunc(b.m.List(), func(tr train.Train) bool { return tr.ID == "10" }); i < 0 || b.m.List()[i].Name != "Coal Drag" {
		t.Errorf("the consist address is not listed by the consist's name")
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[2], 60, ""); err == nil {
		t.Error("a member's speed should be refused")
	}

	// The lead's functions up to F12 go to the consist address, where each
	// decoder takes the ones it follows; higher ones stay the lead's.
	for _, fn := range []int{0, 2, 20} {
		if _, err := b.m.SetFunction(b.ctx, ids[0], fn, true); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "functions at the consist address", func() bool {
		lead, back, third := b.station.DecoderState(1001, true), b.station.DecoderState(1002, true), b.station.DecoderState(3, false)
		return lead.Functions[0] && lead.Functions[2] && lead.Functions[20] && back.Functions[0] && third.Functions[2]
	})
	if back, third := b.station.DecoderState(1002, true), b.station.DecoderState(3, false); back.Functions[2] || third.Functions[0] || third.Functions[20] {
		t.Errorf("functions reached decoders that do not follow them: %v, %v", back.Functions, third.Functions)
	}
	if tr, _ := b.m.Get(ids[2]); slices.Contains(tr.On, 0) || !slices.Contains(tr.On, 2) {
		t.Errorf("member's functions = %v", tr.On)
	}

	// A member's emergency stop stops the consist: its decoder ignores
	// its own address for speed.
	if _, err := b.m.EmergencyStop(b.ctx, ids[2]); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the consist to stop", func() bool {
		return b.station.DecoderState(1001, true).EStop && b.station.DecoderState(1002, true).EStop
	})

	// Breaking up clears CV 19, and each locomotive answers its own
	// address again.
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 20, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.BreakUp(b.ctx, c.ID); err == nil || !strings.Contains(err.Error(), "stop Coal Drag") {
		t.Fatalf("broken up while moving: %v", err)
	}
	// After a restart only the command station knows the consist address
	// is moving.
	f, err := train.OpenFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	again := train.New(train.Options{Bus: b.local, File: f, SearchWindow: 150 * time.Millisecond})
	if _, err := again.BreakUp(b.ctx, c.ID); err == nil || !strings.Contains(err.Error(), "stop Coal Drag") {
		t.Fatalf("broken up after a restart while moving: %v", err)
	}
	// Held by the layout's emergency stop, it still has its speed to go
	// back to; broken up, its locomotives would go back to it apart.
	if _, err := b.m.SetEmergencyStop(b.ctx, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the hold", func() bool { return b.station.DecoderState(3, false).Held })
	if _, err := b.m.BreakUp(b.ctx, c.ID); err == nil || !strings.Contains(err.Error(), "stop Coal Drag") {
		t.Fatalf("broken up while held: %v", err)
	}
	if _, err := again.BreakUp(b.ctx, c.ID); err == nil || !strings.Contains(err.Error(), "stop Coal Drag") {
		t.Fatalf("broken up while held after a restart: %v", err)
	}
	if _, err := b.m.SetEmergencyStop(b.ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[0], 0, ""); err != nil {
		t.Fatal(err)
	}
	c, err = b.m.BreakUp(b.ctx, c.ID)
	if err != nil || c.MadeUp {
		t.Fatalf("broken up = %+v, %v", c, err)
	}
	for _, d := range []struct {
		address uint16
		long    bool
	}{{1001, true}, {1002, true}, {3, false}} {
		if v := b.station.DecoderState(d.address, d.long).CVs[19]; v != 0 {
			t.Errorf("decoder %d CV19 = %#x after the break up", d.address, v)
		}
	}
	if _, err := b.m.SetSpeed(b.ctx, ids[2], 25, train.Forward); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a former member to answer its own address", func() bool { return b.station.DecoderState(3, false).Speed.Value == 25 })
	if s := b.station.DecoderState(1001, true).Speed.Value; s != 0 {
		t.Errorf("the former lead moved to %v", s)
	}
}

// Two made-up DCC consists cannot share an address.
func TestDCCConsistAddressInUse(t *testing.T) {
	b := newBench(t)
	ids := rosterOf(t, b, 1001, 1002, 1003, 1004)
	first, err := b.m.CreateConsist(train.Consist{Name: "First", Kind: train.ConsistDCC, Address: 20, Members: []train.Member{{Train: ids[0]}, {Train: ids[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.m.CreateConsist(train.Consist{Name: "Second", Kind: train.ConsistDCC, Address: 20, Members: []train.Member{{Train: ids[2]}, {Train: ids[3]}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.MakeUp(b.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.MakeUp(b.ctx, second.ID); err == nil || !strings.Contains(err.Error(), "First uses consist address 20") {
		t.Errorf("second at the same address = %v", err)
	}
}
