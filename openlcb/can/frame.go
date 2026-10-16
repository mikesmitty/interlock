package can

import (
	"fmt"

	"github.com/mikesmitty/interlock/openlcb"
)

// Frame is one CAN frame. OpenLCB uses extended (29-bit) identifiers and
// no remote frames; the other forms are carried for hub transparency.
type Frame struct {
	ID       uint32
	Extended bool
	RTR      bool
	Len      uint8
	Data     [8]byte
}

// FrameType is the OpenLCB frame type field of a message frame (bits 26
// to 24 of the identifier).
type FrameType uint8

// Message frame types.
const (
	FrameTypeGlobalAddressed FrameType = 1
	FrameTypeDatagramOnly    FrameType = 2
	FrameTypeDatagramFirst   FrameType = 3
	FrameTypeDatagramMiddle  FrameType = 4
	FrameTypeDatagramLast    FrameType = 5
	FrameTypeStreamData      FrameType = 7
)

// Control frame fields (bits 26 to 12 of a control frame identifier).
// CID frames carry a sequence number 7 to 4 in the top three bits and
// twelve bits of the node ID below; the rest use the values here.
const (
	ControlRID uint16 = 0x0700
	ControlAMD uint16 = 0x0701
	ControlAME uint16 = 0x0702
	ControlAMR uint16 = 0x0703
)

// Identifier layout.
const (
	idPriorityBit   = 1 << 28
	idMessageBit    = 1 << 27
	idFrameTypeMask = 0x07000000
	idFieldMask     = 0x00FFF000
	idControlMask   = 0x07FFF000
	idAliasMask     = 0x00000FFF
)

// Addressed payload flags in the first payload byte of an addressed
// message frame, above the destination alias.
const (
	flagNotFirst = 0x20
	flagNotLast  = 0x10
)

// New returns an extended frame. data longer than 8 bytes is truncated.
func New(id uint32, data []byte) Frame {
	f := Frame{ID: id & 0x1FFFFFFF, Extended: true}
	f.Len = uint8(copy(f.Data[:], data))
	return f
}

// Payload returns the data bytes actually present.
func (f Frame) Payload() []byte { return f.Data[:f.Len] }

// IsOpenLCB reports whether this is an OpenLCB message frame (rather than a
// control frame or a non-OpenLCB frame).
func (f Frame) IsOpenLCB() bool { return f.Extended && f.ID&idMessageBit != 0 }

// IsControl reports whether this is an OpenLCB CAN control frame (CID,
// RID, AMD, AME, AMR).
func (f Frame) IsControl() bool { return f.Extended && f.ID&idMessageBit == 0 }

// SourceAlias returns the low twelve bits: the sender's alias.
func (f Frame) SourceAlias() openlcb.Alias { return openlcb.Alias(f.ID & idAliasMask) }

// Type returns the frame type of a message frame.
func (f Frame) Type() FrameType { return FrameType((f.ID & idFrameTypeMask) >> 24) }

// MTI returns the MTI of a global or addressed message frame.
func (f Frame) MTI() openlcb.MTI { return openlcb.MTI((f.ID & idFieldMask) >> 12) }

// DatagramDest returns the destination alias of a datagram or stream frame.
func (f Frame) DatagramDest() openlcb.Alias { return openlcb.Alias((f.ID & idFieldMask) >> 12) }

// AddressedDest returns the destination alias from the first two payload
// bytes of an addressed message frame, and false if they are missing.
func (f Frame) AddressedDest() (openlcb.Alias, bool) {
	if f.Len < 2 {
		return 0, false
	}
	return openlcb.Alias(uint16(f.Data[0]&0x0F)<<8 | uint16(f.Data[1])), true
}

// ControlField returns bits 26 to 12 of a control frame: the sequence and
// node ID bits of a CID, or one of the Control* values.
func (f Frame) ControlField() uint16 { return uint16((f.ID & idControlMask) >> 12) }

// ControlSequence returns the CID sequence number (7 to 4), or 0 for the
// other control frames.
func (f Frame) ControlSequence() uint8 { return uint8((f.ID & idFrameTypeMask) >> 24) }

// IsCID reports whether this is a Check ID frame.
func (f Frame) IsCID() bool { return f.IsControl() && f.ControlSequence() >= 4 }

// String returns the GridConnect form.
func (f Frame) String() string { return string(AppendGridConnect(nil, f)) }

