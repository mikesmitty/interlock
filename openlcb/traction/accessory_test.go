package traction

import (
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
)

func TestAccessoryEvents(t *testing.T) {
	// OpenMRN's accy_address_user_to_binary at both ends of the range.
	for _, c := range []struct {
		number   int
		binary   uint16
		reversed openlcb.EventID
	}{
		{1, 4, 0x0101020000FF0008},
		{2, 5, 0x0101020000FF000A},
		{2044, 2047, 0x0101020000FF0FFE},
		{2045, 0, 0x0101020000FF0000},
		{2048, 3, 0x0101020000FF0006},
	} {
		if b, ok := AccessoryBinary(c.number); !ok || b != c.binary {
			t.Errorf("AccessoryBinary(%d) = %d %v, want %d", c.number, b, ok, c.binary)
		}
		if n := AccessoryNumber(c.binary); n != c.number {
			t.Errorf("AccessoryNumber(%d) = %d, want %d", c.binary, n, c.number)
		}
		if ev, ok := AccessoryEvent(c.number, false); !ok || ev != c.reversed {
			t.Errorf("AccessoryEvent(%d, reversed) = %v %v, want %v", c.number, ev, ok, c.reversed)
		}
		if ev, _ := AccessoryEvent(c.number, true); ev != c.reversed+1 {
			t.Errorf("AccessoryEvent(%d, normal) = %v, want %v", c.number, ev, c.reversed+1)
		}
	}
	for _, n := range []int{0, -1, 2049} {
		if _, ok := AccessoryEvent(n, true); ok {
			t.Errorf("AccessoryEvent(%d) should fail", n)
		}
	}

	// Every number comes back from its events.
	for n := AccessoryFirst; n <= AccessoryLast; n++ {
		for _, normal := range []bool{true, false} {
			ev, _ := AccessoryEvent(n, normal)
			got, ok := ParseAccessoryEvent(ev)
			if want := (AccessoryCommand{Number: n, Normal: normal, Activate: true}); !ok || got != want {
				t.Fatalf("ParseAccessoryEvent(%v) = %+v %v, want %+v", ev, got, ok, want)
			}
		}
	}
	if got, ok := ParseAccessoryEvent(0x0101020000FE0009); !ok || got != (AccessoryCommand{Number: 1, Normal: true}) {
		t.Errorf("deactivate = %+v %v", got, ok)
	}
	for _, ev := range []openlcb.EventID{0x0101020001000400, openlcb.EventEmergencyStop, 0x0101020000FD0009} {
		if _, ok := ParseAccessoryEvent(ev); ok {
			t.Errorf("ParseAccessoryEvent(%v) should fail", ev)
		}
	}
	if got := AccessoryActivate.Encode(); got != 0x0101020000FF0FFF {
		t.Errorf("activate range = %v", got)
	}
}
