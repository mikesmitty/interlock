// Package socketcan joins a Linux SocketCAN interface (can0, a USB CAN
// adapter with a kernel driver, or a virtual vcan0) to the hub. The frame
// layout is the kernel's struct can_frame; the socket code builds on
// Linux only, and New returns ErrUnsupported elsewhere.
package socketcan

import (
	"encoding/binary"
	"errors"

	"github.com/mikesmitty/interlock/openlcb/can"
)

// ErrUnsupported is returned where the operating system has no SocketCAN.
var ErrUnsupported = errors.New("socketcan: only available on Linux")

// Kind is the link kind reported in Status.
const Kind = "socketcan"

// struct can_frame: a 32 bit identifier with flag bits, the data length,
// three bytes of padding and eight bytes of data.
const (
	FrameSize = 16
	flagEFF   = 0x80000000 // extended 29 bit identifier
	flagRTR   = 0x40000000 // remote transmission request
	flagERR   = 0x20000000 // error frame from the driver
	maskEFF   = 0x1FFFFFFF
	maskSFF   = 0x7FF
)

// Marshal lays a frame out as the kernel expects it, in host byte order
// (little endian on every platform Interlock runs on).
func Marshal(f can.Frame) [FrameSize]byte {
	var b [FrameSize]byte
	id := f.ID
	if f.Extended {
		id = id&maskEFF | flagEFF
	} else {
		id &= maskSFF
	}
	binary.LittleEndian.PutUint32(b[:4], id)
	b[4] = f.Len
	copy(b[8:], f.Data[:f.Len])
	return b
}

// Unmarshal reads a frame the kernel delivered. Error and remote frames
// are not OpenLCB traffic and come back with ok false.
func Unmarshal(b []byte) (f can.Frame, ok bool) {
	if len(b) < FrameSize {
		return can.Frame{}, false
	}
	id := binary.LittleEndian.Uint32(b[:4])
	if id&(flagERR|flagRTR) != 0 {
		return can.Frame{}, false
	}
	f.Extended = id&flagEFF != 0
	if f.Extended {
		f.ID = id & maskEFF
	} else {
		f.ID = id & maskSFF
	}
	f.Len = min(b[4], 8)
	copy(f.Data[:], b[8:8+f.Len])
	return f, true
}
