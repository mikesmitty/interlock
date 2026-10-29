package train_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

const (
	localID   = openlcb.NodeID(0x0A0000000001)
	stationID = openlcb.NodeID(0x0A0000000077)
)

type bench struct {
	local   *node.Node
	station *sim.Station
	m       *train.Manager
	file    string
	ctx     context.Context

	mu      sync.Mutex
	changes []train.Train
	layouts []train.Layout
}

func newBench(t *testing.T) *bench {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	b := &bench{}
	b.local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); b.station.HandleFrame(f) }})
	var err error
	b.station, err = sim.NewStation(node.Config{ID: stationID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); b.local.HandleFrame(f) }},
		sim.StationOptions{Decoders: []*sim.Decoder{sim.NewDecoder(1234, true)}, ProgrammingTrack: 1234, ServiceModeDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b.ctx = ctx
	go func() { _ = b.local.Run(ctx) }()
	go func() { _ = b.station.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for b.local.State() != node.StateInitialized || b.station.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	b.file = filepath.Join(t.TempDir(), "trains.yaml")
	f, err := train.OpenFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	mem := memconfig.NewClient(b.local, memconfig.WithLogger(logger), memconfig.WithReplyTimeout(time.Second))
	b.m = train.New(train.Options{Bus: b.local, Memory: mem, File: f, Logger: logger, SearchWindow: 150 * time.Millisecond, Functions: 5})
	b.m.Subscribe(func(tr train.Train) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.changes = append(b.changes, tr)
	})
	b.m.SubscribeLayout(func(l train.Layout) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.layouts = append(b.layouts, l)
	})
	b.local.Observe(func(ev node.Event) {
		if ev.Kind == node.EventMessage {
			b.m.HandleMessage(ev.Message)
		}
	})
	return b
}

