package entity

import (
	"fmt"
	"strings"

	"github.com/mikesmitty/interlock/openlcb"
)

// ProfileID is the ID of an entity a profile builds from one of a node's
// groups: its kind, the node ID as twelve hex digits and the group's
// slug, as in turnout.0aff9024a634.servo_1.
func ProfileID(kind Kind, node openlcb.NodeID, slug string) string {
	return fmt.Sprintf("%s.%012x.%s", kind, uint64(node), slug)
}

// MoveID returns the ID that an entity built from node from would have if
// it were built from the same group of node to. It reports false for an
// ID that isn't one built from node from.
func MoveID(id string, from, to openlcb.NodeID) (string, bool) {
	kind, rest, ok := strings.Cut(id, ".")
	if !ok {
		return id, false
	}
	node, slug, ok := strings.Cut(rest, ".")
	if !ok || node != fmt.Sprintf("%012x", uint64(from)) || slug == "" {
		return id, false
	}
	return ProfileID(Kind(kind), to, slug), true
}
