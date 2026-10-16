package openlcb

import (
	"bytes"
	"errors"
	"fmt"
)

// SNIP is Simple Node Identification Information: who made a node and
// what the user named it.
type SNIP struct {
	Manufacturer    string
	Model           string
	HardwareVersion string
	SoftwareVersion string
	UserName        string
	UserDescription string
}

// Maximum string lengths from the SNIP standard, not counting the
// terminating null.
const (
	snipMaxManufacturer    = 40
	snipMaxModel           = 40
	snipMaxHardwareVersion = 20
	snipMaxSoftwareVersion = 20
	snipMaxUserName        = 62
	snipMaxUserDescription = 63
)

// ErrSNIPVersion reports a SNIP reply whose version bytes are not ones
// this package understands.
var ErrSNIPVersion = errors.New("openlcb: unsupported SNIP version")

// Encode produces the wire form: manufacturer section version 4 with four
// null-terminated strings, then user section version 2 with two. Strings
// are cut to the standard's limits.
func (s SNIP) Encode() []byte {
	var buf bytes.Buffer
	buf.WriteByte(4)
	writeSNIPString(&buf, s.Manufacturer, snipMaxManufacturer)
	writeSNIPString(&buf, s.Model, snipMaxModel)
	writeSNIPString(&buf, s.HardwareVersion, snipMaxHardwareVersion)
	writeSNIPString(&buf, s.SoftwareVersion, snipMaxSoftwareVersion)
	buf.WriteByte(2)
	writeSNIPString(&buf, s.UserName, snipMaxUserName)
	writeSNIPString(&buf, s.UserDescription, snipMaxUserDescription)
	return buf.Bytes()
}

func writeSNIPString(buf *bytes.Buffer, s string, maxLen int) {
	b := []byte(s)
	if idx := bytes.IndexByte(b, 0); idx >= 0 {
		b = b[:idx]
	}
	if len(b) > maxLen {
		b = b[:maxLen]
	}
	buf.Write(b)
	buf.WriteByte(0)
}

// ParseSNIP reads a SNIP reply. The manufacturer section may be version 1
// or 4 and the user section version 1 or 2, each followed by its strings.
// A reply cut short by the sender still yields the strings that arrived.
func ParseSNIP(data []byte) (SNIP, error) {
	var s SNIP
	if len(data) == 0 {
		return s, fmt.Errorf("%w: empty reply", ErrSNIPVersion)
	}
	if v := data[0]; v != 1 && v != 4 {
		return s, fmt.Errorf("%w: manufacturer section version %d", ErrSNIPVersion, v)
	}
	rest := data[1:]
	fields := []*string{&s.Manufacturer, &s.Model, &s.HardwareVersion, &s.SoftwareVersion}
	for _, field := range fields {
		var ok bool
		*field, rest, ok = readSNIPString(rest)
		if !ok {
			return s, nil
		}
	}
	if len(rest) == 0 {
		return s, nil
	}
	if v := rest[0]; v != 1 && v != 2 {
		return s, fmt.Errorf("%w: user section version %d", ErrSNIPVersion, v)
	}
	rest = rest[1:]
	for _, field := range []*string{&s.UserName, &s.UserDescription} {
		var ok bool
		*field, rest, ok = readSNIPString(rest)
		if !ok {
			return s, nil
		}
	}
	return s, nil
}

// readSNIPString returns the string up to the next null and the remainder.
// Without a null it returns what is there and ok false.
func readSNIPString(data []byte) (value string, rest []byte, ok bool) {
	idx := bytes.IndexByte(data, 0)
	if idx < 0 {
		return string(data), nil, false
	}
	return string(data[:idx]), data[idx+1:], true
}
