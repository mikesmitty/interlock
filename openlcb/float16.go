package openlcb

import "math"

// Float16 converts an IEEE 754 half-precision value (as used by the
// Traction protocol for speed and by CDI float elements of size 2) to
// float32.
func Float16(bits uint16) float32 {
	sign := uint32(bits>>15) << 31
	exp := uint32(bits>>10) & 0x1F
	frac := uint32(bits) & 0x3FF
	switch exp {
	case 0:
		if frac == 0 {
			return math.Float32frombits(sign)
		}
		// Subnormal: normalize.
		exp = 1
		for frac&0x400 == 0 {
			frac <<= 1
			exp--
		}
		frac &= 0x3FF
		return math.Float32frombits(sign | (exp+127-15)<<23 | frac<<13)
	case 0x1F:
		return math.Float32frombits(sign | 0xFF<<23 | frac<<13)
	default:
		return math.Float32frombits(sign | (exp+127-15)<<23 | frac<<13)
	}
}

// ToFloat16 converts a float32 to IEEE 754 half precision, rounding to
// nearest even and saturating to infinity.
func ToFloat16(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int(b>>23)&0xFF - 127 + 15
	frac := b & 0x7FFFFF
	switch {
	case b&0x7FFFFFFF == 0:
		return sign
	case exp >= 0x1F:
		if b&0x7F800000 == 0x7F800000 && frac != 0 {
			return sign | 0x7E00 // NaN
		}
		return sign | 0x7C00 // infinity or overflow
	case exp <= 0:
		if exp < -10 {
			return sign
		}
		// Subnormal result.
		frac |= 0x800000
		shift := uint(14 - exp)
		half := uint32(1) << (shift - 1)
		rounded := frac >> shift
		if frac&(half<<1-1) > half || (frac&(half<<1-1) == half && rounded&1 == 1) {
			rounded++
		}
		return sign | uint16(rounded)
	default:
		rounded := frac >> 13
		rem := frac & 0x1FFF
		if rem > 0x1000 || (rem == 0x1000 && rounded&1 == 1) {
			rounded++
			if rounded == 0x400 {
				rounded = 0
				exp++
				if exp >= 0x1F {
					return sign | 0x7C00
				}
			}
		}
		return sign | uint16(exp)<<10 | uint16(rounded)
	}
}
