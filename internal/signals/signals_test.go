package signals

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// lowerDeck is the shape of the Express & Local lower deck, drawn straight:
// two terminals with two tracks each, a double crossover and a split to
// four tracks at each end, and a gap between the corner blocks and the
// local stop's. Trains keep right, so the wall-side tracks (y 0 and 33 in
// the middle, 33 at the ends) carry them toward the left terminal.
func lowerDeck() trackplan.Plan {
	p := trackplan.Plan{Grid: trackplan.DefaultGrid}
	points := map[[2]float64]string{}
	pt := func(x, y float64) string {
		key := [2]float64{x, y}
		id, ok := points[key]
		if !ok {
			id = fmt.Sprintf("p%d", len(points)+1)
			points[key] = id
			p.Points = append(p.Points, trackplan.Point{ID: id, X: x, Y: y})
		}
		return id
	}
	track := func(id, block string, ax, ay, bx, by float64) {
		p.Tracks = append(p.Tracks, trackplan.Track{ID: id, Block: block, A: pt(ax, ay), B: pt(bx, by)})
	}
	block := func(id, name string) { p.Blocks = append(p.Blocks, trackplan.Block{ID: id, Name: name}) }
	turnout := func(id string, x, y, cx, ty float64) {
		p.Junctions = append(p.Junctions, trackplan.Junction{
			ID: id, Kind: trackplan.KindTurnout, Switch: "turnout." + id,
			Ends: []string{pt(x, y), pt(cx, y), pt(cx, ty)}, At: &trackplan.Vec{X: (x + cx) / 2, Y: y},
		})
	}
	crossover := func(id string, x1, x2 float64) {
		p.Junctions = append(p.Junctions, trackplan.Junction{
			ID: id, Kind: trackplan.KindDoubleCrossover, Switch: "turnout." + id,
			Ends: []string{pt(x1, 33), pt(x2, 33), pt(x1, 66), pt(x2, 66)},
		})
	}
	for _, b := range []string{"lt-wall", "lt-aisle", "rt-wall", "rt-aisle"} {
		block(b, b)
	}
	for _, b := range []string{"wall-local", "wall-express", "aisle-express", "aisle-local"} {
		block("corner-"+b, "corner "+b)
		block("stop-"+b, "stop "+b)
	}
	// The terminals, bumpers at their far ends.
	track("lt-wall", "lt-wall", 0, 33, 500, 33)
	track("lt-aisle", "lt-aisle", 0, 66, 500, 66)
	track("rt-wall", "rt-wall", 3300, 33, 3800, 33)
	track("rt-aisle", "rt-aisle", 3300, 66, 3800, 66)
	crossover("left-x", 500, 770)
	crossover("right-x", 3030, 3300)
	// The splits: the express track straight on, the local on the branch.
	turnout("left-wall", 770, 33, 900, 15)
	turnout("left-aisle", 770, 66, 900, 84)
	turnout("right-wall", 3030, 33, 2900, 15)
	turnout("right-aisle", 3030, 66, 2900, 84)
	track("lead-lw", "", 900, 15, 1050, 0)
	track("lead-le", "", 900, 33, 1050, 33)
	track("lead-la", "", 900, 66, 1050, 66)
	track("lead-ll", "", 900, 84, 1050, 99)
	track("lead-rw", "", 2780, 0, 2900, 15)
	track("lead-re", "", 2780, 33, 2900, 33)
	track("lead-ra", "", 2780, 66, 2900, 66)
	track("lead-rl", "", 2780, 99, 2900, 84)
	for i, name := range []string{"wall-local", "wall-express", "aisle-express", "aisle-local"} {
		y := float64(i) * 33
		track("corner-"+name, "corner-"+name, 1050, y, 2000, y)
		track("stop-"+name, "stop-"+name, 2000, y, 2780, y)
	}
	for _, id := range []string{"lt-wall", "lt-aisle"} {
		for i := range p.Tracks {
			if p.Tracks[i].ID == id {
				bumper(&p, p.Tracks[i].A)
			}
		}
	}
	for _, id := range []string{"rt-wall", "rt-aisle"} {
		for i := range p.Tracks {
			if p.Tracks[i].ID == id {
				bumper(&p, p.Tracks[i].B)
			}
		}
	}
	signal := func(id, tr string, at float64, reverse, lower bool) {
		sg := trackplan.Signal{ID: id, Track: tr, At: at, Reverse: reverse, Upper: "head." + id + ".upper"}
		if lower {
			sg.Lower = "head." + id + ".lower"
		}
		p.Signals = append(p.Signals, sg)
	}
	signal("gap1-wall", "lt-wall", 490, false, true)
	signal("gap1-aisle", "lt-aisle", 490, false, true)
	signal("gap2-local", "corner-wall-local", 10, true, true)
	signal("gap2-express", "corner-wall-express", 10, true, true)
	signal("gap3-east-express", "corner-aisle-express", 940, false, false)
	signal("gap3-east-local", "corner-aisle-local", 940, false, false)
	signal("gap3-west-express", "stop-wall-express", 10, true, false)
	signal("gap3-west-local", "stop-wall-local", 10, true, false)
	signal("gap4-express", "stop-aisle-express", 770, false, true)
	signal("gap4-local", "stop-aisle-local", 770, false, true)
	signal("gap5-wall", "rt-wall", 10, true, true)
	signal("gap5-aisle", "rt-aisle", 10, true, true)
	return p
}

