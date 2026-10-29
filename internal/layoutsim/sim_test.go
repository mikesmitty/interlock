package layoutsim

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// fakeHardware records what the detectors report, with the time each
// report went out.
type fakeHardware struct {
	mu       sync.Mutex
	decoders map[uint16]*sim.Decoder
	servos   map[string]string
	now      time.Duration
	reports  []string
}

func newFake(trains []TrainSpec) *fakeHardware {
	f := &fakeHardware{decoders: map[uint16]*sim.Decoder{}, servos: map[string]string{}}
	for _, t := range trains {
		d := sim.NewDecoder(t.Address, t.long())
		d.CVs[3], d.CVs[4] = 0, 0
		f.decoders[t.Address] = d
	}
	return f
}

func (f *fakeHardware) DecoderState(address uint16, _ bool) sim.Decoder {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.decoders[address]
}

func (f *fakeHardware) Power() bool { return true }

func (f *fakeHardware) Turnout(n openlcb.NodeID, servo int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.servos[fmt.Sprintf("%v/%d", n, servo)]; ok {
		return s
	}
	return "closed"
}

func (f *fakeHardware) Accessory(number int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.servos[fmt.Sprintf("dcc/%d", number)]; ok {
		return s
	}
	return "unknown"
}

func (f *fakeHardware) SetTrack(n openlcb.NodeID, track int, loco *sim.Locomotive) {
	f.mu.Lock()
	defer f.mu.Unlock()
	what := "clear"
	if loco != nil {
		what = fmt.Sprint(loco.Address.Number)
	}
	f.reports = append(f.reports, fmt.Sprintf("%.2f %d/%d %s", f.now.Seconds(), (n-StationID)/0x10, track+1, what))
}

func (f *fakeHardware) set(address uint16, step float32, reverse bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decoders[address].Speed = traction.Speed{Value: step, Reverse: reverse}
}

// run steps the layout every 10 ms for d.
func (f *fakeHardware) run(s *Sim, d time.Duration) {
	const dt = 10 * time.Millisecond
	t0 := time.Unix(1000, 0)
	for end := f.now + d; f.now < end; {
		f.mu.Lock()
		f.now += dt
		now := f.now
		f.mu.Unlock()
		s.Step(t0.Add(now), dt)
	}
}

// reported lists the reports made, leaving out the clears every
// detector sends a second after the first step.
func (f *fakeHardware) reported() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.reports {
		if !strings.HasPrefix(r, "1.01 ") || !strings.HasSuffix(r, "clear") {
			out = append(out, r)
		}
	}
	return out
}

var quiet = slog.New(slog.DiscardHandler)

func newTestSim(trains []TrainSpec) (*Sim, *fakeHardware) {
	f := newFake(trains)
	s := New(f, trains, quiet)
	s.SetPlan(TestLayout(), "r1")
	return s, f
}

func TestTrainRunsFromBlockToBlock(t *testing.T) {
	// Local 1 starts with its B end at the east end of Main St's outer
	// platform, 456 mm long, its decoder 57 mm behind its A end, so 399
	// mm from where it starts: x = 715. At 160 mm/s westward it reaches
	// the platform's end (x = 374) in 2.13 s, and the next block past the
	// turnout (x = 248) in 2.92 s.
	trains := TestTrains()[:1]
	s, f := newTestSim(trains)
	f.set(101, 126, false)
	f.run(s, 4*time.Second)
	want := []string{
		"0.04 1/1 101",   // seen at the first step, reported after EntryDelay
		"2.95 3/1 101",   // into West outer, past the turnout
		"3.13 1/1 clear", // a second after the decoder left the platform
	}
	got := f.reported()
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("reports:\n got %v\nwant %v", got, want)
	}
}

func TestTrailingATurnoutSetAgainstStops(t *testing.T) {
	trains := TestTrains()[:1]
	s, f := newTestSim(trains)
	// Backing east, Local 1's B end leads straight into the closed end
	// of Main St's east turnout, thrown for the siding.
	f.servos[fmt.Sprintf("%v/%d", OutpostID(1), 0)] = "thrown"
	f.set(101, 126, true)
	f.run(s, time.Second)
	if got := s.trains[0].stopped; !strings.Contains(got, "main-east") {
		t.Fatalf("stopped = %q; want it stopped at main-east", got)
	}
	before := s.trains[0].path.Length()
	f.servos[fmt.Sprintf("%v/%d", OutpostID(1), 0)] = "closed"
	f.run(s, time.Second)
	if s.trains[0].stopped != "" {
		t.Errorf("still stopped once closed: %q", s.trains[0].stopped)
	}
	if l := s.trains[0].path.Length(); math.Abs(l-before) > 1e-6 {
		t.Errorf("the train went from %v to %v mm long", before, l)
	}
	// 160 mm east: through the turnout's 126 mm and onto the straight
	// beyond, which runs westward from its A point.
	if rear := s.trains[0].path[0]; rear.Leg != (trackplan.Leg{Piece: "outer-bottom-east"}) || math.Abs(rear.To-rear.From-34) > 0.5 {
		t.Errorf("B end on %+v; want it 34 mm into the east block's straight", rear)
	}
}

