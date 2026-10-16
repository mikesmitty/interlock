package can

import (
	"fmt"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

// MaxDatagram is the largest datagram payload.
const MaxDatagram = 72

// assemblyTimeout is how long a partial message waits for its next frame.
const assemblyTimeout = 3 * time.Second

// AssemblyError reports a frame that could not join a message: a middle or
// last frame with no start, a datagram that grew too large, or the like.
// Code is the OpenLCB error code a node should answer with where one
// applies.
type AssemblyError struct {
	Code  openlcb.ErrorCode
	Frame Frame
	Msg   string
}

func (e *AssemblyError) Error() string {
	return fmt.Sprintf("can: %s (%v): %v", e.Msg, e.Code, e.Frame)
}

type assemblyKey struct {
	src, dst openlcb.Alias
	mti      openlcb.MTI // 0 for datagrams
}

type partial struct {
	payload []byte
	updated time.Time
}

// Assembler turns incoming message frames into messages. Single-frame
// messages come out at once; addressed messages split across frames and
// multi-frame datagrams come out when their last frame arrives. Control
// frames and stream frames are not its business and are reported as such.
type Assembler struct {
	// Resolve, when set, supplies the node ID for an alias so message
	// handles carry both. The alias cache's Lookup fits.
	Resolve func(openlcb.Alias) (openlcb.NodeID, bool)

	partials map[assemblyKey]*partial
}

// NewAssembler returns an assembler with no partial messages.
func NewAssembler(resolve func(openlcb.Alias) (openlcb.NodeID, bool)) *Assembler {
	return &Assembler{Resolve: resolve, partials: make(map[assemblyKey]*partial)}
}

func (a *Assembler) handle(alias openlcb.Alias) openlcb.NodeHandle {
	h := openlcb.NodeHandle{Alias: alias}
	if a.Resolve != nil {
		h.ID, _ = a.Resolve(alias)
	}
	return h
}

// Push feeds one frame. It returns a message when one is complete, nil
// when the frame was absorbed into a partial message or is not a message
// frame, and an *AssemblyError when the frame had to be dropped.
func (a *Assembler) Push(f Frame, now time.Time) (*openlcb.Message, error) {
	if !f.IsOpenLCB() {
		return nil, nil
	}
	switch f.Type() {
	case FrameTypeGlobalAddressed:
		return a.pushMessage(f, now)
	case FrameTypeDatagramOnly, FrameTypeDatagramFirst, FrameTypeDatagramMiddle, FrameTypeDatagramLast:
		return a.pushDatagram(f, now)
	default:
		return nil, nil // streams and reserved types
	}
}

func (a *Assembler) pushMessage(f Frame, now time.Time) (*openlcb.Message, error) {
	mti := f.MTI()
	src := f.SourceAlias()
	if !mti.IsAddressed() {
		return &openlcb.Message{MTI: mti, Source: a.handle(src), Payload: append([]byte(nil), f.Payload()...)}, nil
	}
	dst, ok := f.AddressedDest()
	if !ok {
		return nil, &AssemblyError{Code: openlcb.ErrCodeInvalidArguments, Frame: f, Msg: "addressed frame without destination"}
	}
	flags := f.Data[0] & 0xF0
	body := f.Payload()[2:]
	key := assemblyKey{src: src, dst: dst, mti: mti}
	msg := &openlcb.Message{MTI: mti, Source: a.handle(src), Dest: a.handle(dst)}

	switch {
	case flags&flagNotFirst == 0 && flags&flagNotLast == 0:
		delete(a.partials, key) // an abandoned earlier message, if any
		msg.Payload = append([]byte(nil), body...)
		return msg, nil
	case flags&flagNotFirst == 0:
		a.partials[key] = &partial{payload: append([]byte(nil), body...), updated: now}
		return nil, nil
	}
	p, ok := a.partials[key]
	if !ok {
		return nil, &AssemblyError{Code: openlcb.ErrCodeOutOfOrderMiddleWithStart, Frame: f, Msg: "continuation frame without a first frame"}
	}
	p.payload = append(p.payload, body...)
	p.updated = now
	if flags&flagNotLast != 0 {
		return nil, nil
	}
	delete(a.partials, key)
	msg.Payload = p.payload
	return msg, nil
}

func (a *Assembler) pushDatagram(f Frame, now time.Time) (*openlcb.Message, error) {
	src := f.SourceAlias()
	dst := f.DatagramDest()
	key := assemblyKey{src: src, dst: dst}
	msg := &openlcb.Message{MTI: openlcb.MTIDatagram, Source: a.handle(src), Dest: a.handle(dst)}

	switch f.Type() {
	case FrameTypeDatagramOnly:
		delete(a.partials, key)
		msg.Payload = append([]byte(nil), f.Payload()...)
		return msg, nil
	case FrameTypeDatagramFirst:
		if _, ok := a.partials[key]; ok {
			delete(a.partials, key)
			return nil, &AssemblyError{Code: openlcb.ErrCodeOutOfOrderStartBeforeEnd, Frame: f, Msg: "datagram start before previous end"}
		}
		a.partials[key] = &partial{payload: append([]byte(nil), f.Payload()...), updated: now}
		return nil, nil
	}
	p, ok := a.partials[key]
	if !ok {
		return nil, &AssemblyError{Code: openlcb.ErrCodeOutOfOrderMiddleWithStart, Frame: f, Msg: "datagram continuation without start"}
	}
	if len(p.payload)+int(f.Len) > MaxDatagram {
		delete(a.partials, key)
		return nil, &AssemblyError{Code: openlcb.ErrCodeInvalidArguments, Frame: f, Msg: "datagram longer than 72 bytes"}
	}
	p.payload = append(p.payload, f.Payload()...)
	p.updated = now
	if f.Type() == FrameTypeDatagramMiddle {
		return nil, nil
	}
	delete(a.partials, key)
	msg.Payload = p.payload
	return msg, nil
}

// Expire drops partial messages that have waited too long for their next
// frame and returns how many were dropped.
func (a *Assembler) Expire(now time.Time) int {
	dropped := 0
	for key, p := range a.partials {
		if now.Sub(p.updated) > assemblyTimeout {
			delete(a.partials, key)
			dropped++
		}
	}
	return dropped
}

// Pending returns the number of partial messages waiting for frames.
func (a *Assembler) Pending() int { return len(a.partials) }

// Frames splits a message into the frames that carry it on CAN. The
// source alias must be set; addressed messages also need the destination
// alias. Datagrams longer than MaxDatagram and global messages longer than
// 8 bytes return an error.
func Frames(m *openlcb.Message) ([]Frame, error) {
	src := m.Source.Alias
	if src == 0 {
		return nil, fmt.Errorf("can: message %v has no source alias", m.MTI)
	}
	if m.MTI == openlcb.MTIDatagram {
		if m.Dest.Alias == 0 {
			return nil, fmt.Errorf("can: datagram has no destination alias")
		}
		if len(m.Payload) > MaxDatagram {
			return nil, fmt.Errorf("can: datagram of %d bytes exceeds %d", len(m.Payload), MaxDatagram)
		}
		return DatagramFrames(src, m.Dest.Alias, m.Payload), nil
	}
	if m.MTI.IsSpecial() {
		return nil, fmt.Errorf("can: %v is not supported", m.MTI)
	}
	if m.MTI.IsAddressed() {
		if m.Dest.Alias == 0 {
			return nil, fmt.Errorf("can: %v has no destination alias", m.MTI)
		}
		return AddressedFrames(m.MTI, src, m.Dest.Alias, m.Payload), nil
	}
	if len(m.Payload) > 8 {
		return nil, fmt.Errorf("can: global %v payload of %d bytes exceeds 8", m.MTI, len(m.Payload))
	}
	return []Frame{GlobalFrame(m.MTI, src, m.Payload)}, nil
}
