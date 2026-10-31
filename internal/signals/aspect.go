package signals

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// world is what a signal's aspect depends on.
type world struct {
	plan     trackplan.Plan
	net      *trackplan.Network
	blocks   map[string]trackplan.Block
	routes   []interlocking.Route
	trains   map[string]tracking.Train
	entities map[string]entity.Entity
}

// position is how a junction is set, as its turnout says: "" while it
// moves or is unknown. A junction without a turnout stays closed.
func (w *world) position(junction string) trackplan.Position {
	id := w.net.Switch(junction)
	if id == "" {
		return trackplan.Closed
	}
	switch w.entities[id].State {
	case entity.StateClosed:
		return trackplan.Closed
	case entity.StateThrown:
		return trackplan.Thrown
	}
	return ""
}

// look is what a signal says before the signal ahead is taken into
// account: stop, or approach, which that may lift to clear. route is the
// way on when it doesn't say stop.
type look struct {
	view  Signal
	route interlocking.Route
}

// evaluate works out what every signal shows. Whether a signal says stop
// doesn't depend on any other signal, so a signal need look only one
// ahead.
func (w *world) evaluate() []Signal {
	looks := make([]look, len(w.plan.Signals))
	proceeds := make(map[string]bool, len(looks))
	for i, sg := range w.plan.Signals {
		looks[i] = w.look(sg)
		looks[i].view.ID = sg.ID
		proceeds[sg.ID] = looks[i].view.Indication != Stop
	}
	out := make([]Signal, len(looks))
	for i, sg := range w.plan.Signals {
		v := looks[i].view
		if v.Indication != Stop {
			if next, ok := w.next(looks[i].route); ok && proceeds[next] {
				v.Indication = Clear
			}
		}
		v.Upper, v.Lower = aspects(v, sg)
		out[i] = v
	}
	return out
}

func stopped(kind, format string, args ...any) look {
	return look{view: Signal{Kind: kind, Indication: Stop, Reason: fmt.Sprintf(format, args...)}}
}

func (w *world) look(sg trackplan.Signal) look {
	leg := trackplan.Leg{Piece: sg.Track, Reverse: sg.Reverse}
	if w.net.Block(leg) == "" {
		return stopped("", "It stands on track in no block, so it has no block end to govern.")
	}
	port, _, err := w.net.BlockEnd(leg, w.position)
	var against *trackplan.SetAgainstError
	switch {
	case errors.Is(err, trackplan.ErrTrackEnds):
		return stopped("", "The track ends ahead.")
	case errors.As(err, &against):
		return stopped("", "A turnout in the block isn't set for the way out.")
	case err != nil:
		return stopped("", "The block has no end ahead.")
	}
	var ways []interlocking.Route
	home := false
	for _, r := range w.routes {
		if r.From.Block == port.Block && r.From.Point == port.Point {
			ways = append(ways, r)
			home = home || slices.ContainsFunc(r.Legs, func(l trackplan.Leg) bool { return l.Junction })
		}
	}
	switch {
	case len(ways) == 0:
		return stopped("", "No track leads on from here to another block.")
	case home:
		return w.home(ways)
	}
	return w.automatic(ways[0])
}

// home is a signal with a junction ahead, which stays at stop until a
// route is set past it and goes back as soon as a train takes it.
func (w *world) home(ways []interlocking.Route) look {
	i := slices.IndexFunc(ways, func(r interlocking.Route) bool { return r.State != "" })
	if i < 0 {
		return stopped(KindHome, "No route is set.")
	}
	r := ways[i]
	if len(r.Waiting) > 0 {
		return stopped(KindHome, "The route's turnouts aren't in place yet.")
	}
	// A route's state is no help here: it counts a train standing just
	// short of it as on it, and that train is the one waiting to go.
	if name, ok := w.onLegs(r.Legs); ok {
		return stopped(KindHome, "%s is on the route.", name)
	}
	if r.Released > 0 {
		return stopped(KindHome, "A train has gone through on the route.")
	}
	if name, ok := w.occupied(r.To.Block); ok {
		return stopped(KindHome, "%s is in %s.", name, w.blockName(r.To.Block))
	}
	return look{view: Signal{Kind: KindHome, Indication: Approach, Route: r.ID, Diverging: slices.ContainsFunc(r.Legs, w.net.Diverges)}, route: r}
}

