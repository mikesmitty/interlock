package driving

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/layoutsim"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// harness drives the simulated test layout's trains with the real
// interlocking setting their routes. Time is simulated: each look the
// drive takes at its train moves the layout on by step, so a minute's
// drive takes a moment. Tracking is perfect: what the drive sees is
// where the train really is, with the speeds its decoder really gives.
type harness struct {
	t    *testing.T
	plan trackplan.Plan
	net  *trackplan.Network
	sim  *layoutsim.Sim
	ix   *interlocking.Interlocking
	d    *Driver
	ents *fakeEntities

	mu       sync.Mutex
	specs    []layoutsim.TrainSpec
	decoders map[uint16]*sim.Decoder
	now      time.Time
	// queue are speed commands on their way to the decoders.
	queue []queued
	// speeds are what tracking would measure: a run's speed at each step
	// held from one entry to the next.
	speeds  map[uint16][]tracking.SpeedPoint
	entered map[uint16]time.Time
	steady  map[uint16]float64
	since   map[uint16]time.Time
	// set are the speeds set, as the train manager would publish them.
	set []string
}

type queued struct {
	at      time.Time
	address uint16
	speed   traction.Speed
}

// step is how far the layout moves on at each look, and latency how
// long a speed command takes to reach the decoder.
const (
	step    = 50 * time.Millisecond
	latency = 60 * time.Millisecond
)

func newHarness(t *testing.T) *harness {
	plan := layoutsim.TestLayout().Normalize()
	h := &harness{
		t: t, plan: plan, net: plan.Network(), specs: layoutsim.TestTrains(),
		decoders: map[uint16]*sim.Decoder{}, now: time.Unix(1000, 0),
		speeds: map[uint16][]tracking.SpeedPoint{}, entered: map[uint16]time.Time{},
		steady: map[uint16]float64{}, since: map[uint16]time.Time{},
	}
	for i, spec := range h.specs {
		h.specs[i] = spec.Normalize()
		h.decoders[spec.Address] = sim.NewDecoder(spec.Address, false)
	}
	h.ents = &fakeEntities{ents: map[string]entity.Entity{}, stuck: map[string]bool{}}
	for _, j := range plan.Junctions {
		h.ents.ents[j.Switch] = entity.Entity{ID: j.Switch, Name: j.ID, Kind: entity.KindTurnout, Available: true, State: entity.StateClosed}
	}
	h.sim = layoutsim.New(h, h.specs, slog.New(slog.DiscardHandler))
	h.sim.SetPlan(plan, "r1")
	st := trackplan.State{Plan: plan, Revision: "r1", Topology: plan.Topology()}
	h.ix = interlocking.New(interlocking.Options{Entities: h.ents, Plan: fakePlan{st}, Tracking: fakeTracking{}, Logger: slog.New(slog.DiscardHandler)})
	h.ents.ix = h.ix
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = h.ix.Run(ctx); close(done) }()
	h.d = New(Options{
		Tracking: h, Interlocking: h.ix, Trains: h, Plan: fakePlan{st}, Entities: h.ents,
		Logger: slog.New(slog.DiscardHandler), Tick: time.Millisecond, Now: h.clock,
	})
	// The driver stops before the interlocking, as in Interlock.
	driverCtx, stopDriver := context.WithCancel(ctx)
	driving := make(chan struct{})
	go func() { _ = h.d.Run(driverCtx); close(driving) }()
	t.Cleanup(func() { stopDriver(); <-driving; cancel(); <-done })
	eventually(t, "the routes", func() bool { return len(h.ix.List()) > 0 })
	for _, spec := range h.specs {
		h.ix.HandleTrain(h.view(spec))
	}
	return h
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) spec(id string) layoutsim.TrainSpec {
	for i, s := range h.specs {
		if id == fmt.Sprintf("local-%d", i+1) {
			return s
		}
	}
	h.t.Fatalf("no train %s", id)
	return layoutsim.TrainSpec{}
}

