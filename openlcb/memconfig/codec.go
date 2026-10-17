// Package memconfig implements the OpenLCB Memory Configuration protocol:
// the datagram format shared by both sides, a client that reads and
// writes other nodes' address spaces, and a server that exposes address
// spaces of the local node.
//
// Behavior follows the Memory Configuration standard as practiced by the
// devices Interlock targets: replies to reads always come as a datagram
// even when the acknowledgment did not promise one, writes are complete
// when the acknowledgment arrives unless it says a reply is pending, and
// nodes may ignore commands they do not implement rather than reject them.
package memconfig

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/mikesmitty/interlock/openlcb"
)

// ContentType is the first byte of every memory configuration datagram.
const ContentType = 0x20

// Command bytes (the second datagram byte). Read and write commands carry
// the address space in their low two bits when it is one of the three
// well-known spaces; otherwise the space follows the address as a byte.
const (
	CmdWrite           byte = 0x00
	CmdWriteUnderMask  byte = 0x08
	CmdWriteReply      byte = 0x10
	CmdWriteFailed     byte = 0x18
	CmdRead            byte = 0x40
	CmdReadReply       byte = 0x50
	CmdReadFailed      byte = 0x58
	CmdOptions         byte = 0x80
	CmdOptionsReply    byte = 0x82
	CmdSpaceInfo       byte = 0x84
	CmdSpaceInfoReply  byte = 0x86 // low bit set when the space is present
	CmdLock            byte = 0x88
	CmdLockReply       byte = 0x8A
	CmdUniqueID        byte = 0x8C
	CmdUniqueIDReply   byte = 0x8D
	CmdUnfreeze        byte = 0xA0
	CmdFreeze          byte = 0xA1
	CmdUpdateComplete  byte = 0xA8
	CmdReboot          byte = 0xA9
	CmdFactoryReset    byte = 0xAA
	CmdEnterBootloader byte = 0xAB

	rwCommandMask = 0xFC
	rwSpaceMask   = 0x03
)

// Well-known address spaces.
const (
	SpaceCDI              uint8 = 0xFF
	SpaceAll              uint8 = 0xFE
	SpaceConfig           uint8 = 0xFD
	SpaceACDIManufacturer uint8 = 0xFC
	SpaceACDIUser         uint8 = 0xFB
	SpaceFDI              uint8 = 0xFA
	SpaceFunction         uint8 = 0xF9
	SpaceDCCCV            uint8 = 0xF8
	SpaceFirmware         uint8 = 0xEF
)

// MaxTransfer is the most bytes one read or write datagram carries.
const MaxTransfer = 64

// Error codes specific to memory configuration.
const (
	ErrCodeSpaceUnknown  = openlcb.ErrCodeAddressSpaceUnknown
	ErrCodeOutOfBounds   = openlcb.ErrCodeAddressOutOfBounds
	ErrCodeWriteReadOnly = openlcb.ErrCodeWriteToReadOnly
)

// ErrMalformed reports a datagram that does not parse as the command it
// claims to be.
var ErrMalformed = errors.New("memconfig: malformed datagram")

// SpaceName names the well-known spaces and formats the rest in hex.
func SpaceName(space uint8) string {
	switch space {
	case SpaceCDI:
		return "CDI"
	case SpaceAll:
		return "all memory"
	case SpaceConfig:
		return "configuration"
	case SpaceACDIManufacturer:
		return "ACDI manufacturer"
	case SpaceACDIUser:
		return "ACDI user"
	case SpaceFDI:
		return "function description"
	case SpaceFunction:
		return "function configuration"
	case SpaceDCCCV:
		return "DCC CVs"
	case SpaceFirmware:
		return "firmware"
	default:
		return fmt.Sprintf("space 0x%02X", space)
	}
}

// IsReadWrite reports whether cmd is a read or write command or reply
// (anything below 0x80 uses the shared address and space layout).
func IsReadWrite(cmd byte) bool { return cmd < CmdOptions }

// IsReply reports whether cmd is a reply a server sends rather than a
// request a client sends.
func IsReply(cmd byte) bool {
	switch {
	case IsReadWrite(cmd):
		// Replies set bit 4: 0x10-0x1B write replies, 0x50-0x5B read replies.
		return cmd&0x10 != 0
	case cmd == CmdOptionsReply, cmd == CmdLockReply, cmd == CmdUniqueIDReply:
		return true
	case cmd&0xFE == CmdSpaceInfoReply:
		return true
	default:
		return false
	}
}

// rwHeader builds the common prefix of read and write datagrams.
func rwHeader(cmd byte, space uint8, address uint32) []byte {
	p := make([]byte, 0, 7+MaxTransfer)
	p = append(p, ContentType, cmd)
	p = binary.BigEndian.AppendUint32(p, address)
	if space >= SpaceConfig {
		p[1] |= space - SpaceACDIManufacturer
	} else {
		p = append(p, space)
	}
	return p
}

