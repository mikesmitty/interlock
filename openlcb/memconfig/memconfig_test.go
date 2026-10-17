package memconfig_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
)

const (
	localID = openlcb.NodeID(0x0A0000000001)
	peerID  = openlcb.NodeID(0x0A0000000042)
)

// wire connects two nodes back to back, each receiving what the other
// sends, like a two-node CAN bus.
type wire struct {
	local, peer *node.Node
}

func newWire(t *testing.T) *wire {
	t.Helper()
	w := &wire{}
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	w.local = node.New(node.Config{
		ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); w.peer.HandleFrame(f) },
	})
	w.peer = node.New(node.Config{
		ID: peerID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); w.local.HandleFrame(f) },
	})
	return w
}

func (w *wire) run(ctx context.Context, t *testing.T) {
	t.Helper()
	for _, n := range []*node.Node{w.local, w.peer} {
		go func() {
			if err := n.Run(ctx); err != nil {
				t.Errorf("node stopped: %v", err)
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for w.local.State() != node.StateInitialized || w.peer.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatalf("nodes did not initialize: %v %v", w.local.State(), w.peer.State())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newServerWire(ctx context.Context, t *testing.T) (*memconfig.Client, *memconfig.Server, *memconfig.Memory) {
	t.Helper()
	w := newWire(t)
	config := make([]byte, 200)
	for i := range config {
		config[i] = byte(i)
	}
	mem := memconfig.NewMemory(config, false)
	srv := memconfig.NewServer()
	srv.Name = "test node"
	srv.AddSpace(memconfig.SpaceConfig, mem)
	srv.AddSpace(memconfig.SpaceCDI, memconfig.NewMemory(append([]byte("<cdi/>"), 0, 'x', 'x'), true))
	memconfig.NewClient(w.peer, memconfig.WithServer(srv), memconfig.WithLogger(slog.New(slog.DiscardHandler)))
	client := memconfig.NewClient(w.local, memconfig.WithReplyTimeout(500*time.Millisecond), memconfig.WithLogger(slog.New(slog.DiscardHandler)))
	w.run(ctx, t)
	return client, srv, mem
}

func TestReadWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, _, mem := newServerWire(ctx, t)

	data, err := client.Read(ctx, peerID, memconfig.SpaceConfig, 10, 16)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := mem.Bytes()[10:26]; !bytes.Equal(data, want) {
		t.Errorf("Read = % X, want % X", data, want)
	}

	// Short read at the end of the space.
	data, err = client.Read(ctx, peerID, memconfig.SpaceConfig, 190, 64)
	if err != nil {
		t.Fatalf("Read at end: %v", err)
	}
	if len(data) != 10 {
		t.Errorf("Read at end returned %d bytes, want 10", len(data))
	}

	// Whole space with unknown size.
	all, err := client.ReadAll(ctx, peerID, memconfig.SpaceConfig, 0, -1, nil)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(all, mem.Bytes()) {
		t.Errorf("ReadAll returned %d bytes, want %d", len(all), len(mem.Bytes()))
	}
	var progress []int
	part, err := client.ReadAll(ctx, peerID, memconfig.SpaceConfig, 4, 100, func(done, _ int) { progress = append(progress, done) })
	if err != nil {
		t.Fatalf("ReadAll sized: %v", err)
	}
	if len(part) != 100 || !bytes.Equal(part, mem.Bytes()[4:104]) {
		t.Errorf("ReadAll sized returned %d bytes", len(part))
	}
	if len(progress) != 2 || progress[1] != 100 {
		t.Errorf("progress = %v, want [64 100]", progress)
	}

	// Write across a piece boundary and read back.
	payload := bytes.Repeat([]byte{0xAB}, 70)
	if err := client.Write(ctx, peerID, memconfig.SpaceConfig, 100, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := mem.Bytes()[100:170]; !bytes.Equal(got, payload) {
		t.Errorf("after Write: % X", got)
	}

	// Errors from the node.
	_, err = client.Read(ctx, peerID, 0x55, 0, 8)
	var oerr *openlcb.Error
	if !errors.As(err, &oerr) || oerr.Code != memconfig.ErrCodeSpaceUnknown {
		t.Errorf("Read of unknown space: %v, want space unknown", err)
	}
	_, err = client.Read(ctx, peerID, memconfig.SpaceConfig, 500, 8)
	if !errors.As(err, &oerr) || oerr.Code != memconfig.ErrCodeOutOfBounds {
		t.Errorf("Read out of bounds: %v, want out of bounds", err)
	}
	err = client.Write(ctx, peerID, memconfig.SpaceCDI, 0, []byte{1})
	if !errors.As(err, &oerr) || oerr.Code != memconfig.ErrCodeWriteReadOnly {
		t.Errorf("Write to read-only: %v, want write to read-only", err)
	}
	err = client.Write(ctx, peerID, memconfig.SpaceConfig, 199, []byte{1, 2})
	if !errors.As(err, &oerr) || oerr.Code != memconfig.ErrCodeOutOfBounds {
		t.Errorf("Write out of bounds: %v, want out of bounds", err)
	}
}

func TestReadCDI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, _, _ := newServerWire(ctx, t)
	xml, err := client.ReadCDI(ctx, peerID, nil)
	if err != nil {
		t.Fatalf("ReadCDI: %v", err)
	}
	if string(xml) != "<cdi/>" {
		t.Errorf("ReadCDI = %q, want <cdi/>", xml)
	}
}

func TestInfoLockAndCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, srv, _ := newServerWire(ctx, t)

	opts, err := client.Options(ctx, peerID)
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	if opts.Name != "test node" || opts.HighestSpace != memconfig.SpaceCDI || opts.LowestSpace != memconfig.SpaceConfig {
		t.Errorf("Options = %+v", opts)
	}
	if opts.Available&memconfig.AvailUnalignedReads == 0 || opts.WriteLengths&memconfig.WriteLength64 == 0 {
		t.Errorf("Options capabilities = %+v", opts)
	}

	info, err := client.SpaceInfo(ctx, peerID, memconfig.SpaceConfig)
	if err != nil {
		t.Fatalf("SpaceInfo: %v", err)
	}
	if !info.Present || info.Size() != 200 || info.ReadOnly {
		t.Errorf("SpaceInfo config = %+v", info)
	}
	info, err = client.SpaceInfo(ctx, peerID, memconfig.SpaceCDI)
	if err != nil {
		t.Fatalf("SpaceInfo CDI: %v", err)
	}
	if !info.Present || !info.ReadOnly || info.Size() != 9 {
		t.Errorf("SpaceInfo CDI = %+v", info)
	}
	info, err = client.SpaceInfo(ctx, peerID, 0x33)
	if err != nil {
		t.Fatalf("SpaceInfo absent: %v", err)
	}
	if info.Present {
		t.Errorf("SpaceInfo absent = %+v", info)
	}

	holder, err := client.Lock(ctx, peerID, localID)
	if err != nil || holder != localID {
		t.Errorf("Lock = %v, %v; want ours", holder, err)
	}
	other := openlcb.NodeID(0x050101010101)
	holder, err = client.Lock(ctx, peerID, other)
	if err != nil || holder != localID {
		t.Errorf("Lock by other = %v, %v; want still ours", holder, err)
	}
	holder, err = client.Lock(ctx, peerID, 0)
	if err != nil || holder != 0 {
		t.Errorf("Unlock = %v, %v; want none", holder, err)
	}

	var mu sync.Mutex
	calls := map[string]int{}
	note := func(name string) func() {
		return func() { mu.Lock(); calls[name]++; mu.Unlock() }
	}
	srv.OnUpdateComplete = note("update")
	srv.OnReboot = note("reboot")
	srv.OnFactoryReset = note("reset")
	if err := client.UpdateComplete(ctx, peerID); err != nil {
		t.Errorf("UpdateComplete: %v", err)
	}
	if err := client.Reboot(ctx, peerID); err != nil {
		t.Errorf("Reboot: %v", err)
	}
	if err := client.FactoryReset(ctx, peerID); err != nil {
		t.Errorf("FactoryReset: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		done := calls["update"] == 1 && calls["reboot"] == 1 && calls["reset"] == 1
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hooks not all called: %v", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Freeze on a node without firmware support is rejected: the firmware
	// space does not exist there.
	err = client.Freeze(ctx, peerID, memconfig.SpaceFirmware)
	var oerr *openlcb.Error
	if !errors.As(err, &oerr) || oerr.Code != memconfig.ErrCodeSpaceUnknown {
		t.Errorf("Freeze: %v, want address space unknown", err)
	}
}

// A node in the style of simple-dcc: acknowledges reads without the reply
// pending flag but still sends the reply, and silently ignores commands it
// does not know.
func TestLenientPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w := newWire(t)
	quiet := slog.New(slog.DiscardHandler)
	w.peer.HandleDatagrams(memconfig.ContentType, func(msg *openlcb.Message) (node.DatagramFlags, error) {
		rw, err := memconfig.ParseReadWrite(msg.Payload)
		if err != nil || rw.Command != memconfig.CmdRead {
			return node.DatagramFlags{}, nil // ignored, like an unknown subcommand
		}
		go func() {
			reply := memconfig.ReadReply(rw.Space, rw.Address, bytes.Repeat([]byte{0x5A}, int(rw.Data[0])))
			if _, err := w.peer.SendDatagram(ctx, msg.Source.ID, reply); err != nil {
				t.Errorf("peer reply: %v", err)
			}
		}()
		return node.DatagramFlags{}, nil
	})
	client := memconfig.NewClient(w.local, memconfig.WithReplyTimeout(300*time.Millisecond), memconfig.WithLogger(quiet))
	w.run(ctx, t)

	data, err := client.Read(ctx, peerID, memconfig.SpaceConfig, 0, 8)
	if err != nil {
		t.Fatalf("Read from lenient peer: %v", err)
	}
	if len(data) != 8 || data[0] != 0x5A {
		t.Errorf("Read = % X", data)
	}
	start := time.Now()
	_, err = client.Options(ctx, peerID)
	if !errors.Is(err, memconfig.ErrNoReply) {
		t.Errorf("Options from quiet peer: %v, want ErrNoReply", err)
	}
	if took := time.Since(start); took < 250*time.Millisecond || took > 2*time.Second {
		t.Errorf("Options gave up after %v, want about the reply timeout", took)
	}
	// Writes need no reply.
	if err := client.Write(ctx, peerID, memconfig.SpaceConfig, 0, []byte{1}); err != nil {
		t.Errorf("Write to lenient peer: %v", err)
	}
}

func TestCodec(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"read config uses space bits", memconfig.ReadRequest(memconfig.SpaceConfig, 0x84, 64), []byte{0x20, 0x41, 0, 0, 0, 0x84, 64}},
		{"read CDI uses space bits", memconfig.ReadRequest(memconfig.SpaceCDI, 0, 64), []byte{0x20, 0x43, 0, 0, 0, 0, 64}},
		{"read all memory uses space bits", memconfig.ReadRequest(memconfig.SpaceAll, 1, 2), []byte{0x20, 0x42, 0, 0, 0, 1, 2}},
		{"read CV space names it", memconfig.ReadRequest(memconfig.SpaceDCCCV, 7, 1), []byte{0x20, 0x40, 0, 0, 0, 7, 0xF8, 1}},
		{"write explicit space", memconfig.WriteRequest(0xEF, 0x1000, []byte{9, 8}), []byte{0x20, 0x00, 0, 0, 0x10, 0, 0xEF, 9, 8}},
		{"read failed", memconfig.ReadFailedReply(memconfig.SpaceConfig, 5, memconfig.ErrCodeOutOfBounds), []byte{0x20, 0x59, 0, 0, 0, 5, 0x10, 0x82}},
		{"space info", memconfig.SpaceInfoRequest(0xFD), []byte{0x20, 0x84, 0xFD}},
		{"factory reset", memconfig.FactoryResetRequest(0x050101011800), []byte{0x20, 0xAA, 5, 1, 1, 1, 0x18, 0}},
		{"lock", memconfig.LockRequest(0), []byte{0x20, 0x88, 0, 0, 0, 0, 0, 0}},
		{"freeze", memconfig.FreezeRequest(0xEF), []byte{0x20, 0xA1, 0xEF}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % X, want % X", c.name, c.got, c.want)
		}
	}

	rw, err := memconfig.ParseReadWrite([]byte{0x20, 0x51, 0, 0, 1, 0, 'a', 'b'})
	if err != nil || rw.Command != memconfig.CmdReadReply || rw.Space != 0xFD || rw.Address != 0x100 || string(rw.Data) != "ab" {
		t.Errorf("ParseReadWrite reply = %+v, %v", rw, err)
	}
	rw, err = memconfig.ParseReadWrite([]byte{0x20, 0x18, 0, 0, 0, 2, 0xF8, 0x10, 0x83})
	if err != nil || !rw.Failed() || rw.Space != 0xF8 || rw.ErrorCode() != memconfig.ErrCodeWriteReadOnly {
		t.Errorf("ParseReadWrite failure = %+v, %v", rw, err)
	}
	if _, err := memconfig.ParseReadWrite([]byte{0x20, 0x40, 0, 0, 0}); !errors.Is(err, memconfig.ErrMalformed) {
		t.Errorf("short datagram: %v", err)
	}
	if _, err := memconfig.ParseReadWrite([]byte{0x20, 0x40, 0, 0, 0, 0}); !errors.Is(err, memconfig.ErrMalformed) {
		t.Errorf("explicit space missing: %v", err)
	}

	opts := memconfig.Options{Available: 0x6000, WriteLengths: 0xF2, HighestSpace: 0xFF, LowestSpace: 0xEF, Name: "x"}
	back, err := memconfig.ParseOptions(opts.Encode())
	if err != nil || back != opts {
		t.Errorf("Options round trip = %+v, %v", back, err)
	}
	for _, info := range []memconfig.SpaceInfo{
		{Space: 0xFD, Present: true, HighestAddress: 995},
		{Space: 0xFF, Present: true, HighestAddress: 4000, ReadOnly: true, Description: "CDI"},
		{Space: 0xF8, Present: true, HighestAddress: 1024, LowestAddress: 1, Description: "CVs"},
		{Space: 0x33},
	} {
		back, err := memconfig.ParseSpaceInfo(info.Encode())
		if err != nil || back != info {
			t.Errorf("SpaceInfo round trip: got %+v, want %+v (%v)", back, info, err)
		}
	}
	// canbus-outpost's absent reply has only three bytes.
	info, err := memconfig.ParseSpaceInfo([]byte{0x20, 0x86, 0x44})
	if err != nil || info.Present || info.Space != 0x44 {
		t.Errorf("absent space info = %+v, %v", info, err)
	}
	if memconfig.SpaceName(0xFD) != "configuration" || memconfig.SpaceName(0x12) != "space 0x12" {
		t.Errorf("SpaceName wrong")
	}
	for cmd, reply := range map[byte]bool{0x00: false, 0x10: true, 0x18: true, 0x40: false, 0x50: true, 0x5B: true, 0x80: false, 0x82: true, 0x86: true, 0x87: true, 0x8A: true, 0xA8: false} {
		if memconfig.IsReply(cmd) != reply {
			t.Errorf("IsReply(0x%02X) = %v", cmd, !reply)
		}
	}
}
