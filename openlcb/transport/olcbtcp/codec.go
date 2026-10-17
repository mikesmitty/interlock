// Package olcbtcp carries whole OpenLCB messages over TCP in the framing
// of the OpenLCB TCP Transfer standard, and joins such a connection to
// the CAN side of the hub as a gateway: messages from TCP are put on CAN
// under aliases the gateway allocates for their nodes, and CAN frames are
// assembled into messages for TCP.
package olcbtcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mikesmitty/interlock/openlcb"
)

// Framing: a 16 bit flags word, a 24 bit size counting everything after
// it, the sending gateway's node ID, a 48 bit capture time, then the
// message: MTI, source node ID, destination node ID for addressed MTIs,
// and the payload.
const (
	FlagMessage  = 0x8000 // an OpenLCB message; clear means a link control message
	FlagChaining = 0x4000 // another header follows (never sent, refused when seen)
	FlagNotFirst = 0x0800 // a later part of a fragmented message
	FlagNotLast  = 0x0400 // more parts follow
	flagReserved = 0x3000 // must be zero

	headerLen  = 2 + 3 + 6 + 6
	sizeStart  = 2
	sizeEnd    = 5
	maxSize    = 1<<24 - 1
	MaxPayload = 65536 // longer messages are refused rather than fragmented
)

// Header is what a frame carries besides the message.
type Header struct {
	Flags   uint16
	Gateway openlcb.NodeID
	// Capture is the sender's 48 bit timestamp or sequence number.
	Capture uint64
}

// ErrShort says a buffer does not yet hold a whole frame.
var ErrShort = errors.New("olcbtcp: incomplete frame")

// Encode appends a frame carrying msg to dst. Source and Dest need only
// their IDs.
func Encode(dst []byte, msg *openlcb.Message, gateway openlcb.NodeID, capture uint64) ([]byte, error) {
	if msg.Source.ID == 0 {
		return dst, fmt.Errorf("olcbtcp: message %v has no source node ID", msg.MTI)
	}
	if msg.MTI.IsAddressed() && msg.Dest.ID == 0 {
		return dst, fmt.Errorf("olcbtcp: addressed message %v has no destination node ID", msg.MTI)
	}
	if len(msg.Payload) > MaxPayload {
		return dst, fmt.Errorf("olcbtcp: payload of %d bytes is too long", len(msg.Payload))
	}
	msgLen := 2 + 6 + len(msg.Payload)
	if msg.MTI.IsAddressed() {
		msgLen += 6
	}
	size := 6 + 6 + msgLen
	dst = binary.BigEndian.AppendUint16(dst, FlagMessage)
	dst = append(dst, byte(size>>16), byte(size>>8), byte(size))
	dst = append(dst, gateway.AsSlice()...)
	dst = append(dst, byte(capture>>40), byte(capture>>32), byte(capture>>24), byte(capture>>16), byte(capture>>8), byte(capture))
	dst = binary.BigEndian.AppendUint16(dst, uint16(msg.MTI))
	dst = append(dst, msg.Source.ID.AsSlice()...)
	if msg.MTI.IsAddressed() {
		dst = append(dst, msg.Dest.ID.AsSlice()...)
	}
	return append(dst, msg.Payload...), nil
}

// Decode reads one frame from the start of buf. It returns the message
// (nil for a link control frame, which is skipped), the header and how
// many bytes the frame took. ErrShort means buf needs more bytes; other
// errors mean the frame was bad, and n says how much to discard.
func Decode(buf []byte) (msg *openlcb.Message, hdr Header, n int, err error) {
	if len(buf) < sizeEnd {
		return nil, Header{}, 0, ErrShort
	}
	size := int(buf[sizeStart])<<16 | int(buf[sizeStart+1])<<8 | int(buf[sizeStart+2])
	total := sizeEnd + size
	if size < 12 {
		return nil, Header{}, min(total, len(buf)), fmt.Errorf("olcbtcp: frame size %d is too small", size)
	}
	if len(buf) < total {
		return nil, Header{}, 0, ErrShort
	}
	hdr.Flags = binary.BigEndian.Uint16(buf)
	hdr.Gateway = openlcb.NodeIDFromBytes([6]byte(buf[5:11]))
	hdr.Capture = uint64(buf[11])<<40 | uint64(buf[12])<<32 | uint64(buf[13])<<24 | uint64(buf[14])<<16 | uint64(buf[15])<<8 | uint64(buf[16])
	if hdr.Flags&FlagMessage == 0 {
		return nil, hdr, total, nil
	}
	if hdr.Flags&flagReserved != 0 || hdr.Flags&FlagChaining != 0 {
		return nil, hdr, total, fmt.Errorf("olcbtcp: unsupported flags 0x%04X", hdr.Flags)
	}
	if hdr.Flags&(FlagNotFirst|FlagNotLast) != 0 {
		return nil, hdr, total, errors.New("olcbtcp: fragmented messages are not supported")
	}
	body := buf[headerLen:total]
	if len(body) < 8 {
		return nil, hdr, total, errors.New("olcbtcp: message shorter than its MTI and source")
	}
	msg = &openlcb.Message{MTI: openlcb.MTI(binary.BigEndian.Uint16(body))}
	msg.Source.ID = openlcb.NodeIDFromBytes([6]byte(body[2:8]))
	body = body[8:]
	if msg.MTI.IsAddressed() {
		if len(body) < 6 {
			return nil, hdr, total, fmt.Errorf("olcbtcp: addressed %v without a destination", msg.MTI)
		}
		msg.Dest.ID = openlcb.NodeIDFromBytes([6]byte(body[:6]))
		body = body[6:]
	}
	if len(body) > 0 {
		msg.Payload = append([]byte(nil), body...)
	}
	return msg, hdr, total, nil
}

// Reader reads frames from a stream.
type Reader struct {
	r   io.Reader
	buf []byte
	// Errors counts frames that were bad and skipped.
	Errors uint64
}

// NewReader wraps r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, buf: make([]byte, 0, 4096)}
}

// Next returns the next message, skipping link control and bad frames.
// It returns the stream's error when it ends.
func (r *Reader) Next() (*openlcb.Message, Header, error) {
	for {
		msg, hdr, n, err := Decode(r.buf)
		switch {
		case errors.Is(err, ErrShort):
			if len(r.buf) >= sizeEnd {
				size := int(r.buf[sizeStart])<<16 | int(r.buf[sizeStart+1])<<8 | int(r.buf[sizeStart+2])
				if size > maxSize || sizeEnd+size > 1<<20 {
					r.Errors++
					r.buf = r.buf[:0]
				}
			}
			if err := r.fill(); err != nil {
				return nil, Header{}, err
			}
		case err != nil:
			r.Errors++
			r.buf = r.buf[n:]
		case msg == nil:
			r.buf = r.buf[n:]
		default:
			r.buf = r.buf[n:]
			return msg, hdr, nil
		}
	}
}

// fill reads more bytes, compacting the buffer first.
func (r *Reader) fill() error {
	if cap(r.buf)-len(r.buf) < 1024 {
		fresh := make([]byte, len(r.buf), max(2*cap(r.buf), 4096))
		copy(fresh, r.buf)
		r.buf = fresh
	}
	n, err := r.r.Read(r.buf[len(r.buf):cap(r.buf)])
	r.buf = r.buf[:len(r.buf)+n]
	if n == 0 && err != nil {
		return err
	}
	return nil
}
