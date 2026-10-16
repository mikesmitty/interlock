package registry

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/node"
)

const (
	localID openlcb.NodeID = 0x050101012200
	peerID  openlcb.NodeID = 0x050101012201
)

var peerSNIP = openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", HardwareVersion: "Rev A", SoftwareVersion: "0.2.1", UserName: "Yard"}

// pair starts a local node and a peer wired back to back.
func pair(t *testing.T) (local, peer *node.Node, ctx context.Context) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 20 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) {
			if peer != nil {
				peer.HandleFrame(f)
			}
		}})
	peer = node.New(node.Config{ID: peerID, SNIP: peerSNIP, Logger: logger, CIDWait: 20 * time.Millisecond, ReplyTimeout: time.Second,
		Protocols: openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolSimpleNodeInformation | openlcb.ProtocolCDI,
		Send:      func(f can.Frame) { local.HandleFrame(f) }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = local.Run(ctx) }()
	go func() { _ = peer.Run(ctx) }()
	return local, peer, ctx
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegistryFindsAndIdentifiesPeer(t *testing.T) {
	local, peer, ctx := pair(t)
	localSNIP := openlcb.SNIP{Manufacturer: "Interlock", Model: "Controller", UserName: "Desk"}
	r := New(local, Options{Interval: 0, LocalSNIP: localSNIP, LocalProtocols: openlcb.ProtocolSimple | openlcb.ProtocolDatagram,
		Logger: slog.New(slog.DiscardHandler), Timeout: 2 * time.Second})

	var updates []Info
	r.Subscribe(func(info Info) { updates = append(updates, info) })
	go func() { _ = r.Run(ctx) }()

	waitFor(t, "peer identification", func() bool {
		info, ok := r.Get(peerID)
		return ok && info.SNIP != nil && info.Protocols != nil
	})
	info, _ := r.Get(peerID)
	if !info.Online || info.Alias != peer.Alias() || *info.SNIP != peerSNIP {
		t.Fatalf("peer info = %+v", info)
	}
	if !info.ProtocolBits.Has(openlcb.ProtocolCDI) || len(info.Protocols) != 4 {
		t.Errorf("protocols = %v (%v)", info.Protocols, info.ProtocolBits)
	}
	if info.Name() != "Yard" {
		t.Errorf("Name = %q", info.Name())
	}
	if info.FirstSeen.IsZero() || info.LastSeen.Before(info.FirstSeen) {
		t.Errorf("times = %v %v", info.FirstSeen, info.LastSeen)
	}

	list := r.List()
	if len(list) != 2 || !list[0].Local || list[0].ID != localID || list[1].ID != peerID {
		t.Fatalf("List = %+v", list)
	}
	if list[0].SNIP == nil || *list[0].SNIP != localSNIP || !list[0].Online || list[0].Alias != local.Alias() {
		t.Errorf("local entry = %+v", list[0])
	}
	if len(updates) == 0 {
		t.Error("no subscriber updates")
	}

	// A peer that withdraws its alias goes offline.
	local.HandleFrame(can.AMRFrame(peer.Alias(), peerID))
	waitFor(t, "peer offline", func() bool {
		info, _ := r.Get(peerID)
		return !info.Online
	})
	// And comes back when it speaks again.
	local.HandleFrame(can.AMDFrame(peer.Alias(), peerID))
	waitFor(t, "peer online again", func() bool {
		info, _ := r.Get(peerID)
		return info.Online
	})

	if err := r.RefreshNode(ctx, 0x050101019999); err == nil {
		t.Error("RefreshNode of an unknown node succeeded")
	}
	if err := r.RefreshNode(ctx, peerID); err != nil {
		t.Errorf("RefreshNode: %v", err)
	}
	if err := r.Refresh(ctx); err != nil {
		t.Errorf("Refresh: %v", err)
	}
}

func TestRegistryRecordsIdentificationFailure(t *testing.T) {
	// A node that announces itself but never answers requests.
	logger := slog.New(slog.DiscardHandler)
	sent := make(chan can.Frame, 1024)
	local := node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: 100 * time.Millisecond,
		Send: func(f can.Frame) { sent <- f }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = local.Run(ctx) }()
	r := New(local, Options{Logger: logger, Timeout: 200 * time.Millisecond})
	go func() { _ = r.Run(ctx) }()

	waitFor(t, "local init", func() bool { return local.State() == node.StateInitialized })
	local.HandleFrame(can.AMDFrame(0x555, peerID))
	waitFor(t, "failure recorded", func() bool {
		info, ok := r.Get(peerID)
		return ok && info.Error != ""
	})
	info, _ := r.Get(peerID)
	if info.SNIP != nil || !info.Online {
		t.Errorf("info = %+v", info)
	}
	if info.Name() != peerID.String() {
		t.Errorf("Name without SNIP = %q", info.Name())
	}
}

func TestInfoName(t *testing.T) {
	tests := []struct {
		info Info
		want string
	}{
		{Info{ID: peerID}, "05.01.01.01.22.01"},
		{Info{ID: peerID, SNIP: &openlcb.SNIP{Manufacturer: "m", Model: "x"}}, "m x"},
		{Info{ID: peerID, SNIP: &openlcb.SNIP{Manufacturer: "m", Model: "x", UserName: "Yard"}}, "Yard"},
		{Info{ID: peerID, SNIP: &openlcb.SNIP{}}, "05.01.01.01.22.01"},
	}
	for _, tt := range tests {
		if got := tt.info.Name(); got != tt.want {
			t.Errorf("Name() = %q, want %q", got, tt.want)
		}
	}
}
