// Package refs describes rewriting what Interlock keeps that names one
// node's entities, events or node ID so it names another node's instead,
// for a board swapped for a new one or given a new node ID. The track
// plan, the automations and the entity settings each rewrite their own
// references; internal/replace works out the renames and runs them.
package refs

import "github.com/mikesmitty/interlock/openlcb"

// Rewrite says what each reference becomes. A function that reports
// false leaves the reference as it is.
type Rewrite struct {
	Entity func(id string) (string, bool)
	Event  func(ev openlcb.EventID) (openlcb.EventID, bool)
	Node   func(id openlcb.NodeID) (openlcb.NodeID, bool)
}

func (rw Rewrite) entity(id string) (string, bool) {
	if rw.Entity == nil || id == "" {
		return id, false
	}
	return rw.Entity(id)
}

// RenameEntity applies the entity rename to *id, reporting a change.
func (rw Rewrite) RenameEntity(id *string) (Change, bool) {
	to, ok := rw.entity(*id)
	if !ok || to == *id {
		return Change{}, false
	}
	c := Change{Kind: KindEntity, From: *id, To: to}
	*id = to
	return c, true
}

// RenameEvent applies the event rename to *ev, reporting a change.
func (rw Rewrite) RenameEvent(ev *openlcb.EventID) (Change, bool) {
	if rw.Event == nil || *ev == 0 {
		return Change{}, false
	}
	to, ok := rw.Event(*ev)
	if !ok || to == *ev {
		return Change{}, false
	}
	c := Change{Kind: KindEvent, From: ev.String(), To: to.String()}
	*ev = to
	return c, true
}

// RenameNode applies the node rename to *id, reporting a change.
func (rw Rewrite) RenameNode(id *openlcb.NodeID) (Change, bool) {
	if rw.Node == nil || *id == 0 {
		return Change{}, false
	}
	to, ok := rw.Node(*id)
	if !ok || to == *id {
		return Change{}, false
	}
	c := Change{Kind: KindNode, From: id.String(), To: to.String()}
	*id = to
	return c, true
}

// The kinds of reference.
const (
	KindEntity = "entity"
	KindEvent  = "event"
	KindNode   = "node"
)

// Change is one reference rewritten, or to be.
type Change struct {
	// Area is where it is kept: "Track plan", "Automations", "Entities".
	Area string `json:"area"`
	// Item is what holds it there, by name: a block, an automation.
	Item string `json:"item"`
	// Field says which of the item's references it is.
	Field string `json:"field"`
	Kind  string `json:"kind"`
	From  string `json:"from"`
	To    string `json:"to"`
	// Missing marks an entity the new node doesn't have (yet).
	Missing bool `json:"missing,omitempty"`
}

// In fills in where a change is.
func (c Change) In(area, item, field string) Change {
	c.Area, c.Item, c.Field = area, item, field
	return c
}