// ReadRequest asks for count bytes (1 to 64) at address in space.
func ReadRequest(space uint8, address uint32, count uint8) []byte {
	return append(rwHeader(CmdRead, space, address), count)
}

// WriteRequest writes data (1 to 64 bytes) at address in space.
func WriteRequest(space uint8, address uint32, data []byte) []byte {
	return append(rwHeader(CmdWrite, space, address), data...)
}

// ReadReply is the successful answer to a read.
func ReadReply(space uint8, address uint32, data []byte) []byte {
	return append(rwHeader(CmdReadReply, space, address), data...)
}

// ReadFailedReply is the failing answer to a read.
func ReadFailedReply(space uint8, address uint32, code openlcb.ErrorCode) []byte {
	return binary.BigEndian.AppendUint16(rwHeader(CmdReadFailed, space, address), uint16(code))
}

// WriteReply is the optional successful answer to a write.
func WriteReply(space uint8, address uint32) []byte {
	return rwHeader(CmdWriteReply, space, address)
}

// WriteFailedReply is the failing answer to a write.
func WriteFailedReply(space uint8, address uint32, code openlcb.ErrorCode) []byte {
	return binary.BigEndian.AppendUint16(rwHeader(CmdWriteFailed, space, address), uint16(code))
}

// OptionsRequest asks what the node's memory configuration implementation
// can do.
func OptionsRequest() []byte { return []byte{ContentType, CmdOptions} }

// SpaceInfoRequest asks about one address space.
func SpaceInfoRequest(space uint8) []byte { return []byte{ContentType, CmdSpaceInfo, space} }

// LockRequest asks to lock the node's configuration for requester, or
// releases the lock when requester is zero.
func LockRequest(requester openlcb.NodeID) []byte {
	return append([]byte{ContentType, CmdLock}, requester.AsSlice()...)
}

// UpdateCompleteRequest tells the node a series of writes is finished.
func UpdateCompleteRequest() []byte { return []byte{ContentType, CmdUpdateComplete} }

// RebootRequest asks the node to restart.
func RebootRequest() []byte { return []byte{ContentType, CmdReboot} }

// FactoryResetRequest asks the node to return to factory defaults. The
// node's own ID is required as confirmation.
func FactoryResetRequest(id openlcb.NodeID) []byte {
	return append([]byte{ContentType, CmdFactoryReset}, id.AsSlice()...)
}

// FreezeRequest and UnfreezeRequest bracket a firmware upgrade of space.
func FreezeRequest(space uint8) []byte { return []byte{ContentType, CmdFreeze, space} }

// UnfreezeRequest ends the frozen state entered by FreezeRequest.
func UnfreezeRequest(space uint8) []byte { return []byte{ContentType, CmdUnfreeze, space} }

// ReadWrite is a decoded read or write command or reply.
type ReadWrite struct {
	// Command is the command byte with the space bits cleared.
	Command byte
	Space   uint8
	Address uint32
	// Data is the count byte for reads, the data for writes and read
	// replies, and the error code for failures.
	Data []byte
}

// Failed reports whether this is a failure reply.
func (rw ReadWrite) Failed() bool { return rw.Command&0x08 != 0 && rw.Command&0x10 != 0 }

// ErrorCode returns the error code of a failure reply.
func (rw ReadWrite) ErrorCode() openlcb.ErrorCode {
	if len(rw.Data) < 2 {
		return openlcb.ErrCodePermanent
	}
	return openlcb.ErrorCode(binary.BigEndian.Uint16(rw.Data))
}

// ParseReadWrite decodes a read or write command or reply. The payload
// includes the content type byte.
func ParseReadWrite(payload []byte) (ReadWrite, error) {
	if len(payload) < 6 || payload[0] != ContentType || !IsReadWrite(payload[1]) {
		return ReadWrite{}, ErrMalformed
	}
	rw := ReadWrite{Command: payload[1] & rwCommandMask, Address: binary.BigEndian.Uint32(payload[2:6])}
	rest := payload[6:]
	if bits := payload[1] & rwSpaceMask; bits != 0 {
		rw.Space = SpaceACDIManufacturer + bits
	} else {
		if len(rest) < 1 {
			return ReadWrite{}, ErrMalformed
		}
		rw.Space = rest[0]
		rest = rest[1:]
	}
	rw.Data = rest
	return rw, nil
}

// Options is the configuration options reply.
type Options struct {
	Available    uint16
	WriteLengths uint8
	HighestSpace uint8
	LowestSpace  uint8
	Name         string
}

