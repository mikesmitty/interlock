package eventtable

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/nodeconfig"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

const nodeID = openlcb.NodeID(0x0A0000000042)

// fakeMemory serves space 0xFD from a byte slice.
type fakeMemory struct {
	mu    sync.Mutex
	data  []byte
	reads int
}

func (m *fakeMemory) ReadAll(_ context.Context, _ openlcb.NodeID, space uint8, address uint32, size int, _ memconfig.Progress) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if space != memconfig.SpaceConfig || int(address) >= len(m.data) {
		return nil, &openlcb.Error{Code: openlcb.ErrCodeAddressOutOfBounds}
	}
	end := min(int(address)+size, len(m.data))
	return append([]byte(nil), m.data[address:end]...), nil
}

func (m *fakeMemory) Write(_ context.Context, _ openlcb.NodeID, _ uint8, address uint32, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy(m.data[address:], data)
	return nil
}

func (m *fakeMemory) UpdateComplete(context.Context, openlcb.NodeID) error { return nil }
func (m *fakeMemory) Reboot(context.Context, openlcb.NodeID) error         { return nil }
func (m *fakeMemory) FactoryReset(context.Context, openlcb.NodeID) error   { return nil }

type fakeLayouts struct{ doc *cdi.CDI }

func (l fakeLayouts) CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool) {
	if id != nodeID {
		return nil, nil, false
	}
	return l.doc, nil, true
}

type fakeStore struct {
	mu      sync.Mutex
	records map[openlcb.NodeID]store.ValuesRecord
}

func (s *fakeStore) NodeValues(_ context.Context, id openlcb.NodeID) (*store.ValuesRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (s *fakeStore) SaveNodeValues(_ context.Context, rec store.ValuesRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.NodeID] = rec
	return nil
}

func (s *fakeStore) DeleteNodeValues(_ context.Context, id openlcb.NodeID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}

// outpostImage builds a configuration space where every event ID is the
// node ID followed by its index in the document.
func outpostImage(t *testing.T) (*cdi.CDI, []byte) {
	t.Helper()
	doc, err := cdi.Parse(sim.OutpostCDI)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 996)
	n := 0
	for _, v := range doc.Variables() {
		switch {
		case v.Element.Kind == cdi.KindEventID:
			n++
			copy(data[v.Address:], nodeID.Event(uint16(n)).AsSlice())
		case v.Element.Name == "Enabled" && v.Address > 132:
			data[v.Address] = 1
		}
	}
	return doc, data
}

