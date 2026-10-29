// Package traction encodes and decodes the Train Control protocol: the
// commands a throttle sends to a train node, the replies that come back,
// the Train Search event, the node IDs of DCC locomotives, and the events
// that drive DCC accessory decoders. It is pure functions over bytes; the
// node package carries the messages.
//
// Speeds travel as IEEE half-precision floats whose sign is the direction
// (negative is reverse, so a negative zero is "stopped, facing reverse").
// The unit is scale miles per hour, which DCC command stations map to
// 128-step speed steps one for one, so 0 to 126 covers the range.
package traction

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/mikesmitty/interlock/openlcb"
)

// Well-known events of the Train Control family.
const (
	// EventIsTrain is produced by every train node.
	EventIsTrain openlcb.EventID = 0x0101000000000303
	// EventIsProxy is produced by traction proxy nodes.
	EventIsProxy openlcb.EventID = 0x0101000000000304
	// EventProgrammingTrack is produced by the node that owns a DCC
	// programming track; CV reads and writes go to its space 0xF8.
	EventProgrammingTrack openlcb.EventID = 0x090099FEFFFF0002
)

// SpaceCV is the memory space of a train node or command station that
// maps DCC configuration variables: CV n is at address n-1.
const SpaceCV uint8 = 0xF8

// Command bytes: the first byte of a Traction Control Command payload.
const (
	CmdSetSpeed      byte = 0x00
	CmdSetFunction   byte = 0x01
	CmdEmergencyStop byte = 0x02
	CmdQuerySpeeds   byte = 0x10
	CmdQueryFunction byte = 0x11
	CmdController    byte = 0x20
	CmdConsist       byte = 0x30
	CmdManagement    byte = 0x40
	// FlagListener is set on a command a train forwards to the listeners
	// in its consist; a receiver applies it but forwards it no further.
	FlagListener byte = 0x80
	// CmdMask strips FlagListener from a command byte.
	CmdMask byte = 0x7F
)

// Controller configuration subcommands, the second byte after CmdController.
const (
	ControllerAssign  byte = 0x01
	ControllerRelease byte = 0x02
	ControllerQuery   byte = 0x03
	// ControllerChanged is sent by a train to its controller when another
	// controller asks to take over; the controller answers with
	// ControllerChangedReply.
	ControllerChanged byte = 0x04
)

// Results of a controller assign, the third byte of the reply.
const (
	AssignOK                  byte = 0x00
	AssignRefusedByController byte = 0x01
	AssignRefusedByTrain      byte = 0x02
)

// Consist subcommands and the flags of a consist member.
const (
	ConsistAttach byte = 0x01
	ConsistDetach byte = 0x02
	ConsistQuery  byte = 0x03

	ConsistFlagAliasValid byte = 0x01
	ConsistFlagReverse    byte = 0x02
	ConsistFlagLinkF0     byte = 0x04
	ConsistFlagLinkFn     byte = 0x08
	ConsistFlagHide       byte = 0x80
)

// Traction management subcommands.
const (
	ManagementReserve   byte = 0x01
	ManagementRelease   byte = 0x02
	ManagementNoop      byte = 0x03
	ManagementHeartbeat byte = 0x03
)

// MaxFunction is the highest function number DCC defines (F0 to F68).
const MaxFunction = 68

// ErrShort reports a payload too short for what it claims to be.
var ErrShort = errors.New("traction: payload too short")

// Speed is a speed and direction.
type Speed struct {
	// Value is the magnitude, 0 to 126 in practice.
	Value float32
	// Reverse is the direction; false is forward.
	Reverse bool
}

// Encode returns the two wire bytes.
func (s Speed) Encode() [2]byte {
	v := s.Value
	if v < 0 {
		v = -v
	}
	if s.Reverse {
		v = float32(math.Copysign(float64(v), -1))
	}
	bits := openlcb.ToFloat16(v)
	return [2]byte{byte(bits >> 8), byte(bits)}
}

// String reads like "30 forward" or "0 reverse".
func (s Speed) String() string {
	dir := "forward"
	if s.Reverse {
		dir = "reverse"
	}
	return strconv.FormatFloat(float64(s.Value), 'f', -1, 32) + " " + dir
}