// eventually waits for the simulated nodes, which run on their own
// goroutines, to catch up.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRoster(t *testing.T) {
	b := newBench(t)
	tr, err := b.m.Create(train.Entry{Name: "Big Boy", Address: 4014, Functions: map[int]string{0: "Headlight", 2: " Whistle "}})
	if err != nil {
		t.Fatal(err)
	}
	if tr.ID != "big-boy" || !tr.Long || !tr.Roster || tr.Functions[2] != "Whistle" || tr.Direction != train.Forward {
		t.Errorf("created = %+v", tr)
	}
	// Locomotives may share an address.
	again, err := b.m.Create(train.Entry{Name: "Big Boy", Address: 4014})
	if err != nil || again.ID != "big-boy-2" {
		t.Errorf("same name and address = %+v, %v", again, err)
	}
	if _, err := b.m.Create(train.Entry{Name: "Bad", Address: 0}); err == nil {
		t.Error("address 0 should be invalid")
	}
	short, err := b.m.Create(train.Entry{Address: 3})
	if err != nil || short.ID != "3-short" || short.Long || short.Name != "3 (short)" {
		t.Errorf("short = %+v, %v", short, err)
	}
	long3, err := b.m.Create(train.Entry{Name: "3", Address: 3, Long: true})
	if err != nil || long3.ID != "locomotive-3" || !long3.Long {
		t.Errorf("long 3 = %+v, %v", long3, err)
	}
	if got, ok := b.m.Get("4014"); !ok || got.ID != "big-boy" {
		t.Errorf("Get by address = %+v %v", got, ok)
	}
	if got, ok := b.m.Get("3l"); !ok || got.ID != "locomotive-3" {
		t.Errorf("Get by long address = %+v %v", got, ok)
	}
	if list := b.m.List(); len(list) != 4 || list[0].Name != "3" {
		t.Errorf("List = %+v", list)
	}
	updated, err := b.m.Update(b.ctx, "big-boy", func(e *train.Entry) error {
		e.Name, e.Functions, e.Momentary, e.RoadName = "UP 4014", map[int]string{0: "Headlight"}, []int{2}, " Union Pacific "
		return nil
	})
	if err != nil || updated.Name != "UP 4014" || updated.Address != 4014 || len(updated.Momentary) != 1 || updated.RoadName != "Union Pacific" {
		t.Errorf("Update = %+v, %v", updated, err)
	}
	if name := b.m.Name(4014, true); name != "Big Boy / UP 4014" {
		t.Errorf("Name of a shared address = %q", name)
	}

	// The file holds the roster and loads back.
	data, err := os.ReadFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: UP 4014") || !strings.Contains(string(data), "0: Headlight") {
		t.Errorf("file:\n%s", data)
	}
	f, err := train.OpenFile(b.file)
	if err != nil {
		t.Fatal(err)
	}
	if entries := f.List(); len(entries) != 4 || entries[0].ID != "big-boy" || entries[0].Momentary[0] != 2 {
		t.Errorf("reloaded = %+v", entries)
	}
	if err := b.m.Delete(b.ctx, "locomotive-3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.m.Get("locomotive-3"); ok {
		t.Error("deleted train still there")
	}
	if err := b.m.Delete(b.ctx, "nope"); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("delete unknown = %v", err)
	}
	if err := os.WriteFile(b.file, []byte("trains:\n  - name: x\n    address: 5\n    colr: red\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := train.OpenFile(b.file); err == nil || !strings.Contains(err.Error(), "colr") {
		t.Errorf("unknown field = %v", err)
	}
	if err := os.WriteFile(b.file, []byte("trains:\n  - id: \"7\"\n    address: 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := train.OpenFile(b.file); err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("an ID that is another address = %v", err)
	}
}

func TestOlderRoster(t *testing.T) {
	// Entries from before IDs of their own are known by their address,
	// until the address changes.
	path := filepath.Join(t.TempDir(), "trains.yaml")
	if err := os.WriteFile(path, []byte("trains:\n  - id: \"4014\"\n    name: Big Boy\n    address: 4014\n    long: true\n  - name: Switcher\n    address: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := train.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b := newBench(t)
	m := train.New(train.Options{Bus: b.local, File: f, Logger: slog.New(slog.DiscardHandler)})
	if got, ok := m.Get("3"); !ok || got.Name != "Switcher" || got.ID != "3" {
		t.Errorf("Get(3) = %+v %v", got, ok)
	}
	moved, err := m.Update(b.ctx, "4014", func(e *train.Entry) error { e.Address = 4015; return nil })
	if err != nil || moved.ID != "big-boy" || moved.Address != 4015 {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
	if _, ok := m.Get("4014"); ok {
		t.Error("the old address still has a train")
	}
	reloaded, err := train.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if entries := reloaded.List(); len(entries) != 2 || entries[0].ID != "big-boy" || entries[1].ID != "3" {
		t.Errorf("file after the move = %+v", entries)
	}
}

func TestDecoders(t *testing.T) {
	b := newBench(t)
	cvDir := filepath.Join(t.TempDir(), train.CVDirName)
	store := train.OpenCVStore(cvDir, nil)
	m := train.New(train.Options{Bus: b.local, Logger: slog.New(slog.DiscardHandler), CVs: store})
	esuID := uint32(16777468)
	p42, err := m.Create(train.Entry{Name: "Kato P42", Address: 150, Functions: map[int]string{1: "Bell", 2: "Horn"}, Momentary: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	// The first decoder is the fitted one.
	p42, esu, err := m.AddDecoder(b.ctx, p42.ID, train.Decoder{Name: "ESU", File: "ESU_LokPilot5.xml", Family: "ESU LokPilot 5", Model: "LokPilot 5 nano DCC",
		Identity: &train.Identity{Manufacturer: 151, Version: 255, ProductID: &esuID}}, false)
	if err != nil || esu.ID != "esu" || p42.Installed != "esu" {
		t.Fatalf("AddDecoder = %+v %+v, %v", p42, esu, err)
	}
	// Another one, not fitted, with its own address and labels.
	p42, dt, err := m.AddDecoder(b.ctx, p42.ID, train.Decoder{File: "Digitrax_01x3.xml", Family: "Series 3", Model: "DN163K1A",
		Identity: &train.Identity{Manufacturer: 129, Version: 50}, Address: 42, Functions: map[int]string{0: "Light"}}, false)
	if err != nil || dt.ID != "dn163k1a" || p42.Installed != "esu" || p42.Address != 150 {
		t.Fatalf("second decoder = %+v %+v, %v", p42, dt, err)
	}
	if _, err := m.Create(train.Entry{Name: "Thief", Address: 9, Decoders: []train.Decoder{{ID: "esu"}}}); err == nil {
		t.Error("a decoder belongs to one locomotive")
	}

	// The Digitrax decoder on the programming track, while the ESU one
	// is fitted: found by its own address and identity.
	matches := m.FindDecoder(train.Identity{Manufacturer: 129, Version: 51}, 42, false)
	if len(matches) != 1 || matches[0].Decoder != dt.ID || matches[0].Installed || !matches[0].Identity {
		t.Errorf("FindDecoder(Digitrax) = %+v", matches)
	}
	matches = m.FindDecoder(train.Identity{Manufacturer: 151, Version: 255, ProductID: &esuID}, 150, true)
	if len(matches) != 1 || matches[0].Decoder != esu.ID || !matches[0].Installed {
		t.Errorf("FindDecoder(ESU) = %+v", matches)
	}
	// Moved to another address since: found by identity alone.
	if matches = m.FindDecoder(train.Identity{Manufacturer: 151, ProductID: &esuID}, 3, false); len(matches) != 1 || matches[0].Decoder != esu.ID {
		t.Errorf("FindDecoder(ESU at 3) = %+v", matches)
	}

	// Swapping decoders swaps the address and labels.
	p42, err = m.Install(b.ctx, p42.ID, dt.ID)
	if err != nil || p42.Installed != dt.ID || p42.Address != 42 || p42.Long || p42.Functions[0] != "Light" || len(p42.Momentary) != 0 {
		t.Fatalf("Install(Digitrax) = %+v, %v", p42, err)
	}
	if _, ok := m.Get("150"); ok {
		t.Error("nothing should drive 150 now")
	}
	p42, err = m.Install(b.ctx, p42.ID, esu.ID)
	if err != nil || p42.Address != 150 || p42.Functions[2] != "Horn" || p42.Momentary[0] != 2 {
		t.Fatalf("Install(ESU) = %+v, %v", p42, err)
	}
	if d, _ := p42.Decoder(dt.ID); d.Address != 42 || d.Functions[0] != "Light" {
		t.Errorf("the Digitrax decoder kept %+v", d)
	}

	// CV values are kept per decoder.
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"1": 3, "0.255.261": 0xBC}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"29": 34}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCVs(p42.ID, "nope", map[string]int{"1": 3}, false); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("SetCVs on another decoder = %v", err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cvDir, "esu.yaml"))
	if err != nil || !strings.Contains(string(data), "0.255.261: 188") || !strings.Contains(string(data), `"29": 34`) {
		t.Errorf("CV file = %s, %v", data, err)
	}
	reopened := train.OpenCVStore(cvDir, nil)
	if got, err := reopened.Get(esu.ID); err != nil || len(got.CVs) != 3 || got.CVs["1"] != 3 || got.Updated.IsZero() {
		t.Errorf("reopened = %+v, %v", got, err)
	}
	// Changes not written yet are kept until the decoder holds them.
	if err := m.SetChanges(p42.ID, esu.ID, map[string]train.CVChange{"29": {Value: 0xFF, Bits: 0x20}, "3": {Value: 9, Bits: 0xFF}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); got.Changes["29"] != (train.CVChange{Value: 0x20, Bits: 0x20}) || len(got.Changes) != 2 {
		t.Errorf("changes = %+v", got.Changes)
	}
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"29": 2}, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); len(got.Changes) != 2 {
		t.Errorf("a read without the change dropped it: %+v", got.Changes)
	}
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"29": 34}, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); len(got.Changes) != 1 || got.Changes["3"].Value != 9 {
		t.Errorf("after writing CV 29, changes = %+v", got.Changes)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(cvDir, "esu.yaml")); !strings.Contains(string(data), `"3": {value: 9, bits: 255}`) {
		t.Errorf("CV file = %s", data)
	}
	if got, _ := train.OpenCVStore(cvDir, nil).Get(esu.ID); got.Changes["3"] != (train.CVChange{Value: 9, Bits: 0xFF}) {
		t.Errorf("reopened changes = %+v", got.Changes)
	}
	var bad *train.InvalidError
	if err := m.SetChanges(p42.ID, esu.ID, map[string]train.CVChange{"3": {Value: 9}}); !errors.As(err, &bad) {
		t.Errorf("a change of no bits = %v", err)
	}
	if err := m.SetChanges(p42.ID, esu.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); got.Changes != nil {
		t.Errorf("cleared changes = %+v", got.Changes)
	}

	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"8": 151}, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); len(got.CVs) != 1 {
		t.Errorf("replaced = %+v", got)
	}

	if _, err := m.RemoveDecoder(b.ctx, p42.ID, esu.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cvDir, "esu.yaml")); !os.IsNotExist(err) {
		t.Errorf("the removed decoder's values are still there: %v", err)
	}
	got, _ := m.Get(p42.ID)
	if got.Installed != "" || got.Address != 150 || len(got.Decoders) != 1 {
		t.Errorf("after removing the fitted decoder = %+v", got)
	}
}

