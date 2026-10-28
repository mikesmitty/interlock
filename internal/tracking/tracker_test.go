package tracking

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/layoutsim"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

type fakeEntities []entity.Entity

func (f fakeEntities) List() []entity.Entity { return f }

type fakeTrains struct {
	trains     []train.Train
	cars       []train.CarState
	formations []train.FormationState
}

func (f fakeTrains) List() []train.Train                { return f.trains }
func (f fakeTrains) Cars() []train.CarState             { return f.cars }
func (f fakeTrains) Formations() []train.FormationState { return f.formations }
func (f fakeTrains) Layout() train.Layout               { return train.Layout{} }

type fakePlan trackplan.State

func (f fakePlan) Get() trackplan.State { return trackplan.State(f) }

type fakeStore struct {
	mu     sync.Mutex
	values map[string]string
}

func (f *fakeStore) Setting(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeStore) SetSetting(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
	return nil
}

type fakeCVs struct {
	mu     sync.Mutex
	values map[string]int
}

func (f *fakeCVs) Get(string) (train.CVValues, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cvs := map[string]int{}
	for k, v := range f.values {
		cvs[k] = v
	}
	return train.CVValues{CVs: cvs}, nil
}

func reporter(outpost, track int, addrs ...uint16) entity.Entity {
	e := entity.Entity{ID: entity.ProfileID(entity.KindReporter, layoutsim.OutpostID(outpost), fmt.Sprintf("track_%d", track)), Kind: entity.KindReporter, Available: true}
	for _, a := range addrs {
		e.Locomotives = append(e.Locomotives, entity.Locomotive{Address: detector.Address{Type: detector.Short, Number: a}})
	}
	return e
}

// eventually waits for cond, failing after a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never %s", what)
}

