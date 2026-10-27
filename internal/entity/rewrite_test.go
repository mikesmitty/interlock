package entity

import (
	"testing"

	"github.com/mikesmitty/interlock/internal/refs"
	"github.com/mikesmitty/interlock/openlcb"
)

func TestMoveID(t *testing.T) {
	const from, to = openlcb.NodeID(0x0aff9024a634), openlcb.NodeID(0x050101012200)
	tests := []struct {
		id, want string
		ok       bool
	}{
		{"turnout.0aff9024a634.servo_1", "turnout.050101012200.servo_1", true},
		{"reporter.0aff9024a634.track_4", "reporter.050101012200.track_4", true},
		{"turnout.0a737c38ad26.servo_1", "turnout.0a737c38ad26.servo_1", false},
		{"sensor.manual.yard_door", "sensor.manual.yard_door", false},
		{"turnout.0aff9024a634", "turnout.0aff9024a634", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := MoveID(tt.id, from, to)
		if got != tt.want || ok != tt.ok {
			t.Errorf("MoveID(%q) = %q, %v; want %q, %v", tt.id, got, ok, tt.want, tt.ok)
		}
	}
	if got := ProfileID(KindBlock, to, "track_1"); got != "block.050101012200.track_1" {
		t.Errorf("ProfileID = %q", got)
	}
}

func TestManagerRewrite(t *testing.T) {
	h := newHarness(t, t.TempDir())
	waitFor(t, "entities", func() bool { return len(h.m.List()) == 4 })
	const old = openlcb.NodeID(0x0a0000000041)
	file := h.m.opts.File
	// What was set for the failed board's entities, and the new board's
	// own guess, which keeps what was set for it.
	if err := file.SetOverride("turnout.0a0000000041.servo_1", Override{Name: "Yard throat"}); err != nil {
		t.Fatal(err)
	}
	if err := file.SetOverride("switch.0a0000000041.guess", Override{Hidden: true}); err != nil {
		t.Fatal(err)
	}
	if err := file.SetOverride("switch.0a0000000042.guess", Override{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	oldOn, newOn := openlcb.EventID(0x0a00000000410010), openlcb.EventID(0x0a00000000420010)
	if _, err := h.m.Create(Manual{Kind: KindSensor, Name: "Yard door", Events: map[string]openlcb.EventID{"on": oldOn, "off": other.Event(0x11)}}); err != nil {
		t.Fatal(err)
	}
	rw := refs.Rewrite{
		Entity: func(id string) (string, bool) { return MoveID(id, old, nodeID) },
		Event: func(ev openlcb.EventID) (openlcb.EventID, bool) {
			return newOn, ev == oldOn
		},
	}

	preview, err := h.m.Rewrite(rw, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 2 {
		t.Fatalf("preview = %+v, want the turnout's name and the door's event", preview)
	}
	if c := preview[0]; c.Item != "Yard throat" || c.From != "turnout.0a0000000041.servo_1" || c.To != "turnout.0a0000000042.servo_1" || c.Kind != refs.KindEntity {
		t.Errorf("name change = %+v", c)
	}
	if c := preview[1]; c.Item != "Yard door" || c.Field != "on event" || c.To != newOn.String() {
		t.Errorf("event change = %+v", c)
	}
	if got := h.get(t, "turnout.0a0000000042.servo_1").Name; got != "Yard Servo 1" {
		t.Errorf("a preview renamed the turnout to %q", got)
	}

	if _, err := h.m.Rewrite(rw, true); err != nil {
		t.Fatal(err)
	}
	if got := h.get(t, "turnout.0a0000000042.servo_1").Name; got != "Yard throat" {
		t.Errorf("turnout name = %q, want the old board's", got)
	}
	if o := file.Override("turnout.0a0000000041.servo_1"); o != (Override{}) {
		t.Errorf("the old override is still there: %+v", o)
	}
	if g := h.get(t, "switch.0a0000000042.guess"); g.Hidden {
		t.Error("the new guess took the old one's hidden setting over its own")
	}
	if got := h.get(t, "sensor.manual.yard_door").Events["on"]; got != newOn {
		t.Errorf("door on event = %s, want %s", got, newOn)
	}
}
