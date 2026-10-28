package driving

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/layoutsim"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

func TestDriveStopsAtTheMarker(t *testing.T) {
	for _, tc := range []struct {
		name, train, to, marker string
		routes                  []string
		speed                   int
	}{
		// Around the outer loop from Main St to Park Av, through Main St's
		// west turnout.
		{"Local 1 to Park Av", "local-1", "park-outer", "park-outer-stop", []string{"main-outer~west-outer", "west-outer~park-outer"}, 80},
		// Local 2, its decoder in its middle car, around the inner loop
		// from Park Av to Main St.
		{"Local 2 to Main St", "local-2", "main-inner", "main-inner-stop", []string{"park-inner~west-inner", "west-inner~main-inner"}, 60},
		// Flat out, which has to brake from well before the block.
		{"Local 1 fast", "local-1", "park-outer", "park-outer-stop", nil, 126},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			v, err := h.d.Drive(context.Background(), tc.train, Request{To: tc.to, Speed: tc.speed})
			if err != nil {
				t.Fatal(err)
			}
			if v.Marker != tc.marker || v.Direction != train.Forward || v.State != StateStarting {
				t.Errorf("began %+v", v)
			}
			if tc.routes != nil && strings.Join(v.Routes, " ") != strings.Join(tc.routes, " ") {
				t.Errorf("routes %v; want %v", v.Routes, tc.routes)
			}
			v = h.wait(tc.train)
			if v.State != StateArrived || v.Off == nil {
				t.Fatalf("ended %+v", v)
			}
			front, marker := h.front(tc.train, false), h.markerAt(tc.marker)
			off := math.Hypot(front.X-marker.X, front.Y-marker.Y)
			t.Logf("stopped %.1f mm from the marker; %.1f mm short by the estimate, after %v", off, *v.Off, v.Ended.Sub(v.Started))
			if off > 5 {
				t.Errorf("stopped %.1f mm from the marker", off)
			}
			pos, _ := h.sim.Position(h.spec(tc.train).Address)
			if blocks := pos.Stretch.Blocks(h.net); len(blocks) != 1 || blocks[0] != tc.to {
				t.Errorf("stopped in %v; want all of it in %s", blocks, tc.to)
			}
			eventually(t, "the routes released", func() bool {
				for _, id := range v.Routes {
					if r, _ := h.ix.Get(id); r.State != "" {
						return false
					}
				}
				return true
			})
		})
	}
}

func TestDriveRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var refused *RefusedError
	if _, err := h.d.Drive(ctx, "local-1", Request{To: "nowhere"}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "no block") {
		t.Errorf("to a block not in the plan: %v", err)
	}
	if _, err := h.d.Drive(ctx, "nobody", Request{To: "park-outer"}); !errors.Is(err, ErrNoTrain) {
		t.Errorf("a train not tracked: %v", err)
	}
	if _, err := h.d.Drive(ctx, "local-1", Request{To: "park-outer", Marker: "main-inner-stop"}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "isn't on") {
		t.Errorf("to a marker elsewhere: %v", err)
	}
	if _, err := h.d.Drive(ctx, "local-1", Request{To: "park-outer", Direction: "sideways"}); !errors.As(err, &refused) {
		t.Errorf("sideways: %v", err)
	}
	// Something in the way: a train the interlocking knows is in West
	// outer, which the way to Park Av runs through.
	h.ix.HandleTrain(tracking.Train{ID: "ghost", Name: "A ghost", Block: "west-outer"})
	_, err := h.d.Drive(ctx, "local-1", Request{To: "park-outer"})
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "A ghost is in West outer") {
		t.Errorf("into a train: %v", err)
	}
	for _, r := range h.ix.List() {
		if r.State != "" {
			t.Errorf("route %s left set", r.ID)
		}
	}
	h.ix.HandleTrain(tracking.Train{ID: "ghost", Removed: true})
	if _, err := h.d.Drive(ctx, "local-1", Request{To: "park-outer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.Drive(ctx, "local-1", Request{To: "west-outer"}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "being driven") {
		t.Errorf("driven twice: %v", err)
	}
}