// advance moves the layout on by d, passing on the speed commands due.
func (h *harness) advance(d time.Duration) {
	const dt = 10 * time.Millisecond
	h.mu.Lock()
	defer h.mu.Unlock()
	for end := h.now.Add(d); h.now.Before(end); {
		h.now = h.now.Add(dt)
		for len(h.queue) > 0 && !h.queue[0].at.After(h.now) {
			q := h.queue[0]
			h.queue = h.queue[1:]
			h.decoders[q.address].Speed = q.speed
		}
		h.sim.Step(h.now, dt)
		for _, ts := range h.sim.State().Trains {
			target := float64(h.decoders[ts.Address].Speed.Value)
			if h.decoders[ts.Address].Speed.Reverse {
				target = -target
			}
			switch {
			case ts.Step == 0 || ts.Step != target:
				h.steady[ts.Address] = 0
			case h.steady[ts.Address] != ts.Step:
				h.steady[ts.Address], h.since[ts.Address] = ts.Step, h.now
			}
		}
	}
}

// view is a train as perfect tracking would have it. The caller doesn't
// hold h.mu.
func (h *harness) view(spec layoutsim.TrainSpec) tracking.Train {
	pos, ok := h.sim.Position(spec.Address)
	if !ok {
		h.t.Fatalf("%s isn't on the layout", spec.Name)
	}
	var stepNow float64
	for _, ts := range h.sim.State().Trains {
		if ts.Address == spec.Address {
			stepNow = ts.Step
		}
	}
	h.mu.Lock()
	d := h.decoders[spec.Address]
	speeds := slices.Clone(h.speeds[spec.Address])
	reverse := d.Speed.Reverse
	h.mu.Unlock()
	m := tracking.Motion{Start: d.CVs[2], Mid: d.CVs[6], High: d.CVs[5], Accel: d.CVs[3], Decel: d.CVs[4], Top: spec.TopSpeed, Speeds: speeds}
	dir := train.Forward
	if stepNow < 0 || (stepNow == 0 && reverse) {
		dir = train.Reverse
	}
	i := slices.IndexFunc(h.specs, func(s layoutsim.TrainSpec) bool { return s.Address == spec.Address })
	return tracking.Train{
		ID: fmt.Sprintf("local-%d", i+1), Name: spec.Name, Address: detector.Address{Type: detector.Short, Number: spec.Address},
		Roster: []string{fmt.Sprintf("motor-%d", i+1)}, Block: pos.Block, Seen: true, Placed: true,
		Blocks: pos.Stretch.Blocks(h.net), Direction: dir, Speed: m.Speed(stepNow), Length: spec.Length,
		Momentum: true, Stretch: pos.Stretch, Step: stepNow, Motion: m, Speeds: speeds,
		Addresses: []detector.Address{{Type: detector.Short, Number: spec.Address}},
	}
}

// Estimate moves the layout on by a step and returns where the train
// is, telling the interlocking where every train is too.
func (h *harness) Estimate(_ context.Context, id string) (tracking.Train, error) {
	h.advance(step)
	var out tracking.Train
	found := false
	for _, spec := range h.specs {
		v := h.view(spec)
		h.ix.HandleTrain(v)
		if v.ID == id {
			out, found = v, true
		}
	}
	if !found {
		return tracking.Train{}, tracking.ErrNotTracked
	}
	return out, nil
}

// SetSpeed sends a speed command on its way to the decoder and publishes
// the train's new speed, as the train manager does.
func (h *harness) SetSpeed(_ context.Context, id string, speed float32, direction string) (train.Train, error) {
	var address uint16
	if _, err := fmt.Sscanf(id, "motor-%d", &address); err != nil {
		return train.Train{}, err
	}
	address = h.specs[address-1].Address
	h.mu.Lock()
	h.queue = append(h.queue, queued{at: h.now.Add(latency), address: address, speed: traction.Speed{Value: speed, Reverse: direction == train.Reverse}})
	h.set = append(h.set, fmt.Sprintf("%d %v %s", address, speed, direction))
	h.mu.Unlock()
	tr := train.Train{Entry: train.Entry{ID: id, Address: address}, Speed: speed, Direction: direction}
	h.d.HandleTrain(tr)
	return tr, nil
}

