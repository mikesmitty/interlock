package can

import (
	"errors"
	"fmt"
)

// GridConnect is the text framing used over serial and TCP links:
// ":X" + hex identifier + "N" + hex data + ";" for extended frames, "S"
// for standard ones and "R" in place of "N" for remote frames.

// ErrGridConnect reports text that is not a GridConnect frame.
var ErrGridConnect = errors.New("can: malformed GridConnect frame")

const hexDigits = "0123456789ABCDEF"

// AppendGridConnect appends the GridConnect form of f to dst.
func AppendGridConnect(dst []byte, f Frame) []byte {
	dst = append(dst, ':')
	if f.Extended {
		dst = append(dst, 'X')
		for shift := 28; shift >= 0; shift -= 4 {
			dst = append(dst, hexDigits[(f.ID>>uint(shift))&0xF])
		}
	} else {
		dst = append(dst, 'S')
		for shift := 8; shift >= 0; shift -= 4 {
			dst = append(dst, hexDigits[(f.ID>>uint(shift))&0xF])
		}
	}
	if f.RTR {
		dst = append(dst, 'R')
	} else {
		dst = append(dst, 'N')
	}
	for _, b := range f.Payload() {
		dst = append(dst, hexDigits[b>>4], hexDigits[b&0xF])
	}
	return append(dst, ';')
}

// ParseGridConnect reads exactly one frame from s.
func ParseGridConnect(s string) (Frame, error) {
	var p GridConnectParser
	frames := p.Push([]byte(s))
	if len(frames) != 1 || p.Errors != 0 {
		return Frame{}, fmt.Errorf("%w: %q", ErrGridConnect, s)
	}
	return frames[0], nil
}

// GridConnectParser extracts frames from a byte stream, tolerating line
// endings, other text and frames split across reads.
type GridConnectParser struct {
	// Errors counts malformed frames dropped so far.
	Errors int

	buf     []byte
	inFrame bool
}

// maxFrameText bounds a frame's text so garbage without a ';' cannot grow
// the buffer forever.
const maxFrameText = 64

// Push consumes bytes and returns the complete frames they finished.
func (p *GridConnectParser) Push(data []byte) []Frame {
	var frames []Frame
	for _, c := range data {
		switch {
		case c == ':':
			p.buf = p.buf[:0]
			p.inFrame = true
		case !p.inFrame:
			// Text between frames: line endings, prompts, noise.
		case c == ';':
			p.inFrame = false
			if f, err := decodeGridConnect(p.buf); err == nil {
				frames = append(frames, f)
			} else {
				p.Errors++
			}
		case len(p.buf) >= maxFrameText:
			p.inFrame = false
			p.Errors++
		default:
			p.buf = append(p.buf, c)
		}
	}
	return frames
}

// decodeGridConnect decodes the text between ':' and ';'.
func decodeGridConnect(text []byte) (Frame, error) {
	if len(text) < 2 {
		return Frame{}, ErrGridConnect
	}
	var f Frame
	var maxIDDigits int
	switch text[0] {
	case 'X', 'x':
		f.Extended = true
		maxIDDigits = 8
	case 'S', 's':
		maxIDDigits = 3
	default:
		return Frame{}, ErrGridConnect
	}
	i := 1
	for ; i < len(text); i++ {
		v, ok := hexValue(text[i])
		if !ok {
			break
		}
		if i > maxIDDigits {
			return Frame{}, ErrGridConnect
		}
		f.ID = f.ID<<4 | uint32(v)
	}
	if i == 1 || i >= len(text) {
		return Frame{}, ErrGridConnect
	}
	switch text[i] {
	case 'N', 'n':
	case 'R', 'r':
		f.RTR = true
	default:
		return Frame{}, ErrGridConnect
	}
	data := text[i+1:]
	if len(data)%2 != 0 || len(data) > 16 {
		return Frame{}, ErrGridConnect
	}
	for j := 0; j < len(data); j += 2 {
		hi, ok1 := hexValue(data[j])
		lo, ok2 := hexValue(data[j+1])
		if !ok1 || !ok2 {
			return Frame{}, ErrGridConnect
		}
		f.Data[f.Len] = hi<<4 | lo
		f.Len++
	}
	if f.Extended && f.ID > 0x1FFFFFFF || !f.Extended && f.ID > 0x7FF {
		return Frame{}, ErrGridConnect
	}
	return f, nil
}

func hexValue(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