// DecodeSpeed reads two wire bytes. A NaN, which trains report after an
// emergency stop, comes back as a zero speed with estop set.
func DecodeSpeed(b []byte) (s Speed, estop bool) {
	if len(b) < 2 {
		return Speed{}, false
	}
	bits := uint16(b[0])<<8 | uint16(b[1])
	f := openlcb.Float16(bits)
	if math.IsNaN(float64(f)) {
		return Speed{Reverse: bits&0x8000 != 0}, true
	}
	return Speed{Value: float32(math.Abs(float64(f))), Reverse: math.Signbit(float64(f))}, false
}

// --- commands ------------------------------------------------------------

// SetSpeed builds a set speed and direction command.
func SetSpeed(s Speed) []byte {
	b := s.Encode()
	return []byte{CmdSetSpeed, b[0], b[1]}
}

// SetFunction builds a set function command; value is 1 for on and 0 for
// off on the usual binary functions.
func SetFunction(fn uint32, value uint16) []byte {
	return []byte{CmdSetFunction, byte(fn >> 16), byte(fn >> 8), byte(fn), byte(value >> 8), byte(value)}
}

// EmergencyStop builds an emergency stop command.
func EmergencyStop() []byte { return []byte{CmdEmergencyStop} }

// QuerySpeeds asks for the set, commanded and actual speeds.
func QuerySpeeds() []byte { return []byte{CmdQuerySpeeds} }

// QueryFunction asks for a function's value.
func QueryFunction(fn uint32) []byte {
	return []byte{CmdQueryFunction, byte(fn >> 16), byte(fn >> 8), byte(fn)}
}

// AssignController asks the train to take controller as its controller.
func AssignController(controller openlcb.NodeID) []byte {
	return append([]byte{CmdController, ControllerAssign, 0}, controller.AsSlice()...)
}

// ReleaseController tells the train that controller lets it go.
func ReleaseController(controller openlcb.NodeID) []byte {
	return append([]byte{CmdController, ControllerRelease, 0}, controller.AsSlice()...)
}

// QueryController asks who controls the train.
func QueryController() []byte { return []byte{CmdController, ControllerQuery} }

// ControllerChangedReply answers a ControllerChanged notice: accept lets
// the other controller take over, false keeps the train.
func ControllerChangedReply(accept bool) []byte {
	result := byte(1)
	if accept {
		result = 0
	}
	return []byte{CmdController, ControllerChanged, result}
}

// AttachConsist adds member to the train's consist with flags.
func AttachConsist(member openlcb.NodeID, flags byte) []byte {
	return append([]byte{CmdConsist, ConsistAttach, flags}, member.AsSlice()...)
}

// DetachConsist removes member from the train's consist.
func DetachConsist(member openlcb.NodeID) []byte {
	return append([]byte{CmdConsist, ConsistDetach, 0}, member.AsSlice()...)
}

// QueryConsist asks for the consist: how many members, and the member at
// index when index is not negative.
func QueryConsist(index int) []byte {
	if index < 0 {
		return []byte{CmdConsist, ConsistQuery}
	}
	return []byte{CmdConsist, ConsistQuery, byte(index)}
}

// Reserve asks the train to be reserved for this node.
func Reserve() []byte { return []byte{CmdManagement, ManagementReserve} }

// Release gives up a reservation.
func Release() []byte { return []byte{CmdManagement, ManagementRelease} }

// Noop keeps a reservation alive.
func Noop() []byte { return []byte{CmdManagement, ManagementNoop} }

// --- replies ------------------------------------------------------------

// Speeds is the answer to QuerySpeeds.
type Speeds struct {
	// Set is the last speed a throttle asked for.
	Set Speed
	// EStop is true after an emergency stop.
	EStop bool
	// Commanded is what the train is being told to do now, and Actual what
	// it is doing, where the train knows; many echo Set for both.
	Commanded, Actual Speed
}