func bumper(p *trackplan.Plan, point string) {
	for i := range p.Points {
		if p.Points[i].ID == point {
			p.Points[i].Bumper = true
		}
	}
}

type fakePlan struct{ st trackplan.State }

func (p fakePlan) Get() trackplan.State { return p.st }

type fakeTracking struct{}

func (fakeTracking) List() []tracking.Train { return nil }

// fakeEntities are turnouts that move as soon as they are commanded,
// unless stuck, and heads with red, yellow and green lamps.
type fakeEntities struct {
	mu    sync.Mutex
	ents  map[string]entity.Entity
	stuck map[string]bool
	sent  []string
	subs  []func(entity.Entity)
}

func (f *fakeEntities) List() []entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Collect(maps.Values(f.ents))
}

func (f *fakeEntities) get(id string) entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ents[id]
}

func (f *fakeEntities) stick(id string, stuck bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stuck[id] = stuck
}

func (f *fakeEntities) put(e entity.Entity) {
	f.mu.Lock()
	f.ents[e.ID] = e
	subs := slices.Clone(f.subs)
	f.mu.Unlock()
	for _, sub := range subs {
		sub(e)
	}
}

func (f *fakeEntities) Command(_ context.Context, id, command string) (entity.Entity, error) {
	f.mu.Lock()
	e, ok := f.ents[id]
	if !ok || f.stuck[id] {
		f.mu.Unlock()
		return e, nil
	}
	f.sent = append(f.sent, id+"="+command)
	f.mu.Unlock()
	if e.Kind == entity.KindTurnout {
		e.State = map[string]string{"throw": entity.StateThrown, "close": entity.StateClosed}[command]
	} else {
		if !slices.Contains(e.Commands, command) {
			return e, entity.ErrUnknownCommand
		}
		e.State = command
	}
	f.put(e)
	return e, nil
}

type rig struct {
	t      *testing.T
	plan   trackplan.Plan
	net    *trackplan.Network
	ents   *fakeEntities
	ix     *interlocking.Interlocking
	engine *Engine
}

