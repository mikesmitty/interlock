package tracking

import (
	"fmt"
	"log/slog"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/layoutsim"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// bench runs the simulated test layout with tracking following its
// detectors, as Interlock would over the bus, minus the bus.
type bench struct {
	t        *testing.T
	sim      *layoutsim.Sim
	l        *layout
	net      *trackplan.Network
	specs    []layoutsim.TrainSpec
	decoders map[uint16]*sim.Decoder
	servos   map[string]string
	known    bool
	now      time.Time
}

func newBench(t *testing.T, momentumKnown bool) *bench {
	plan := layoutsim.TestLayout()
	b := &bench{
		t: t, l: newLayout(), net: plan.Network(), specs: layoutsim.TestTrains(),
		decoders: map[uint16]*sim.Decoder{}, servos: map[string]string{}, known: momentumKnown,
		now: time.Unix(1000, 0),
	}
	for i, spec := range b.specs {
		b.specs[i] = spec.Normalize()
		b.decoders[spec.Address] = sim.NewDecoder(spec.Address, false)
	}
	b.l.now = b.now
	b.l.setPlan(plan, "r1")
	for _, j := range plan.Junctions {
		b.setTurnout(j.Switch, entity.StateClosed)
	}
	b.sim = layoutsim.New(b, b.specs, slog.New(slog.DiscardHandler))
	b.sim.SetPlan(plan, "r1")
	b.l.cvs = b.cvs
	trains, cars, formations := b.roster()
	b.l.setRoster(trains, cars, formations)
	for _, tr := range trains {
		b.l.train(tr)
	}
	return b
}

// roster is the trains as the roster has them: a motor car with the
// decoder, and cars, made up into a train.
func (b *bench) roster() ([]train.Train, []train.CarState, []train.FormationState) {
	var trains []train.Train
	var cars []train.CarState
	var formations []train.FormationState
	for i, spec := range b.specs {
		motor := fmt.Sprintf("motor-%d", i+1)
		trains = append(trains, train.Train{
			Entry: train.Entry{
				ID: motor, Name: spec.Name + " motor car", Address: spec.Address,
				Length: spec.Cars[spec.Motor], Installed: fmt.Sprintf("d%d", i+1),
			},
			Roster: true, Direction: train.Forward,
		})
		f := train.FormationState{Formation: train.Formation{ID: fmt.Sprintf("local-%d", i+1), Name: spec.Name}}
		for j, length := range spec.Cars {
			if j == spec.Motor {
				f.Vehicles = append(f.Vehicles, train.Vehicle{Locomotive: motor})
				continue
			}
			id := fmt.Sprintf("car-%d-%d", i+1, j+1)
			cars = append(cars, train.CarState{Car: train.Car{ID: id, Name: id, Length: length}})
			f.Vehicles = append(f.Vehicles, train.Vehicle{Car: id})
		}
		formations = append(formations, f)
	}
	return trains, cars, formations
}

// cvs is what the CV store would hold for a decoder: its momentum when
// it has been read, nothing when not.
func (b *bench) cvs(string) decoderCVs {
	if !b.known {
		return decoderCVs{}
	}
	d := sim.NewDecoder(1, false)
	return decoderCVs{start: d.CVs[2], mid: d.CVs[6], high: d.CVs[5], accel: d.CVs[3], decel: d.CVs[4], known: true}
}

func (b *bench) DecoderState(address uint16, _ bool) sim.Decoder { return *b.decoders[address] }
func (b *bench) Power() bool                                     { return true }

func (b *bench) Turnout(n openlcb.NodeID, servo int) string {
	return b.servos[entity.ProfileID(entity.KindTurnout, n, fmt.Sprintf("servo_%d", servo+1))]
}

// SetTrack is a detector's report, which reaches tracking as its
// reporter entity changing.
func (b *bench) SetTrack(n openlcb.NodeID, track int, loco *sim.Locomotive) {
	e := entity.Entity{ID: entity.ProfileID(entity.KindReporter, n, fmt.Sprintf("track_%d", track+1)), Kind: entity.KindReporter, Available: true}
	if loco != nil {
		e.Locomotives = []entity.Locomotive{{Address: loco.Address, Facing: loco.Facing}}
	}
	// As Run does: the trains move on to the report first.
	b.l.tick(b.now)
	b.l.entity(e)
}

// setTurnout throws or closes a servo; the entity follows at once, as
// the servo's state only says where it is once it has moved.
func (b *bench) setTurnout(id, state string) {
	b.servos[id] = state
	b.l.entity(entity.Entity{ID: id, Kind: entity.KindTurnout, Available: true, State: state})
}

// drive sets a train's speed on the command station and tells tracking,
// as the train manager does.
func (b *bench) drive(address uint16, step float32, reverse bool) {
	b.decoders[address].Speed = traction.Speed{Value: step, Reverse: reverse}
	dir := train.Forward
	if reverse {
		dir = train.Reverse
	}
	b.l.train(train.Train{Entry: train.Entry{Address: address}, Speed: step, Direction: dir})
}

// run steps the layout and tracking every 20 ms for d, calling check
// after each step.
func (b *bench) run(d time.Duration, check func()) {
	const dt = 20 * time.Millisecond
	for end := b.now.Add(d); b.now.Before(end); {
		b.now = b.now.Add(dt)
		b.sim.Step(b.now, dt)
		b.l.tick(b.now)
		if check != nil {
			check()
		}
	}
}

func (b *bench) tracked(address uint16) *tracked {
	return b.l.trains[b.l.unitOf(detector.Address{Type: detector.Short, Number: address})]
}

// errorOf is how far tracking has the decoder from where it really is,
// in millimeters on the plan.
func (b *bench) errorOf(address uint16) float64 {
	t := b.tracked(address)
	truth, ok := b.sim.Position(address)
	if t == nil || t.stretch == nil || !ok {
		return math.Inf(1)
	}
	spec := b.spec(address)
	leg, at := truth.Stretch.Back(spec.Decoder)
	want := b.net.At(leg, at)
	leg, at = b.l.decoder(t)
	got := b.l.net.At(leg, at)
	return math.Hypot(got.X-want.X, got.Y-want.Y)
}

func (b *bench) spec(address uint16) layoutsim.TrainSpec {
	for _, s := range b.specs {
		if s.Address == address {
			return s
		}
	}
	b.t.Fatalf("no train at %d", address)
	return layoutsim.TrainSpec{}
}

// blocksAgree checks the blocks tracking has the train in against the
// blocks it is really in, allowing for being off by tol at either end.
func (b *bench) blocksAgree(address uint16, tol float64) error {
	t := b.tracked(address)
	truth, _ := b.sim.Position(address)
	got := b.l.view(t).Blocks
	s := truth.Stretch
	if s.Length() <= 2*tol {
		return nil
	}
	certain := s.Trim(tol).Reversed(b.net).Trim(tol).Reversed(b.net).Blocks(b.net)
	position := func(j string) trackplan.Position { return b.l.position(j) }
	grown, _, _ := s.Grow(b.net, tol, position)
	grown, _, _ = grown.Reversed(b.net).Grow(b.net, tol, position)
	possible := grown.Blocks(b.net)
	for _, blk := range certain {
		if !slices.Contains(got, blk) {
			return fmt.Errorf("tracking has it in %v; it is in %s too", got, blk)
		}
	}
	for _, blk := range got {
		if !slices.Contains(possible, blk) {
			return fmt.Errorf("tracking has it in %v; it isn't in %s (it is in %v)", got, blk, s.Blocks(b.net))
		}
	}
	return nil
}

func TestTrackingFollowsATrainAroundTheLoop(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprintf("momentum known %v", known), func(t *testing.T) {
			b := newBench(t, known)
			b.run(1500*time.Millisecond, nil)
			if tr := b.tracked(101); tr == nil || tr.block != "main-outer" || tr.stretch != nil {
				t.Fatalf("before it moves: %+v; want it in main-outer, not placed", tr)
			}
			b.drive(101, 80, false)
			worst := 0.0
			var blocksWrong error
			b.run(60*time.Second, func() {
				tr := b.tracked(101)
				if tr.samples < 2 {
					return
				}
				worst = max(worst, b.errorOf(101))
				if err := b.blocksAgree(101, 40); err != nil && blocksWrong == nil {
					blocksWrong = fmt.Errorf("at %v: %w", b.now.Sub(time.Unix(1000, 0)), err)
				}
			})
			tr := b.tracked(101)
			truth, _ := b.sim.Position(101)
			if tr.block != truth.Block || !tr.seen {
				t.Errorf("tracking has the decoder in %s (seen %v); it is in %s", tr.block, tr.seen, truth.Block)
			}
			// The decoder's top speed is 160 mm/s at full.
			if math.Abs(tr.top-160) > 8 {
				t.Errorf("measured top speed %.1f mm/s; want about 160", tr.top)
			}
			limit := 25.0
			if !known {
				// Without the momentum it runs ahead while the train gathers speed.
				limit = 60
			}
			t.Logf("decoder within %.1f mm once the speed was measured at %.1f mm/s", worst, tr.top)
			if worst > limit {
				t.Errorf("tracking had the decoder up to %.0f mm off once the speed was measured; want under %.0f", worst, limit)
			}
			if blocksWrong != nil {
				t.Error(blocksWrong)
			}
			if v := b.l.view(tr); v.Name != "Local 1" || !v.Placed || v.Direction != train.Forward || len(v.Line) < 2 || v.Decoder == nil {
				t.Errorf("view %+v", v)
			}
		})
	}
}