// ParseSpeeds reads a QuerySpeeds reply.
func ParseSpeeds(payload []byte) (Speeds, error) {
	if len(payload) < 4 || payload[0]&CmdMask != CmdQuerySpeeds {
		return Speeds{}, fmt.Errorf("%w: speeds reply %X", ErrShort, payload)
	}
	var s Speeds
	var nan bool
	s.Set, nan = DecodeSpeed(payload[1:3])
	// The standard puts the emergency stop flag in bit 0; some command
	// stations set bit 7 instead.
	s.EStop = nan || payload[3]&0x81 != 0
	if len(payload) >= 6 {
		s.Commanded, _ = DecodeSpeed(payload[4:6])
	} else {
		s.Commanded = s.Set
	}
	if len(payload) >= 8 {
		s.Actual, _ = DecodeSpeed(payload[6:8])
	} else {
		s.Actual = s.Commanded
	}
	return s, nil
}

// ParseFunction reads a QueryFunction reply.
func ParseFunction(payload []byte) (fn uint32, value uint16, err error) {
	if len(payload) < 6 || payload[0]&CmdMask != CmdQueryFunction {
		return 0, 0, fmt.Errorf("%w: function reply %X", ErrShort, payload)
	}
	fn = uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	value = uint16(payload[4])<<8 | uint16(payload[5])
	return fn, value, nil
}

// ParseAssignReply reads the answer to AssignController: AssignOK or why
// the train refused.
func ParseAssignReply(payload []byte) (byte, error) {
	if len(payload) < 3 || payload[0]&CmdMask != CmdController || payload[1] != ControllerAssign {
		return 0, fmt.Errorf("%w: assign reply %X", ErrShort, payload)
	}
	return payload[2], nil
}

// ParseControllerReply reads the answer to QueryController: the controlling
// node, or zero when the train is free.
func ParseControllerReply(payload []byte) (openlcb.NodeID, error) {
	if len(payload) < 9 || payload[0]&CmdMask != CmdController || payload[1] != ControllerQuery {
		return 0, fmt.Errorf("%w: controller reply %X", ErrShort, payload)
	}
	return openlcb.NodeIDFromBytes([6]byte(payload[3:9])), nil
}

// ConsistResult is the answer to AttachConsist or DetachConsist.
type ConsistResult struct {
	Member openlcb.NodeID
	Code   openlcb.ErrorCode
}

// ParseConsistResult reads an attach or detach reply.
func ParseConsistResult(payload []byte) (ConsistResult, error) {
	if len(payload) < 10 || payload[0]&CmdMask != CmdConsist || (payload[1] != ConsistAttach && payload[1] != ConsistDetach) {
		return ConsistResult{}, fmt.Errorf("%w: consist reply %X", ErrShort, payload)
	}
	return ConsistResult{
		Member: openlcb.NodeIDFromBytes([6]byte(payload[2:8])),
		Code:   openlcb.ErrorCode(uint16(payload[8])<<8 | uint16(payload[9])),
	}, nil
}

// ConsistEntry is one member of a consist, from a QueryConsist reply.
type ConsistEntry struct {
	Index  int
	Flags  byte
	Member openlcb.NodeID
}

// ParseConsistQuery reads a QueryConsist reply: the member count and, when
// an index was asked for and exists, that member.
func ParseConsistQuery(payload []byte) (count int, entry *ConsistEntry, err error) {
	if len(payload) < 3 || payload[0]&CmdMask != CmdConsist || payload[1] != ConsistQuery {
		return 0, nil, fmt.Errorf("%w: consist query reply %X", ErrShort, payload)
	}
	count = int(payload[2])
	if len(payload) >= 11 {
		entry = &ConsistEntry{Index: int(payload[3]), Flags: payload[4], Member: openlcb.NodeIDFromBytes([6]byte(payload[5:11]))}
	}
	return count, entry, nil
}

// --- commands as received -----------------------------------------------

// Command is a Traction Control Command decoded, as a train node or a
// listener receives it.
type Command struct {
	// Kind is the command byte without FlagListener.
	Kind byte
	// Listener is set on a command forwarded by a consist.
	Listener bool
	// Speed for CmdSetSpeed.
	Speed Speed
	// Function and Value for CmdSetFunction and CmdQueryFunction.
	Function uint32
	Value    uint16
	// Sub is the subcommand of CmdController, CmdConsist and CmdManagement.
	Sub byte
	// Flags and Node for controller and consist subcommands that name a node.
	Flags byte
	Node  openlcb.NodeID
	// Index is the member asked for by a consist query, or -1.
	Index int
}

