package interlocking

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// ErrNoRoute is a route the plan doesn't have.
var ErrNoRoute = errors.New("interlocking: no such route")

// RefusedError is a route that can't be set, or canceled, as things
// stand.
type RefusedError struct {
	Reason string
	// Occupied is set when canceling was refused for a train on the
	// route, which canceling with force overrides.
	Occupied bool
}

func (e *RefusedError) Error() string { return e.Reason }

func refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// command is a turnout command setting a route sends.
type command struct {
	entity, command string
}

// lock is a turnout held where a route needs it.
type lock struct {
	state string
	route *route
}

// state is what the interlocking knows. One goroutine owns it.
type state struct {
	net    *trackplan.Network
	routes []*route
	byID   map[string]*route
	byKey  map[string]*route
	blocks map[string]trackplan.Block
	// junctions are the plan's junctions by ID, for their switches.
	junctions map[string]trackplan.Junction
	trains    map[string]tracking.Train
	entities  map[string]entity.Entity
	// locks are the turnouts the routes set hold, by entity ID.
	locks map[string]lock
	now   time.Time
	// changed are the routes to publish, by ID, with what each was last
	// published as; all is set when the plan changed them all.
	changed   map[string]bool
	published map[string]string
	all       bool
}

func newState() *state {
	return &state{
		net:       trackplan.Plan{}.Network(),
		byID:      map[string]*route{},
		byKey:     map[string]*route{},
		blocks:    map[string]trackplan.Block{},
		junctions: map[string]trackplan.Junction{},
		trains:    map[string]tracking.Train{},
		entities:  map[string]entity.Entity{},
		locks:     map[string]lock{},
		changed:   map[string]bool{},
		published: map[string]string{},
	}
}

// setPlan takes a newly saved plan's routes. Routes set stay set if the
// plan still has them, and are canceled if not.
func (s *state) setPlan(st trackplan.State) {
	s.net = st.Plan.Network()
	s.blocks = map[string]trackplan.Block{}
	for _, b := range st.Plan.Blocks {
		s.blocks[b.ID] = b
	}
	s.junctions = map[string]trackplan.Junction{}
	for _, j := range st.Plan.Junctions {
		s.junctions[j.ID] = j
	}
	old := s.byKey
	s.routes = routesOf(st.Topology, s.net)
	s.byID = make(map[string]*route, len(s.routes))
	s.byKey = make(map[string]*route, len(s.routes))
	for _, r := range s.routes {
		s.byID[r.ID] = r
		s.byKey[r.key] = r
		was, ok := old[r.key]
		if !ok || !was.active {
			continue
		}
		r.active, r.Since, r.Train = true, was.Since, was.Train
		if slices.Equal(was.Legs, r.Legs) {
			r.Released, r.visited = was.Released, was.visited
		} else {
			r.visited = make([]bool, len(r.Legs))
		}
	}
	s.all = true
	s.update()
}

// train follows a tracked train.
func (s *state) train(t tracking.Train) {
	if t.Removed {
		delete(s.trains, t.ID)
	} else {
		s.trains[t.ID] = t
	}
	s.update()
}

// entity follows the turnouts and the blocks' occupancy.
func (s *state) entity(e entity.Entity) {
	if e.Kind != entity.KindTurnout && e.Kind != entity.KindBlock {
		return
	}
	if e.Removed {
		delete(s.entities, e.ID)
	} else {
		s.entities[e.ID] = e
	}
	s.update()
}

// stateOf is the turnout entity state a position is, and commandOf the
// command that sets it.
func stateOf(p trackplan.Position) string {
	if p == trackplan.Thrown {
		return entity.StateThrown
	}
	return entity.StateClosed
}

func commandOf(p trackplan.Position) string {
	if p == trackplan.Thrown {
		return "throw"
	}
	return "close"
}

// position is how a junction is set, as its turnout says: "" while it
// moves or is unknown. A junction without a turnout stays closed.
func (s *state) position(junction string) trackplan.Position {
	j, ok := s.junctions[junction]
	if !ok || j.Switch == "" {
		return trackplan.Closed
	}
	switch s.entities[j.Switch].State {
	case entity.StateClosed:
		return trackplan.Closed
	case entity.StateThrown:
		return trackplan.Thrown
	}
	return ""
}

