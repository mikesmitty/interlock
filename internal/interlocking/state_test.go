package interlocking

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/layoutsim"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// ladder is a line from block A through two turnouts to block B, with
// the turnouts' branches to blocks C and D:
//
//	A ===== j1 ---- j2 ===== B
//	          \       \
//	           C       D
func ladder() trackplan.Plan {
	return trackplan.Plan{
		Grid: trackplan.DefaultGrid,
		Points: []trackplan.Point{
			{ID: "p0", X: 0, Y: 0, Bumper: true}, {ID: "p1", X: 300, Y: 0}, {ID: "p2", X: 426, Y: 0}, {ID: "p3", X: 424.49, Y: 16.39},
			{ID: "p4", X: 626, Y: 0}, {ID: "p5", X: 752, Y: 0}, {ID: "p6", X: 750.49, Y: 16.39}, {ID: "p7", X: 1052, Y: 0, Bumper: true},
			{ID: "p8", X: 624, Y: 50, Bumper: true}, {ID: "p9", X: 950, Y: 50, Bumper: true},
		},
		Tracks: []trackplan.Track{
			{ID: "a1", A: "p0", B: "p1", Block: "a"},
			{ID: "t2", A: "p2", B: "p4"},
			{ID: "b1", A: "p5", B: "p7", Block: "b"},
			{ID: "c1", A: "p3", B: "p8", Block: "c"},
			{ID: "d1", A: "p6", B: "p9", Block: "d"},
		},
		Junctions: []trackplan.Junction{
			{ID: "j1", Kind: trackplan.KindTurnout, Ends: []string{"p1", "p2", "p3"}, At: &trackplan.Vec{X: 363.32}, Switch: "turnout.j1"},
			{ID: "j2", Kind: trackplan.KindTurnout, Ends: []string{"p4", "p5", "p6"}, At: &trackplan.Vec{X: 689.32}, Switch: "turnout.j2"},
		},
		Blocks: []trackplan.Block{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"}, {ID: "d", Name: "D"}},
	}
}

type harness struct {
	t *testing.T
	s *state
}

func newHarness(t *testing.T, plan trackplan.Plan) *harness {
	t.Helper()
	h := &harness{t: t, s: newState()}
	h.s.setPlan(trackplan.State{Plan: plan, Revision: "r1", Topology: plan.Topology()})
	for _, j := range plan.Junctions {
		if j.Switch != "" {
			h.turnout(j.Switch, entity.StateClosed)
		}
	}
	return h
}

func (h *harness) turnout(id, state string) {
	h.s.entity(entity.Entity{ID: id, Kind: entity.KindTurnout, Name: strings.TrimPrefix(id, "turnout."), Available: true, State: state})
}

func (h *harness) route(id string) *route {
	h.t.Helper()
	r, ok := h.s.byID[id]
	if !ok {
		h.t.Fatalf("no route %s", id)
	}
	return r
}

// set sets a route and moves its turnouts as it commands them.
func (h *harness) set(id string) (Route, error) {
	v, cmds, err := h.s.set(id)
	for _, c := range cmds {
		h.turnout(c.entity, map[string]string{"throw": entity.StateThrown, "close": entity.StateClosed}[c.command])
	}
	return v, err
}

// place puts a train on a route's way from rear to front, in
// millimeters from where the route leaves its first block: back into
// that block below 0, into the next beyond its length.
func (h *harness) place(id, name, via string, rear, front float64) {
	h.t.Helper()
	n, r := h.s.net, h.route(via)
	p := trackplan.Stretch{{Leg: r.Legs[0]}}
	if rear < 0 {
		back, _, _ := p.Reversed(n).Grow(n, -rear, h.s.position)
		p = back.Reversed(n)
	}
	p, _, _ = p.Grow(n, front, h.s.position)
	if rear > 0 {
		p = p.Trim(rear)
	}
	h.s.train(tracking.Train{ID: id, Name: name, Placed: true, Stretch: p, Blocks: p.Blocks(n)})
}

func (h *harness) refused(id, want string) {
	h.t.Helper()
	_, err := h.set(id)
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, want) {
		h.t.Errorf("setting %s: %v; want refused with %q", id, err, want)
	}
}

func (h *harness) held(id string) string { return h.s.locks[id].state }