// PayloadNodeID returns the node ID in the first six payload bytes of an
// AMD, AME or AMR frame.
func (f Frame) PayloadNodeID() (openlcb.NodeID, bool) {
	if f.Len < 6 {
		return 0, false
	}
	return openlcb.NodeIDFromBytes([6]byte(f.Data[:6])), true
}

func controlID(field uint16, sequence uint8, src openlcb.Alias) uint32 {
	return idPriorityBit | uint32(sequence&7)<<24 | uint32(field&0x0FFF)<<12 | uint32(src&0xFFF)
}

// CIDFrame returns Check ID frame n (1 to 4) for the given node ID and the
// alias being checked. Frame 1 carries the top twelve bits of the ID.
func CIDFrame(n int, id openlcb.NodeID, alias openlcb.Alias) Frame {
	if n < 1 || n > 4 {
		panic(fmt.Sprintf("can: CID frame number %d out of range", n))
	}
	sequence := uint8(8 - n) // 7, 6, 5, 4
	bits := uint16(uint64(id)>>(12*(4-n))) & 0x0FFF
	return New(controlID(bits, sequence, alias), nil)
}

// RIDFrame returns a Reserve ID frame.
func RIDFrame(alias openlcb.Alias) Frame { return New(controlID(ControlRID, 0, alias), nil) }

// AMDFrame returns an Alias Map Definition frame announcing id at alias.
func AMDFrame(alias openlcb.Alias, id openlcb.NodeID) Frame {
	b := id.Bytes()
	return New(controlID(ControlAMD, 0, alias), b[:])
}

// AMEFrame returns an Alias Map Enquiry. A zero id asks every node to
// answer; otherwise only the node with that ID does.
func AMEFrame(alias openlcb.Alias, id openlcb.NodeID) Frame {
	if id == 0 {
		return New(controlID(ControlAME, 0, alias), nil)
	}
	b := id.Bytes()
	return New(controlID(ControlAME, 0, alias), b[:])
}

// AMRFrame returns an Alias Map Reset, withdrawing the alias for id.
func AMRFrame(alias openlcb.Alias, id openlcb.NodeID) Frame {
	b := id.Bytes()
	return New(controlID(ControlAMR, 0, alias), b[:])
}

func messageID(frameType FrameType, field uint16, src openlcb.Alias) uint32 {
	return idPriorityBit | idMessageBit | uint32(frameType&7)<<24 | uint32(field&0x0FFF)<<12 | uint32(src&0xFFF)
}

// GlobalFrame returns the single frame of a global message. The payload
// must be 8 bytes or fewer.
func GlobalFrame(mti openlcb.MTI, src openlcb.Alias, payload []byte) Frame {
	return New(messageID(FrameTypeGlobalAddressed, uint16(mti), src), payload)
}

// AddressedFrames splits an addressed message into frames: each carries
// the destination alias and flags in its first two bytes and up to six
// bytes of payload. An empty payload still produces one frame.
func AddressedFrames(mti openlcb.MTI, src, dst openlcb.Alias, payload []byte) []Frame {
	id := messageID(FrameTypeGlobalAddressed, uint16(mti), src)
	var frames []Frame
	for first := true; first || len(payload) > 0; first = false {
		chunk := payload
		if len(chunk) > 6 {
			chunk = chunk[:6]
		}
		payload = payload[len(chunk):]
		var flags byte
		if !first {
			flags |= flagNotFirst
		}
		if len(payload) > 0 {
			flags |= flagNotLast
		}
		data := make([]byte, 0, 8)
		data = append(data, flags|byte(dst>>8), byte(dst))
		data = append(data, chunk...)
		frames = append(frames, New(id, data))
	}
	return frames
}

// DatagramFrames splits a datagram (up to 72 bytes) into frames: one
// "only" frame for 8 bytes or fewer, otherwise first, middle and last
// frames of 8 bytes each.
func DatagramFrames(src, dst openlcb.Alias, payload []byte) []Frame {
	if len(payload) <= 8 {
		return []Frame{New(messageID(FrameTypeDatagramOnly, uint16(dst), src), payload)}
	}
	var frames []Frame
	for i := 0; i < len(payload); i += 8 {
		end := min(i+8, len(payload))
		frameType := FrameTypeDatagramMiddle
		switch {
		case i == 0:
			frameType = FrameTypeDatagramFirst
		case end == len(payload):
			frameType = FrameTypeDatagramLast
		}
		frames = append(frames, New(messageID(frameType, uint16(dst), src), payload[i:end]))
	}
	return frames
}