// ParseCommand decodes a command payload.
func ParseCommand(payload []byte) (Command, error) {
	if len(payload) == 0 {
		return Command{}, ErrShort
	}
	c := Command{Kind: payload[0] & CmdMask, Listener: payload[0]&FlagListener != 0, Index: -1}
	need := func(n int) error {
		if len(payload) < n {
			return fmt.Errorf("%w: command %X", ErrShort, payload)
		}
		return nil
	}
	switch c.Kind {
	case CmdSetSpeed:
		if err := need(3); err != nil {
			return c, err
		}
		c.Speed, _ = DecodeSpeed(payload[1:3])
	case CmdSetFunction:
		if err := need(6); err != nil {
			return c, err
		}
		c.Function = uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
		c.Value = uint16(payload[4])<<8 | uint16(payload[5])
	case CmdQueryFunction:
		if err := need(4); err != nil {
			return c, err
		}
		c.Function = uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	case CmdEmergencyStop, CmdQuerySpeeds:
	case CmdController, CmdConsist, CmdManagement:
		if err := need(2); err != nil {
			return c, err
		}
		c.Sub = payload[1]
		switch {
		case c.Kind == CmdManagement:
		case c.Kind == CmdController && c.Sub == ControllerQuery:
		case c.Kind == CmdConsist && c.Sub == ConsistQuery:
			if len(payload) >= 3 {
				c.Index = int(payload[2])
			}
		case c.Kind == CmdController && c.Sub == ControllerChanged:
			if len(payload) >= 9 {
				c.Flags = payload[2]
				c.Node = openlcb.NodeIDFromBytes([6]byte(payload[3:9]))
			} else if len(payload) >= 3 {
				c.Flags = payload[2] // a reply carries only the result
			}
		default:
			if err := need(9); err != nil {
				return c, err
			}
			c.Flags = payload[2]
			c.Node = openlcb.NodeIDFromBytes([6]byte(payload[3:9]))
		}
	default:
		return c, fmt.Errorf("traction: unknown command 0x%02X", payload[0])
	}
	return c, nil
}

// --- DCC node IDs -------------------------------------------------------

// Node ID ranges reserved for legacy protocols. A DCC train node is
// 06.01.00.00 followed by the address, so NodeIDMask covers all four
// bytes above it. OpenMRN checks only 06.01, but a command station may
// give its own node an ID elsewhere in 06.01, and that is no locomotive.
const (
	NodeIDDCC     openlcb.NodeID = 0x060100000000
	NodeIDMask    openlcb.NodeID = 0xFFFFFFFF0000
	dccLongSelect openlcb.NodeID = 0xC000
	// ShortAddressMax is the highest DCC short (primary) address.
	ShortAddressMax = 127
)

// DCCNodeID returns the node ID of a DCC locomotive, as command stations
// assign them: 06.01.00.00 followed by the address, with the 0xC000
// selector set for long addresses.
func DCCNodeID(address uint16, long bool) openlcb.NodeID {
	id := NodeIDDCC | openlcb.NodeID(address&0x3FFF)
	if long {
		id |= dccLongSelect
	}
	return id
}

// DCCAddress reads the address out of a DCC locomotive's node ID.
func DCCAddress(id openlcb.NodeID) (address uint16, long, ok bool) {
	if id&NodeIDMask != NodeIDDCC {
		return 0, false, false
	}
	address = uint16(id & 0x3FFF)
	long = id&dccLongSelect == dccLongSelect || address > ShortAddressMax
	return address, long, true
}

// AddressName reads like "1234", "3 (short)" or "3 (long)": the address,
// qualified when it is in the range both kinds share.
func AddressName(address uint16, long bool) string {
	s := strconv.Itoa(int(address))
	if address > ShortAddressMax {
		return s
	}
	if long {
		return s + " (long)"
	}
	return s + " (short)"
}
