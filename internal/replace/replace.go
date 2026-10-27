// Package replace moves what Interlock keeps about one node onto another:
// for a board swapped for a new one after a failure, or given a new node
// ID. The entities a profile builds from a node are named after its node
// ID, and its default events change with it, so everything that names
// them is rewritten: the track plan's links, the automations, and the
// settings and events of entities.
package replace

import (
	"context"
	"fmt"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/refs"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
)

// Part is something that keeps references and rewrites its own.
type Part interface {
	Rewrite(rw refs.Rewrite, apply bool) ([]refs.Change, error)
}

// Nodes is the node table.
type Nodes interface {
	Get(id openlcb.NodeID) (registry.Info, bool)
	Forget(ctx context.Context, id openlcb.NodeID) error
}

// Events is the named event table.
type Events interface {
	NodeEntries(id openlcb.NodeID) []eventtable.Entry
}

// Entities looks entities up by ID.
type Entities interface {
	Get(id string) (entity.Entity, bool)
}

// Replacer moves references from one node to another.
type Replacer struct {
	Nodes    Nodes
	Events   Events
	Entities Entities
	// Parts are rewritten in this order.
	Parts []Part
}

// Result is what a replacement changed, or would change.
type Result struct {
	From    openlcb.NodeID `json:"from"`
	To      openlcb.NodeID `json:"to"`
	Changes []refs.Change  `json:"changes"`
	// Notes say what was left alone and why.
	Notes     []string `json:"notes,omitempty"`
	Applied   bool     `json:"applied"`
	Forgotten bool     `json:"forgotten,omitempty"`
}

// InvalidError says why a replacement can't be made.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return "replace: " + e.Reason }

// Replace works out what naming node from must name node to instead, and
// with apply rewrites it; with forget as well, node from is forgotten
// afterward. Node to must have been seen on the bus, so its entities and
// events are known; node from may already be forgotten.
func (r *Replacer) Replace(ctx context.Context, from, to openlcb.NodeID, apply, forget bool) (Result, error) {
	res := Result{From: from, To: to, Changes: []refs.Change{}}
	if from == to {
		return res, &InvalidError{Reason: "a node can't replace itself"}
	}
	newInfo, ok := r.Nodes.Get(to)
	if !ok {
		return res, &InvalidError{Reason: fmt.Sprintf("node %s hasn't been seen on the bus; connect the new board first", to)}
	}
	if newInfo.Local {
		return res, &InvalidError{Reason: "Interlock's own node can't take another node's place"}
	}
	oldInfo, known := r.Nodes.Get(from)

	events, note := r.eventMap(from, to, oldInfo, known, newInfo)
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	rw := refs.Rewrite{
		Entity: func(id string) (string, bool) { return entity.MoveID(id, from, to) },
		Event: func(ev openlcb.EventID) (openlcb.EventID, bool) {
			moved, ok := events[ev]
			return moved, ok
		},
		Node: func(id openlcb.NodeID) (openlcb.NodeID, bool) { return to, id == from },
	}
	for _, p := range r.Parts {
		changes, err := p.Rewrite(rw, apply)
		res.Changes = append(res.Changes, changes...)
		if err != nil {
			return res, err
		}
	}

	missing := 0
	for i, c := range res.Changes {
		if c.Kind != refs.KindEntity {
			continue
		}
		if _, ok := r.Entities.Get(c.To); !ok {
			res.Changes[i].Missing = true
			missing++
		}
	}
	if missing > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d of the new node's entities aren't there yet. It may be a different kind of board, or Interlock hasn't read its configuration yet; the links will work once the entities appear.", missing))
	}

	if !apply {
		return res, nil
	}
	res.Applied = true
	if forget && known {
		if err := r.Nodes.Forget(ctx, from); err != nil {
			return res, fmt.Errorf("forget node %s: %w", from, err)
		}
		res.Forgotten = true
	}
	return res, nil
}

// eventMap matches the old node's events to the new node's by where they
// are in the configuration, which only means the same thing on the same
// kind of board.
func (r *Replacer) eventMap(from, to openlcb.NodeID, oldInfo registry.Info, known bool, newInfo registry.Info) (map[openlcb.EventID]openlcb.EventID, string) {
	old := r.Events.NodeEntries(from)
	if len(old) == 0 {
		return nil, "Interlock has no record of the old node's events, so automations and hand-built entities that use its events are left as they are."
	}
	if known && !sameModel(oldInfo, newInfo) {
		return nil, "The two nodes aren't the same model, so their events weren't matched; automations and hand-built entities that use the old node's events are left as they are."
	}
	current := r.Events.NodeEntries(to)
	if len(current) == 0 {
		return nil, "Interlock hasn't read the new node's events yet, so automations and hand-built entities that use the old node's events are left as they are. Try again once its configuration has been read."
	}
	byKey := make(map[string]openlcb.EventID, len(current))
	for _, e := range current {
		byKey[e.Key] = e.Event
	}
	moves := map[openlcb.EventID]openlcb.EventID{}
	for _, e := range old {
		if moved, ok := byKey[e.Key]; ok && e.Event != 0 && moved != 0 && moved != e.Event {
			moves[e.Event] = moved
		}
	}
	return moves, ""
}

func sameModel(a, b registry.Info) bool {
	return a.SNIP != nil && b.SNIP != nil && a.SNIP.Manufacturer == b.SNIP.Manufacturer && a.SNIP.Model == b.SNIP.Model
}