// covering is the track a train covers with the clearance added at both
// ends; nil while it isn't placed.
func (s *state) covering(t tracking.Train) trackplan.Stretch {
	if !t.Stretch.Valid(s.net) {
		return nil
	}
	grown, _, _ := t.Stretch.Grow(s.net, Clearance, s.position)
	both, _, _ := grown.Reversed(s.net).Grow(s.net, Clearance, s.position)
	return both
}

// on is the train on a leg, if any, and whether it is on the leg itself
// rather than on track that crosses it or shares a junction with it.
func (s *state) on(leg trackplan.Leg) (tracking.Train, bool, bool) {
	var found tracking.Train
	hit, exact := false, false
	for _, id := range slices.Sorted(maps.Keys(s.trains)) {
		t := s.trains[id]
		for _, span := range s.covering(t) {
			if span.To-span.From < 0.5 || !s.net.Fouls(span.Leg, leg) {
				continue
			}
			same := span.Leg.Piece == leg.Piece && span.Leg.Junction == leg.Junction && span.Leg.Route == leg.Route
			if !hit || (same && !exact) {
				found, hit, exact = t, true, same
			}
		}
	}
	return found, hit, exact
}

// inBlock is a train in a block, by tracking's estimate or, for one not
// placed, where a detector last saw it.
func (s *state) inBlock(block string, placed bool) (tracking.Train, bool) {
	for _, id := range slices.Sorted(maps.Keys(s.trains)) {
		t := s.trains[id]
		if placed && !t.Placed {
			continue
		}
		if slices.Contains(t.Blocks, block) || (!t.Placed && t.Block == block) {
			return t, true
		}
	}
	return tracking.Train{}, false
}

// set sets a route: it checks that nothing stands in the way, locks its
// turnouts and returns the commands that move those not yet in place. A
// route set already sends again those that have moved away since.
func (s *state) set(id string) (Route, []command, error) {
	r, ok := s.byID[id]
	if !ok {
		return Route{}, nil, ErrNoRoute
	}
	if !r.active {
		if err := s.check(r); err != nil {
			return r.view(), nil, err
		}
		r.active, r.Since, r.Train, r.Released = true, s.now, "", 0
		r.visited = make([]bool, len(r.Legs))
		s.changed[r.ID] = true
		s.update()
	}
	var cmds []command
	sent := map[string]bool{}
	for _, i := range r.held() {
		id := r.switches[i]
		if id == "" || sent[id] || s.entities[id].State == stateOf(r.need[i]) {
			continue
		}
		sent[id] = true
		cmds = append(cmds, command{id, commandOf(r.need[i])})
	}
	return r.view(), cmds, nil
}

// check says why a route can't be set, if it can't.
func (s *state) check(r *route) error {
	for _, set := range r.Turnouts {
		if set.Switch == "" {
			if set.Position != trackplan.Closed {
				return refuse("The way needs the %s thrown, and it has no turnout linked to throw it.", s.junctionName(set.Junction))
			}
			continue
		}
		e, ok := s.entities[set.Switch]
		switch {
		case !ok:
			return refuse("The turnout %s linked to the %s isn't known.", set.Switch, s.junctionName(set.Junction))
		case !e.Available:
			return refuse("%s is offline.", e.Name)
		}
	}
	for i, id := range r.switches {
		if l, ok := s.locks[id]; ok && id != "" && l.state != stateOf(r.need[i]) {
			return refuse("%s is held %s by the route %s.", s.entityName(id), l.state, s.describe(l.route))
		}
	}
	for _, o := range s.routes {
		if !o.active || o == r {
			continue
		}
		if o.To.Block == r.To.Block {
			return refuse("The route %s is set into %s already.", s.describe(o), s.blockName(r.To.Block))
		}
		for _, leg := range r.Legs {
			for _, k := range o.held() {
				if s.net.Fouls(leg, o.Legs[k]) {
					return refuse("It shares track with the route %s, which is set.", s.describe(o))
				}
			}
		}
	}
	if t, ok := s.inBlock(r.To.Block, false); ok {
		return refuse("%s is in %s.", t.Name, s.blockName(r.To.Block))
	}
	if b := s.blocks[r.To.Block]; b.Occupancy != "" && s.entities[b.Occupancy].State == entity.StateOccupied {
		return refuse("%s is occupied.", s.blockName(r.To.Block))
	}
	for i, leg := range r.Legs {
		t, ok, exact := s.on(leg)
		if !ok {
			continue
		}
		if id := r.switches[i]; id != "" && s.entities[id].State != stateOf(r.need[i]) {
			return refuse("%s is on %s, which would have to move.", t.Name, s.entityName(id))
		}
		if !exact {
			return refuse("%s is in the way.", t.Name)
		}
	}
	return nil
}

