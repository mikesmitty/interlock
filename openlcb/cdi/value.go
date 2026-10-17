package cdi

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikesmitty/interlock/openlcb"
)

// Values travel as strings: integers in decimal, floats in decimal, event
// IDs as dotted hex, strings as themselves, blobs as hex.

// Signed reports whether an int element holds negative values, which the
// schema leaves to the min value to say.
func (e *Element) Signed() bool {
	if e.Min == "" {
		return false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(e.Min), 0, 64)
	return err == nil && n < 0
}

// Decode turns the stored bytes of a variable into its string value. b
// must be at least Size bytes long. Error messages are written for the
// person editing the value; callers add the variable's name.
func (e *Element) Decode(b []byte) (string, error) {
	if len(b) < e.Size {
		return "", fmt.Errorf("needs %d bytes, only %d were read", e.Size, len(b))
	}
	b = b[:e.Size]
	switch e.Kind {
	case KindInt, KindAction:
		if e.Signed() {
			return strconv.FormatInt(signedInt(b), 10), nil
		}
		return strconv.FormatUint(unsignedInt(b), 10), nil
	case KindFloat:
		var f float64
		switch e.Size {
		case 2:
			f = float64(openlcb.Float16(binary.BigEndian.Uint16(b)))
		case 4:
			f = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
		case 8:
			f = math.Float64frombits(binary.BigEndian.Uint64(b))
		default:
			return "", fmt.Errorf("a %d byte float is not supported", e.Size)
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	case KindString:
		s := b
		for i, c := range s {
			if c == 0 {
				s = s[:i]
				break
			}
		}
		return strings.ToValidUTF8(string(s), "�"), nil
	case KindEventID:
		return openlcb.EventIDFromBytes([8]byte(b)).String(), nil
	case KindBlob:
		return strings.ToUpper(hex.EncodeToString(b)), nil
	default:
		return "", fmt.Errorf("a %s has no value", e.Kind)
	}
}

// Encode turns a string value into the bytes to store, checking it
// against the element's size, range and map.
func (e *Element) Encode(value string) ([]byte, error) {
	switch e.Kind {
	case KindInt, KindAction:
		return e.encodeInt(value)
	case KindFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", value)
		}
		if err := e.checkRange(f); err != nil {
			return nil, err
		}
		out := make([]byte, e.Size)
		switch e.Size {
		case 2:
			binary.BigEndian.PutUint16(out, openlcb.ToFloat16(float32(f)))
		case 4:
			binary.BigEndian.PutUint32(out, math.Float32bits(float32(f)))
		case 8:
			binary.BigEndian.PutUint64(out, math.Float64bits(f))
		default:
			return nil, fmt.Errorf("a %d byte float is not supported", e.Size)
		}
		return out, nil
	case KindString:
		if !utf8.ValidString(value) {
			return nil, errors.New("text is not valid UTF-8")
		}
		if len(value) > e.Size-1 {
			return nil, fmt.Errorf("text is %d bytes; at most %d fit", len(value), e.Size-1)
		}
		out := make([]byte, e.Size)
		copy(out, value)
		return out, nil
	case KindEventID:
		id, err := openlcb.ParseEventID(value)
		if err != nil {
			return nil, errors.New("an event ID is 8 bytes of hex, such as 05.01.01.01.22.00.00.01")
		}
		return id.AsSlice(), nil
	case KindBlob:
		b, err := hex.DecodeString(strings.NewReplacer(" ", "", ".", "", ":", "").Replace(value))
		if err != nil || len(b) != e.Size {
			return nil, fmt.Errorf("expected %d bytes of hex", e.Size)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("a %s has no value", e.Kind)
	}
}

func (e *Element) encodeInt(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if e.Size < 1 || e.Size > 8 {
		return nil, fmt.Errorf("a %d byte integer is not supported", e.Size)
	}
	var n int64
	var u uint64
	signed := e.Signed()
	if signed {
		v, err := strconv.ParseInt(value, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", value)
		}
		bits := uint(e.Size * 8)
		if bits < 64 && (v < -(1<<(bits-1)) || v >= 1<<(bits-1)) {
			return nil, fmt.Errorf("%d does not fit in %d bytes", v, e.Size)
		}
		n = v
		u = uint64(v)
	} else {
		v, err := strconv.ParseUint(value, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", value)
		}
		if e.Size < 8 && v >= 1<<(uint(e.Size)*8) {
			return nil, fmt.Errorf("%d does not fit in %d bytes", v, e.Size)
		}
		u = v
		n = int64(v)
	}
	if len(e.Map) > 0 && e.Kind == KindInt {
		found := false
		for _, rel := range e.Map {
			if p, err := strconv.ParseInt(strings.TrimSpace(rel.Property), 0, 64); err == nil && p == n {
				found = true
				break
			}
		}
		if !found && e.Hints == nil {
			return nil, fmt.Errorf("%d is not one of the choices", n)
		}
	} else if err := e.checkRange(float64(n)); err != nil {
		return nil, err
	}
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, u)
	return out[8-e.Size:], nil
}

func (e *Element) checkRange(v float64) error {
	if e.Min != "" {
		if lo, err := strconv.ParseFloat(strings.TrimSpace(e.Min), 64); err == nil && v < lo {
			return fmt.Errorf("%v is below the minimum of %v", v, lo)
		}
	}
	if e.Max != "" {
		if hi, err := strconv.ParseFloat(strings.TrimSpace(e.Max), 64); err == nil && v > hi {
			return fmt.Errorf("%v is above the maximum of %v", v, hi)
		}
	}
	return nil
}

func unsignedInt(b []byte) uint64 {
	var u uint64
	for _, c := range b {
		u = u<<8 | uint64(c)
	}
	return u
}

func signedInt(b []byte) int64 {
	u := unsignedInt(b)
	shift := uint(64 - len(b)*8)
	return int64(u<<shift) >> shift
}

// DefaultBytes returns the bytes a variable holds at factory defaults:
// the default value when one is given, else zeros.
func (e *Element) DefaultBytes() []byte {
	if e.Default != "" && e.Kind != KindGroup {
		if b, err := e.Encode(e.Default); err == nil {
			return b
		}
	}
	return make([]byte, e.Size)
}