// Capability bits in Options.Available.
const (
	AvailWriteUnderMask   uint16 = 0x8000
	AvailUnalignedReads   uint16 = 0x4000
	AvailUnalignedWrites  uint16 = 0x2000
	AvailACDIManufacturer uint16 = 0x0800
	AvailACDIUserRead     uint16 = 0x0400
	AvailACDIUserWrite    uint16 = 0x0200
	AvailStreamReads      uint16 = 0x1000
)

// Write length bits in Options.WriteLengths.
const (
	WriteLength1         uint8 = 0x80
	WriteLength2         uint8 = 0x40
	WriteLength4         uint8 = 0x20
	WriteLength64        uint8 = 0x10
	WriteLengthArbitrary uint8 = 0x02
	WriteLengthStream    uint8 = 0x01
)

// Encode builds the options reply datagram.
func (o Options) Encode() []byte {
	p := []byte{ContentType, CmdOptionsReply}
	p = binary.BigEndian.AppendUint16(p, o.Available)
	p = append(p, o.WriteLengths, o.HighestSpace, o.LowestSpace)
	if o.Name != "" {
		p = append(p, o.Name...)
		p = append(p, 0)
	}
	return p
}

// ParseOptions decodes an options reply.
func ParseOptions(payload []byte) (Options, error) {
	if len(payload) < 7 || payload[0] != ContentType || payload[1] != CmdOptionsReply {
		return Options{}, ErrMalformed
	}
	o := Options{
		Available:    binary.BigEndian.Uint16(payload[2:4]),
		WriteLengths: payload[4],
		HighestSpace: payload[5],
		LowestSpace:  payload[6],
	}
	if len(payload) > 7 {
		o.Name, _, _ = strings.Cut(string(payload[7:]), "\x00")
	}
	return o, nil
}

// SpaceInfo describes one address space.
type SpaceInfo struct {
	Space   uint8
	Present bool
	// HighestAddress is the last valid address; the size is one more
	// unless LowestAddress is nonzero.
	HighestAddress uint32
	LowestAddress  uint32
	ReadOnly       bool
	Description    string
}

// Size is the number of addressable bytes.
func (s SpaceInfo) Size() uint32 {
	if !s.Present || s.HighestAddress < s.LowestAddress {
		return 0
	}
	return s.HighestAddress - s.LowestAddress + 1
}

// Flag bits in a space info reply.
const (
	spaceFlagReadOnly   = 0x01
	spaceFlagLowAddress = 0x02
)

// Encode builds the space info reply datagram.
func (s SpaceInfo) Encode() []byte {
	if !s.Present {
		return []byte{ContentType, CmdSpaceInfoReply, s.Space}
	}
	p := []byte{ContentType, CmdSpaceInfoReply | 0x01, s.Space}
	p = binary.BigEndian.AppendUint32(p, s.HighestAddress)
	var flags byte
	if s.ReadOnly {
		flags |= spaceFlagReadOnly
	}
	if s.LowestAddress != 0 {
		flags |= spaceFlagLowAddress
	}
	p = append(p, flags)
	if s.LowestAddress != 0 {
		p = binary.BigEndian.AppendUint32(p, s.LowestAddress)
	}
	if s.Description != "" {
		if s.LowestAddress == 0 {
			p = binary.BigEndian.AppendUint32(p, 0)
		}
		p = append(p, s.Description...)
		p = append(p, 0)
	}
	return p
}

// ParseSpaceInfo decodes a space info reply.
func ParseSpaceInfo(payload []byte) (SpaceInfo, error) {
	if len(payload) < 3 || payload[0] != ContentType || payload[1]&0xFE != CmdSpaceInfoReply {
		return SpaceInfo{}, ErrMalformed
	}
	s := SpaceInfo{Space: payload[2], Present: payload[1]&0x01 != 0}
	if !s.Present {
		return s, nil
	}
	if len(payload) < 8 {
		return SpaceInfo{}, ErrMalformed
	}
	s.HighestAddress = binary.BigEndian.Uint32(payload[3:7])
	flags := payload[7]
	s.ReadOnly = flags&spaceFlagReadOnly != 0
	rest := payload[8:]
	if flags&spaceFlagLowAddress != 0 || len(rest) >= 4 {
		if len(rest) < 4 {
			return SpaceInfo{}, ErrMalformed
		}
		s.LowestAddress = binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
	}
	if len(rest) > 0 {
		s.Description, _, _ = strings.Cut(string(rest), "\x00")
	}
	return s, nil
}

// ParseLockReply returns the node ID holding the lock after a lock
// request, zero if none.
func ParseLockReply(payload []byte) (openlcb.NodeID, error) {
	if len(payload) < 8 || payload[0] != ContentType || payload[1] != CmdLockReply {
		return 0, ErrMalformed
	}
	return openlcb.NodeIDFromBytes([6]byte(payload[2:8])), nil
}