func TestTrackerFollowsAndKeepsTheTrains(t *testing.T) {
	plan := layoutsim.TestLayout()
	store := &fakeStore{values: map[string]string{}}
	cvs := &fakeCVs{values: map[string]int{}}
	motor := train.Train{Entry: train.Entry{ID: "motor", Name: "Motor car", Address: 101, Length: 114, Installed: "d1"}, Roster: true, Direction: train.Forward}
	// In the roster but never seen: not listed.
	unseen := train.Train{Entry: train.Entry{ID: "other", Name: "Other", Address: 7}, Roster: true, Direction: train.Forward}
	local := train.FormationState{Formation: train.Formation{ID: "local-1", Name: "Local 1", Vehicles: []train.Vehicle{{Locomotive: "motor"}, {Car: "c1"}, {Car: "c2"}, {Car: "c3"}}}}
	var cars []train.CarState
	for _, id := range []string{"c1", "c2", "c3"} {
		cars = append(cars, train.CarState{Car: train.Car{ID: id, Name: id, Length: 114}})
	}
	tk := New(Options{
		Entities: fakeEntities{reporter(1, 1, 101)},
		Trains:   fakeTrains{trains: []train.Train{motor, unseen}, cars: cars, formations: []train.FormationState{local}},
		Plan:     fakePlan{Plan: plan, Revision: "r1"},
		CVs:      cvs,
		Store:    store,
		Tick:     10 * time.Millisecond,
	})
	var mu sync.Mutex
	var heard []Train
	tk.Subscribe(func(tr Train) {
		mu.Lock()
		heard = append(heard, tr)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- tk.Run(ctx) }()

	eventually(t, "named the train in its block", func() bool {
		l := tk.List()
		return len(l) == 1 && l[0].ID == "local-1" && l[0].Name == "Local 1" && l[0].Formation == "local-1" && l[0].Length == 456 && l[0].Block == "main-outer" && l[0].Seen && !l[0].Momentum
	})
	// Into the next block, which places it.
	tk.HandleEntity(reporter(3, 1, 101))
	eventually(t, "placed it", func() bool {
		l := tk.List()
		return l[0].Placed && l[0].Block == "west-outer" && len(l[0].Line) > 1
	})
	mu.Lock()
	n := len(heard)
	mu.Unlock()
	if n == 0 {
		t.Error("no subscriber was told")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var st saved
	if err := json.Unmarshal([]byte(store.values[settingKey]), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Trains) != 1 || st.Trains[0].Block != "west-outer" || len(st.Trains[0].Stretch) == 0 || st.Trains[0].At != 57 || st.Trains[0].Length != 456 {
		t.Errorf("saved %+v", st)
	}
}

func TestRefreshCVs(t *testing.T) {
	l := newLayout()
	values := decoderCVs{}
	l.cvs = func(string) decoderCVs { return values }
	l.train(train.Train{Entry: train.Entry{ID: "a", Name: "A", Address: 3, Installed: "d"}, Roster: true})
	key := addressKey(detector.Address{Type: detector.Short, Number: 3})
	delete(l.changed, key)
	values = decoderCVs{accel: 5, decel: 5, known: true}
	l.refreshCVs()
	if !l.changed[key] || !l.roster[detector.Address{Type: detector.Short, Number: 3}].cvs.known {
		t.Errorf("changed %v, cvs %+v", l.changed[key], l.roster[detector.Address{Type: detector.Short, Number: 3}].cvs)
	}
}

// line is a layout of one straight block of track, a, 1000 mm long and
// detected, for trains laid out by hand.
func line(t *testing.T) *layout {
	t.Helper()
	plan := trackplan.Plan{
		Points: []trackplan.Point{{ID: "p1", X: 0, Y: 0}, {ID: "p2", X: 1000, Y: 0}},
		Tracks: []trackplan.Track{{ID: "t1", A: "p1", B: "p2", Block: "a"}},
		Blocks: []trackplan.Block{{ID: "a", Name: "A", Reporter: "r"}},
	}
	l := newLayout()
	l.setPlan(plan, "r1")
	return l
}

// place lays a train out by hand with its decoder at x along the line.
func place(l *layout, addr detector.Address, x float64) *tracked {
	t := l.get(addr)
	leg := l.net.Legs("a")[0]
	t.block, t.seen = "a", true
	t.stretch = l.layOut(leg, x, t.at, t.length, l.position)
	return t
}

func TestALocomotiveJoinsATrain(t *testing.T) {
	l := line(t)
	addr := detector.Address{Type: detector.Short, Number: 5}
	l.setRoster([]train.Train{{Entry: train.Entry{ID: "loco", Name: "Loco", Address: 5, Length: 100}, Roster: true}}, nil, nil)
	alone := place(l, addr, 500)
	if alone.key != addressKey(addr) || alone.at != 50 || math.Abs(alone.stretch.Length()-100) > 1e-9 {
		t.Fatalf("on its own: key %s, decoder at %v, %v mm long", alone.key, alone.at, alone.stretch.Length())
	}
	clear(l.changed)

	// Made up at the rear of a train of two cars: the train is laid out
	// around the decoder, which stays put.
	l.car(train.CarState{Car: train.Car{ID: "c1", Name: "C1", Length: 150}})
	l.car(train.CarState{Car: train.Car{ID: "c2", Name: "C2", Length: 150}})
	l.formation(train.FormationState{Formation: train.Formation{ID: "set", Name: "Set", Vehicles: []train.Vehicle{{Car: "c1"}, {Car: "c2"}, {Locomotive: "loco"}}}})
	tr := l.trains[formationKey("set")]
	if tr == nil || l.trains[addressKey(addr)] != nil {
		t.Fatalf("followed: %v", slices.Collect(maps.Keys(l.trains)))
	}
	if !l.changed[addressKey(addr)] || !l.changed[formationKey("set")] {
		t.Errorf("changed %v; want the locomotive gone and the train new", l.changed)
	}
	if tr.at != 350 || tr.length != 400 || math.Abs(tr.stretch.Length()-400) > 1e-9 {
		t.Errorf("decoder at %v of %v, laid out %v mm", tr.at, tr.length, tr.stretch.Length())
	}
	leg, at := l.decoder(tr)
	if p := l.net.At(leg, at); math.Abs(p.X-500) > 1e-9 {
		t.Errorf("decoder moved to %v", p)
	}
	if v := l.view(tr); v.ID != "set" || v.Name != "Set" || v.Formation != "set" || v.Length != 400 || !slices.Equal(v.Roster, []string{"loco"}) {
		t.Errorf("view %+v", v)
	}
}

func TestATurnedLocomotiveRunsTheTrainBackward(t *testing.T) {
	l := line(t)
	l.setRoster(
		[]train.Train{{Entry: train.Entry{ID: "loco", Name: "Loco", Address: 5, Length: 100}, Roster: true}},
		[]train.CarState{{Car: train.Car{ID: "c1", Name: "C1", Length: 100}}},
		[]train.FormationState{{Formation: train.Formation{ID: "set", Name: "Set", Vehicles: []train.Vehicle{{Car: "c1"}, {Locomotive: "loco", Reverse: true}}}}},
	)
	tr := place(l, detector.Address{Type: detector.Short, Number: 5}, 500)
	l.train(train.Train{Entry: train.Entry{Address: 5}, Speed: 40, Direction: train.Forward})
	if tr.target != -40 || l.view(tr).Direction != train.Reverse {
		t.Errorf("target %v, going %s; want the train going backward", tr.target, l.view(tr).Direction)
	}
}

func TestATrainIsFollowedByTheDecoderLastSeen(t *testing.T) {
	l := line(t)
	l.setRoster(
		[]train.Train{
			{Entry: train.Entry{ID: "front", Name: "Front", Address: 5, Length: 100}, Roster: true},
			{Entry: train.Entry{ID: "rear", Name: "Rear", Address: 6, Length: 100}, Roster: true},
		},
		nil,
		[]train.FormationState{{Formation: train.Formation{ID: "pair", Name: "Pair", Vehicles: []train.Vehicle{{Locomotive: "front"}, {Locomotive: "rear", Reverse: true}}}}},
	)
	front, rear := detector.Address{Type: detector.Short, Number: 5}, detector.Address{Type: detector.Short, Number: 6}
	tr := place(l, front, 500)
	l.enter(rear, "a")
	if l.trains[formationKey("pair")] != tr || tr.addr != rear || tr.at != 150 || !tr.backward {
		t.Fatalf("followed by %v at %v (backward %v)", tr.addr, tr.at, tr.backward)
	}
	// The train didn't move: the rear decoder is 100 mm behind the front's.
	leg, at := l.decoder(tr)
	if p := l.net.At(leg, at); math.Abs(p.X-400) > 1e-9 {
		t.Errorf("rear decoder at %v", p)
	}
	// Only the decoder followed takes the train's speed.
	l.train(train.Train{Entry: train.Entry{Address: 5}, Speed: 30, Direction: train.Forward})
	l.train(train.Train{Entry: train.Entry{Address: 6}, Speed: 20, Direction: train.Reverse})
	if tr.target != 20 {
		t.Errorf("target %v; want the rear locomotive's reverse as the train's forward", tr.target)
	}
}
