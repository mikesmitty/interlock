package openlcb

import "testing"

func TestEventRange(t *testing.T) {
	tests := []struct {
		name    string
		base    EventID
		bits    uint8
		encoded EventID
	}{
		// Bit 16 of the base is 1, so the low bits stay 0.
		{"bit above set", 0x0A00000000430000, 16, 0x0A00000000430000},
		// Bit 16 of the base is 0, so the low bits are filled with 1s.
		{"bit above clear", 0x0A00000000420000, 16, 0x0A0000000042FFFF},
		{"eight bits", 0x0501010122000100, 8, 0x0501010122000100},
		{"eight bits filled", 0x0501010122000000, 8, 0x05010101220000FF},
		{"one bit", 0x0000000000000002, 1, 0x0000000000000002},
		{"one bit filled", 0x0000000000000004, 1, 0x0000000000000005},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewEventRange(tt.base, tt.bits)
			if got := r.Encode(); got != tt.encoded {
				t.Errorf("Encode() = %v, want %v", got, tt.encoded)
			}
			back := ParseEventRange(tt.encoded)
			if back != r {
				t.Errorf("ParseEventRange(%v) = %+v, want %+v", tt.encoded, back, r)
			}
			if !r.Contains(tt.base) || !r.Contains(tt.base|r.Mask()) {
				t.Errorf("range %v should contain its ends", r)
			}
			if r.Contains(tt.base + r.Mask() + 1) {
				t.Errorf("range %v should not contain the next ID", r)
			}
			if r.Size() != 1<<tt.bits {
				t.Errorf("Size() = %d", r.Size())
			}
		})
	}
	if s := NewEventRange(0x0A00000000430000, 16).String(); s != "0A.00.00.00.00.43.00.00/16" {
		t.Errorf("String() = %q", s)
	}
	// A base with stray low bits is normalized.
	if r := NewEventRange(0x0A00000000431234, 16); r.Base != 0x0A00000000430000 {
		t.Errorf("NewEventRange did not clear low bits: %v", r.Base)
	}
}