func TestDriveToTheEndOfABlockWithoutMarkers(t *testing.T) {
	h := newHarness(t)
	v, err := h.d.Drive(context.Background(), "local-1", Request{To: "west-outer"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Marker != "" {
		t.Errorf("marker %s in a block with none", v.Marker)
	}
	v = h.wait("local-1")
	if v.State != StateArrived || math.Abs(*v.Off) > 2 {
		t.Fatalf("ended %+v", v)
	}
	// Short of the end of West outer by the margin.
	pos, _ := h.sim.Position(h.spec("local-1").Address)
	if blocks := pos.Stretch.Blocks(h.net); len(blocks) != 1 || blocks[0] != "west-outer" {
		t.Errorf("stopped in %v", blocks)
	}
}

func TestADriveTakenOverByAThrottle(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.Drive(context.Background(), "local-1", Request{To: "park-outer"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the train running", func() bool { v, _ := h.d.Get("local-1"); return v.State == StateRunning })
	h.d.HandleTrain(train.Train{Entry: train.Entry{Address: 101}, Speed: 12, Direction: train.Forward})
	v := h.wait("local-1")
	if v.State != StateStopped || !strings.Contains(v.Error, "throttle") {
		t.Errorf("ended %+v", v)
	}
	// It leaves the train to the throttle.
	h.mu.Lock()
	last := h.set[len(h.set)-1]
	h.mu.Unlock()
	if last == "101 0 forward" {
		t.Error("the drive stopped the train a throttle took over")
	}
}

func TestStoppingADrive(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.Drive(context.Background(), "local-1", Request{To: "park-outer"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the train running", func() bool { v, _ := h.d.Get("local-1"); return v.State == StateRunning && v.Remaining < 1500 })
	if _, err := h.d.Stop(context.Background(), "local-1"); err != nil {
		t.Fatal(err)
	}
	v := h.wait("local-1")
	if v.State != StateStopped || v.Error != "" {
		t.Fatalf("ended %+v", v)
	}
	if _, err := h.d.Stop(context.Background(), "local-1"); !errors.Is(err, ErrNotDriving) {
		t.Errorf("stopping it again: %v", err)
	}
	// The route ahead it didn't reach is canceled.
	eventually(t, "the route ahead canceled", func() bool {
		r, _ := h.ix.Get("west-outer~park-outer")
		return r.State == ""
	})
	for _, ts := range h.sim.State().Trains {
		if ts.Address == 101 && ts.Step != 0 {
			t.Errorf("still at step %v", ts.Step)
		}
	}
}

func TestMeasuringRunsTheTrainAroundAtEachStep(t *testing.T) {
	h := newHarness(t)
	v, err := h.d.Measure(context.Background(), "local-1", MeasureRequest{Steps: []int{90, 40}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindMeasure || v.Speed != 40 {
		t.Errorf("began %+v", v)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if v, _ = h.d.Get("local-1"); !v.Active() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still measuring: %+v", v)
		}
		time.Sleep(time.Millisecond)
	}
	if v.State != StateMeasured || len(v.Measuring.Done) != 2 || len(v.Measuring.Skipped) != 0 {
		t.Fatalf("ended %+v, %+v", v, v.Measuring)
	}
	h.mu.Lock()
	speeds := h.speeds[101]
	h.mu.Unlock()
	for _, p := range speeds {
		if (p.Step == 40 || p.Step == 90) && p.Runs < runsPerStep {
			t.Errorf("step %d measured over %d runs", p.Step, p.Runs)
		}
	}
	if len(v.Routes) < 2 {
		t.Errorf("routes %v; want those set as it went", v.Routes)
	}
	for _, ts := range h.sim.State().Trains {
		if ts.Address == 101 && ts.Step != 0 {
			t.Errorf("still at step %v", ts.Step)
		}
	}
	t.Logf("measured %v over routes %v in %v", speeds, v.Routes, v.Ended.Sub(v.Started))
}

func TestATrainRunningWaitsShortOfARouteNotYetSet(t *testing.T) {
	h := newHarness(t)
	// Local 1 is running by hand when the drive takes it over, and Main
	// St's east turnout, which its last route throws, doesn't move.
	east := entity.ProfileID(entity.KindTurnout, layoutsim.OutpostID(1), "servo_1")
	h.ents.stuck[east] = true
	// Long enough a wait for it to reach the turnout.
	h.d.opts.SetTimeout = time.Minute
	h.mu.Lock()
	h.decoders[101].Speed = traction.Speed{Value: 80}
	h.mu.Unlock()
	for {
		h.advance(100 * time.Millisecond)
		pos, _ := h.sim.Position(101)
		if leg, _ := leading(h.net, pos.Stretch, false); h.net.Block(leg) == "west-outer" {
			break
		}
	}
	v, err := h.d.Drive(context.Background(), "local-1", Request{To: "main-siding", Speed: 80})
	if err != nil {
		t.Fatal(err)
	}
	if last := v.Routes[len(v.Routes)-1]; last != "east-outer~main-siding" {
		t.Fatalf("routes %v", v.Routes)
	}
	v = h.wait("local-1")
	if v.State != StateFailed || !strings.Contains(v.Error, "didn't move") {
		t.Fatalf("ended %+v", v)
	}
	h.advance(time.Second) // for the last command to arrive
	pos, _ := h.sim.Position(101)
	if leg, at := leading(h.net, pos.Stretch, false); h.net.Block(leg) != "east-outer" || h.net.Length(leg)-at < endMargin-5 {
		t.Errorf("stopped with its front %.0f mm along %s in %q; want it in East outer, short of the turnout", at, leg.Piece, h.net.Block(leg))
	}
	for _, ts := range h.sim.State().Trains {
		if ts.Address == 101 && ts.Step != 0 {
			t.Errorf("still at step %v", ts.Step)
		}
	}
	// The routes it set that the train never reached are canceled.
	eventually(t, "the route not reached canceled", func() bool {
		r, _ := h.ix.Get("east-outer~main-siding")
		return r.State == ""
	})
}