func TestTrackingFollowsATrainBackingWithItsDecoderInTheMiddle(t *testing.T) {
	// Local 2's decoder is in its middle car, and it backs around the
	// inner loop from Park Av.
	b := newBench(t, true)
	b.drive(102, 70, true)
	worst := 0.0
	var blocksWrong error
	b.run(60*time.Second, func() {
		if b.tracked(102).samples < 2 {
			return
		}
		worst = max(worst, b.errorOf(102))
		if err := b.blocksAgree(102, 40); err != nil && blocksWrong == nil {
			blocksWrong = err
		}
	})
	tr := b.tracked(102)
	t.Logf("decoder within %.1f mm once the speed was measured at %.1f mm/s", worst, tr.top)
	if tr.samples < 2 || worst > 25 {
		t.Errorf("%d measures, up to %.0f mm off", tr.samples, worst)
	}
	if blocksWrong != nil {
		t.Error(blocksWrong)
	}
	if v := b.l.view(tr); v.Direction != train.Reverse {
		t.Errorf("direction %s", v.Direction)
	}
}

func TestTrackingFollowsATrainIntoTheSiding(t *testing.T) {
	b := newBench(t, true)
	b.drive(101, 100, false)
	// Around the loop once to measure its speed, then into the siding
	// through Main St's east turnout, thrown.
	b.run(time.Second, nil)
	for b.tracked(101).samples < 3 {
		b.run(time.Second, nil)
		if b.now.After(time.Unix(1200, 0)) {
			t.Fatal("never measured")
		}
	}
	east := entity.ProfileID(entity.KindTurnout, layoutsim.OutpostID(1), "servo_1")
	for b.tracked(101).block != "east-outer" {
		b.run(100*time.Millisecond, nil)
	}
	b.setTurnout(east, entity.StateThrown)
	for b.tracked(101).block != "main-siding" {
		b.run(100*time.Millisecond, nil)
		if b.now.After(time.Unix(1400, 0)) {
			truth, _ := b.sim.Position(101)
			t.Fatalf("tracking has it in %s; it is in %s", b.tracked(101).block, truth.Block)
		}
	}
	b.run(2*time.Second, nil)
	if err := b.blocksAgree(101, 40); err != nil {
		t.Error(err)
	}
	if e := b.errorOf(101); e > 25 {
		t.Errorf("%.0f mm off in the siding", e)
	}
}