// cancel releases a route. A train on it keeps it set unless force is
// given.
func (s *state) cancel(id string, force bool) (Route, error) {
	r, ok := s.byID[id]
	if !ok {
		return Route{}, ErrNoRoute
	}
	if !r.active {
		return r.view(), nil
	}
	if !force {
		for _, i := range r.held() {
			if t, ok, _ := s.on(r.Legs[i]); ok {
				return r.view(), &RefusedError{Reason: fmt.Sprintf("%s is on it. The route is released behind the train as it goes.", t.Name), Occupied: true}
			}
		}
	}
	s.release(r)
	s.update()
	return r.view(), nil
}

func (s *state) release(r *route) {
	r.active, r.visited = false, nil
	r.State, r.Waiting, r.Train, r.Released, r.Since = "", nil, "", 0, time.Time{}
	s.changed[r.ID] = true
}

// update follows the trains through the routes set: a route a train is
// on is occupied, the legs behind the train are released as it goes,
// and once it is wholly in the block the route leads into, the route
// is.
func (s *state) update() {
	for _, r := range s.routes {
		if !r.active {
			continue
		}
		first := -1
		for _, i := range r.held() {
			t, ok, _ := s.on(r.Legs[i])
			if !ok {
				continue
			}
			r.visited[i] = true
			if first < 0 {
				first = i
				r.Train = t.ID
			}
		}
		switch {
		case first >= 0:
			// Behind the train, the legs it has been on and left.
			for r.Released < first && r.visited[r.Released] {
				r.Released++
			}
		default:
			if _, in := s.inBlock(r.To.Block, true); in {
				s.release(r)
				continue
			}
		}
		r.Waiting = nil
		for _, i := range r.held() {
			id := r.switches[i]
			if id != "" && s.entities[id].State != stateOf(r.need[i]) && !slices.Contains(r.Waiting, r.Legs[i].Piece) {
				r.Waiting = append(r.Waiting, r.Legs[i].Piece)
			}
		}
		switch {
		case first >= 0:
			r.State = StateOccupied
		case len(r.Waiting) > 0:
			r.State = StateSetting
		default:
			r.State = StateSet
		}
	}
	s.locks = map[string]lock{}
	for _, r := range s.routes {
		for _, i := range r.held() {
			if id := r.switches[i]; id != "" {
				if _, ok := s.locks[id]; !ok {
					s.locks[id] = lock{state: stateOf(r.need[i]), route: r}
				}
			}
		}
	}
	for _, r := range s.routes {
		sig := fmt.Sprintf("%s|%v|%s|%d|%v", r.State, r.Waiting, r.Train, r.Released, r.Since)
		if s.published[r.ID] != sig {
			s.published[r.ID] = sig
			s.changed[r.ID] = true
		}
	}
}

// The names things go by in what the interlocking says.

func (s *state) blockName(id string) string {
	if b, ok := s.blocks[id]; ok && b.Name != "" {
		return b.Name
	}
	return id
}

func (s *state) entityName(id string) string {
	if e, ok := s.entities[id]; ok && e.Name != "" {
		return e.Name
	}
	return id
}

func (s *state) junctionName(id string) string {
	if j, ok := s.junctions[id]; ok {
		return fmt.Sprintf("%s %s", kindName(j.Kind), id)
	}
	return "junction " + id
}

func kindName(kind string) string {
	if kind == trackplan.KindDoubleCrossover {
		return "double crossover"
	}
	return kind
}

func (s *state) describe(r *route) string {
	return fmt.Sprintf("from %s to %s", s.blockName(r.From.Block), s.blockName(r.To.Block))
}
