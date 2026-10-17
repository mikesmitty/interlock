package detector

import (
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
)

func TestReports(t *testing.T) {
	tests := []struct {
		suffix uint16
		report Report
		text   string
	}{
		{0x0000, Report{Exit, Address{Long, 0}}, "0"},
		{0x04D2, Report{Exit, Address{Long, 1234}}, "1234"},
		{0x44D2, Report{EntryWest, Address{Long, 1234}}, "1234"},
		{0x84D2, Report{EntryEast, Address{Long, 1234}}, "1234"},
		{0xC4D2, Report{Entry, Address{Long, 1234}}, "1234"},
		{0x3803, Report{Exit, Address{Short, 3}}, "3 (short)"},
		{0xB87F, Report{EntryEast, Address{Short, 127}}, "127 (short)"},
		{0x790C, Report{EntryWest, Address{Consist, 12}}, "consist 12"},
		{0x27FF, Report{Exit, Address{Long, 10239}}, "10239"},
	}
	for _, tt := range tests {
		got := Parse(tt.suffix)
		if got != tt.report {
			t.Errorf("Parse(%#04x) = %+v, want %+v", tt.suffix, got, tt.report)
		}
		if back := got.Suffix(); back != tt.suffix {
			t.Errorf("%+v.Suffix() = %#04x, want %#04x", got, back, tt.suffix)
		}
		if s := got.Address.String(); s != tt.text {
			t.Errorf("address string = %q, want %q", s, tt.text)
		}
	}
	if !EntryWest.Entered() || Exit.Entered() {
		t.Error("Entered is wrong")
	}
	if EntryWest.Orientation() != "west" || EntryEast.Orientation() != "east" || Entry.Orientation() != "" {
		t.Error("Orientation is wrong")
	}
}

func TestRangeDecode(t *testing.T) {
	reporter := openlcb.EventID(0x0A00000000430000)
	rng := Range(reporter)
	if rng.Bits != 16 || rng.Base != reporter {
		t.Fatalf("Range = %+v", rng)
	}
	r, ok := Decode(0x0A0000000043C4D2, rng)
	if !ok || r.Kind != Entry || r.Address.Number != 1234 {
		t.Errorf("Decode = %+v, %v", r, ok)
	}
	if _, ok := Decode(0x0A0000000044C4D2, rng); ok {
		t.Error("decoded an event outside the range")
	}
	if ev := r.Event(rng); ev != 0x0A0000000043C4D2 {
		t.Errorf("Event = %v", ev)
	}
	// A reporter ID with stray low bytes still gives the right range.
	if Range(0x0A00000000431234) != rng {
		t.Error("Range did not clear the low bytes")
	}
}
