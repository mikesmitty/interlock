package interlocking

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

type fakePlan struct{ st trackplan.State }

func (p fakePlan) Get() trackplan.State { return p.st }

// fakeEntities are the ladder's turnouts, which move as soon as they are
// commanded.
type fakeEntities struct {
	mu   sync.Mutex
	ix   *Interlocking
	ents map[string]entity.Entity
	sent []string
}

func (f *fakeEntities) List() []entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []entity.Entity
	for _, e := range f.ents {
		out = append(out, e)
	}
	return out
}

func (f *fakeEntities) Command(_ context.Context, id, command string) (entity.Entity, error) {
	f.mu.Lock()
	e := f.ents[id]
	f.mu.Unlock()
	if err := f.ix.Guard(e, command); err != nil {
		return e, err
	}
	e.State = map[string]string{"throw": entity.StateThrown, "close": entity.StateClosed}[command]
	f.mu.Lock()
	f.ents[id] = e
	f.sent = append(f.sent, id+" "+command)
	f.mu.Unlock()
	f.ix.HandleEntity(e)
	return e, nil
}

type fakeTracking struct{}

func (fakeTracking) List() []tracking.Train { return nil }

type fakeStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *fakeStore) Setting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *fakeStore) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func start(t *testing.T, store *fakeStore) (*Interlocking, *fakeEntities, func()) {
	t.Helper()
	plan := ladder()
	ents := &fakeEntities{ents: map[string]entity.Entity{}}
	for _, id := range []string{"turnout.j1", "turnout.j2"} {
		ents.ents[id] = entity.Entity{ID: id, Kind: entity.KindTurnout, Name: strings.ToUpper(id[8:]), Available: true, State: entity.StateClosed}
	}
	ix := New(Options{Entities: ents, Plan: fakePlan{trackplan.State{Plan: plan, Revision: "r1", Topology: plan.Topology()}}, Tracking: fakeTracking{}, Store: store})
	ents.ix = ix
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = ix.Run(ctx)
		close(done)
	}()
	eventually(t, "the routes", func() bool { return len(ix.List()) > 0 })
	return ix, ents, func() {
		cancel()
		<-done
	}
}

func TestSettingARoute(t *testing.T) {
	store := &fakeStore{m: map[string]string{}}
	ix, ents, stop := start(t, store)
	var mu sync.Mutex
	var updates []Update
	ix.Subscribe(func(u Update) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	})
	ctx := context.Background()

	v, err := ix.Set(ctx, "a~c")
	if err != nil || v.ID != "a~c" {
		t.Fatalf("Set = %+v, %v", v, err)
	}
	if len(ents.sent) != 1 || ents.sent[0] != "turnout.j1 throw" {
		t.Errorf("sent %v", ents.sent)
	}
	eventually(t, "the route set", func() bool { r, _ := ix.Get("a~c"); return r.State == StateSet })

	j1 := ents.ents["turnout.j1"]
	var locked *entity.LockedError
	if err := ix.Guard(j1, "close"); !errors.As(err, &locked) || locked.Reason != "J1 is locked thrown by the route from A to C." {
		t.Errorf("closing J1: %v", err)
	}
	if err := ix.Guard(j1, "throw"); err != nil {
		t.Errorf("throwing J1: %v", err)
	}
	var refused *RefusedError
	if _, err := ix.Set(ctx, "a~b"); !errors.As(err, &refused) {
		t.Errorf("a~b over J1 the other way: %v", err)
	}
	if _, err := ix.Set(ctx, "nowhere"); !errors.Is(err, ErrNoRoute) {
		t.Errorf("no such route: %v", err)
	}

	// Kept over a restart, where J1 has been closed meanwhile.
	stop()
	ix, _, stop = start(t, store)
	if r, _ := ix.Get("a~c"); r.State != StateSetting || len(r.Waiting) != 1 {
		t.Errorf("after a restart: %+v", r)
	}
	if err := ix.Guard(j1, "close"); err == nil {
		t.Error("J1 not locked after a restart")
	}
	if r, err := ix.Cancel(ctx, "a~c", false); err != nil || r.State != "" {
		t.Errorf("Cancel = %+v, %v", r, err)
	}
	if err := ix.Guard(j1, "close"); err != nil {
		t.Errorf("J1 still locked: %v", err)
	}
	stop()

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, u := range updates {
		if u.Route != nil && u.Route.ID == "a~c" && u.Route.State == StateSet {
			found = true
		}
	}
	if !found {
		t.Errorf("no update had a~c set: %+v", updates)
	}
}
