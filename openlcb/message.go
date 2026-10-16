package openlcb

import "fmt"

// Alias is a 12-bit CAN node alias. Zero means unknown or none.
type Alias uint16

// String formats the alias as 0xNNN.
func (a Alias) String() string { return fmt.Sprintf("0x%03X", uint16(a)) }

// NodeHandle names a node by whichever of its node ID and CAN alias are
// known. Messages assembled from CAN frames always have the alias; the ID
// is filled in when the alias cache knows it.
type NodeHandle struct {
	ID    NodeID
	Alias Alias
}

// String shows the ID, the alias, or both.
func (h NodeHandle) String() string {
	switch {
	case h.ID != 0 && h.Alias != 0:
		return h.ID.String() + " (" + h.Alias.String() + ")"
	case h.ID != 0:
		return h.ID.String()
	case h.Alias != 0:
		return "alias " + h.Alias.String()
	default:
		return "unknown"
	}
}

// IsZero reports whether neither ID nor alias is known.
func (h NodeHandle) IsZero() bool { return h.ID == 0 && h.Alias == 0 }

// Message is one OpenLCB message, independent of the transport that
// carried it. Dest is zero for global messages.
type Message struct {
	MTI     MTI
	Source  NodeHandle
	Dest    NodeHandle
	Payload []byte
}

// IsGlobal reports whether the message has no destination.
func (m *Message) IsGlobal() bool { return !m.MTI.IsAddressed() }

// EventID returns the event ID at the start of the payload for MTIs that
// carry one, and false otherwise.
func (m *Message) EventID() (EventID, bool) {
	if !m.MTI.HasEvent() || len(m.Payload) < 8 {
		return 0, false
	}
	return EventIDFromBytes([8]byte(m.Payload[:8])), true
}

// PayloadNodeID returns the node ID at the start of the payload, for the
// messages whose payload is one (Verified Node ID, Initialization
// Complete, Verify Node ID Global with a target).
func (m *Message) PayloadNodeID() (NodeID, bool) {
	if len(m.Payload) < 6 {
		return 0, false
	}
	return NodeIDFromBytes([6]byte(m.Payload[:6])), true
}

// String describes the message in one line for logs.
func (m *Message) String() string {
	if m.IsGlobal() {
		return fmt.Sprintf("%v from %v %X", m.MTI, m.Source, m.Payload)
	}
	return fmt.Sprintf("%v from %v to %v %X", m.MTI, m.Source, m.Dest, m.Payload)
}
