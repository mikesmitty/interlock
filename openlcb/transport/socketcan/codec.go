package socketcan

import "github.com/mikesmitty/interlock/openlcb/can"

// Codec implements transport.Codec for struct can_frame: one frame per
// read and per write.
type Codec struct {
	partial []byte
}

// Append implements transport.Codec.
func (c *Codec) Append(dst []byte, f can.Frame) []byte {
	b := Marshal(f)
	return append(dst, b[:]...)
}

// Push implements transport.Codec. The kernel delivers whole frames, but
// a short read is pieced together all the same.
func (c *Codec) Push(b []byte) (frames []can.Frame, bad uint64) {
	c.partial = append(c.partial, b...)
	for len(c.partial) >= FrameSize {
		f, ok := Unmarshal(c.partial[:FrameSize])
		c.partial = c.partial[FrameSize:]
		if !ok {
			bad++
			continue
		}
		frames = append(frames, f)
	}
	if len(c.partial) == 0 {
		c.partial = nil
	}
	return frames, bad
}

// MaxWrite implements transport.Codec: a raw CAN socket takes exactly one
// frame per write.
func (c *Codec) MaxWrite() int { return FrameSize }
