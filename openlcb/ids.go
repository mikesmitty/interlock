package openlcb

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// NodeID is a 48-bit OpenLCB node identifier, held in the low 48 bits.
// Zero is not a valid node ID.
type NodeID uint64

// EventID is a 64-bit OpenLCB event identifier. By convention a node's own
// events use its node ID as the upper six bytes; see NodeID.Event.
type EventID uint64

const (
	// NodeIDMax is the largest value a 48-bit node ID can take.
	NodeIDMax NodeID = 1<<48 - 1

	// LocalNodeIDPrefix is the first byte of the locally assigned range in
	// the OpenLCB Unique Identifiers standard: IDs a layout hands out itself
	// when a node has no manufacturer or OpenLCB assigned ID. They cannot
	// collide with assigned IDs, only with another layout's own allocations.
	LocalNodeIDPrefix NodeID = 0x0A0000000000
)

// ErrInvalidNodeID reports a node ID that is zero, too large or unparsable.
var ErrInvalidNodeID = errors.New("openlcb: invalid node ID")

// ErrInvalidEventID reports an event ID that could not be parsed.
var ErrInvalidEventID = errors.New("openlcb: invalid event ID")

// Valid reports whether the ID is non-zero and fits in 48 bits.
func (id NodeID) Valid() bool {
	return id != 0 && id <= NodeIDMax
}

// String formats the ID as six dotted hex bytes, for example 05.01.01.01.22.00.
func (id NodeID) String() string {
	return formatHexBytes(uint64(id), 6)
}

// Bytes returns the ID in wire (big-endian) order.
func (id NodeID) Bytes() [6]byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(id))
	var out [6]byte
	copy(out[:], buf[2:])
	return out
}

// AsSlice returns the wire-order bytes as a new slice, for building payloads.
func (id NodeID) AsSlice() []byte {
	b := id.Bytes()
	return b[:]
}

// NodeIDFromBytes builds an ID from six wire-order bytes.
func NodeIDFromBytes(b [6]byte) NodeID {
	var buf [8]byte
	copy(buf[2:], b[:])
	return NodeID(binary.BigEndian.Uint64(buf[:]))
}

// IsLocal reports whether the ID lies in the locally assigned range.
func (id NodeID) IsLocal() bool {
	return id>>40 == LocalNodeIDPrefix>>40
}

// Event returns the event ID formed from this node ID and a two-byte suffix,
// the usual layout for events a node produces or consumes by default.
func (id NodeID) Event(suffix uint16) EventID {
	return EventID(id)<<16 | EventID(suffix)
}

// MarshalText formats the ID as dotted hex, so it reads well in JSON and YAML.
func (id NodeID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText parses dotted hex, as ParseNodeID does.
func (id *NodeID) UnmarshalText(text []byte) error {
	parsed, err := ParseNodeID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ParseNodeID reads a node ID written as six hex bytes, separated by dots
// or colons or run together: 05.01.01.01.22.00, 05:01:01:01:22:00 or
// 050101012200. Case does not matter. Zero is rejected.
func ParseNodeID(s string) (NodeID, error) {
	value, err := parseHexBytes(s, 6)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidNodeID, s)
	}
	id := NodeID(value)
	if !id.Valid() {
		return 0, fmt.Errorf("%w: %q", ErrInvalidNodeID, s)
	}
	return id, nil
}

// RandomLocalNodeID returns a fresh ID in the locally assigned range: the
// 0A prefix over 40 random bits.
func RandomLocalNodeID() (NodeID, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("openlcb: random node ID: %w", err)
	}
	var buf [8]byte
	copy(buf[3:], b[:])
	return LocalNodeIDPrefix | NodeID(binary.BigEndian.Uint64(buf[:])), nil
}

// String formats the ID as eight dotted hex bytes.
func (id EventID) String() string {
	return formatHexBytes(uint64(id), 8)
}

// Bytes returns the ID in wire (big-endian) order.
func (id EventID) Bytes() [8]byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], uint64(id))
	return out
}

// AsSlice returns the wire-order bytes as a new slice, for building payloads.
func (id EventID) AsSlice() []byte {
	b := id.Bytes()
	return b[:]
}

// EventIDFromBytes builds an ID from eight wire-order bytes.
func EventIDFromBytes(b [8]byte) EventID {
	return EventID(binary.BigEndian.Uint64(b[:]))
}

// Node returns the node ID in the upper six bytes of the event ID. It is
// only meaningful for events that follow the node ID plus suffix convention.
func (id EventID) Node() NodeID {
	return NodeID(id >> 16)
}

// Suffix returns the low two bytes of the event ID.
func (id EventID) Suffix() uint16 {
	return uint16(id)
}

// MarshalText formats the ID as dotted hex.
func (id EventID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText parses dotted hex, as ParseEventID does.
func (id *EventID) UnmarshalText(text []byte) error {
	parsed, err := ParseEventID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ParseEventID reads an event ID written as eight hex bytes, separated by
// dots or colons or run together. Unlike node IDs, zero is allowed: an
// all-zero event ID is how configuration tools write "no event".
func ParseEventID(s string) (EventID, error) {
	value, err := parseHexBytes(s, 8)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidEventID, s)
	}
	return EventID(value), nil
}

// formatHexBytes writes the low n bytes of value as dotted upper-case hex.
func formatHexBytes(value uint64, n int) string {
	var sb strings.Builder
	for i := n - 1; i >= 0; i-- {
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		fmt.Fprintf(&sb, "%02X", byte(value>>(8*i)))
	}
	return sb.String()
}

// parseHexBytes reads exactly n hex bytes from s: two digits per byte,
// separated by dots or colons or run together.
func parseHexBytes(s string, n int) (uint64, error) {
	var parts []string
	switch {
	case strings.ContainsRune(s, '.'):
		parts = strings.Split(s, ".")
	case strings.ContainsRune(s, ':'):
		parts = strings.Split(s, ":")
	default:
		if len(s) != 2*n {
			return 0, fmt.Errorf("expected %d hex digits, got %d", 2*n, len(s))
		}
		parts = make([]string, n)
		for i := range parts {
			parts[i] = s[2*i : 2*i+2]
		}
	}
	if len(parts) != n {
		return 0, fmt.Errorf("expected %d bytes, got %d", n, len(parts))
	}
	var value uint64
	for _, part := range parts {
		if len(part) != 2 {
			return 0, fmt.Errorf("byte %q must be two hex digits", part)
		}
		b, err := strconv.ParseUint(part, 16, 8)
		if err != nil {
			return 0, fmt.Errorf("byte %q must be two hex digits", part)
		}
		value = value<<8 | b
	}
	return value, nil
}