func TestSharedAddress(t *testing.T) {
	b := newBench(t)
	a, err := b.m.Create(train.Entry{Name: "Challenger", Address: 1234})
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.m.Create(train.Entry{Name: "Cab forward", Address: 1234})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.SetSpeed(b.ctx, a.ID, 12, train.Forward); err != nil {
		t.Fatal(err)
	}
	// One train on the bus, so both entries see its state.
	if got, _ := b.m.Get(c.ID); got.Speed != 12 || !got.Controlled {
		t.Errorf("the other entry = %+v", got)
	}
	b.mu.Lock()
	seen := map[string]bool{}
	for _, ch := range b.changes {
		if ch.Speed == 12 {
			seen[ch.ID] = true
		}
	}
	b.mu.Unlock()
	if !seen[a.ID] || !seen[c.ID] {
		t.Errorf("changes published for %v", seen)
	}
	// The address alone is not a roster entry to delete.
	if err := b.m.Delete(b.ctx, "1234"); err == nil {
		t.Error("deleting by address should name the roster entries instead")
	}
	// Deleting one leaves the train to the other.
	if err := b.m.Delete(b.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.m.Get(c.ID); !got.Controlled {
		t.Errorf("the train was released: %+v", got)
	}
}

func TestDrive(t *testing.T) {
	b := newBench(t)
	if _, err := b.m.Create(train.Entry{Name: "Challenger", Address: 1234}); err != nil {
		t.Fatal(err)
	}
	// By address, which finds the roster entry.
	tr, err := b.m.Acquire(b.ctx, "1234")
	if err != nil {
		t.Fatal(err)
	}
	want := traction.DCCNodeID(1234, true)
	if tr.Node != want || !tr.Online || !tr.Controlled || tr.Controller != localID || !tr.Known || tr.Speed != 0 {
		t.Fatalf("acquired = %+v", tr)
	}
	simTrain, ok := b.station.Train(1234, true)
	if !ok || simTrain.Controller() != localID {
		t.Fatal("the station does not show Interlock as controller")
	}

	tr, err = b.m.SetSpeed(b.ctx, "1234", 30, train.Reverse)
	if err != nil || tr.Speed != 30 || tr.Direction != train.Reverse {
		t.Fatalf("SetSpeed = %+v, %v", tr, err)
	}
	eventually(t, "decoder speed", func() bool {
		d := b.station.DecoderState(1234, true)
		return d.Speed.Value == 30 && d.Speed.Reverse
	})
	tr, err = b.m.SetFunction(b.ctx, "1234", 2, true)
	if err != nil || len(tr.On) != 1 || tr.On[0] != 2 {
		t.Fatalf("SetFunction = %+v, %v", tr, err)
	}
	if _, err := b.m.SetFunction(b.ctx, "1234", 99, true); err == nil {
		t.Error("F99 should be invalid")
	}
	tr, err = b.m.EmergencyStop(b.ctx, "1234")
	if err != nil || !tr.EStop || tr.Speed != 0 {
		t.Fatalf("EmergencyStop = %+v, %v", tr, err)
	}
	tr, err = b.m.SetSpeed(b.ctx, "1234", 10, "")
	if err != nil || tr.EStop || tr.Speed != 10 || tr.Direction != train.Reverse {
		t.Fatalf("SetSpeed after estop = %+v, %v", tr, err)
	}

	// Refresh reads back what another throttle did.
	b.station.ChangeDecoder(1234, true, func(d *sim.Decoder) { d.Functions[4] = true })
	tr, err = b.m.Refresh(b.ctx, "1234")
	if err != nil || len(tr.On) != 2 || tr.On[1] != 4 || !tr.Controlled {
		t.Fatalf("Refresh = %+v, %v", tr, err)
	}

	tr, err = b.m.Release(b.ctx, "1234")
	if err != nil || tr.Controlled {
		t.Fatalf("Release = %+v, %v", tr, err)
	}
	eventually(t, "controller released", func() bool { return simTrain.Controller() == 0 })

	// A command by address drives a train outside the roster, creating
	// its node on the command station.
	got, err := b.m.Command(b.ctx, train.Command{Address: 77, Kind: train.CommandSpeed, Speed: 20, Direction: train.Forward})
	if err != nil || got.ID != "77" || got.Roster || got.Speed != 20 || !got.Controlled {
		t.Fatalf("Command = %+v, %v", got, err)
	}
	if _, ok := b.station.Train(77, false); !ok {
		t.Error("no train node for 77")
	}
	if _, err := b.m.Command(b.ctx, train.Command{Address: 77, Kind: "fly"}); err == nil {
		t.Error("unknown command should fail")
	}
	got, err = b.m.Command(b.ctx, train.Command{Address: 77, Kind: train.CommandStop})
	if err != nil || got.Speed != 0 {
		t.Fatalf("stop = %+v, %v", got, err)
	}
	if name := b.m.Name(1234, true); name != "Challenger" {
		t.Errorf("Name = %q", name)
	}
	if name := b.m.Name(77, false); name != "locomotive 77 (short)" {
		t.Errorf("Name = %q", name)
	}

	// A search lists what exists.
	found, err := b.m.Search(b.ctx, "7")
	if err != nil || len(found) != 1 || found[0].Address != 77 || found[0].Roster {
		t.Fatalf("Search = %+v, %v", found, err)
	}
	if _, err := b.m.Search(b.ctx, "x"); err == nil {
		t.Error("bad query should fail")
	}

	// A train that no command station answers for.
	b.station.SetProgrammingTrack(0)
	fast, cancel := context.WithTimeout(b.ctx, 2*time.Second)
	defer cancel()
	_, err = b.m.SetSpeed(fast, "20000", 1, "")
	if !errors.Is(err, train.ErrNotFound) {
		t.Errorf("address out of range = %v", err)
	}
}

// The layout's emergency stop holds every train at the speed it keeps,
// as the Train Control Standard has it: a speed set meanwhile is what the
// train goes back to, and Resuming asks the trains what that is, since
// another throttle may have set one.
func TestEmergencyStopHold(t *testing.T) {
	b := newBench(t)
	if _, err := b.m.SetSpeed(b.ctx, "1234", 40, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.SetSpeed(b.ctx, "3", 0, ""); err != nil {
		t.Fatal(err)
	}
	if l, err := b.m.SetEmergencyStop(b.ctx, true); err != nil || !l.EmergencyStop {
		t.Fatalf("SetEmergencyStop = %+v, %v", l, err)
	}
	eventually(t, "the station's hold", func() bool { return b.station.DecoderState(1234, true).Held })
	if got, _ := b.m.Get("1234"); got.EStop || got.Speed != 40 {
		t.Errorf("held train = %+v", got)
	}

	// A speed set during the hold is what the train goes back to; the
	// hold stays.
	tr, err := b.m.SetSpeed(b.ctx, "1234", 30, "")
	if err != nil || tr.Speed != 30 || tr.EStop || !b.m.Layout().EmergencyStop {
		t.Fatalf("speed during the hold = %+v, %v; layout %+v", tr, err, b.m.Layout())
	}
	eventually(t, "the new speed", func() bool {
		d := b.station.DecoderState(1234, true)
		return d.Speed.Value == 30 && d.Held
	})

	// Another throttle sets locomotive 3 going, and a train node Interlock
	// never drove.
	b.station.ChangeDecoder(3, false, func(d *sim.Decoder) { d.Speed = traction.Speed{Value: 12, Reverse: true} })
	if _, err := b.local.Producers(b.ctx, traction.AddressSearch(5, false, true), 300*time.Millisecond, true); err != nil {
		t.Fatal(err)
	}
	b.station.ChangeDecoder(5, false, func(d *sim.Decoder) { d.Speed = traction.Speed{Value: 20} })
	b.m.HandleNode(registry.Info{ID: traction.DCCNodeID(5, false), Online: true})
	moving, err := b.m.Resuming(b.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tr := range moving {
		got[tr.ID] = fmt.Sprintf("%v %s", tr.Speed, tr.Direction)
	}
	want := map[string]string{"1234": "30 forward", "3": "12 reverse", "5": "20 forward"}
	if !maps.Equal(got, want) {
		t.Errorf("Resuming = %v, want %v", got, want)
	}
	if tr, _ := b.m.Get("3"); tr.Speed != 12 || tr.Direction != train.Reverse || tr.EStop {
		t.Errorf("locomotive 3 after Resuming = %+v", tr)
	}

	// Set to 0 now, it stays put after the hold; the others go back to
	// their speeds.
	if _, err := b.m.SetSpeed(b.ctx, "3", 0, ""); err != nil {
		t.Fatal(err)
	}
	if moving, err = b.m.Resuming(b.ctx); err != nil || len(moving) != 2 {
		t.Errorf("Resuming after a stop = %+v, %v", moving, err)
	}
	if l, err := b.m.SetEmergencyStop(b.ctx, false); err != nil || l.EmergencyStop {
		t.Fatalf("clear = %+v, %v", l, err)
	}
	eventually(t, "the end of the hold", func() bool { return !b.station.DecoderState(1234, true).Held })
	if d := b.station.DecoderState(3, false); d.Speed.Value != 0 {
		t.Errorf("locomotive 3 resumed at %v", d.Speed)
	}

	// With nothing set to move, Resuming says so.
	for _, id := range []string{"1234", "5"} {
		if _, err := b.m.SetSpeed(b.ctx, id, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	if moving, err = b.m.Resuming(b.ctx); err != nil || len(moving) != 0 {
		t.Errorf("Resuming with every train stopped = %+v, %v", moving, err)
	}
}

// After a restart Interlock asks the command stations whether they hold
// their trains, and a train read back held with a speed is not stopped.
func TestEmergencyStopLearnedAtStart(t *testing.T) {
	b := newBench(t)
	if _, err := b.m.SetSpeed(b.ctx, "1234", 40, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.SetEmergencyStop(b.ctx, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the station's hold", func() bool { return b.station.DecoderState(1234, true).Held })
	f, err := train.OpenFile(filepath.Join(t.TempDir(), "again.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	again := train.New(train.Options{Bus: b.local, File: f, SearchWindow: 150 * time.Millisecond, Functions: 1})
	b.local.Observe(func(ev node.Event) {
		if ev.Kind == node.EventMessage {
			again.HandleMessage(ev.Message)
		}
	})
	again.HandleNode(registry.Info{ID: stationID, Online: true})
	eventually(t, "the hold learned", func() bool { return again.Layout().EmergencyStop })
	tr, err := again.Acquire(b.ctx, "1234")
	if err != nil || tr.Speed != 40 || tr.EStop {
		t.Errorf("read back while held = %+v, %v", tr, err)
	}
	// The station leaving takes its hold with it.
	again.HandleNode(registry.Info{ID: stationID})
	if again.Layout().EmergencyStop {
		t.Error("hold kept after the station left")
	}
}

func TestLayoutAndNotices(t *testing.T) {
	b := newBench(t)
	l, err := b.m.SetPower(b.ctx, false)
	if err != nil || !l.PowerOff {
		t.Fatalf("SetPower = %+v, %v", l, err)
	}
	deadline := time.Now().Add(time.Second)
	for b.station.Power() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if b.station.Power() {
		t.Error("station still powered")
	}
	if l, err = b.m.SetPower(b.ctx, true); err != nil || l.PowerOff {
		t.Fatalf("SetPower on = %+v, %v", l, err)
	}

	tr, err := b.m.SetSpeed(b.ctx, "1234", 40, "")
	if err != nil || tr.Speed != 40 {
		t.Fatal(err)
	}
	if l, err = b.m.SetEmergencyStop(b.ctx, true); err != nil || !l.EmergencyStop {
		t.Fatalf("SetEmergencyStop = %+v, %v", l, err)
	}
	if got, _ := b.m.Get("1234"); got.EStop || got.Speed != 40 {
		t.Errorf("train held by the emergency stop = %+v", got)
	}
	if l, err = b.m.SetEmergencyStop(b.ctx, false); err != nil || l.EmergencyStop {
		t.Fatalf("clear = %+v, %v", l, err)
	}

	// Someone else's Emergency Off shows up in the layout state.
	station := b.station
	if err := station.Send(b.ctx, openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventEmergencyOff.AsSlice()}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for !b.m.Layout().PowerOff && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !b.m.Layout().PowerOff {
		t.Error("layout did not see Emergency Off from the bus")
	}

	// A train telling us another throttle took it.
	simTrain, _ := station.Train(1234, true)
	notice := append([]byte{traction.CmdController, traction.ControllerChanged, 0}, openlcb.NodeID(0x050101010101).AsSlice()...)
	reply, err := simTrain.TractionCommand(b.ctx, localID, notice, true)
	if err != nil || len(reply) != 3 || reply[2] != 0 {
		t.Fatalf("notice reply = %X, %v", reply, err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		got, _ := b.m.Get("1234")
		if !got.Controlled && got.Controller == 0x050101010101 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("train still controlled: %+v", got)
		}
		time.Sleep(time.Millisecond)
	}
	// The next command takes it back.
	if tr, err = b.m.SetSpeed(b.ctx, "1234", 5, ""); err != nil || !tr.Controlled {
		t.Fatalf("SetSpeed after losing control = %+v, %v", tr, err)
	}
	b.mu.Lock()
	n := len(b.changes)
	b.mu.Unlock()
	if n == 0 {
		t.Error("no train changes were published")
	}
}

func TestTrackPower(t *testing.T) {
	b := newBench(t)
	known := func(off bool) func() bool {
		return func() bool { l := b.m.Layout(); return l.PowerKnown && l.PowerOff == off }
	}
	// A command station arriving is asked, and says its power is off, as
	// it is at boot.
	b.m.HandleNode(registry.Info{ID: stationID, Online: true})
	eventually(t, "power known off", known(true))
	if _, err := b.m.SetPower(b.ctx, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "power known on", known(false))
	eventually(t, "station powered", b.station.Power)

	// A second station that disagrees leaves the power unknown until it
	// goes.
	other := openlcb.NodeID(0x0A0000000088)
	b.m.HandleNode(registry.Info{ID: other, Online: true})
	b.m.HandleMessage(&openlcb.Message{MTI: openlcb.MTIConsumerIdentifiedValid,
		Source: openlcb.NodeHandle{ID: other}, Payload: openlcb.EventEmergencyOff.AsSlice()})
	if l := b.m.Layout(); l.PowerKnown {
		t.Fatalf("stations disagree, layout = %+v", l)
	}
	b.m.HandleNode(registry.Info{ID: other})
	if !known(false)() {
		t.Fatalf("after the other station left, layout = %+v", b.m.Layout())
	}
	// Switching power brings them all into line.
	b.m.HandleMessage(&openlcb.Message{MTI: openlcb.MTIConsumerIdentifiedInvalid,
		Source: openlcb.NodeHandle{ID: other}, Payload: openlcb.EventClearEmergencyOff.AsSlice()})
	if l := b.m.Layout(); l.PowerKnown {
		t.Fatalf("stations disagree again, layout = %+v", l)
	}
	b.m.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventEmergencyOff.AsSlice()})
	if !known(true)() {
		t.Fatalf("after Emergency Off, layout = %+v", b.m.Layout())
	}
	// With no station left to say, the power is unknown.
	b.m.HandleNode(registry.Info{ID: stationID})
	b.m.HandleNode(registry.Info{ID: other})
	if l := b.m.Layout(); l.PowerKnown {
		t.Fatalf("no stations, layout = %+v", l)
	}
	// Interlock's own node is not asked about.
	b.m.HandleNode(registry.Info{ID: localID, Online: true, Local: true})
}

func TestImport(t *testing.T) {
	b := newBench(t)
	cvDir := filepath.Join(t.TempDir(), train.CVDirName)
	store := train.OpenCVStore(cvDir, nil)
	m := train.New(train.Options{Bus: b.local, Logger: slog.New(slog.DiscardHandler), CVs: store})
	if _, err := m.Create(train.Entry{Name: "Old timer", Address: 9}); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2025, 7, d, 0, 0, 0, 0, time.UTC) }
	items := []train.Import{
		{Key: "a", Entry: train.Entry{Name: "Kato P42 Digitrax", Address: 150, RoadName: "Amtrak", RoadNumber: "150"},
			Decoder: &train.Decoder{File: "Digitrax_01x3.xml", Family: "Series 3", Model: "Series 3", Identity: &train.Identity{Manufacturer: 129}}, CVs: map[string]int{"1": 3, "8": 129}, Updated: day(1)},
		{Key: "b", Entry: train.Entry{Name: "Kato P42 ESU", Address: 150, RoadName: "Amtrak", RoadNumber: "150", Functions: map[int]string{2: "Horn"}, Momentary: []int{2}},
			Decoder: &train.Decoder{File: "ESU_LokPilot5.xml", Family: "ESU LokPilot 5", Model: "LokPilot 5 nano DCC"}, CVs: map[string]int{"1": 3, "8": 151}, Updated: day(2)},
		{Key: "c", Entry: train.Entry{Name: "Test bench", Address: 2350},
			Decoder: &train.Decoder{File: "ESU_LokPilot5.xml", Model: "LokPilot 5 nano DCC"}, CVs: map[string]int{"8": 151}},
		{Key: "d", Entry: train.Entry{Name: "old timer", Address: 9}},
	}
	plan := m.PlanImport(items)
	want := []train.ImportStep{
		{Key: "a", Action: train.ImportDecoder, Into: "b", Name: "Digitrax", DecoderName: "Series 3"},
		{Key: "b", Action: train.ImportNew, Name: "Kato P42", DecoderName: "ESU"},
		{Key: "c", Action: train.ImportNew, Name: "Test bench", DecoderName: "LokPilot 5 nano DCC"},
		{Key: "d", Action: train.ImportSkip, Into: "old-timer", Name: "old timer"},
	}
	for i, w := range want {
		got := plan[i]
		got.Note = ""
		if got != w {
			t.Errorf("step %d = %+v, want %+v", i, plan[i], w)
		}
	}

	if _, err := m.ApplyImport(b.ctx, items, []train.ImportStep{{Key: "a", Action: train.ImportDecoder, Into: "nowhere"}}); err == nil {
		t.Error("a decoder for no locomotive should be refused")
	}
	res, err := m.ApplyImport(b.ctx, items, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trains) != 2 || res.Decoders != 3 || res.CVs != 5 || res.Skipped != 1 {
		t.Errorf("result = %+v", res)
	}
	p42, ok := m.Get("kato-p42")
	if !ok || len(p42.Decoders) != 2 || p42.Installed != "esu" || p42.Address != 150 || p42.Functions[2] != "Horn" || p42.RoadName != "Amtrak" {
		t.Fatalf("P42 = %+v", p42)
	}
	if d, _ := p42.Decoder("digitrax"); d.Address != 150 || !d.Long || d.Identity == nil {
		t.Errorf("Digitrax decoder = %+v", d)
	}
	// Imported values wait for a read of the decoder.
	if got, _ := m.CVs("kato-p42", "digitrax"); len(got.CVs) != 0 || got.Imported["8"] != 129 || len(got.Imported) != 2 || !got.Updated.Equal(day(1)) {
		t.Errorf("Digitrax CVs = %+v", got)
	}
	if err := m.SetCVs("kato-p42", "digitrax", map[string]int{"8": 129}, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs("kato-p42", "digitrax"); got.CVs["8"] != 129 || len(got.Imported) != 1 || got.Imported["1"] != 3 || got.Updated.Equal(day(1)) {
		t.Errorf("after a read, Digitrax CVs = %+v", got)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cvDir, "digitrax.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "imported:\n  \"1\": 3\n") {
		t.Errorf("file:\n%s", data)
	}
	if err := m.SetCVs("kato-p42", "digitrax", map[string]int{"1": 4}, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs("kato-p42", "digitrax"); got.Imported != nil || got.CVs["1"] != 4 {
		t.Errorf("after replacing, Digitrax CVs = %+v", got)
	}
}

func TestCVSessions(t *testing.T) {
	b := newBench(t)
	store := train.OpenCVStore(filepath.Join(t.TempDir(), train.CVDirName), nil)
	now := time.Date(2026, 10, 17, 19, 0, 0, 0, time.UTC)
	train.SetCVStoreClock(store, func() time.Time { return now })
	m := train.New(train.Options{Bus: b.local, Logger: slog.New(slog.DiscardHandler), CVs: store})
	p42, err := m.Create(train.Entry{Name: "Kato P42", Address: 150})
	if err != nil {
		t.Fatal(err)
	}
	p42, esu, err := m.AddDecoder(b.ctx, p42.ID, train.Decoder{File: "ESU_LokPilot5.xml", Model: "LokPilot 5 nano DCC"}, false)
	if err != nil {
		t.Fatal(err)
	}
	session := func() *train.CVSession {
		t.Helper()
		got, err := m.CVs(p42.ID, esu.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Session
	}

	if s := session(); s != nil {
		t.Fatalf("a session before any work: %+v", s)
	}
	// Reads and writes begin a session and are listed by what came last.
	for _, op := range []struct {
		cv      string
		value   int
		written bool
	}{{"1", 3, false}, {"29", 34, false}, {"3", 4, true}, {"29", 38, true}} {
		if err := m.Programmed(p42.ID, esu.ID, op.cv, op.value, op.written); err != nil {
			t.Fatal(err)
		}
	}
	s := session()
	if s == nil || !slices.Equal(s.Read, []string{"1"}) || !slices.Equal(s.Written, []string{"29", "3"}) || !s.Started.Equal(now) {
		t.Fatalf("session = %+v", s)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); got.CVs["29"] != 38 {
		t.Errorf("a value in the session is not saved: %+v", got.CVs)
	}
	if err := m.Programmed(p42.ID, "nope", "1", 3, false); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("Programmed on another decoder = %v", err)
	}

	// Changing settings keeps it going; two idle hours end it.
	now = now.Add(train.SessionIdle - time.Minute)
	if err := m.SetChanges(p42.ID, esu.ID, map[string]train.CVChange{"3": {Value: 5, Bits: 0xFF}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(train.SessionIdle - time.Minute)
	if s := session(); s == nil || !s.Last.Equal(now.Add(-train.SessionIdle+time.Minute)) {
		t.Fatalf("a change did not keep the session going: %+v", s)
	}
	now = now.Add(2 * time.Minute)
	if s := session(); s != nil {
		t.Fatalf("an idle session went on: %+v", s)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); got.CVs["1"] != 3 {
		t.Errorf("the values went with the session: %+v", got.CVs)
	}

	// Ended by hand or for every decoder at once.
	if err := m.Programmed(p42.ID, esu.ID, "1", 3, false); err != nil {
		t.Fatal(err)
	}
	if s := session(); s == nil || !s.Started.Equal(now) || len(s.Written) != 0 {
		t.Fatalf("a new session = %+v", s)
	}
	if err := m.EndSession(p42.ID, esu.ID); err != nil || session() != nil {
		t.Errorf("EndSession = %v, %+v", err, session())
	}
	if err := m.EndSession(p42.ID, "nope"); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("EndSession on another decoder = %v", err)
	}
	if err := m.Programmed(p42.ID, esu.ID, "1", 3, false); err != nil {
		t.Fatal(err)
	}
	m.EndSessions()
	if s := session(); s != nil {
		t.Errorf("EndSessions left %+v", s)
	}

	// Work on the programming track begins with a check of the decoder.
	if m.SessionChecked(esu.ID) {
		t.Error("checked without a check")
	}
	if err := m.StartSession(p42.ID, esu.ID, train.CheckMatched); err != nil || !m.SessionChecked(esu.ID) || session().Check != train.CheckMatched {
		t.Fatalf("StartSession = %v, %+v", err, session())
	}
	if err := m.StartSession(p42.ID, esu.ID, train.CheckAccepted); err != nil || session().Check != train.CheckMatched {
		t.Errorf("going on without a match undid one: %v, %+v", err, session())
	}
	if err := m.StartSession(p42.ID, esu.ID, "maybe"); err == nil {
		t.Error("StartSession took an unknown check")
	}
	if err := m.StartSession(p42.ID, "nope", train.CheckMatched); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("StartSession on another decoder = %v", err)
	}
	// Checking another decoder there ends this one's session; one begun
	// by writes on the main goes on.
	_, sound, err := m.AddDecoder(b.ctx, p42.ID, train.Decoder{File: "ESU_LokSound5.xml", Model: "LokSound 5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Programmed(p42.ID, sound.ID, "3", 4, true); err != nil || m.SessionChecked(sound.ID) {
		t.Fatalf("a write on the main = %v, checked %v", err, m.SessionChecked(sound.ID))
	}
	if err := m.StartSession(p42.ID, esu.ID, train.CheckMatched); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, sound.ID); got.Session == nil {
		t.Error("a check ended a session on the main")
	}
	if err := m.StartSession(p42.ID, sound.ID, train.CheckAccepted); err != nil || session() != nil {
		t.Errorf("checking another decoder left %+v", session())
	}
}

func TestDecoderProfiles(t *testing.T) {
	b := newBench(t)
	dir := filepath.Join(t.TempDir(), train.CVDirName)
	store := train.OpenCVStore(dir, nil)
	m := train.New(train.Options{Bus: b.local, Logger: slog.New(slog.DiscardHandler), CVs: store})
	p42, err := m.Create(train.Entry{Name: "Kato P42", Address: 150})
	if err != nil {
		t.Fatal(err)
	}
	p42, esu, err := m.AddDecoder(b.ctx, p42.ID, train.Decoder{File: "ESU_LokPilot5.xml", Model: "LokPilot 5 nano DCC"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var invalid *train.InvalidError
	if _, err := m.KeepProfile(p42.ID, esu.ID, "As delivered", false); !errors.As(err, &invalid) {
		t.Errorf("a profile of nothing = %v", err)
	}

	// A profile keeps the values read and imported, not changes.
	if err := store.Import(esu.ID, map[string]int{"2": 5, "3": 10}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"3": 12, "29": 34}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetChanges(p42.ID, esu.ID, map[string]train.CVChange{"4": {Value: 8, Bits: 0xFF}}); err != nil {
		t.Fatal(err)
	}
	st, err := m.KeepProfile(p42.ID, esu.ID, "  As delivered ", false)
	if err != nil || st.Name != "As delivered" || !maps.Equal(st.CVs, map[string]int{"2": 5, "3": 12, "29": 34}) || st.Kept.IsZero() {
		t.Fatalf("KeepProfile = %+v, %v", st, err)
	}
	for _, name := range []string{"", strings.Repeat("x", 65)} {
		if _, err := m.KeepProfile(p42.ID, esu.ID, name, false); !errors.As(err, &invalid) {
			t.Errorf("KeepProfile(%q) = %v", name, err)
		}
	}
	if _, err := m.KeepProfile(p42.ID, "nope", "Tuned", false); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("KeepProfile on another decoder = %v", err)
	}

	// The same name again replaces it only when asked.
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"3": 14}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.KeepProfile(p42.ID, esu.ID, "As delivered", false); !errors.Is(err, train.ErrProfileExists) {
		t.Errorf("keeping a name twice = %v", err)
	}
	if st, err := m.KeepProfile(p42.ID, esu.ID, "As delivered", true); err != nil || st.CVs["3"] != 14 {
		t.Errorf("replacing a profile = %+v, %v", st, err)
	}
	if _, err := m.KeepProfile(p42.ID, esu.ID, "Tuned", false); err != nil {
		t.Fatal(err)
	}

	// Profiles outlast replaced values and a restart.
	if err := m.SetCVs(p42.ID, esu.ID, map[string]int{"1": 3}, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := train.OpenCVStore(dir, nil)
	got, err := reopened.Get(esu.ID)
	if err != nil || len(got.Profiles) != 2 || got.Profiles[0].Name != "As delivered" || got.Profiles[1].Name != "Tuned" || got.Profiles[0].CVs["29"] != 34 {
		t.Fatalf("profiles after a restart = %+v, %v", got.Profiles, err)
	}

	if err := m.DeleteProfile(p42.ID, esu.ID, "As delivered"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CVs(p42.ID, esu.ID); len(got.Profiles) != 1 || got.Profiles[0].Name != "Tuned" {
		t.Errorf("profiles after a delete = %+v", got.Profiles)
	}
	if err := m.DeleteProfile(p42.ID, esu.ID, "As delivered"); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("deleting a profile twice = %v", err)
	}
}