func newRig(t *testing.T, plan trackplan.Plan) *rig {
	t.Helper()
	plan = plan.Normalize()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	st := trackplan.State{Plan: plan, Revision: "r1", Topology: plan.Topology()}
	ents := &fakeEntities{ents: map[string]entity.Entity{}, stuck: map[string]bool{}}
	for _, j := range plan.Junctions {
		ents.ents[j.Switch] = entity.Entity{ID: j.Switch, Name: j.ID, Kind: entity.KindTurnout, Available: true, State: entity.StateClosed}
	}
	for _, sg := range plan.Signals {
		for _, head := range []string{sg.Upper, sg.Lower} {
			if head != "" {
				ents.ents[head] = entity.Entity{
					ID: head, Name: head, Kind: entity.KindSignalHead, Available: true, State: entity.StateUnknown,
					Commands: []string{entity.AspectDark, entity.AspectRed, entity.AspectYellow, entity.AspectGreen},
				}
			}
		}
	}
	log := slog.New(slog.DiscardHandler)
	ix := interlocking.New(interlocking.Options{Entities: ents, Plan: fakePlan{st}, Tracking: fakeTracking{}, Logger: log})
	engine := New(Options{Entities: ents, Plan: fakePlan{st}, Tracking: fakeTracking{}, Interlocking: ix, Logger: log})
	ents.subs = append(ents.subs, ix.HandleEntity, engine.HandleEntity)
	ix.Subscribe(engine.HandleRoutes)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = ix.Run(ctx) })
	wg.Go(func() { _ = engine.Run(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	r := &rig{t: t, plan: plan, net: plan.Network(), ents: ents, ix: ix, engine: engine}
	eventually(t, "the routes", func() bool { return len(ix.List()) > 0 })
	engine.HandleRoutes(interlocking.Update{})
	eventually(t, "the signals", func() bool { return r.signal("gap1-wall").Kind == KindHome })
	return r
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *rig) signal(id string) Signal {
	for _, v := range r.engine.List() {
		if v.ID == id {
			return v
		}
	}
	return Signal{}
}

// shows waits for a signal to show an upper and lower aspect, as drawn and
// on its heads.
func (r *rig) shows(id, upper, lower string) {
	r.t.Helper()
	sg := r.plan.Signals[slices.IndexFunc(r.plan.Signals, func(s trackplan.Signal) bool { return s.ID == id })]
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := r.signal(id)
		heads := r.ents.get(sg.Upper).State == upper && (sg.Lower == "" || r.ents.get(sg.Lower).State == lower)
		if v.Upper == upper && v.Lower == lower && heads {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("%s shows %s over %s (%q), head %s; want %s over %s", id, v.Upper, v.Lower, v.Reason, r.ents.get(sg.Upper).State, upper, lower)
		}
		time.Sleep(time.Millisecond)
	}
}

// route finds the route between two blocks with the junction across or not.
func (r *rig) route(from, to string, across bool) string {
	r.t.Helper()
	for _, rt := range r.ix.List() {
		if rt.From.Block != from || rt.To.Block != to {
			continue
		}
		thrown := slices.ContainsFunc(rt.Turnouts, func(s trackplan.Setting) bool {
			return s.Position == trackplan.Thrown && (s.Junction == "left-x" || s.Junction == "right-x")
		})
		if thrown == across {
			return rt.ID
		}
	}
	r.t.Fatalf("no route from %s to %s", from, to)
	return ""
}

func (r *rig) set(id string) {
	r.t.Helper()
	if _, err := r.ix.Set(context.Background(), id); err != nil {
		r.t.Fatalf("setting %s: %v", id, err)
	}
}

// place puts a train on a track, covering from one distance along it to
// another.
func (r *rig) place(name, track string, from, to float64) tracking.Train {
	leg := trackplan.Leg{Piece: track}
	t := tracking.Train{ID: name, Name: name, Placed: true, Blocks: []string{r.net.Block(leg)}, Stretch: trackplan.Stretch{{Leg: leg, From: from, To: to}}}
	r.engine.HandleTrain(t)
	r.ix.HandleTrain(t)
	return t
}

func TestLowerDeckKinds(t *testing.T) {
	r := newRig(t, lowerDeck())
	for _, id := range []string{"gap1-wall", "gap1-aisle", "gap2-local", "gap2-express", "gap4-express", "gap4-local", "gap5-wall", "gap5-aisle"} {
		if got := r.signal(id).Kind; got != KindHome {
			t.Errorf("%s is %q, want home", id, got)
		}
	}
	for _, id := range []string{"gap3-east-express", "gap3-east-local", "gap3-west-express", "gap3-west-local"} {
		if got := r.signal(id).Kind; got != KindAutomatic {
			t.Errorf("%s is %q, want automatic", id, got)
		}
	}
	// With nothing set, every home signal is at stop and each automatic
	// signal warns of it.
	r.shows("gap1-wall", entity.AspectRed, entity.AspectRed)
	r.shows("gap2-local", entity.AspectRed, entity.AspectRed)
	r.shows("gap5-aisle", entity.AspectRed, entity.AspectRed)
	r.shows("gap3-east-express", entity.AspectYellow, "")
	r.shows("gap3-west-local", entity.AspectYellow, "")
	if got := r.signal("gap1-wall").Reason; got != "No route is set." {
		t.Errorf("reason = %q", got)
	}
}

func TestHomeSignalShowsTheRoute(t *testing.T) {
	r := newRig(t, lowerDeck())
	// Leaving the left terminal straight ahead: the next signal, at gap 3,
	// is clear.
	r.set(r.route("lt-aisle", "corner-aisle-express", false))
	r.shows("gap1-aisle", entity.AspectGreen, entity.AspectGreen)

	// Across the double crossover from the other track is diverging, which
	// the interlocking refuses until the first route is gone.
	id := r.route("lt-aisle", "corner-aisle-express", false)
	if _, err := r.ix.Cancel(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	r.shows("gap1-aisle", entity.AspectRed, entity.AspectRed)
	r.set(r.route("lt-wall", "corner-aisle-express", true))
	r.shows("gap1-wall", entity.AspectGreen, entity.AspectYellow)
}

func TestLocalJoiningIsNotDiverging(t *testing.T) {
	r := newRig(t, lowerDeck())
	// Into a terminal there is no next signal, so the upper head is yellow.
	// A local takes the turnout's branch, but that is where it joins: the
	// train has no choice there, and the double crossover is straight.
	r.set(r.route("corner-wall-local", "lt-wall", false))
	r.shows("gap2-local", entity.AspectYellow, entity.AspectGreen)
	if _, err := r.ix.Cancel(context.Background(), r.route("corner-wall-local", "lt-wall", false), false); err != nil {
		t.Fatal(err)
	}
	r.shows("gap2-local", entity.AspectRed, entity.AspectRed)
	r.set(r.route("corner-wall-local", "lt-aisle", true))
	r.shows("gap2-local", entity.AspectYellow, entity.AspectYellow)
}

func TestSignalsFollowATrain(t *testing.T) {
	r := newRig(t, lowerDeck())
	r.set(r.route("lt-aisle", "corner-aisle-express", false))
	r.shows("gap1-aisle", entity.AspectGreen, entity.AspectGreen)
	r.shows("gap3-east-express", entity.AspectYellow, "")

	// A train in the local stop: the automatic signal before it stops, and
	// the home signal before that warns.
	r.place("Local", "stop-aisle-express", 100, 600)
	r.shows("gap3-east-express", entity.AspectRed, "")
	r.shows("gap1-aisle", entity.AspectYellow, entity.AspectGreen)
	if got := r.signal("gap3-east-express").Reason; got != "Local is in stop aisle-express." {
		t.Errorf("reason = %q", got)
	}
	r.engine.HandleTrain(tracking.Train{ID: "Local", Removed: true})
	r.ix.HandleTrain(tracking.Train{ID: "Local", Removed: true})
	r.shows("gap1-aisle", entity.AspectGreen, entity.AspectGreen)

	// The route set for a train at the signal, its nose within the
	// clearance of the junction: the route counts it as on it, but it
	// hasn't gone.
	r.place("Express", "lt-aisle", 0, 490)
	eventually(t, "the route to count the train", func() bool {
		v, _ := r.ix.Get(r.route("lt-aisle", "corner-aisle-express", false))
		return v.State == interlocking.StateOccupied
	})
	r.shows("gap1-aisle", entity.AspectGreen, entity.AspectGreen)

	// Past the signal it goes back to stop behind the train.
	t2 := r.place("Express", "lt-aisle", 100, 500)
	t2.Stretch = append(t2.Stretch, trackplan.Span{Leg: trackplan.Leg{Piece: "left-x", Junction: true, Route: 1}, To: 40})
	r.engine.HandleTrain(t2)
	r.ix.HandleTrain(t2)
	r.shows("gap1-aisle", entity.AspectRed, entity.AspectRed)
	if got := r.signal("gap1-aisle").Reason; got != "Express is on the route." {
		t.Errorf("reason = %q", got)
	}
}

func TestHomeSignalWaitsForTheTurnouts(t *testing.T) {
	r := newRig(t, lowerDeck())
	r.ents.stick("turnout.left-x", true)
	r.set(r.route("lt-wall", "corner-aisle-express", true))
	eventually(t, "the route to be seen", func() bool { return r.signal("gap1-wall").Reason == "The route's turnouts aren't in place yet." })
	r.shows("gap1-wall", entity.AspectRed, entity.AspectRed)
	r.ents.stick("turnout.left-x", false)
	e := r.ents.get("turnout.left-x")
	e.State = entity.StateThrown
	r.ents.put(e)
	r.shows("gap1-wall", entity.AspectGreen, entity.AspectYellow)
}

func TestDetectorStopsASignal(t *testing.T) {
	plan := lowerDeck()
	for i := range plan.Blocks {
		if plan.Blocks[i].ID == "stop-aisle-local" {
			plan.Blocks[i].Occupancy = "block.stop"
		}
	}
	r := newRig(t, plan)
	// A detector that hasn't reported says nothing.
	r.ents.put(entity.Entity{ID: "block.stop", Kind: entity.KindBlock, Available: true, State: entity.StateUnknown})
	r.shows("gap3-east-local", entity.AspectYellow, "")
	r.ents.put(entity.Entity{ID: "block.stop", Kind: entity.KindBlock, Available: true, State: entity.StateOccupied})
	r.shows("gap3-east-local", entity.AspectRed, "")
}

func TestAutomaticStopsForAnOpposingRoute(t *testing.T) {
	r := newRig(t, lowerDeck())
	r.shows("gap3-east-express", entity.AspectYellow, "")
	r.set("stop-aisle-express~corner-aisle-express")
	r.shows("gap3-east-express", entity.AspectRed, "")
	if got := r.signal("gap3-east-express").Reason; got != "A route is set the other way." {
		t.Errorf("reason = %q", got)
	}
}

func TestHeads(t *testing.T) {
	r := newRig(t, lowerDeck())
	r.shows("gap1-aisle", entity.AspectRed, entity.AspectRed)

	t.Run("put back", func(*testing.T) {
		e := r.ents.get("head.gap1-aisle.upper")
		e.State = entity.AspectGreen
		r.ents.put(e)
		r.shows("gap1-aisle", entity.AspectRed, entity.AspectRed)
	})
	t.Run("set when it comes back", func(t *testing.T) {
		e := r.ents.get("head.gap1-aisle.upper")
		e.Available = false
		r.ents.put(e)
		r.set(r.route("lt-aisle", "corner-aisle-express", false))
		eventually(t, "the signal", func() bool { return r.signal("gap1-aisle").Upper == entity.AspectGreen })
		if got := r.ents.get(e.ID).State; got != entity.AspectRed {
			t.Errorf("offline head set to %s", got)
		}
		e = r.ents.get(e.ID)
		e.Available = true
		r.ents.put(e)
		r.shows("gap1-aisle", entity.AspectGreen, entity.AspectGreen)
	})
	t.Run("no lamp for the aspect", func(t *testing.T) {
		e := r.ents.get("head.gap1-aisle.upper")
		e.Commands = []string{entity.AspectDark, entity.AspectRed, entity.AspectYellow}
		r.ents.put(e)
		eventually(t, "the next most restrictive", func() bool { return r.ents.get(e.ID).State == entity.AspectYellow })
	})
}

func TestShownBy(t *testing.T) {
	head := func(lamps ...string) entity.Entity { return entity.Entity{Commands: lamps} }
	tests := []struct {
		name   string
		head   entity.Entity
		aspect string
		want   string
	}{
		{"has it", head("red", "yellow", "green"), "green", "green"},
		{"no green", head("red", "yellow"), "green", "yellow"},
		{"red and green only", head("red", "green"), "yellow", "red"},
		{"nothing to show", head("lunar"), "red", ""},
	}
	for _, tt := range tests {
		if got := shownBy(tt.head, tt.aspect); got != tt.want {
			t.Errorf("%s: shownBy = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSignalsWithNothingToGovern(t *testing.T) {
	plan := lowerDeck()
	plan.Signals = []trackplan.Signal{
		{ID: "gap1-wall", Track: "lt-wall", At: 490},
		{ID: "bumper", Track: "lt-wall", At: 100, Reverse: true},
		{ID: "no-block", Track: "lead-le", At: 10},
	}
	r := newRig(t, plan)
	for id, reason := range map[string]string{
		"bumper":   "The track ends ahead.",
		"no-block": "It stands on track in no block, so it has no block end to govern.",
	} {
		v := r.signal(id)
		if v.Kind != "" || v.Indication != Stop || v.Reason != reason || v.Upper != entity.AspectRed || v.Lower != "" {
			t.Errorf("%s = %+v", id, v)
		}
	}
	// A signal with no heads still shows on the plan, two lamps for a home
	// signal.
	if v := r.signal("gap1-wall"); v.Upper != entity.AspectRed || v.Lower != entity.AspectRed {
		t.Errorf("gap1-wall = %+v", v)
	}
}

func TestPublishes(t *testing.T) {
	r := newRig(t, lowerDeck())
	got := make(chan Update, 16)
	defer r.engine.Subscribe(func(u Update) { got <- u })()
	r.set(r.route("lt-aisle", "corner-aisle-express", false))
	select {
	case u := <-got:
		if u.Signal == nil || u.Signal.ID != "gap1-aisle" || u.Signal.Indication != Clear || u.Signal.Route == "" {
			t.Errorf("update = %+v", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no update")
	}
	r.engine.HandlePlan(trackplan.State{Plan: r.plan})
	eventually(t, "the plan's update", func() bool {
		select {
		case u := <-got:
			return len(u.Signals) == 12
		default:
			return false
		}
	})
}
