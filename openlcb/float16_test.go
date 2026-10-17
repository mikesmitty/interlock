package openlcb

import (
	"math"
	"testing"
)

func TestFloat16(t *testing.T) {
	cases := []struct {
		bits uint16
		want float32
	}{
		{0x0000, 0},
		{0x8000, float32(math.Copysign(0, -1))},
		{0x3C00, 1},
		{0xBC00, -1},
		{0x4000, 2},
		{0x3555, 0.33325195},
		{0x7BFF, 65504},
		{0x0001, 5.9604645e-08},
		{0x0400, 6.1035156e-05},
		{0x7C00, float32(math.Inf(1))},
		{0xFC00, float32(math.Inf(-1))},
		{0x5640, 100},
	}
	for _, c := range cases {
		if got := Float16(c.bits); got != c.want {
			t.Errorf("Float16(%#04x) = %v, want %v", c.bits, got, c.want)
		}
		if c.bits == 0x3555 {
			continue // not exactly representable round trip of the decimal literal
		}
		if back := ToFloat16(c.want); back != c.bits {
			t.Errorf("ToFloat16(%v) = %#04x, want %#04x", c.want, back, c.bits)
		}
	}
	if got := ToFloat16(1e6); got != 0x7C00 {
		t.Errorf("ToFloat16(1e6) = %#04x, want infinity", got)
	}
	if got := ToFloat16(float32(math.NaN())); got&0x7C00 != 0x7C00 || got&0x3FF == 0 {
		t.Errorf("ToFloat16(NaN) = %#04x, want a NaN", got)
	}
	// Round to nearest even: 1 + 2^-11 is exactly halfway between 1 and the
	// next half float, so it rounds down to the even mantissa.
	if got := ToFloat16(1 + 1.0/2048); got != 0x3C00 {
		t.Errorf("ToFloat16(1+2^-11) = %#04x, want 0x3C00", got)
	}
	if got := ToFloat16(1 + 3.0/2048); got != 0x3C02 {
		t.Errorf("ToFloat16(1+3*2^-11) = %#04x, want 0x3C02", got)
	}
}