func info(online bool) registry.Info {
	return registry.Info{
		ID:     nodeID,
		Online: online,
		SNIP:   &openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", SoftwareVersion: "0.2.1", UserName: "Yard"},
		CDI:    &registry.CDIStatus{State: registry.CDIReady},
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTable(t *testing.T) {
	writeSettle = 20 * time.Millisecond
	doc, image := outpostImage(t)
	mem := &fakeMemory{data: image}
	layouts := fakeLayouts{doc: doc}
	st := &fakeStore{records: map[openlcb.NodeID]store.ValuesRecord{}}
	reader := nodeconfig.New(mem, layouts, slog.New(slog.DiscardHandler))
	table := New(reader, layouts, st, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = table.Run(ctx) }()

	var mu sync.Mutex
	var changes int
	table.Subscribe(func(openlcb.NodeID) {
		mu.Lock()
		changes++
		mu.Unlock()
	})

	// Offline: nothing to read yet.
	table.HandleNode(info(false))
	time.Sleep(20 * time.Millisecond)
	if entries := table.NodeEntries(nodeID); len(entries) != 0 {
		t.Fatalf("entries before the node is online: %d", len(entries))
	}

	table.HandleNode(info(true))
	waitFor(t, "first read", func() bool { return len(table.NodeEntries(nodeID)) > 0 })
	entries := table.NodeEntries(nodeID)
	if len(entries) != 32 {
		t.Fatalf("entries = %d, want 32", len(entries))
	}
	first := entries[0]
	if first.Name != "Switch Machines / Servo 1 / Throw Command Event" || first.Key != "253:132" || first.Event != nodeID.Event(1) || first.NodeName != "Yard" {
		t.Errorf("first entry = %+v", first)
	}
	if entries[20].Name != "RailCom Detectors / Track 1 / Occupied Event" {
		t.Errorf("entry 20 = %+v", entries[20])
	}
	values, ok := table.Values(nodeID)
	if !ok || values.Values["253:540"] != "1" || values.Cached || values.Version != "0.2.1" {
		t.Errorf("values = %+v, %v", values, ok)
	}
	if got := table.Lookup(nodeID.Event(3)); len(got) != 1 || got[0].Name != "Switch Machines / Servo 1 / Thrown Feedback Event" {
		t.Errorf("Lookup = %+v", got)
	}
	if all := table.Entries(); len(all) != 32 {
		t.Errorf("Entries = %d", len(all))
	}

	// A repeated update while online does not read again.
	mem.mu.Lock()
	reads := mem.reads
	mem.mu.Unlock()
	table.HandleNode(info(true))
	time.Sleep(30 * time.Millisecond)
	mem.mu.Lock()
	if mem.reads != reads {
		t.Errorf("reads = %d after a repeated update, want %d", mem.reads, reads)
	}
	mem.mu.Unlock()

	// Identify replies set directions.
	table.HandleMessage(&openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Source: openlcb.NodeHandle{ID: nodeID}, Payload: nodeID.Event(3).AsSlice()})
	table.HandleMessage(&openlcb.Message{MTI: openlcb.MTIConsumerIdentifiedUnknown, Source: openlcb.NodeHandle{ID: nodeID}, Payload: nodeID.Event(1).AsSlice()})
	entries = table.NodeEntries(nodeID)
	if !entries[2].Produced || entries[2].Consumed || !entries[0].Consumed || entries[0].Produced {
		t.Errorf("directions = %+v / %+v", entries[0], entries[2])
	}

	// A write seen on the bus changes a value; the table reads again once
	// the writes settle.
	mem.mu.Lock()
	copy(mem.data[132:], nodeID.Event(0x99).AsSlice())
	mem.mu.Unlock()
	write := memconfig.WriteRequest(memconfig.SpaceConfig, 132, nodeID.Event(0x99).AsSlice())
	table.HandleMessage(&openlcb.Message{MTI: openlcb.MTIDatagram, Dest: openlcb.NodeHandle{ID: nodeID}, Payload: write})
	table.HandleMessage(&openlcb.Message{MTI: openlcb.MTIDatagram, Dest: openlcb.NodeHandle{ID: nodeID}, Payload: write})
	waitFor(t, "re-read after write", func() bool { return table.NodeEntries(nodeID)[0].Event == nodeID.Event(0x99) })
	mem.mu.Lock()
	if mem.reads != reads+2 {
		t.Errorf("reads after write = %d, want %d (one per segment)", mem.reads, reads+2)
	}
	mem.mu.Unlock()

	// Coming back online reads again.
	table.HandleNode(info(false))
	table.HandleNode(info(true))
	waitFor(t, "re-read after reconnect", func() bool {
		mem.mu.Lock()
		defer mem.mu.Unlock()
		return mem.reads >= reads+4
	})

	// A renamed node renames its entries without a read.
	renamed := info(true)
	renamed.SNIP = &openlcb.SNIP{SoftwareVersion: "0.2.1", UserName: "Siding"}
	table.HandleNode(renamed)
	if entries := table.NodeEntries(nodeID); entries[0].NodeName != "Siding" {
		t.Errorf("node name after rename = %q", entries[0].NodeName)
	}

	// The store has the latest values; a fresh table restores from it.
	st.mu.Lock()
	rec, saved := st.records[nodeID]
	st.mu.Unlock()
	if !saved || rec.Values["253:132"] != nodeID.Event(0x99).String() {
		t.Fatalf("stored record = %+v, %v", rec, saved)
	}
	fresh := New(reader, layouts, st, slog.New(slog.DiscardHandler))
	fresh.Restore(ctx, []registry.Info{info(false)})
	if entries := fresh.NodeEntries(nodeID); len(entries) != 32 || entries[0].Event != nodeID.Event(0x99) {
		t.Errorf("restored entries = %d", len(entries))
	}
	if values, ok := fresh.Values(nodeID); !ok || !values.Cached {
		t.Errorf("restored values = %+v, %v", values, ok)
	}

	// Forgetting a node removes everything.
	forgotten := info(false)
	forgotten.Forgotten = true
	table.HandleNode(forgotten)
	if table.NodeEntries(nodeID) != nil {
		t.Error("entries survived Forget")
	}
	st.mu.Lock()
	_, saved = st.records[nodeID]
	st.mu.Unlock()
	if saved {
		t.Error("store record survived Forget")
	}
	mu.Lock()
	if changes < 4 {
		t.Errorf("subscriber saw %d changes", changes)
	}
	mu.Unlock()
}
