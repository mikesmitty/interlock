package registry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
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

// fakeStore records what the registry persists.
type fakeStore struct {
	mu    sync.Mutex
	nodes map[openlcb.NodeID]store.NodeRecord
	cdis  map[openlcb.NodeID]store.CDIRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{nodes: map[openlcb.NodeID]store.NodeRecord{}, cdis: map[openlcb.NodeID]store.CDIRecord{}}
}

func (f *fakeStore) Nodes(context.Context) ([]store.NodeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.NodeRecord
	for _, rec := range f.nodes {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeStore) SaveNode(_ context.Context, rec store.NodeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if old, ok := f.nodes[rec.ID]; ok && rec.SNIP == nil {
		rec.SNIP = old.SNIP
	}
	f.nodes[rec.ID] = rec
	return nil
}

func (f *fakeStore) DeleteNode(_ context.Context, id openlcb.NodeID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.nodes, id)
	delete(f.cdis, id)
	return nil
}

func (f *fakeStore) CDI(_ context.Context, id openlcb.NodeID) (*store.CDIRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.cdis[id]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (f *fakeStore) SaveCDI(_ context.Context, rec store.CDIRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cdis[rec.NodeID] = rec
	return nil
}

func (f *fakeStore) DeleteCDI(_ context.Context, id openlcb.NodeID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cdis, id)
	return nil
}

// fakeCDI serves a document with a progress callback and counts reads.
type fakeCDI struct {
	xml   []byte
	reads atomic.Int32
	fail  atomic.Bool
}

func (f *fakeCDI) ReadCDI(_ context.Context, _ openlcb.NodeID, progress memconfig.Progress) ([]byte, error) {
	f.reads.Add(1)
	if f.fail.Load() {
		return nil, errors.New("node went quiet")
	}
	progress(len(f.xml)/2, len(f.xml))
	progress(len(f.xml), len(f.xml))
	return f.xml, nil
}

const tinyCDI = `<cdi><identification><model>T</model></identification><segment space="253"><name>S</name><int size="1"><name>A</name></int></segment></cdi>`

func TestRegistryReadsAndCachesCDI(t *testing.T) {
	local, _, ctx := pair(t)
	st := newFakeStore()
	reader := &fakeCDI{xml: []byte(tinyCDI)}
	opts := Options{Interval: 0, Logger: slog.New(slog.DiscardHandler), Timeout: 2 * time.Second, Store: st, CDI: reader}
	// The peer in pair() advertises CDI but not memory configuration;
	// give the registry a client view that says both.
	r := New(local, opts)
	go func() { _ = r.Run(ctx) }()

	waitFor(t, "identification", func() bool {
		info, ok := r.Get(peerID)
		return ok && info.SNIP != nil
	})
	info, _ := r.Get(peerID)
	if info.CDI != nil {
		t.Fatalf("CDI planned although the peer lacks memory configuration: %+v", info.CDI)
	}
	st.mu.Lock()
	rec, saved := st.nodes[peerID]
	st.mu.Unlock()
	if !saved || rec.SNIP == nil || rec.SNIP.UserName != "Yard" || !rec.Protocols.Has(openlcb.ProtocolCDI) {
		t.Errorf("node not persisted after identification: %+v", rec)
	}
}

func TestRegistryCDIFlow(t *testing.T) {
	local, peer, ctx := pair(t)
	_ = peer
	st := newFakeStore()
	reader := &fakeCDI{xml: []byte(tinyCDI)}
	r := New(local, Options{Interval: 0, Logger: slog.New(slog.DiscardHandler), Timeout: 2 * time.Second, Store: st, CDI: reader})
	go func() { _ = r.Run(ctx) }()
	waitFor(t, "identification", func() bool {
		info, ok := r.Get(peerID)
		return ok && info.SNIP != nil
	})

	// Simulate what identify does for a node with the right protocols.
	r.mu.Lock()
	info := r.nodes[peerID]
	info.ProtocolBits |= openlcb.ProtocolMemoryConfiguration | openlcb.ProtocolDatagram
	r.planCDILocked(info)
	planned := info.CDI != nil && info.CDI.State == CDIPending
	r.mu.Unlock()
	if !planned {
		t.Fatal("CDI not planned")
	}
	r.requestCDI(peerID)
	waitFor(t, "CDI ready", func() bool {
		info, _ := r.Get(peerID)
		return info.CDI != nil && info.CDI.State == CDIReady
	})
	info2, _ := r.Get(peerID)
	if info2.CDI.Cached || info2.CDI.Segments != 1 || info2.CDI.FetchedAt.IsZero() {
		t.Errorf("CDI status = %+v", info2.CDI)
	}
	doc, xml, ok := r.CDI(peerID)
	if !ok || string(xml) != tinyCDI || len(doc.Segments) != 1 {
		t.Errorf("CDI() = %v %q %v", doc != nil, xml, ok)
	}
	st.mu.Lock()
	cached, saved := st.cdis[peerID]
	st.mu.Unlock()
	if !saved || cached.SoftwareVersion != "0.2.1" || string(cached.XML) != tinyCDI {
		t.Errorf("CDI not cached: %+v", cached)
	}

	// A second registry restores the node and its CDI from the store
	// without reading the node.
	r2 := New(local, Options{Logger: slog.New(slog.DiscardHandler), Store: st, CDI: reader})
	if err := r2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	restored, ok := r2.Get(peerID)
	if !ok || restored.Online || restored.SNIP == nil || restored.SNIP.UserName != "Yard" {
		t.Fatalf("restored = %+v", restored)
	}
	if restored.CDI == nil || restored.CDI.State != CDIReady || !restored.CDI.Cached {
		t.Errorf("restored CDI = %+v", restored.CDI)
	}
	if reads := reader.reads.Load(); reads != 1 {
		t.Errorf("node read %d times, want 1", reads)
	}

	// Refresh drops the cache and reads again.
	if err := r.RefreshCDI(ctx, peerID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "CDI re-read", func() bool {
		info, _ := r.Get(peerID)
		return info.CDI != nil && info.CDI.State == CDIReady && reader.reads.Load() == 2
	})

	// A failing read ends in the error state.
	reader.fail.Store(true)
	if err := r.RefreshCDI(ctx, peerID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "CDI error", func() bool {
		info, _ := r.Get(peerID)
		return info.CDI != nil && info.CDI.State == CDIError
	})
	if info, _ := r.Get(peerID); info.CDI.Error == "" {
		t.Error("error state without a message")
	}
	if _, _, ok := r.CDI(peerID); ok {
		t.Error("CDI still available after a failed refresh")
	}

	// Forget refuses online nodes and removes offline ones.
	if err := r.Forget(ctx, peerID); !errors.Is(err, ErrNodeOnline) {
		t.Errorf("Forget online = %v", err)
	}
	r.mu.Lock()
	r.nodes[peerID].Online = false
	r.mu.Unlock()
	var forgotten bool
	var mu sync.Mutex
	r.Subscribe(func(info Info) {
		mu.Lock()
		defer mu.Unlock()
		if info.ID == peerID && info.Forgotten {
			forgotten = true
		}
	})
	if err := r.Forget(ctx, peerID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !forgotten {
		t.Error("no forgotten update published")
	}
	if _, ok := r.Get(peerID); ok {
		t.Error("node still listed")
	}
	st.mu.Lock()
	_, still := st.nodes[peerID]
	st.mu.Unlock()
	if still {
		t.Error("node still stored")
	}
}
