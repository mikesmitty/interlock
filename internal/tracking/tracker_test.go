package tracking

import (
	"context"
	"encoding/json"
	"fmt"
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

type fakeTrains []train.Train

func (f fakeTrains) List() []train.Train  { return f }
func (f fakeTrains) Layout() train.Layout { return train.Layout{} }

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
	local := train.Train{Entry: train.Entry{ID: "local-1", Name: "Local 1", Address: 101, Length: 456, DecoderAt: 57, Installed: "d1"}, Roster: true, Direction: train.Forward}
	tk := New(Options{
		Entities: fakeEntities{reporter(1, 1, 101)},
		Trains:   fakeTrains{local},
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
		return len(l) == 1 && l[0].Name == "Local 1" && l[0].Block == "main-outer" && l[0].Seen && !l[0].Momentum
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
	if len(st.Trains) != 1 || st.Trains[0].Block != "west-outer" || len(st.Trains[0].Stretch) == 0 {
		t.Errorf("saved %+v", st)
	}
}

func TestRefreshCVs(t *testing.T) {
	l := newLayout()
	values := decoderCVs{}
	l.train(train.Train{Entry: train.Entry{ID: "a", Name: "A", Address: 3, Installed: "d"}, Roster: true}, func(string) decoderCVs { return values })
	delete(l.changed, detector.Address{Type: detector.Short, Number: 3})
	values = decoderCVs{accel: 5, decel: 5, known: true}
	l.refreshCVs(func(string) decoderCVs { return values })
	addr := detector.Address{Type: detector.Short, Number: 3}
	if !l.changed[addr] || !l.roster[addr].cvs.known {
		t.Errorf("changed %v, cvs %+v", l.changed[addr], l.roster[addr].cvs)
	}
}