func TestThrownTurnoutTakesTheSiding(t *testing.T) {
	trains := []TrainSpec{{Name: "Local 1", Address: 101, Length: 456, Decoder: 57, TopSpeed: 160, Block: "east-outer"}}
	s, f := newTestSim(trains)
	f.servos[fmt.Sprintf("%v/%d", OutpostID(1), 0)] = "thrown"
	f.set(101, 126, false)
	f.run(s, 7*time.Second)
	if b := s.trains[0].block; b != "main-siding" {
		t.Errorf("decoder in %q; want main-siding", b)
	}
	got := strings.Join(f.reported(), "; ")
	if !strings.Contains(got, "1/2 101") || strings.Contains(got, "1/1 101") {
		t.Errorf("reports %s; want the siding's detector, not the platform's", got)
	}
}

func TestAccessoryDecoderTurnout(t *testing.T) {
	trains := []TrainSpec{{Name: "Local 1", Address: 101, Length: 456, Decoder: 57, TopSpeed: 160, Block: "east-outer"}}
	f := newFake(trains)
	s := New(f, trains, quiet)
	plan := TestLayout()
	for i := range plan.Junctions {
		if plan.Junctions[i].ID == "main-east" {
			plan.Junctions[i].Switch = "turnout.dcc.5"
		}
	}
	s.SetPlan(plan, "r1")
	// The servo it no longer follows says closed.
	f.servos[fmt.Sprintf("dcc/%d", 5)] = "thrown"
	f.set(101, 126, false)
	f.run(s, 7*time.Second)
	if b := s.trains[0].block; b != "main-siding" {
		t.Errorf("decoder in %q; want main-siding", b)
	}
}

func TestTrainsDontPassThroughEachOther(t *testing.T) {
	plan := trackplan.Plan{
		Points: []trackplan.Point{{ID: "p1", X: 0}, {ID: "p2", X: 500}, {ID: "p3", X: 1000}},
		Tracks: []trackplan.Track{{ID: "t1", A: "p1", B: "p2", Block: "a"}, {ID: "t2", A: "p2", B: "p3", Block: "b"}},
		Blocks: []trackplan.Block{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}},
		Grid:   33,
	}
	trains := []TrainSpec{
		{Name: "Moving", Address: 1, Length: 200, TopSpeed: 200, Block: "a"},
		{Name: "Standing", Address: 2, Length: 200, TopSpeed: 200, Block: "b"},
	}
	f := newFake(trains)
	s := New(f, trains, quiet)
	s.SetPlan(plan, "r1")
	f.set(1, 126, false)
	f.run(s, 3*time.Second)
	if got := s.trains[0].stopped; got != "ran into Standing" {
		t.Errorf("stopped = %q", got)
	}
	if leg, at := s.trains[0].path.Back(0); leg.Piece != "t1" || at < 495 || at > 500 {
		t.Errorf("A end at %s %.1f; want it at the standing train's end", leg.Piece, at)
	}
}

func TestStateCarriesOver(t *testing.T) {
	trains := TestTrains()
	s, f := newTestSim(trains)
	f.set(102, 80, false)
	f.run(s, 2*time.Second)
	st := s.State()

	again := New(newFake(trains), trains, quiet)
	again.Restore(st)
	again.SetPlan(TestLayout(), "r1")
	if got := again.State(); fmt.Sprint(got) != fmt.Sprint(st) {
		t.Errorf("restored %v\nwant %v", got, st)
	}
	// A plan without its track puts it back where it starts.
	moved := TestLayout()
	for i := range moved.Tracks {
		moved.Tracks[i].ID += "-x"
	}
	again.SetPlan(moved, "r2")
	if leg, _ := again.trains[1].path.Back(0); leg.Piece != "inner-top-park-x" {
		t.Errorf("Local 2's A end on %s after its track went", leg.Piece)
	}
}

func TestSpeedCurve(t *testing.T) {
	cvs := map[uint16]byte{2: 0, 5: 255, 6: 0}
	if got := speed(126, cvs, 160); got != 160 {
		t.Errorf("top speed = %v", got)
	}
	if got := speed(63.5, cvs, 160); math.Abs(got-80) > 1e-9 {
		t.Errorf("middle step = %v", got)
	}
	cvs[6] = 64 // a slow middle: half the top step at a quarter of the speed
	if got := speed(63.5, cvs, 160); math.Abs(got-160*64.0/255) > 1e-9 {
		t.Errorf("middle step with CV 6 = %v", got)
	}
	if speed(0.2, cvs, 160) != 0 {
		t.Error("moves below the first step")
	}
}

func TestParseEntity(t *testing.T) {
	n, track, ok := parseEntity("reporter.0a0000000120.track_3", "track_")
	if !ok || n != OutpostID(2) || track != 2 {
		t.Errorf("got %v %d %v", n, track, ok)
	}
	for _, id := range []string{"", "reporter.0a0000000120.servo_3", "reporter.zz.track_1", "turnout.0a0000000120.servo_0"} {
		if _, _, ok := parseEntity(id, "track_"); ok {
			t.Errorf("%q parsed", id)
		}
	}
}
