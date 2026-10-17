package firmware_test

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/firmware"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

const (
	localID   = openlcb.NodeID(0x0A0000000001)
	outpostID = openlcb.NodeID(0x0A0000000042)
)

// fakeNodes is the registry: it always knows the outpost.
type fakeNodes struct {
	outpost *sim.Outpost
	mu      sync.Mutex
	refresh int
}

func (n *fakeNodes) Get(id openlcb.NodeID) (registry.Info, bool) {
	if id != outpostID {
		return registry.Info{}, false
	}
	snip := n.outpost.SNIPInfo()
	return registry.Info{ID: id, Online: true, SNIP: &snip, ProtocolBits: n.outpost.LocalProtocols()}, true
}

func (n *fakeNodes) RefreshNode(context.Context, openlcb.NodeID) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.refresh++
	return nil
}

type bench struct {
	local   *node.Node
	outpost *sim.Outpost
	nodes   *fakeNodes
	up      *firmware.Upgrader
	mu      sync.Mutex
	seen    []firmware.Job
}

func newBench(t *testing.T, settle time.Duration) *bench {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	var local *node.Node
	var outpost *sim.Outpost
	local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); outpost.HandleFrame(f) }})
	var err error
	outpost, err = sim.NewOutpost(node.Config{ID: outpostID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		SNIP: openlcb.SNIP{UserName: "Yard"},
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); local.HandleFrame(f) }})
	if err != nil {
		t.Fatal(err)
	}
	outpost.SetFirmwareTiming(sim.FirmwareTiming{Restart: 20 * time.Millisecond, Confirm: 150 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	go func() { _ = local.Run(ctx) }()
	go func() { _ = outpost.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for local.State() != node.StateInitialized || outpost.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mem := memconfig.NewClient(local, memconfig.WithLogger(logger))
	b := &bench{local: local, outpost: outpost, nodes: &fakeNodes{outpost: outpost}}
	b.up = firmware.New(firmware.Options{Bus: local, Memory: mem, Nodes: b.nodes, Logger: logger,
		RestartTimeout: 3 * time.Second, Settle: settle})
	b.up.Subscribe(func(j firmware.Job) { b.mu.Lock(); b.seen = append(b.seen, j); b.mu.Unlock() })
	go func() { _ = b.up.Run(ctx) }()
	return b
}

// wait polls until the job is over and returns it.
func (b *bench) wait(t *testing.T) firmware.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, ok := b.up.Job(outpostID)
		if ok && !j.Phase.Active() {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("upgrade did not finish: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (b *bench) phases() []firmware.Phase {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []firmware.Phase
	for _, j := range b.seen {
		if len(out) == 0 || out[len(out)-1] != j.Phase {
			out = append(out, j.Phase)
		}
	}
	return out
}

func TestUpgrade(t *testing.T) {
	b := newBench(t, 400*time.Millisecond)
	before := b.outpost.SNIPInfo().SoftwareVersion
	image := sim.FirmwareImage("9.9.9", false)
	ctx := context.Background()

	j, err := b.up.Start(ctx, outpostID, "outpost-9.9.9.bin", image)
	if err != nil {
		t.Fatal(err)
	}
	if j.Phase != firmware.PhaseChecking || j.Size != len(image) || j.From != before || j.NodeName != "Yard" {
		t.Errorf("started job = %+v", j)
	}
	if _, err := b.up.Start(ctx, outpostID, "again.bin", image); !errors.Is(err, firmware.ErrBusy) {
		t.Errorf("second Start = %v, want ErrBusy", err)
	}
	done := b.wait(t)
	if done.Phase != firmware.PhaseDone || done.To != "9.9.9" || done.Written != len(image) {
		t.Fatalf("finished job = %+v", done)
	}
	if !strings.Contains(done.Message, "9.9.9") || !strings.Contains(done.Message, before) {
		t.Errorf("message = %q", done.Message)
	}
	if got := b.outpost.SNIPInfo().SoftwareVersion; got != "9.9.9" {
		t.Errorf("outpost runs %q", got)
	}
	if b.outpost.OnTrial() {
		t.Error("the image should have confirmed itself within the settle time")
	}
	want := []firmware.Phase{firmware.PhaseChecking, firmware.PhaseFreezing, firmware.PhaseWriting, firmware.PhaseUnfreezing,
		firmware.PhaseRestarting, firmware.PhaseVerifying, firmware.PhaseDone}
	if got := b.phases(); strings.Join(phaseStrings(got), ",") != strings.Join(phaseStrings(want), ",") {
		t.Errorf("phases = %v, want %v", got, want)
	}
	b.nodes.mu.Lock()
	refreshed := b.nodes.refresh
	b.nodes.mu.Unlock()
	if refreshed == 0 {
		t.Error("the registry was not asked to refresh the node")
	}
	if _, ok := b.local.Cache().LookupID(outpostID); !ok {
		t.Error("the outpost should still be known by alias after its restart")
	}
}

func TestUpgradeRolledBack(t *testing.T) {
	b := newBench(t, 600*time.Millisecond)
	before := b.outpost.SNIPInfo().SoftwareVersion
	if _, err := b.up.Start(context.Background(), outpostID, "bad.bin", sim.FirmwareImage("0.0.1", true)); err != nil {
		t.Fatal(err)
	}
	done := b.wait(t)
	if done.Phase != firmware.PhaseFailed || !strings.Contains(done.Message, "went back to version "+before) {
		t.Fatalf("finished job = %+v", done)
	}
	if got := b.outpost.SNIPInfo().SoftwareVersion; got != before {
		t.Errorf("outpost runs %q, want %q", got, before)
	}
}

func TestUpgradeRejected(t *testing.T) {
	b := newBench(t, 0)
	ctx := context.Background()
	if _, err := b.up.Start(ctx, outpostID, "photo.jpg", []byte("not firmware at all, just some bytes")); err != nil {
		t.Fatal(err)
	}
	done := b.wait(t)
	if done.Phase != firmware.PhaseFailed || !strings.Contains(done.Message, "not firmware for this hardware") {
		t.Fatalf("finished job = %+v", done)
	}
	if b.outpost.Server.Frozen() {
		t.Error("the outpost was left frozen after the failure")
	}

	// An image that stops short of its end is caught at Unfreeze.
	short := sim.FirmwareImage("1.0.0", false)
	short = short[:len(short)-10]
	if _, err := b.up.Start(ctx, outpostID, "short.bin", short); err != nil {
		t.Fatal(err)
	}
	done = b.wait(t)
	if done.Phase != firmware.PhaseFailed || !strings.Contains(done.Message, "did not arrive whole") {
		t.Fatalf("finished job = %+v", done)
	}

	// Bad files never reach the bus.
	if _, err := b.up.Start(ctx, outpostID, "empty.bin", nil); err == nil {
		t.Error("an empty file should be refused")
	}
	if _, err := b.up.Start(ctx, 0x0A0000000099, "x.bin", short); !errors.Is(err, firmware.ErrNotFound) {
		t.Errorf("unknown node = %v", err)
	}
	if err := b.up.Cancel(outpostID); !errors.Is(err, firmware.ErrNoJob) {
		t.Errorf("Cancel with nothing running = %v", err)
	}
}

func TestUpgradeCanceled(t *testing.T) {
	b := newBench(t, 0)
	// A big image gives time to cancel during the writes.
	image := append(sim.FirmwareImage("2.0.0", false), make([]byte, 100*1024)...)
	if _, err := b.up.Start(context.Background(), outpostID, "big.bin", image); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, _ := b.up.Job(outpostID)
		if j.Phase == firmware.PhaseWriting && j.Written > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw a write: %+v", j)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := b.up.Cancel(outpostID); err != nil {
		t.Fatal(err)
	}
	done := b.wait(t)
	if done.Phase != firmware.PhaseFailed || !strings.Contains(done.Message, "Canceled") {
		t.Fatalf("finished job = %+v", done)
	}
	time.Sleep(50 * time.Millisecond)
	if b.outpost.Server.Frozen() {
		t.Error("cancel should unfreeze the node")
	}
	if got := b.outpost.SNIPInfo().SoftwareVersion; got == "2.0.0" {
		t.Error("the canceled image must not run")
	}
}

func phaseStrings(ps []firmware.Phase) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func TestUnpackUF2(t *testing.T) {
	block := func(addr uint32, payload []byte, flags uint32) []byte {
		b := make([]byte, 512)
		binary.LittleEndian.PutUint32(b[0:], 0x0A324655)
		binary.LittleEndian.PutUint32(b[4:], 0x9E5D5157)
		binary.LittleEndian.PutUint32(b[8:], flags)
		binary.LittleEndian.PutUint32(b[12:], addr)
		binary.LittleEndian.PutUint32(b[16:], uint32(len(payload)))
		copy(b[32:], payload)
		binary.LittleEndian.PutUint32(b[508:], 0x0AB16F30)
		return b
	}
	first := make([]byte, 256)
	for i := range first {
		first[i] = byte(i)
	}
	second := []byte("second block")
	file := append(block(0x10000100, second, 0), block(0x10000000, first, 0)...)
	file = append(file, block(0x20000000, []byte("not flash"), 1)...)
	if !firmware.IsUF2(file) {
		t.Fatal("IsUF2 = false")
	}
	image, err := firmware.Decode("x.uf2", file)
	if err != nil {
		t.Fatal(err)
	}
	if len(image) != 256+len(second) || string(image[256:]) != "second block" || image[255] != 255 {
		t.Errorf("image = %d bytes, tail %q", len(image), image[256:])
	}

	// A gap is filled with erased flash.
	gapped := append(block(0x10000000, []byte{1, 2}, 0), block(0x10000004, []byte{3}, 0)...)
	image, err = firmware.UnpackUF2(gapped)
	if err != nil || string(image) != "\x01\x02\xff\xff\x03" {
		t.Errorf("gapped image = %x, %v", image, err)
	}
	if _, err := firmware.UnpackUF2(file[:600]); err == nil {
		t.Error("a torn file should be refused")
	}
	if _, err := firmware.Decode("x.bin", make([]byte, firmware.MaxImage+1)); err == nil {
		t.Error("an oversized file should be refused")
	}
}
