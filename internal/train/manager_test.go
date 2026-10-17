package train_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
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
	b.m = train.New(train.Options{Bus: b.local, File: f, Logger: logger, SearchWindow: 150 * time.Millisecond, Functions: 5})
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
	if tr.ID != "4014" || !tr.Long || !tr.Roster || tr.Functions[2] != "Whistle" || tr.Direction != train.Forward {
		t.Errorf("created = %+v", tr)
	}
	if _, err := b.m.Create(train.Entry{Name: "Again", Address: 4014}); !errors.Is(err, train.ErrExists) {
		t.Errorf("duplicate = %v", err)
	}
	if _, err := b.m.Create(train.Entry{Name: "Bad", Address: 0}); err == nil {
		t.Error("address 0 should be invalid")
	}
	short, err := b.m.Create(train.Entry{Address: 3})
	if err != nil || short.ID != "3" || short.Long || short.Name != "3 (short)" {
		t.Errorf("short = %+v, %v", short, err)
	}
	long3, err := b.m.Create(train.Entry{Address: 3, Long: true})
	if err != nil || long3.ID != "3l" || !long3.Long {
		t.Errorf("long 3 = %+v, %v", long3, err)
	}
	if got, ok := b.m.Get("4014"); !ok || got.Name != "Big Boy" {
		t.Errorf("Get = %+v %v", got, ok)
	}
	if list := b.m.List(); len(list) != 3 || list[0].Name != "3 (long)" {
		t.Errorf("List = %+v", list)
	}
	updated, err := b.m.Update("4014", train.Entry{Name: "UP 4014", Functions: map[int]string{0: "Headlight"}, Momentary: []int{2}})
	if err != nil || updated.Name != "UP 4014" || updated.Address != 4014 || len(updated.Momentary) != 1 {
		t.Errorf("Update = %+v, %v", updated, err)
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
	if entries := f.List(); len(entries) != 3 || entries[0].ID != "4014" || entries[0].Momentary[0] != 2 {
		t.Errorf("reloaded = %+v", entries)
	}
	if err := b.m.Delete(b.ctx, "3l"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.m.Get("3l"); ok {
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
}

func TestDrive(t *testing.T) {
	b := newBench(t)
	if _, err := b.m.Create(train.Entry{Name: "Challenger", Address: 1234}); err != nil {
		t.Fatal(err)
	}
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
		d := b.station.Decoder(1234, true)
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
	simTrain.Decoder.Functions[4] = true
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
	if got, _ := b.m.Get("1234"); !got.EStop || got.Speed != 0 {
		t.Errorf("train after emergency stop = %+v", got)
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