func TestARouteLocksItsTurnouts(t *testing.T) {
	h := newHarness(t, layoutsim.TestLayout())
	east := "turnout.0a0000000110.servo_1"
	h.turnout(east, entity.StateClosed)

	v, cmds, err := h.s.set("east-outer~main-siding")
	if err != nil || v.State != StateSetting || !slices.Equal(v.Waiting, []string{"main-east"}) {
		t.Fatalf("set = %+v, %v", v, err)
	}
	if len(cmds) != 1 || cmds[0] != (command{east, "throw"}) {
		t.Errorf("commands = %+v", cmds)
	}
	if h.held(east) != entity.StateThrown {
		t.Errorf("held %q", h.held(east))
	}
	h.turnout(east, entity.StateMoving)
	h.turnout(east, entity.StateThrown)
	if r := h.route("east-outer~main-siding"); r.State != StateSet || len(r.Waiting) != 0 {
		t.Errorf("once thrown: %s waiting %v", r.State, r.Waiting)
	}
	// Setting it again sends nothing; the turnout is in place.
	if _, cmds, err := h.s.set("east-outer~main-siding"); err != nil || len(cmds) != 0 {
		t.Errorf("set again = %v, %v", cmds, err)
	}

	// Routes over the same turnout, or into the same block, can't be set.
	h.refused("east-outer~main-outer", "is held thrown by the route from East outer to Main St siding")
	h.refused("main-outer~east-outer", "is held thrown")
	h.refused("west-outer~main-siding", "The route from East outer to Main St siding is set into Main St siding already")
	// Others can.
	if _, err := h.set("park-outer~east-inner"); err != nil {
		t.Errorf("an unrelated route: %v", err)
	}

	// The turnout moved away by something else: the route waits for it,
	// and setting it again moves it back.
	h.turnout(east, entity.StateClosed)
	if r := h.route("east-outer~main-siding"); r.State != StateSetting || !slices.Equal(r.Waiting, []string{"main-east"}) {
		t.Errorf("moved away: %s waiting %v", r.State, r.Waiting)
	}
	if _, cmds, _ := h.s.set("east-outer~main-siding"); len(cmds) != 1 || cmds[0].command != "throw" {
		t.Errorf("set again sends %+v", cmds)
	}

	if _, err := h.s.cancel("east-outer~main-siding", false); err != nil {
		t.Fatal(err)
	}
	if r := h.route("east-outer~main-siding"); r.active || r.State != "" || h.held(east) != "" {
		t.Errorf("canceled: active %v, state %q, held %q", r.active, r.State, h.held(east))
	}
	if _, err := h.set("east-outer~main-outer"); err != nil {
		t.Errorf("once canceled: %v", err)
	}
	if _, _, err := h.s.set("nowhere"); !errors.Is(err, ErrNoRoute) {
		t.Errorf("no such route: %v", err)
	}
}

func TestARouteIsReleasedBehindTheTrain(t *testing.T) {
	h := newHarness(t, ladder())
	// The way covers j1, the track between and j2, 126 + 200 + 126 mm.
	r := h.route("a~b")
	if len(r.Legs) != 3 || r.Length != 452 {
		t.Fatalf("a~b covers %+v, %v mm", r.Legs, r.Length)
	}
	if _, err := h.set("a~b"); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		name        string
		rear, front float64
		state       string
		released    int
		j1, j2      string
	}{
		{"waiting in A", -250, -50, StateSet, 0, "closed", "closed"},
		{"its front within the clearance", -220, -20, StateOccupied, 0, "closed", "closed"},
		{"over j1", -100, 100, StateOccupied, 0, "closed", "closed"},
		{"its rear just off j1", 130, 330, StateOccupied, 0, "closed", "closed"},
		{"its rear clear of j1", 160, 360, StateOccupied, 1, "", "closed"},
		{"its rear on j2, the track before it in the clearance", 340, 540, StateOccupied, 1, "", "closed"},
		{"its rear on j2", 360, 560, StateOccupied, 2, "", "closed"},
		{"its rear just off j2", 460, 660, StateOccupied, 2, "", "closed"},
		{"in B", 490, 690, "", 0, "", ""},
	}
	for _, st := range steps {
		h.place("t1", "Local", "a~b", st.rear, st.front)
		if r.State != st.state || r.Released != st.released || h.held("turnout.j1") != st.j1 || h.held("turnout.j2") != st.j2 {
			t.Errorf("%s: %q, released %d, j1 held %q, j2 %q; want %q, %d, %q, %q", st.name, r.State, r.Released, h.held("turnout.j1"), h.held("turnout.j2"), st.state, st.released, st.j1, st.j2)
		}
		if st.state == StateOccupied && r.Train != "t1" {
			t.Errorf("%s: train %q", st.name, r.Train)
		}
	}
}

func TestATrainBackingOutKeepsTheRoute(t *testing.T) {
	h := newHarness(t, ladder())
	if _, err := h.set("a~b"); err != nil {
		t.Fatal(err)
	}
	h.place("t1", "Local", "a~b", -100, 200)
	h.place("t1", "Local", "a~b", -300, -40)
	r := h.route("a~b")
	if !r.active || r.Released != 0 || r.State != StateSet || h.held("turnout.j1") != "closed" {
		t.Errorf("backed out: active %v, released %d, %s", r.active, r.Released, r.State)
	}
}

