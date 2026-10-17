// Package detector decodes the OpenLCB DCC detector protocol (the
// openlcb/documents working note on train detection, as implemented by
// canbus-outpost). A detector produces a 16-bit event range: the upper
// six bytes name the detector and the low two bytes report a locomotive
// entering or leaving.
//
// Suffix layout:
//
//	bits 15..14  0 = exit, 1 = entry facing west, 2 = entry facing east,
//	             3 = entry with the orientation unknown
//	bits 13..0   address code: a long address 0-10239, 0x3800 | short
//	             address, or 0x3900 | consist address
//
// Asked to Identify Producer for the "long address 0 left" event of a
// range, a detector first announces the locomotives present with Producer
// Identified Valid for their entry events.
package detector

import (
	"strconv"

	"github.com/mikesmitty/interlock/openlcb"
)

// RangeBits is the size of a detector's event range.
const RangeBits = 8 * 2

// Kind says what a report means.
type Kind uint8

// Report kinds, in the order of their suffix codes.
const (
	// Exit: the locomotive left the detector.
	Exit Kind = iota
	// EntryWest: the locomotive arrived, facing west.
	EntryWest
	// EntryEast: the locomotive arrived, facing east.
	EntryEast
	// Entry: the locomotive arrived; the detector does not report facing.
	Entry
)

// Entered reports whether the kind is an arrival.
func (k Kind) Entered() bool { return k != Exit }

// Orientation returns "west", "east" or "" for a report.
func (k Kind) Orientation() string {
	switch k {
	case EntryWest:
		return "west"
	case EntryEast:
		return "east"
	default:
		return ""
	}
}

// String implements fmt.Stringer.
func (k Kind) String() string {
	switch k {
	case Exit:
		return "exit"
	case EntryWest:
		return "entry facing west"
	case EntryEast:
		return "entry facing east"
	default:
		return "entry"
	}
}

// AddressType distinguishes the DCC address spaces.
type AddressType uint8

// Address types.
const (
	Long AddressType = iota
	Short
	Consist
)

// String implements fmt.Stringer.
func (t AddressType) String() string {
	switch t {
	case Short:
		return "short"
	case Consist:
		return "consist"
	default:
		return "long"
	}
}

// Address is a DCC address as a detector reports it.
type Address struct {
	Type   AddressType `json:"type"`
	Number uint16      `json:"number"`
}

const (
	addressMask = 0x3FFF
	shortCode   = 0x3800
	consistCode = 0x3900
	spaceMask   = 0x3F80
)

// Code returns the 14-bit address code.
func (a Address) Code() uint16 {
	switch a.Type {
	case Short:
		return shortCode | a.Number&0x7F
	case Consist:
		return consistCode | a.Number&0x7F
	default:
		return a.Number & addressMask
	}
}

// ParseAddress decodes a 14-bit address code.
func ParseAddress(code uint16) Address {
	code &= addressMask
	switch code & spaceMask {
	case shortCode:
		return Address{Type: Short, Number: code & 0x7F}
	case consistCode:
		return Address{Type: Consist, Number: code & 0x7F}
	default:
		return Address{Type: Long, Number: code}
	}
}

// String writes the address the way a throttle shows it: "1234",
// "3 (short)" or "consist 12".
func (a Address) String() string {
	n := strconv.Itoa(int(a.Number))
	switch a.Type {
	case Short:
		return n + " (short)"
	case Consist:
		return "consist " + n
	default:
		return n
	}
}

// Report is one decoded detector event.
type Report struct {
	Kind    Kind
	Address Address
}

// Parse decodes the low 16 bits of a detector event.
func Parse(suffix uint16) Report {
	return Report{Kind: Kind(suffix >> 14), Address: ParseAddress(suffix)}
}

// Suffix encodes the report as the low 16 bits of a detector event.
func (r Report) Suffix() uint16 {
	return uint16(r.Kind)<<14 | r.Address.Code()
}

// Event returns the full event ID for the report from a detector range.
func (r Report) Event(rng openlcb.EventRange) openlcb.EventID {
	return rng.Base | openlcb.EventID(r.Suffix())
}

// Range returns the event range of a detector from its configured reporter
// ID (an event ID whose low two bytes are zero).
func Range(reporter openlcb.EventID) openlcb.EventRange {
	return openlcb.NewEventRange(reporter, RangeBits)
}

// Decode interprets an event ID as a report from the detector with the
// given range, if it falls inside it.
func Decode(id openlcb.EventID, rng openlcb.EventRange) (Report, bool) {
	if rng.Bits != RangeBits || !rng.Contains(id) {
		return Report{}, false
	}
	return Parse(uint16(id)), true
}