// The simulated layout's hardware. The layout calls these from Step,
// with h.mu held.

func (h *harness) DecoderState(address uint16, _ bool) sim.Decoder { return *h.decoders[address] }
func (h *harness) Power() bool                                     { return true }

func (h *harness) Turnout(n openlcb.NodeID, servo int) string {
	return h.ents.state(entity.ProfileID(entity.KindTurnout, n, fmt.Sprintf("servo_%d", servo+1)))
}

func (h *harness) Accessory(number int) string {
	return h.ents.state(entity.AccessoryID(number))
}

// SetTrack is a detector's report. An entry after a run at a step held
// since the last is what tracking would measure the speed at that step
// from.
func (h *harness) SetTrack(_ openlcb.NodeID, _ int, loco *sim.Locomotive) {
	if loco == nil {
		return
	}
	a := loco.Address.Number
	last, ok := h.entered[a]
	h.entered[a] = h.now
	s := h.steady[a]
	if !ok || s == 0 || h.since[a].After(last) {
		return
	}
	d := h.decoders[a]
	var top float64
	for _, spec := range h.specs {
		if spec.Address == a {
			top = spec.TopSpeed
		}
	}
	step := int(max(s, -s))
	v := top * train.SpeedCurve(float64(step), d.CVs[2], d.CVs[6], d.CVs[5])
	speeds := h.speeds[a]
	i, found := slices.BinarySearchFunc(speeds, step, func(p tracking.SpeedPoint, s int) int { return p.Step - s })
	if found {
		speeds[i].Runs++
	} else {
		speeds = slices.Insert(speeds, i, tracking.SpeedPoint{Step: step, Speed: v, Runs: 1})
	}
	h.speeds[a] = speeds
}

// wait waits for a train's drive to end.
func (h *harness) wait(id string) Drive {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := h.d.Get(id); ok && !v.Active() {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	v, _ := h.d.Get(id)
	h.t.Fatalf("the drive didn't end: %+v", v)
	return Drive{}
}

// front is where a train's leading end really is, going the way given.
func (h *harness) front(id string, reverse bool) trackplan.Vec {
	pos, _ := h.sim.Position(h.spec(id).Address)
	leg, at := leading(h.net, pos.Stretch, reverse)
	return h.net.At(leg, at)
}

// markerAt is where a stop marker stands on the plan.
func (h *harness) markerAt(id string) trackplan.Vec {
	for _, m := range h.plan.Markers {
		if m.ID == id {
			leg := trackplan.Leg{Piece: m.Track}
			return h.net.At(leg, m.At)
		}
	}
	h.t.Fatalf("no marker %s", id)
	return trackplan.Vec{}
}

type fakePlan struct{ st trackplan.State }

func (p fakePlan) Get() trackplan.State { return p.st }

type fakeTracking struct{}

func (fakeTracking) List() []tracking.Train { return nil }

// fakeEntities are the layout's turnouts, which move as soon as they
// are commanded.
type fakeEntities struct {
	mu   sync.Mutex
	ix   *interlocking.Interlocking
	ents map[string]entity.Entity
	// stuck are turnouts that don't move when commanded.
	stuck map[string]bool
}

func (f *fakeEntities) List() []entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]entity.Entity, 0, len(f.ents))
	for _, e := range f.ents {
		out = append(out, e)
	}
	return out
}

func (f *fakeEntities) state(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ents[id].State
}

func (f *fakeEntities) Command(_ context.Context, id, command string) (entity.Entity, error) {
	f.mu.Lock()
	e := f.ents[id]
	f.mu.Unlock()
	if err := f.ix.Guard(e, command); err != nil {
		return e, err
	}
	f.mu.Lock()
	if f.stuck[id] {
		f.mu.Unlock()
		return e, nil
	}
	e.State = map[string]string{"throw": entity.StateThrown, "close": entity.StateClosed}[command]
	f.ents[id] = e
	f.mu.Unlock()
	f.ix.HandleEntity(e)
	return e, nil
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
