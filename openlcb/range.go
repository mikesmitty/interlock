package openlcb

import "strconv"

// EventRange is a block of 2^Bits consecutive event IDs that share the
// upper bits of Base, as advertised by Producer Range Identified and
// Consumer Range Identified. A DCC detector, for example, produces a
// 16-bit range whose low bits report locomotive addresses.
type EventRange struct {
	// Base is the first event ID in the range; its low Bits are zero.
	Base EventID
	// Bits is how many low bits vary within the range, 1 to 63.
	Bits uint8
}

// NewEventRange returns the range of 2^bits IDs containing base, with the
// low bits of base cleared.
func NewEventRange(base EventID, bits uint8) EventRange {
	r := EventRange{Bits: bits}
	r.Base = base &^ r.Mask()
	return r
}

// Mask returns the bits that vary within the range.
func (r EventRange) Mask() EventID {
	if r.Bits >= 64 {
		return ^EventID(0)
	}
	return EventID(1)<<r.Bits - 1
}

// Contains reports whether id lies in the range.
func (r EventRange) Contains(id EventID) bool {
	return id&^r.Mask() == r.Base
}

// Size returns how many IDs the range holds.
func (r EventRange) Size() uint64 {
	if r.Bits >= 64 {
		return 0
	}
	return 1 << r.Bits
}

// Encode returns the event ID that advertises the range. The Event
// Transport standard marks the size with a run of identical low bits that
// differ from the lowest bit of the common prefix, so the receiver can
// tell where the prefix ends.
func (r EventRange) Encode() EventID {
	mask := r.Mask()
	base := r.Base &^ mask
	if base&(mask+1) != 0 {
		return base
	}
	return base | mask
}

// ParseEventRange decodes the event ID carried by a Range Identified
// message: the trailing run of identical bits gives the size.
func ParseEventRange(id EventID) EventRange {
	low := id & 1
	var bits uint8
	for bits < 64 && (id>>bits)&1 == low {
		bits++
	}
	return NewEventRange(id, bits)
}

// String formats the range as its base and size, such as
// "0A.00.00.00.00.43.00.00/16".
func (r EventRange) String() string {
	return r.Base.String() + "/" + strconv.Itoa(int(r.Bits))
}