func TestATrainFirstPlacedInTheNextBlockReleasesTheRoute(t *testing.T) {
	h := newHarness(t, ladder())
	if _, err := h.set("a~b"); err != nil {
		t.Fatal(err)
	}
	// Tracking placed it only once it was in B, past the route.
	h.place("t1", "Local", "a~b", 500, 700)
	if r := h.route("a~b"); r.active || len(h.s.locks) != 0 {
		t.Errorf("active %v, locks %v", r.active, h.s.locks)
	}
}

func TestWhatStandsInTheWay(t *testing.T) {
	h := newHarness(t, ladder())
	// A train in the block the route leads into.
	h.place("t1", "Local", "a~c", 200, 300)
	h.refused("a~c", "Local is in C")
	h.s.train(tracking.Train{ID: "t1", Removed: true})
	// One known only by its block.
	h.s.train(tracking.Train{ID: "t2", Name: "Express", Block: "d", Blocks: []string{"d"}})
	h.refused("a~d", "Express is in D")
	h.s.train(tracking.Train{ID: "t2", Removed: true})

	// A train standing over j1's closed route: j1 can't be thrown under
	// it, nor can a route be set over the track it is on.
	h.place("t3", "Freight", "a~b", 0, 120)
	h.refused("a~c", "Freight is on j1, which would have to move")
	// Setting the way it stands on is fine: it is then on the route.
	if v, err := h.set("a~b"); err != nil || v.State != StateOccupied || v.Train != "t3" {
		t.Errorf("the way it is on: %+v, %v", v, err)
	}
	// It can't be canceled while the train is on it, but can by force.
	_, err := h.s.cancel("a~b", false)
	var refused *RefusedError
	if !errors.As(err, &refused) || !refused.Occupied || !strings.Contains(refused.Reason, "Freight is on it") {
		t.Errorf("cancel with a train on it: %v", err)
	}
	if _, err := h.s.cancel("a~b", true); err != nil || h.route("a~b").active {
		t.Errorf("forced: %v", err)
	}

	// A turnout without a switch can't be thrown; one offline can't be
	// set.
	plan := ladder()
	plan.Junctions[0].Switch = ""
	h = newHarness(t, plan)
	h.refused("a~c", "needs the turnout j1 thrown, and it has no turnout linked")
	h.s.entity(entity.Entity{ID: "turnout.j2", Kind: entity.KindTurnout, Name: "J2", State: entity.StateClosed})
	h.refused("a~d", "J2 is offline")
}

func TestRoutesSetStayOverAPlanChangeAndARestart(t *testing.T) {
	h := newHarness(t, ladder())
	if _, err := h.set("a~b"); err != nil {
		t.Fatal(err)
	}
	h.place("t1", "Local", "a~b", -100, 100)
	h.place("t1", "Local", "a~b", 160, 360)
	if h.route("a~b").Released != 1 {
		t.Fatal("j1 not released")
	}
	saved := saved{}
	for _, r := range h.s.routes {
		if r.active {
			saved.Routes = append(saved.Routes, savedRoute{Key: r.key, Since: r.Since, Train: r.Train, Released: r.Released, Visited: r.visited})
		}
	}

	// A label added: the routes stay as they are.
	plan := ladder()
	plan.Labels = []trackplan.Label{{ID: "l1", Text: "Yard"}}
	h.s.setPlan(trackplan.State{Plan: plan, Revision: "r2", Topology: plan.Topology()})
	if r := h.route("a~b"); !r.active || r.Released != 1 || h.held("turnout.j2") != "closed" {
		t.Errorf("after a save: active %v, released %d", r.active, r.Released)
	}

	// Restarted.
	again := newHarness(t, ladder())
	again.s.restore(saved)
	if r := again.route("a~b"); !r.active || r.Released != 1 || r.Train != "t1" || again.held("turnout.j1") != "" || again.held("turnout.j2") != "closed" {
		t.Errorf("restored: active %v, released %d, train %q", r.active, r.Released, r.Train)
	}

	// The track between the turnouts taken out: the route goes.
	plan = ladder()
	plan.Tracks = slices.DeleteFunc(plan.Tracks, func(tr trackplan.Track) bool { return tr.ID == "t2" })
	h.s.setPlan(trackplan.State{Plan: plan, Revision: "r3", Topology: plan.Topology()})
	if _, ok := h.s.byID["a~b"]; ok || len(h.s.locks) != 0 {
		t.Errorf("a route over track that is gone: locks %v", h.s.locks)
	}
}

func TestACrossoversTracksAreSeparate(t *testing.T) {
	h := newHarness(t, layoutsim.TestLayout())
	// Both straight ways through the crossover at once.
	for _, id := range []string{"park-outer~east-outer", "east-inner~park-inner"} {
		if _, err := h.set(id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	// Across, not.
	h.refused("park-outer~east-inner", "is held closed")
}
