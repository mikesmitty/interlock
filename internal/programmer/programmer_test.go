package programmer_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

const (
	localID   = openlcb.NodeID(0x0A0000000001)
	stationID = openlcb.NodeID(0x0A0000000077)
)

// bench joins a programmer to a simulated command station with a
// long-address decoder 1234 on its programming track.
func bench(t *testing.T, opts sim.StationOptions) (context.Context, *programmer.Programmer, *sim.Station) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	var local *node.Node
	var station *sim.Station
	local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); station.HandleFrame(f) }})
	opts.Decoders = []*sim.Decoder{sim.NewDecoder(1234, true)}
	opts.ProgrammingTrack = 1234
	opts.ServiceModeDelay = 10 * time.Millisecond
	var err error
	station, err = sim.NewStation(node.Config{ID: stationID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		SNIP: openlcb.SNIP{UserName: "Bench"},
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); local.HandleFrame(f) }}, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	go func() { _ = local.Run(ctx) }()
	go func() { _ = station.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for local.State() != node.StateInitialized || station.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mem := memconfig.NewClient(local, memconfig.WithLogger(logger))
	trains := train.New(train.Options{Bus: local, Logger: logger, SearchWindow: 150 * time.Millisecond, Functions: 2})
	p := programmer.New(programmer.Options{Bus: local, Memory: mem, Trains: trains, Logger: logger, SearchWindow: 150 * time.Millisecond, History: 3})
	return ctx, p, station
}

func TestProgrammer(t *testing.T) {
	ctx, p, station := bench(t, sim.StationOptions{})
	var finished []programmer.Op
	p.Subscribe(func(op programmer.Op) { finished = append(finished, op) })

	op, err := p.Read(ctx, 8)
	if err != nil || !op.OK || op.Value != 13 || op.Kind != "read" {
		t.Fatalf("Read CV 8 = %+v, %v", op, err)
	}
	if tr := p.Track(); tr.Node != stationID || !tr.Online || tr.Name != "Bench" && tr.Name != stationID.String() {
		t.Errorf("Track = %+v", tr)
	}
	if op, err = p.Write(ctx, 3, 9); err != nil || !op.OK {
		t.Fatalf("Write CV 3 = %+v, %v", op, err)
	}
	if op, err = p.Read(ctx, 3); err != nil || op.Value != 9 {
		t.Fatalf("Read CV 3 = %+v, %v", op, err)
	}
	if _, err := p.Read(ctx, 0); err == nil {
		t.Error("CV 0 should be invalid")
	}
	if _, err := p.Write(ctx, 1, 256); err == nil {
		t.Error("value 256 should be invalid")
	}

	// POM through the train node.
	if op, err = p.WritePOM(ctx, "1234", 5, 200); err != nil || !op.OK || op.Train != "1234" {
		t.Fatalf("WritePOM = %+v, %v", op, err)
	}
	if got := station.DecoderState(1234, true).CVs[5]; got != 200 {
		t.Errorf("CV 5 after POM = %d", got)
	}
	if op, err = p.ReadPOM(ctx, "1234", 5); err != nil || !op.OK || op.Value != 200 || op.Kind != "pom_read" || op.Train != "1234" {
		t.Fatalf("ReadPOM = %+v, %v", op, err)
	}

	// An empty programming track, in plain words.
	station.SetProgrammingTrack(0)
	op, err = p.Read(ctx, 1)
	if err == nil || op.OK || op.Error != "No decoder answered. Is a locomotive on the programming track?" {
		t.Fatalf("read with no locomotive = %+v, %v", op, err)
	}
	if h := p.History(); len(h) != 3 || h[0].CV != 1 || h[0].OK {
		t.Errorf("History = %+v", h)
	}
	if len(finished) != 6 {
		t.Errorf("subscriber saw %d operations", len(finished))
	}

	// Other errors in plain words.
	if msg := programmer.Explain(programmer.ErrNoTrack); msg == "" {
		t.Error("Explain(ErrNoTrack) is empty")
	}
	if msg := programmer.Explain(&openlcb.Error{Code: programmer.CodeShort}); msg != "Short circuit on the programming track." {
		t.Errorf("Explain(short) = %q", msg)
	}
	if msg := programmer.Explain(errors.New("plain")); msg != "plain" {
		t.Errorf("Explain(plain) = %q", msg)
	}
}

// A command station without RailCom cannot read on the main, and says so
// in words that point at RailCom rather than the programming track.
func TestReadOnMainWithoutRailCom(t *testing.T) {
	ctx, p, _ := bench(t, sim.StationOptions{NoRailCom: true})
	op, err := p.ReadPOM(ctx, "1234", 1)
	if err == nil || op.OK || !strings.Contains(op.Error, "cannot read on the main") || !programmer.CannotReadOnMain(err) {
		t.Fatalf("ReadPOM = %+v, %v", op, err)
	}
	if op, err := p.WritePOM(ctx, "1234", 3, 4); err != nil || !op.OK {
		t.Errorf("WritePOM = %+v, %v", op, err)
	}
}
