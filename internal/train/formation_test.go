package train_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mikesmitty/interlock/internal/train"
)

func TestFormations(t *testing.T) {
	b := newBench(t)
	var mu sync.Mutex
	var heard []train.FormationState
	b.m.SubscribeFormations(func(f train.FormationState) {
		mu.Lock()
		defer mu.Unlock()
		heard = append(heard, f)
	})
	last := func() train.FormationState {
		mu.Lock()
		defer mu.Unlock()
		if len(heard) == 0 {
			return train.FormationState{}
		}
		return heard[len(heard)-1]
	}

	loco, err := b.m.Create(train.Entry{Name: "Motor car", Address: 101, Length: 114})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.CreateCar(train.Car{}); err == nil {
		t.Error("a car with no name should be refused")
	}
	c1, err := b.m.CreateCar(train.Car{RoadName: "NYCT", RoadNumber: "6401", Length: 114})
	if err != nil {
		t.Fatal(err)
	}
	if c1.Name != "NYCT 6401" || c1.ID != "nyct-6401" {
		t.Errorf("car = %+v; want it named by its road name and number", c1)
	}
	c2, err := b.m.CreateCar(train.Car{Name: "Trailer"})
	if err != nil {
		t.Fatal(err)
	}

	f, err := b.m.CreateFormation(train.Formation{Name: "6 local", Vehicles: []train.Vehicle{
		{Locomotive: loco.ID}, {Car: c1.ID}, {Car: c2.ID, Reverse: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "6-local" || f.Length != 228 || f.Unmeasured != 1 {
		t.Errorf("formation = %+v; want 228 mm with one car not measured", f)
	}

	// A car is in one train at most.
	if _, err := b.m.CreateFormation(train.Formation{Name: "Other", Vehicles: []train.Vehicle{{Car: c1.ID}}}); err == nil || !strings.Contains(err.Error(), "6 local") {
		t.Errorf("a car in two trains: %v", err)
	}
	if _, err := b.m.CreateFormation(train.Formation{Name: "Twice", Vehicles: []train.Vehicle{{Car: c2.ID}, {Car: c2.ID}}}); err == nil {
		t.Error("a car twice in one train should be refused")
	}
	if _, err := b.m.CreateFormation(train.Formation{Name: "Ghost", Vehicles: []train.Vehicle{{Car: "nope"}}}); err == nil {
		t.Error("a car not in the roster should be refused")
	}
	// A train is never named by an address, which is how tracking names
	// a locomotive on its own.
	if g, err := b.m.CreateFormation(train.Formation{Name: "101"}); err != nil || g.ID != "train-101" {
		t.Errorf("formation named 101: %+v, %v", g, err)
	}

	// Measuring a car, or the locomotive, tells the train's subscribers.
	if _, err := b.m.UpdateCar(c2.ID, func(c *train.Car) error { c.Length = 120; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.ID != f.ID || got.Length != 348 || got.Unmeasured != 0 {
		t.Errorf("after measuring the car: %+v", got)
	}
	if _, err := b.m.Update(b.ctx, loco.ID, func(e *train.Entry) error { e.Length = 120; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.ID != f.ID || got.Length != 354 {
		t.Errorf("after measuring the locomotive: %+v", got)
	}

	// Reordered and turned through an update.
	f, err = b.m.UpdateFormation(f.ID, func(fm *train.Formation) error {
		fm.Vehicles = []train.Vehicle{{Car: c2.ID}, {Car: c1.ID}, {Locomotive: loco.ID, Reverse: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Vehicles[2] != (train.Vehicle{Locomotive: loco.ID, Reverse: true}) {
		t.Errorf("updated = %+v", f)
	}

	// Removing a car or the locomotive takes it out of the train.
	if err := b.m.DeleteCar(c1.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.m.Formation(f.ID); len(got.Vehicles) != 2 || got.Length != 240 {
		t.Errorf("after removing a car: %+v", got)
	}
	if err := b.m.Delete(b.ctx, loco.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := b.m.Formation(f.ID)
	if len(got.Vehicles) != 1 || got.Vehicles[0].Car != c2.ID {
		t.Errorf("after removing the locomotive: %+v", got)
	}
	if l := last(); l.ID != f.ID || len(l.Vehicles) != 1 {
		t.Errorf("subscribers last heard %+v", l)
	}

	// Kept in the file.
	file, err := train.OpenFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	m := train.New(train.Options{Bus: b.local, File: file, Logger: slog.New(slog.DiscardHandler)})
	if again, ok := m.Formation(f.ID); !ok || again.Name != "6 local" || len(again.Vehicles) != 1 || again.Length != 120 {
		t.Errorf("reloaded = %+v", again)
	}
	if cars := m.Cars(); len(cars) != 1 || cars[0].ID != c2.ID {
		t.Errorf("reloaded cars = %+v", cars)
	}

	if err := b.m.DeleteFormation(f.ID); err != nil {
		t.Fatal(err)
	}
	if l := last(); l.ID != f.ID || !l.Removed {
		t.Errorf("subscribers last heard %+v; want it removed", l)
	}
	if _, ok := b.m.Car(c2.ID); !ok {
		t.Error("the car should stay in the roster")
	}
}

func TestFormationFollowsANewLocomotiveID(t *testing.T) {
	b := newBench(t)
	// An entry from before locomotives had IDs of their own is known by
	// its address, and gets an ID of its own when the address changes.
	path := filepath.Join(t.TempDir(), "trains.yaml")
	doc := "trains:\n  - name: Old\n    address: 7\nformations:\n  - id: set\n    name: Set\n    vehicles:\n      - locomotive: \"7\"\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := train.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := train.New(train.Options{Bus: b.local, File: file, Logger: slog.New(slog.DiscardHandler)})
	tr, err := m.Update(b.ctx, "7", func(e *train.Entry) error { e.Address = 4; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Formation("set"); tr.ID == "7" || got.Vehicles[0].Locomotive != tr.ID {
		t.Errorf("formation names %s; the locomotive is %s now", got.Vehicles[0].Locomotive, tr.ID)
	}
}
