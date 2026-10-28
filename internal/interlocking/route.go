// Package interlocking sets routes for trains. A route is a way from one
// block into the next over the track between them: one of the plan's
// links, taken one way. Setting it throws or closes its turnouts and
// locks them, so that nothing in Interlock moves them while it is set,
// and keeps any other route that would share its track or enter the same
// block from being set. As the train runs through, train tracking says
// where its rear is, and the track behind it is released a piece at a
// time; once it is wholly in the block the route leads into, the route
// is released.
//
// One goroutine owns the routes (state.go); the Handle methods only
// queue what changed for it.
package interlocking

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/internal/trackplan"
)

// The states of a route that is set.
const (
	// StateSetting is a route whose turnouts aren't all where it needs
	// them yet.
	StateSetting = "setting"
	// StateSet is a route set for a train, none on it yet.
	StateSet = "set"
	// StateOccupied is a route with a train on it.
	StateOccupied = "occupied"
)

// Clearance is how near a train, by tracking's estimate, may come to
// track before it counts as on it, in millimeters: room for the
// estimate to be out.
const Clearance = 30.0

// Route is a way from one block into another over the track between
// them, and whether it is set.
type Route struct {
	// ID is the two blocks' IDs, from~to, with ~2, ~3 and so on after
	// it when more than one way joins them.
	ID string `json:"id"`
	// From is the end of the block it leaves by, To the end of the block
	// it leads into.
	From trackplan.Port `json:"from"`
	To   trackplan.Port `json:"to"`
	// Turnouts are the positions it needs.
	Turnouts []trackplan.Setting `json:"turnouts,omitempty"`
	// Length is the track between the two blocks, in millimeters.
	Length float64 `json:"length"`
	// Legs are that track, in order from From; none where the blocks
	// meet end to end.
	Legs []trackplan.Leg `json:"legs"`

	// State is how it is set: setting, set or occupied; empty for a
	// route not set.
	State string `json:"state,omitempty"`
	// Waiting are the junctions not yet where the route needs them.
	Waiting []string `json:"waiting,omitempty"`
	// Train is the tracked train on it, or last on it, by ID.
	Train string `json:"train,omitempty"`
	// Released is how many of its legs, from the first, have been
	// released behind the train.
	Released int       `json:"released,omitempty"`
	Since    time.Time `json:"since,omitzero"`
}

// Update is a change to the routes: one route set, changed or released,
// or when the plan has changed, all of them.
type Update struct {
	Route  *Route
	Routes []Route
}

// route is a route as the interlocking keeps it.
type route struct {
	Route
	// key identifies the way by its ends and the positions it needs, to
	// find it again in a changed plan.
	key string
	// switches[i] is the turnout entity that sets legs[i]'s junction and
	// need[i] the position the route needs it in; "" for legs with
	// nothing to set.
	switches []string
	need     []trackplan.Position
	// active is whether the route is set, and visited which of its legs
	// a train has been on since.
	active  bool
	visited []bool
}

func keyOf(from, to trackplan.Port, settings []trackplan.Setting) string {
	parts := make([]string, 0, len(settings))
	for _, s := range settings {
		parts = append(parts, s.Junction+"="+string(s.Position))
	}
	slices.Sort(parts)
	return fmt.Sprintf("%s/%s>%s/%s|%s", from.Block, from.Point, to.Block, to.Point, strings.Join(parts, ";"))
}

// routesOf is every link of the plan taken each way, ordered by the
// blocks they join.
func routesOf(top trackplan.Topology, net *trackplan.Network) []*route {
	var out []*route
	for _, l := range top.Links {
		back := slices.Clone(l.Turnouts)
		slices.Reverse(back)
		for _, way := range [][2]trackplan.Port{{l.From, l.To}, {l.To, l.From}} {
			from, to := way[0], way[1]
			settings := l.Turnouts
			if from != l.From {
				settings = back
			}
			legs, err := net.Way(from, to, settings)
			if err != nil {
				continue // the topology and the network disagree; leave it out
			}
			r := &route{
				Route:    Route{From: from, To: to, Turnouts: settings, Length: l.Length, Legs: legs},
				key:      keyOf(from, to, settings),
				switches: make([]string, len(legs)),
				need:     make([]trackplan.Position, len(legs)),
			}
			for i, leg := range legs {
				if !leg.Junction {
					continue
				}
				if j := slices.IndexFunc(settings, func(s trackplan.Setting) bool { return s.Junction == leg.Piece }); j >= 0 {
					r.switches[i], r.need[i] = settings[j].Switch, settings[j].Position
				}
			}
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b *route) int {
		return cmp.Or(
			strings.Compare(a.From.Block, b.From.Block), strings.Compare(a.To.Block, b.To.Block),
			strings.Compare(a.From.Point, b.From.Point), strings.Compare(a.To.Point, b.To.Point),
			strings.Compare(a.key, b.key),
		)
	})
	count := map[string]int{}
	for _, r := range out {
		base := r.From.Block + "~" + r.To.Block
		count[base]++
		r.ID = base
		if n := count[base]; n > 1 {
			r.ID = fmt.Sprintf("%s~%d", base, n)
		}
	}
	return out
}

// view is the route as published.
func (r *route) view() Route {
	v := r.Route
	v.Waiting = slices.Clone(r.Waiting)
	return v
}

// held are the legs the route still holds: those not yet released
// behind its train.
func (r *route) held() []int {
	if !r.active {
		return nil
	}
	out := make([]int, 0, len(r.Legs)-r.Released)
	for i := r.Released; i < len(r.Legs); i++ {
		out = append(out, i)
	}
	return out
}
