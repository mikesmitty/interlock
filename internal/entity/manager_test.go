package entity

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

const (
	nodeID = openlcb.NodeID(0x0A0000000042)
	other  = openlcb.NodeID(0x0A0000000043)
)

var (
	evThrow    = nodeID.Event(0)
	evClose    = nodeID.Event(1)
	evThrown   = nodeID.Event(2)
	evClosed   = nodeID.Event(3)
	evOccupied = nodeID.Event(0x20)
	evClear    = nodeID.Event(0x21)
	evReporter = other.Event(0)
)

type fakeBus struct {
	mu     sync.Mutex
	events []openlcb.EventID
	msgs   []openlcb.Message
}

func (b *fakeBus) SendEvent(_ context.Context, id openlcb.EventID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, id)
	return nil
}

func (b *fakeBus) Send(_ context.Context, msg openlcb.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, msg)
	return nil
}

func (b *fakeBus) sent() ([]openlcb.EventID, []openlcb.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]openlcb.EventID(nil), b.events...), append([]openlcb.Message(nil), b.msgs...)
}

type fakeNodes struct {
	mu    sync.Mutex
	infos map[openlcb.NodeID]registry.Info
}

func (n *fakeNodes) Get(id openlcb.NodeID) (registry.Info, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	info, ok := n.infos[id]
	return info, ok
}

func (n *fakeNodes) List() []registry.Info {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []registry.Info
	for _, info := range n.infos {
		out = append(out, info)
	}
	return out
}

func (n *fakeNodes) set(info registry.Info) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.infos[info.ID] = info
}

type fakeLayouts struct{}

func (fakeLayouts) CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool) {
	if id != nodeID {
		return nil, nil, false
	}
	return &cdi.CDI{}, nil, true
}

type fakeValues struct{}

func (fakeValues) Values(id openlcb.NodeID) (eventtable.Values, bool) {
	if id != nodeID {
		return eventtable.Values{}, false
	}
	return eventtable.Values{Node: id, Values: map[string]string{"253:132": evThrow.String()}}, true
}

type fakeBuilder struct {
	mu    sync.Mutex
	specs []Spec
}

func (b *fakeBuilder) Build(node NodeInfo, _ *cdi.CDI, _ map[string]string) []Spec {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Spec
	for _, s := range b.specs {
		if s.Node == node.ID {
			out = append(out, s)
		}
	}
	return out
}

func outpostSpecs() []Spec {
	return []Spec{
		{ID: "turnout.0a0000000042.servo_1", Kind: KindTurnout, Name: "Yard Servo 1", Node: nodeID, Group: "Servo 1", Source: SourceProfile, Profile: "canbus-outpost",
			Events: map[string]openlcb.EventID{"throw": evThrow, "close": evClose, "thrown": evThrown, "closed": evClosed}},
		{ID: "block.0a0000000042.track_1", Kind: KindBlock, Name: "Yard Track 1", Node: nodeID, Group: "Track 1", Source: SourceProfile, Profile: "canbus-outpost",
			Events: map[string]openlcb.EventID{"occupied": evOccupied, "clear": evClear}},
		{ID: "reporter.0a0000000042.track_1", Kind: KindReporter, Name: "Yard Track 1", Node: nodeID, Group: "Track 1", Source: SourceProfile, Profile: "canbus-outpost",
			Events: map[string]openlcb.EventID{"range": evReporter}},
		{ID: "switch.0a0000000042.guess", Kind: KindSwitch, Name: "Yard guess", Node: nodeID, Source: SourceGuess, Suggested: true,
			Events: map[string]openlcb.EventID{"on": openlcb.EventClearEmergencyOff, "off": openlcb.EventEmergencyOff}},
	}
}

func nodeInfo(online bool) registry.Info {
	return registry.Info{ID: nodeID, Online: online, SNIP: &openlcb.SNIP{UserName: "Yard"}}
}

type harness struct {
	m       *Manager
	bus     *fakeBus
	nodes   *fakeNodes
	builder *fakeBuilder
	mu      sync.Mutex
	seen    []Entity
}