func TestTrackingTurnsATrainAround(t *testing.T) {
	for _, tc := range []struct {
		known bool
		// The stretch it may be off by: without the momentum, the
		// estimate stops at once and turns about at once while the
		// train slows and gathers speed.
		limit, tol float64
	}{
		{true, 25, 40},
		{false, 300, 300},
	} {
		t.Run(fmt.Sprintf("momentum known %v", tc.known), func(t *testing.T) {
			b := newBench(t, tc.known)
			b.drive(101, 80, false)
			for b.tracked(101).samples < 2 {
				b.run(time.Second, nil)
				if b.now.After(time.Unix(1200, 0)) {
					t.Fatal("never measured")
				}
			}
			// Stop, then back the way it came for 20 seconds.
			b.drive(101, 0, false)
			b.run(3*time.Second, nil)
			b.drive(101, 80, true)
			worst := 0.0
			var blocksWrong error
			b.run(20*time.Second, func() {
				worst = max(worst, b.errorOf(101))
				if err := b.blocksAgree(101, tc.tol); err != nil && blocksWrong == nil {
					blocksWrong = err
				}
			})
			t.Logf("decoder within %.1f mm stopping and backing", worst)
			if worst > tc.limit {
				t.Errorf("up to %.0f mm off stopping and backing", worst)
			}
			if blocksWrong != nil {
				t.Error(blocksWrong)
			}
			truth, _ := b.sim.Position(101)
			if tr := b.tracked(101); tr.block != truth.Block {
				t.Errorf("tracking has it in %s; it is in %s", tr.block, truth.Block)
			}
		})
	}
}

