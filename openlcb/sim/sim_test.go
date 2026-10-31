package sim_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

const (
	localID = openlcb.NodeID(0x0A0000000001)
	peerID  = openlcb.NodeID(0x0A0000000042)
)

// Interlock's node and a simulated outpost, wired back to back.
func outpost(t *testing.T) (*node.Node, *memconfig.Client, *sim.Node, context.Context) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	var local *node.Node
	var peer *sim.Node
	local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); peer.HandleFrame(f) }})
	var err error
	peer, err = sim.New(node.Config{ID: peerID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		SNIP: openlcb.SNIP{UserName: "Yard"},
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); local.HandleFrame(f) }}, sim.OutpostCDI)
	if err != nil {
		t.Fatal(err)
	}
	client := memconfig.NewClient(local, memconfig.WithLogger(logger), memconfig.WithReplyTimeout(500*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	go func() { _ = local.Run(ctx) }()
	go func() { _ = peer.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for local.State() != node.StateInitialized || peer.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return local, client, peer, ctx
}

func TestSimulatedOutpost(t *testing.T) {
	local, client, peer, ctx := outpost(t)

	snip, err := local.SNIP(ctx, peerID)
	if err != nil {
		t.Fatal(err)
	}
	if snip.Manufacturer != "mikesmitty" || snip.Model != "CAN Bus Outpost" || snip.SoftwareVersion != "0.3.1" || snip.UserName != "Yard" {
		t.Errorf("SNIP = %+v", snip)
	}
	protocols, err := local.Protocols(ctx, peerID)
	if err != nil || !protocols.Has(openlcb.ProtocolCDI|openlcb.ProtocolMemoryConfiguration) {
		t.Errorf("protocols = %v, %v", protocols, err)
	}

	var progress int
	xml, err := client.ReadCDI(ctx, peerID, func(done, _ int) { progress = done })
	if err != nil {
		t.Fatalf("ReadCDI: %v", err)
	}
	if !bytes.Equal(xml, sim.OutpostCDI) {
		t.Errorf("CDI read back differs: %d bytes vs %d", len(xml), len(sim.OutpostCDI))
	}
	if progress != len(sim.OutpostCDI) {
		t.Errorf("last progress = %d, want %d", progress, len(sim.OutpostCDI))
	}
	doc, err := cdi.Parse(xml)
	if err != nil {
		t.Fatal(err)
	}

	// Defaults are in place: Servo 1 thrown position 16384, closed 49152,
	// enabled 1, LED timeout 5.
	info, err := client.SpaceInfo(ctx, peerID, memconfig.SpaceConfig)
	if err != nil || info.Size() != 3176 {
		t.Errorf("config space info = %+v, %v", info, err)
	}
	data, err := client.ReadAll(ctx, peerID, memconfig.SpaceConfig, 128, 60, nil)
	if err != nil {
		t.Fatal(err)
	}
	index := doc.Lookup()
	check := func(key, want string) {
		t.Helper()
		v := index[key]
		got, err := v.Element.Decode(data[v.Address-128:])
		if err != nil || got != want {
			t.Errorf("%s (%s) = %q, %v; want %q", key, v.Element.Name, got, err, want)
		}
	}
	check("253:129", "5")
	check("253:172", "16384")
	check("253:174", "49152")
	check("253:180", "1")

	// Writing the user name updates SNIP, as the real firmware does.
	name := index["253:0"]
	enc, _ := name.Element.Encode("East Yard")
	if err := client.Write(ctx, peerID, memconfig.SpaceConfig, 0, enc); err != nil {
		t.Fatal(err)
	}
	snip, err = local.SNIP(ctx, peerID)
	if err != nil || snip.UserName != "East Yard" {
		t.Errorf("SNIP after write = %+v, %v", snip, err)
	}

	// Factory reset restores defaults.
	pos := index["253:172"]
	enc, _ = pos.Element.Encode("1000")
	if err := client.Write(ctx, peerID, memconfig.SpaceConfig, pos.Address, enc); err != nil {
		t.Fatal(err)
	}
	if err := client.FactoryReset(ctx, peerID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for peer.FactoryResets() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("factory reset hook not called")
		}
		time.Sleep(5 * time.Millisecond)
	}
	back, err := client.Read(ctx, peerID, memconfig.SpaceConfig, pos.Address, 2)
	if err != nil || !bytes.Equal(back, []byte{0x40, 0x00}) {
		t.Errorf("after factory reset = % X, %v", back, err)
	}
}

func TestCommandStationCDIParses(t *testing.T) {
	doc, err := cdi.Parse(sim.CommandStationCDI)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Identification.Model != "DCC Command Station" {
		t.Errorf("model = %q", doc.Identification.Model)
	}
}