func newHarness(t *testing.T, dir string) *harness {
	t.Helper()
	file, err := OpenFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		bus:     &fakeBus{},
		nodes:   &fakeNodes{infos: map[openlcb.NodeID]registry.Info{nodeID: nodeInfo(true)}},
		builder: &fakeBuilder{specs: outpostSpecs()},
	}
	h.m = New(Options{Bus: h.bus, Nodes: h.nodes, Layouts: fakeLayouts{}, Values: fakeValues{}, Builder: h.builder, File: file, Logger: slog.New(slog.DiscardHandler)})
	h.m.Subscribe(func(e Entity) {
		h.mu.Lock()
		h.seen = append(h.seen, e)
		h.mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = h.m.Run(ctx) }()
	return h
}

func (h *harness) get(t *testing.T, id string) Entity {
	t.Helper()
	ent, ok := h.m.Get(id)
	if !ok {
		t.Fatalf("entity %s missing", id)
	}
	return ent
}

func (h *harness) report(ev openlcb.EventID) {
	h.m.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Source: openlcb.NodeHandle{ID: nodeID}, Payload: ev.AsSlice()})
}

func (h *harness) identified(mti openlcb.MTI, ev openlcb.EventID) {
	h.m.HandleMessage(&openlcb.Message{MTI: mti, Source: openlcb.NodeHandle{ID: nodeID}, Payload: ev.AsSlice()})
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

func TestManagerFollowsTheBus(t *testing.T) {
	h := newHarness(t, t.TempDir())
	const turnout, block, reporter, guess = "turnout.0a0000000042.servo_1", "block.0a0000000042.track_1", "reporter.0a0000000042.track_1", "switch.0a0000000042.guess"

	waitFor(t, "entities", func() bool { return len(h.m.List()) == 4 })
	list := h.m.List()
	if list[0].Kind != KindTurnout || list[1].Kind != KindBlock || list[2].Kind != KindReporter || list[3].Kind != KindSwitch {
		t.Errorf("order = %v %v %v %v", list[0].Kind, list[1].Kind, list[2].Kind, list[3].Kind)
	}
	tn := h.get(t, turnout)
	if tn.State != StateUnknown || !tn.Available || tn.NodeName != "Yard" || len(tn.Commands) != 2 || tn.Commands[0] != "throw" {
		t.Errorf("turnout = %+v", tn)
	}
	if g := h.get(t, guess); !g.Suggested || g.Source != SourceGuess {
		t.Errorf("guess = %+v", g)
	}
	// The node is asked for its states once it has entities, and each
	// reporter's detector for the locomotives it sees.
	waitFor(t, "identify events", func() bool {
		_, msgs := h.bus.sent()
		for _, msg := range msgs {
			if msg.MTI == openlcb.MTIIdentifyEventsAddressed && msg.Dest.ID == nodeID {
				return true
			}
		}
		return false
	})
	waitFor(t, "identify producer on the reporter's range", func() bool {
		_, msgs := h.bus.sent()
		for _, msg := range msgs {
			if msg.MTI == openlcb.MTIIdentifyProducer && len(msg.Payload) == 8 && openlcb.EventIDFromBytes([8]byte(msg.Payload)) == detector.Range(evReporter).Base {
				return true
			}
		}
		return false
	})
	// A detector that sees nothing answers Valid to address 0 leaving,
	// which says the track is clear.
	h.identified(openlcb.MTIProducerIdentifiedValid, detector.Range(evReporter).Base)
	if r := h.get(t, reporter); r.State != StateClear {
		t.Errorf("reporter after address 0 exit from unknown: %+v", r)
	}

	// Feedback from identification, then commands and feedback on the bus.
	h.identified(openlcb.MTIProducerIdentifiedValid, evClosed)
	if tn = h.get(t, turnout); tn.State != StateClosed {
		t.Errorf("after identified closed: %s", tn.State)
	}
	h.report(evThrow)
	if tn = h.get(t, turnout); tn.State != StateMoving || tn.Commanded != StateThrown {
		t.Errorf("after throw report: %s / %s", tn.State, tn.Commanded)
	}
	h.report(evThrown)
	if tn = h.get(t, turnout); tn.State != StateThrown || tn.Commanded != "" || tn.LastEvent.IsZero() {
		t.Errorf("after thrown feedback: %+v", tn)
	}
	h.identified(openlcb.MTIProducerIdentifiedInvalid, evThrown)
	if tn = h.get(t, turnout); tn.State != StateThrown {
		t.Errorf("invalid feedback should not change a turnout: %s", tn.State)
	}
	h.identified(openlcb.MTIConsumerIdentifiedValid, evClose)
	if tn = h.get(t, turnout); tn.State != StateClosed {
		t.Errorf("consumer identified valid close: %s", tn.State)
	}

	// Commands send the event and apply it.
	tn, err := h.m.Command(context.Background(), turnout, "throw")
	if err != nil || tn.State != StateMoving || tn.Commanded != StateThrown {
		t.Errorf("Command throw = %+v, %v", tn, err)
	}
	if events, _ := h.bus.sent(); len(events) != 1 || events[0] != evThrow {
		t.Errorf("sent events = %v", events)
	}
	if _, err := h.m.Command(context.Background(), turnout, "center"); !errors.Is(err, ErrUnknownCommand) {
		t.Errorf("unbound command: %v", err)
	}
	if _, err := h.m.Command(context.Background(), "turnout.nope", "throw"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing entity: %v", err)
	}

	// Blocks: a report, and an invalid reply meaning the other state.
	h.report(evOccupied)
	if b := h.get(t, block); b.State != StateOccupied {
		t.Errorf("block after occupied: %s", b.State)
	}
	h.identified(openlcb.MTIProducerIdentifiedInvalid, evOccupied)
	if b := h.get(t, block); b.State != StateClear {
		t.Errorf("block after occupied invalid: %s", b.State)
	}

	// Reporters decode detector events in their range.
	rng := detector.Range(evReporter)
	h.report(detector.Report{Kind: detector.EntryEast, Address: detector.Address{Type: detector.Long, Number: 1234}}.Event(rng))
	r := h.get(t, reporter)
	if r.State != StateOccupied || len(r.Locomotives) != 1 || r.Locomotives[0].Name != "1234" || r.Locomotives[0].Facing != "east" {
		t.Errorf("reporter after entry: %+v", r)
	}
	h.report(detector.Report{Kind: detector.EntryWest, Address: detector.Address{Type: detector.Long, Number: 1234}}.Event(rng))
	if r = h.get(t, reporter); len(r.Locomotives) != 1 || r.Locomotives[0].Facing != "west" {
		t.Errorf("reporter after turning: %+v", r)
	}
	h.identified(openlcb.MTIProducerIdentifiedValid, detector.Report{Kind: detector.Entry, Address: detector.Address{Type: detector.Short, Number: 3}}.Event(rng))
	if r = h.get(t, reporter); len(r.Locomotives) != 2 || r.Locomotives[1].Name != "3 (short)" {
		t.Errorf("reporter after identified entry: %+v", r)
	}
	h.report(detector.Report{Kind: detector.Exit, Address: detector.Address{Type: detector.Long, Number: 1234}}.Event(rng))
	h.identified(openlcb.MTIProducerIdentifiedInvalid, detector.Report{Kind: detector.Entry, Address: detector.Address{Type: detector.Short, Number: 3}}.Event(rng))
	if r = h.get(t, reporter); r.State != StateClear || len(r.Locomotives) != 0 {
		t.Errorf("reporter after exits: %+v", r)
	}
	// Address 0 leaving, which detectors answer Valid to, changes nothing.
	h.identified(openlcb.MTIProducerIdentifiedValid, rng.Base)
	if r = h.get(t, reporter); r.State != StateClear {
		t.Errorf("reporter after address 0 exit: %+v", r)
	}

	// Switches follow reports from anyone.
	h.report(openlcb.EventEmergencyOff)
	if g := h.get(t, guess); g.State != StateOff {
		t.Errorf("switch after off: %s", g.State)
	}

	// Overrides: rename, confirm, hide.
	name := "Yard lead"
	ent, err := h.m.Update(turnout, Change{Name: &name})
	if err != nil || ent.Name != "Yard lead" || ent.DefaultName != "Yard Servo 1" {
		t.Errorf("rename = %+v, %v", ent, err)
	}
	yes := true
	if ent, err = h.m.Update(guess, Change{Confirmed: &yes}); err != nil || ent.Suggested {
		t.Errorf("confirm = %+v, %v", ent, err)
	}
	if ent, err = h.m.Update(block, Change{Hidden: &yes}); err != nil || !ent.Hidden {
		t.Errorf("hide = %+v, %v", ent, err)
	}
	if _, err := h.m.Update(turnout, Change{Events: map[string]openlcb.EventID{"throw": 1}}); err == nil {
		t.Error("events of a profile entity should not be editable")
	}
	// A rebuild keeps state and overrides.
	h.m.Rebuild(nodeID)
	if tn = h.get(t, turnout); tn.Name != "Yard lead" || tn.State != StateMoving {
		t.Errorf("after rebuild = %+v", tn)
	}

	// Offline: unavailable, commands refused. Forgotten: gone.
	h.nodes.set(nodeInfo(false))
	h.m.HandleNode(nodeInfo(false))
	if tn = h.get(t, turnout); tn.Available {
		t.Error("turnout available while node offline")
	}
	if _, err := h.m.Command(context.Background(), turnout, "close"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("command while offline: %v", err)
	}
	forgotten := nodeInfo(false)
	forgotten.Forgotten = true
	h.m.HandleNode(forgotten)
	if len(h.m.List()) != 0 {
		t.Errorf("entities after forget: %d", len(h.m.List()))
	}
	h.mu.Lock()
	var removed int
	for _, e := range h.seen {
		if e.Removed {
			removed++
		}
	}
	h.mu.Unlock()
	if removed != 4 {
		t.Errorf("removed notices = %d", removed)
	}
}

func TestManualEntities(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir)
	on, off := other.Event(0x10), other.Event(0x11)
	ent, err := h.m.Create(Manual{Kind: KindSensor, Name: "Yard door", Events: map[string]openlcb.EventID{"on": on, "off": off}})
	if err != nil {
		t.Fatal(err)
	}
	if ent.ID != "sensor.manual.yard_door" || ent.Source != SourceManual || !ent.Available || ent.State != StateUnknown {
		t.Errorf("created = %+v", ent)
	}
	if _, err := h.m.Create(Manual{Kind: KindSensor, Name: "Yard door", Events: map[string]openlcb.EventID{"on": on, "off": off}}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate = %v", err)
	}
	var invalid *InvalidError
	if _, err := h.m.Create(Manual{Kind: KindSensor, Name: "Half", Events: map[string]openlcb.EventID{"on": on}}); !errors.As(err, &invalid) {
		t.Errorf("missing role = %v", err)
	}
	if _, err := h.m.Create(Manual{Kind: "lamp", Name: "x"}); !errors.As(err, &invalid) {
		t.Errorf("unknown kind = %v", err)
	}
	if _, err := h.m.Create(Manual{Kind: KindBlock, Name: "x", Events: map[string]openlcb.EventID{"press": on, "occupied": on, "clear": off}}); !errors.As(err, &invalid) {
		t.Errorf("wrong role = %v", err)
	}
	h.m.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Source: openlcb.NodeHandle{ID: other}, Payload: on.AsSlice()})
	if e := h.get(t, ent.ID); e.State != StateOn {
		t.Errorf("sensor after on: %s", e.State)
	}
	// Its producers are asked about.
	waitFor(t, "identify producer", func() bool {
		_, msgs := h.bus.sent()
		for _, msg := range msgs {
			if msg.MTI == openlcb.MTIIdentifyProducer && len(msg.Payload) == 8 && openlcb.EventIDFromBytes([8]byte(msg.Payload)) == on {
				return true
			}
		}
		return false
	})
	// Events can be changed; the state starts over.
	e, err := h.m.Update(ent.ID, Change{Events: map[string]openlcb.EventID{"on": other.Event(0x12), "off": off}})
	if err != nil || e.Events["on"] != other.Event(0x12) || e.State != StateUnknown {
		t.Errorf("update events = %+v, %v", e, err)
	}
	if err := h.m.Delete("turnout.0a0000000042.servo_1"); !errors.Is(err, ErrNotManual) {
		t.Errorf("delete profile entity = %v", err)
	}

	// A restart loads it from the file.
	again := newHarness(t, dir)
	waitFor(t, "restart", func() bool { _, ok := again.m.Get(ent.ID); return ok })
	if e, _ := again.m.Get(ent.ID); e.Events["on"] != other.Event(0x12) {
		t.Errorf("restored = %+v", e)
	}
	if err := again.m.Delete(ent.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.m.Get(ent.ID); ok {
		t.Error("entity survived Delete")
	}
	third := newHarness(t, dir)
	waitFor(t, "third start", func() bool { return len(third.m.List()) == 4 })
	if _, ok := third.m.Get(ent.ID); ok {
		t.Error("deleted entity came back")
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{"Servo 1": "servo_1", "  Yard Lead--West ": "yard_lead_west", "Track #4": "track_4", "": ""}
	for in, want := range tests {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