func TestTrackingKeepsPlacesOverARestart(t *testing.T) {
	b := newBench(t, true)
	b.drive(101, 80, false)
	b.run(30*time.Second, nil)
	b.drive(101, 0, false)
	b.run(3*time.Second, nil)
	st := b.l.state()

	again := newLayout()
	again.setPlan(layoutsim.TestLayout(), "r1")
	again.setRoster(b.roster())
	again.restore(st)
	before, after := b.tracked(101), again.trains[formationKey("local-1")]
	if len(before.speeds) == 0 {
		t.Fatal("no speeds measured to keep")
	}
	if after == nil || after.block != before.block || after.top != before.top || fmt.Sprint(after.stretch) != fmt.Sprint(before.stretch) || fmt.Sprint(after.speeds) != fmt.Sprint(before.speeds) {
		t.Fatalf("restored %+v\nwant %+v", after, before)
	}
	// The reporter says it is still there: it stays placed.
	again.entity(entity.Entity{ID: entity.ProfileID(entity.KindReporter, layoutsim.OutpostID(1), "track_1"), Kind: entity.KindReporter, Available: true})
	for id, blocks := range b.l.reporters {
		if slices.Contains(blocks, before.block) {
			again.entity(entity.Entity{ID: id, Kind: entity.KindReporter, Available: true, Locomotives: []entity.Locomotive{{Address: after.addr}}})
		}
	}
	if !after.seen || after.stretch == nil {
		t.Errorf("seen %v, placed %v after the restart", after.seen, after.stretch != nil)
	}
}

func TestTrackingMeasuresTheSpeedAtEachStepHeld(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprintf("momentum known %v", known), func(t *testing.T) {
			b := newBench(t, known)
			d := sim.NewDecoder(1, false)
			truth := func(step float64) float64 { return 160 * train.SpeedCurve(step, d.CVs[2], d.CVs[6], d.CVs[5]) }
			for _, step := range []float32{80, 30} {
				b.drive(101, step, false)
				for start := b.now; ; {
					b.run(time.Second, nil)
					if i := slices.IndexFunc(b.tracked(101).speeds, func(p SpeedPoint) bool { return p.Step == int(step) }); i >= 0 && b.tracked(101).speeds[i].Runs >= 2 {
						break
					}
					if b.now.Sub(start) > 5*time.Minute {
						t.Fatalf("step %v never measured: %+v", step, b.tracked(101).speeds)
					}
				}
			}
			tr := b.tracked(101)
			for _, p := range tr.speeds {
				want := truth(float64(p.Step))
				t.Logf("step %d: %.1f mm/s over %d runs; really %.1f", p.Step, p.Speed, p.Runs, want)
				if math.Abs(p.Speed-want) > 0.03*want {
					t.Errorf("step %d measured at %.1f mm/s; want about %.1f", p.Step, p.Speed, want)
				}
			}
			if len(tr.speeds) != 2 {
				t.Errorf("speeds measured at %+v; want steps 30 and 80 only", tr.speeds)
			}
			// Between and beyond the steps measured.
			m := b.l.motion(tr)
			if got, want := m.Speed(55), (tr.speeds[0].Speed+tr.speeds[1].Speed)/2; math.Abs(got-want) > 0.01 {
				t.Errorf("speed at 55 = %.1f; want halfway, %.1f", got, want)
			}
			if got, want := m.Speed(126), truth(126); math.Abs(got-want) > 0.03*want {
				t.Errorf("speed at 126 = %.1f; want about %.1f", got, want)
			}
		})
	}
}

func TestTrackingPublishesATrainComingToAStand(t *testing.T) {
	b := newBench(t, true)
	b.drive(101, 60, false)
	b.run(10*time.Second, nil)
	b.drive(101, 0, false)
	// The tick it comes to a stand in, it doesn't move, but its speed
	// changes to 0 and has to be published.
	for b.tracked(101).step != 0 {
		clear(b.l.changed)
		b.run(20*time.Millisecond, nil)
		if b.now.After(time.Unix(1100, 0)) {
			t.Fatal("never stopped")
		}
	}
	if !b.l.changed[formationKey("local-1")] {
		t.Error("not published as it came to a stand")
	}
}