// automatic is a signal with plain track ahead, which follows the trains
// and needs no route.
func (w *world) automatic(r interlocking.Route) look {
	if name, ok := w.onLegs(r.Legs); ok {
		return stopped(KindAutomatic, "%s is on the track ahead.", name)
	}
	if name, ok := w.occupied(r.To.Block); ok {
		return stopped(KindAutomatic, "%s is in %s.", name, w.blockName(r.To.Block))
	}
	for _, o := range w.routes {
		switch {
		case o.State == "" || o.ID == r.ID:
		case o.To.Block == r.To.Block:
			return stopped(KindAutomatic, "A route is set into %s.", w.blockName(r.To.Block))
		case o.From.Point == r.To.Point && o.To.Point == r.From.Point:
			return stopped(KindAutomatic, "A route is set the other way.")
		}
	}
	return look{view: Signal{Kind: KindAutomatic, Indication: Approach, Route: r.ID}, route: r}
}

// onLegs is a train really on track, whatever clearance the interlocking
// allows it, and its name.
func (w *world) onLegs(legs []trackplan.Leg) (string, bool) {
	for _, id := range slices.Sorted(maps.Keys(w.trains)) {
		t := w.trains[id]
		if !t.Stretch.Valid(w.net) {
			continue
		}
		for _, span := range t.Stretch {
			if span.To-span.From < 0.5 {
				continue
			}
			for _, l := range legs {
				if w.net.Fouls(span.Leg, l) {
					return t.Name, true
				}
			}
		}
	}
	return "", false
}

// occupied is what is in a block, by tracking or its detector. A detector
// that hasn't reported yet doesn't count: an outpost's track reads unknown
// until the first locomotive runs on it.
func (w *world) occupied(block string) (string, bool) {
	for _, id := range slices.Sorted(maps.Keys(w.trains)) {
		t := w.trains[id]
		if slices.Contains(t.Blocks, block) || (!t.Placed && t.Block == block) {
			return t.Name, true
		}
	}
	if b := w.blocks[block]; b.Occupancy != "" && w.entities[b.Occupancy].State == entity.StateOccupied {
		return "A train", true
	}
	return "", false
}

func (w *world) blockName(id string) string {
	if b, ok := w.blocks[id]; ok && b.Name != "" {
		return b.Name
	}
	return id
}

// next is the signal a train comes to next, past the block a route leads
// into: the first in that block, along the way the turnouts lie, for
// trains running its way.
func (w *world) next(r interlocking.Route) (string, bool) {
	leg, err := w.net.Into(r.To, w.position)
	if err != nil {
		return "", false
	}
	_, legs, _ := w.net.BlockEnd(leg, w.position)
	for _, l := range legs {
		if l.Junction {
			continue
		}
		var first *trackplan.Signal
		for i := range w.plan.Signals {
			sg := &w.plan.Signals[i]
			if sg.Track != l.Piece || sg.Reverse != l.Reverse {
				continue
			}
			if first == nil || (sg.At < first.At) != l.Reverse {
				first = sg
			}
		}
		if first != nil {
			return first.ID, true
		}
	}
	return "", false
}

// aspects are what a signal's heads show. The upper head says how to
// proceed: stop at red, approach at yellow, clear at green. The lower
// head, which a home signal has, says which way: red at stop, green on
// the straight way and yellow on a diverging one.
func aspects(v Signal, sg trackplan.Signal) (upper, lower string) {
	switch v.Indication {
	case Clear:
		upper = entity.AspectGreen
	case Approach:
		upper = entity.AspectYellow
	default:
		upper = entity.AspectRed
	}
	switch {
	case v.Kind != KindHome && sg.Lower == "":
	case v.Indication == Stop:
		lower = entity.AspectRed
	case v.Diverging:
		lower = entity.AspectYellow
	default:
		lower = entity.AspectGreen
	}
	return upper, lower
}

// restrictive lists, for each aspect, those a head without its lamp shows
// instead, best first.
var restrictive = map[string][]string{
	entity.AspectGreen:  {entity.AspectGreen, entity.AspectYellow, entity.AspectRed},
	entity.AspectYellow: {entity.AspectYellow, entity.AspectRed},
	entity.AspectRed:    {entity.AspectRed},
}

// shownBy is the aspect a head shows for the one wanted: that one, or
// the next most restrictive it has a lamp for; "" if it has none of them.
func shownBy(head entity.Entity, aspect string) string {
	for _, a := range restrictive[aspect] {
		if slices.Contains(head.Commands, a) {
			return a
		}
	}
	return ""
}
