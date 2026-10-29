package traction

import "github.com/mikesmitty/interlock/openlcb"

// DCC accessory decoders through the well-known accessory events. The low
// 12 bits of a basic accessory event are the output's binary address, the
// 11 bits a DCC accessory packet carries, shifted left once, plus 1 for
// normal (a turnout closed) or 0 for reversed (thrown). The low 19 bits of
// an extended accessory event are the binary address and an aspect byte.
//
// People number accessories 1 to 2048, which is not the binary address:
// number n is binary n+3, and 2045 to 2048 wrap round to 0 to 3, as the
// 2023 DCC standard numbers them and OpenMRN's accy_address_user_to_binary
// converts them. A command station passes the binary address through to
// the packet, so any other conversion puts every turnout off by some
// amount.

// The accessory event ranges.
var (
	// AccessoryActivate holds the basic accessory events that set an
	// output and turn it on: the ones that throw and close turnouts.
	AccessoryActivate = openlcb.EventRange{Base: 0x0101020000FF0000, Bits: 12}
	// AccessoryDeactivate holds the ones that set an output and turn it
	// off.
	AccessoryDeactivate = openlcb.EventRange{Base: 0x0101020000FE0000, Bits: 12}
	// AccessoryExtended holds the extended accessory events: an address
	// and an aspect, as for signals.
	AccessoryExtended = openlcb.EventRange{Base: 0x0101020001000000, Bits: 19}
)

// The accessory numbers people use.
const (
	AccessoryFirst = 1
	AccessoryLast  = 2048
)

// AccessoryBinary converts an accessory number to its binary output
// address. It reports false for a number outside 1 to 2048.
func AccessoryBinary(number int) (uint16, bool) {
	switch {
	case number < AccessoryFirst || number > AccessoryLast:
		return 0, false
	case number >= 2045:
		return uint16(number - 2045), true
	}
	return uint16(number + 3), true
}

// AccessoryNumber converts a binary output address, 0 to 2047, to the
// accessory's number.
func AccessoryNumber(binary uint16) int {
	binary &= 0x7FF
	if binary < 4 {
		return int(binary) + 2045
	}
	return int(binary) - 3
}

// AccessoryEvent is the event that sets an accessory to normal (closes a
// turnout) or reversed (throws it). It reports false for a number outside
// 1 to 2048.
func AccessoryEvent(number int, normal bool) (openlcb.EventID, bool) {
	binary, ok := AccessoryBinary(number)
	if !ok {
		return 0, false
	}
	ev := AccessoryActivate.Base + openlcb.EventID(binary)<<1
	if normal {
		ev++
	}
	return ev, true
}

// AccessoryCommand is what a basic accessory event asks for.
type AccessoryCommand struct {
	Number int
	Normal bool
	// Activate is false for the events that turn the output off.
	Activate bool
}

// ParseAccessoryEvent reads a basic accessory event.
func ParseAccessoryEvent(ev openlcb.EventID) (AccessoryCommand, bool) {
	activate := AccessoryActivate.Contains(ev)
	if !activate && !AccessoryDeactivate.Contains(ev) {
		return AccessoryCommand{}, false
	}
	return AccessoryCommand{Number: AccessoryNumber(uint16(ev >> 1)), Normal: ev&1 == 1, Activate: activate}, true
}
