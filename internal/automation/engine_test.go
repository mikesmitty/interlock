package automation

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	fastclock "github.com/mikesmitty/interlock/internal/clock"
	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

// --- fakes -----------------------------------------------------------------

type fakeEntities struct {
	mu       sync.Mutex
	ents     map[string]entity.Entity
	commands []string
	onChange func(entity.Entity)
}

func newFakeEntities() *fakeEntities {
	return &fakeEntities{ents: map[string]entity.Entity{
		"block.t2":    {ID: "block.t2", Kind: entity.KindBlock, Name: "Track 2", Available: true, State: entity.StateClear},
		"turnout.1":   {ID: "turnout.1", Kind: entity.KindTurnout, Name: "Turnout 1", Available: true, State: entity.StateClosed, Commands: []string{"throw", "close"}},
		"reporter.t2": {ID: "reporter.t2", Kind: entity.KindReporter, Name: "Track 2 reporter", Available: true, State: entity.StateClear},
	}}
}

func (f *fakeEntities) Get(id string) (entity.Entity, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	return e, ok
}

func (f *fakeEntities) List() []entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []entity.Entity
	for _, e := range f.ents {
		out = append(out, e)
	}
	return out
}

func (f *fakeEntities) Command(_ context.Context, id, command string) (entity.Entity, error) {
	f.mu.Lock()
	e, ok := f.ents[id]
	if !ok {
		f.mu.Unlock()
		return entity.Entity{}, entity.ErrNotFound
	}
	if !slices.Contains(e.Commands, command) {
		f.mu.Unlock()
		return e, entity.ErrUnknownCommand
	}
	f.commands = append(f.commands, id+":"+command)
	switch command {
	case "throw":
		e.State = entity.StateThrown
	case "close":
		e.State = entity.StateClosed
	}
	f.ents[id] = e
	f.mu.Unlock()
	if f.onChange != nil {
		f.onChange(e)
	}
	return e, nil
}

func (f *fakeEntities) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.commands)
}

// set changes an entity's state as the bus would and tells the engine.
func (f *fakeEntities) set(id, state string) {
	f.mu.Lock()
	e := f.ents[id]
	e.State = state
	f.ents[id] = e
	f.mu.Unlock()
	if f.onChange != nil {
		f.onChange(e)
	}
}

func (f *fakeEntities) locomotives(locos ...entity.Locomotive) {
	f.mu.Lock()
	e := f.ents["reporter.t2"]
	e.Locomotives = locos
	f.ents["reporter.t2"] = e
	f.mu.Unlock()
	if f.onChange != nil {
		f.onChange(e)
	}
}

func (f *fakeEntities) add(e entity.Entity) {
	f.mu.Lock()
	f.ents[e.ID] = e
	f.mu.Unlock()
	if f.onChange != nil {
		f.onChange(e)
	}
}

type fakeBus struct {
	mu     sync.Mutex
	events []openlcb.EventID
}

func (b *fakeBus) SendEvent(_ context.Context, id openlcb.EventID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, id)
	return nil
}

type fakeNodes struct {
	mu    sync.Mutex
	nodes map[openlcb.NodeID]registry.Info
}

func (n *fakeNodes) Get(id openlcb.NodeID) (registry.Info, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	info, ok := n.nodes[id]
	return info, ok
}

func (n *fakeNodes) List() []registry.Info {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []registry.Info
	for _, info := range n.nodes {
		out = append(out, info)
	}
	return out
}

type memStore struct {
	mu   sync.Mutex
	recs map[string]store.TraceRecord
}

func newMemStore() *memStore { return &memStore{recs: make(map[string]store.TraceRecord)} }

func (m *memStore) SaveTrace(_ context.Context, rec store.TraceRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[rec.ID] = rec
	return nil
}

func (m *memStore) Trace(_ context.Context, id string) (*store.TraceRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[id]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (m *memStore) sorted(automationID string) []store.TraceRecord {
	var out []store.TraceRecord
	for _, rec := range m.recs {
		if automationID == "" || rec.AutomationID == automationID {
			out = append(out, rec)
		}
	}
	slices.SortFunc(out, func(a, b store.TraceRecord) int { return b.StartedAt.Compare(a.StartedAt) })
	return out
}

func (m *memStore) Traces(_ context.Context, automationID string, limit int) ([]store.TraceRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sorted(automationID)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memStore) LatestTraces(_ context.Context) ([]store.TraceRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]bool)
	var out []store.TraceRecord
	for _, rec := range m.sorted("") {
		if !seen[rec.AutomationID] {
			seen[rec.AutomationID] = true
			out = append(out, rec)
		}
	}
	return out, nil
}

func (m *memStore) PruneTraces(_ context.Context, automationID string, keep int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, rec := range m.sorted(automationID) {
		if i >= keep {
			delete(m.recs, rec.ID)
		}
	}
	return nil
}

func (m *memStore) DeleteTraces(_ context.Context, automationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rec := range m.recs {
		if rec.AutomationID == automationID {
			delete(m.recs, id)
		}
	}
	return nil
}

func (m *memStore) count(automationID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sorted(automationID))
}

// --- harness -----------------------------------------------------------------

type harness struct {
	t      *testing.T
	e      *Engine
	ents   *fakeEntities
	bus    *fakeBus
	nodes  *fakeNodes
	trains *fakeTrains
	clock  *fakeClock
	store  *memStore
	done   chan Trace
}

// fakeClock is a fast clock the test sets by hand.
type fakeClock struct {
	mu   sync.Mutex
	fast time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fast
}

// set moves the fast clock and tells the engine, as the real one does.
func (c *fakeClock) set(e *Engine, fast time.Time) {
	c.mu.Lock()
	c.fast = fast
	c.mu.Unlock()
	e.HandleClock(fastclock.State{Time: fast.Format("15:04"), Date: fast.Format("2006-01-02"), Hour: fast.Hour(), Minute: fast.Minute(), Second: fast.Second()})
}

// fakeTrains records train commands.
type fakeTrains struct {
	mu       sync.Mutex
	commands []train.Command
	err      error
}

func (f *fakeTrains) Command(_ context.Context, cmd train.Command) (train.Train, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	if f.err != nil {
		return train.Train{}, f.err
	}
	tr := train.Train{Speed: cmd.Speed, Direction: cmd.Direction, EStop: cmd.Kind == train.CommandEStop}
	if tr.Direction == "" {
		tr.Direction = train.Forward
	}
	return tr, nil
}

func (f *fakeTrains) Name(address uint16, _ bool) string {
	if address == 1234 {
		return "Challenger"
	}
	return ""
}

const peer = openlcb.NodeID(0x050101012200)

func newHarness(t *testing.T, docs ...Automation) *harness {
	t.Helper()
	h := &harness{
		t: t, ents: newFakeEntities(), bus: &fakeBus{}, trains: &fakeTrains{}, clock: &fakeClock{}, store: newMemStore(), done: make(chan Trace, 64),
		nodes: &fakeNodes{nodes: map[openlcb.NodeID]registry.Info{peer: {ID: peer, Online: false, SNIP: &openlcb.SNIP{UserName: "Yard"}}}},
	}
	h.e = New(Options{Entities: h.ents, Bus: h.bus, Nodes: h.nodes, Trains: h.trains, Clock: h.clock, Store: h.store, KeepTraces: 5})
	h.ents.onChange = h.e.HandleEntity
	h.e.SubscribeTraces(func(tr Trace) {
		if tr.Done() {
			h.done <- tr
		}
	})
	for _, doc := range docs {
		if _, err := h.e.Create(doc); err != nil {
			t.Fatalf("Create(%s): %v", doc.Name, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = h.e.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	// Let the dispatcher take its first look before the test changes things.
	time.Sleep(20 * time.Millisecond)
	return h
}

// finished waits for the next finished trace.
func (h *harness) finished() Trace {
	h.t.Helper()
	select {
	case tr := <-h.done:
		return tr
	case <-time.After(3 * time.Second):
		h.t.Fatal("no trace finished in time")
		return Trace{}
	}
}

// quiet asserts no trace finishes for a while.
func (h *harness) quiet(d time.Duration) {
	h.t.Helper()
	select {
	case tr := <-h.done:
		h.t.Fatalf("unexpected trace: %s %s %q", tr.Automation, tr.Result, tr.Error)
	case <-time.After(d):
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s did not happen in time", what)
}

func ms(n int) Duration { return Duration(time.Duration(n) * time.Millisecond) }

func milestone() Automation {
	return Automation{
		Name: "Close Turnout 1", Enabled: true,
		Triggers:   []Trigger{{Type: TriggerState, Entity: "block.t2", To: "occupied"}},
		Conditions: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "thrown"}},
		Actions:    []Action{{Type: ActionDelay, For: ms(30)}, {Type: ActionCommand, Entity: "turnout.1", Command: "close"}},
	}
}

func stepResults(tr Trace) []string {
	out := make([]string, len(tr.Steps))
	for i, s := range tr.Steps {
		out[i] = s.Path + "=" + s.Result
	}
	return out
}

// --- tests ---------------------------------------------------------------------

func TestMilestone(t *testing.T) {
	h := newHarness(t, milestone())
	h.ents.set("turnout.1", entity.StateThrown)
	h.ents.set("block.t2", entity.StateOccupied)
	tr := h.finished()
	if tr.Result != ResultFinished {
		t.Fatalf("result = %s (%s)", tr.Result, tr.Error)
	}
	if tr.Trigger.Description != "Track 2 became occupied" || tr.Trigger.From != "clear" || tr.Trigger.To != "occupied" {
		t.Errorf("trigger = %+v", tr.Trigger)
	}
	want := []string{"conditions[0]=ok", "actions[0]=ok", "actions[1]=ok"}
	if got := stepResults(tr); !slices.Equal(got, want) {
		t.Errorf("steps = %v, want %v", got, want)
	}
	if tr.Steps[0].Description != "Turnout 1 is thrown" || tr.Steps[0].Detail != "was thrown" {
		t.Errorf("condition step = %+v", tr.Steps[0])
	}
	if tr.Steps[1].Detail != "waited 30ms" || tr.Steps[2].Description != "Close Turnout 1" {
		t.Errorf("action steps = %+v", tr.Steps[1:])
	}
	if got := h.ents.sent(); !slices.Equal(got, []string{"turnout.1:close"}) {
		t.Errorf("commands = %v", got)
	}
	if tr.Finished.Sub(tr.Started) < 30*time.Millisecond {
		t.Errorf("run took %v, under the delay", tr.Finished.Sub(tr.Started))
	}

	// The turnout is closed now, so the next train does not run the actions.
	h.ents.set("block.t2", entity.StateClear)
	h.ents.set("block.t2", entity.StateOccupied)
	tr = h.finished()
	if tr.Result != ResultNotRun || len(tr.Steps) != 1 || tr.Steps[0].Result != StepFalse || tr.Steps[0].Detail != "was closed" {
		t.Errorf("second run = %s %v", tr.Result, stepResults(tr))
	}

	s, _ := h.e.Get("close_turnout_1")
	if s.LastResult != ResultNotRun || s.LastTrace != tr.ID || s.Running != 0 || len(s.Problems) != 0 {
		t.Errorf("status = %+v", s)
	}
	if h.store.count("close_turnout_1") != 2 {
		t.Errorf("stored traces = %d", h.store.count("close_turnout_1"))
	}
	list, err := h.e.Traces(context.Background(), "close_turnout_1", 0)
	if err != nil || len(list) != 2 || list[0].ID != tr.ID {
		t.Errorf("Traces = %v, %v", list, err)
	}
	got, err := h.e.Trace(context.Background(), "close_turnout_1", tr.ID)
	if err != nil || got.ID != tr.ID || len(got.Steps) != 1 {
		t.Errorf("Trace = %+v, %v", got, err)
	}
}

func TestStateTriggerFilters(t *testing.T) {
	doc := milestone()
	doc.Name = "From clear"
	doc.Triggers[0].From = "clear"
	doc.Conditions = nil
	doc.Actions = []Action{{Type: ActionLog, Message: "went"}}
	other := Automation{Name: "Unknown entity", Enabled: true,
		Triggers: []Trigger{{Type: TriggerState, Entity: "sensor.new", To: "on"}},
		Actions:  []Action{{Type: ActionLog, Message: "new"}}}
	h := newHarness(t, doc, other)

	h.ents.set("block.t2", entity.StateUnknown)
	h.ents.set("block.t2", entity.StateOccupied) // from unknown: no
	h.quiet(50 * time.Millisecond)
	h.ents.set("block.t2", entity.StateClear)
	h.ents.set("block.t2", entity.StateOccupied) // from clear: yes
	if tr := h.finished(); tr.Automation != "from_clear" || tr.Result != ResultFinished {
		t.Errorf("trace = %+v", tr)
	}
	// The same state again is not a change.
	h.ents.set("block.t2", entity.StateOccupied)
	h.quiet(50 * time.Millisecond)

	// An entity seen for the first time does not fire, even in the state wanted.
	if s, _ := h.e.Get("unknown_entity"); len(s.Problems) != 1 || !strings.Contains(s.Problems[0], "no entity sensor.new") {
		t.Errorf("problems = %v", s.Problems)
	}
	h.ents.add(entity.Entity{ID: "sensor.new", Kind: entity.KindSensor, Name: "New", Available: true, State: entity.StateOn})
	h.quiet(50 * time.Millisecond)
	eventually(t, "problem cleared", func() bool { s, _ := h.e.Get("unknown_entity"); return len(s.Problems) == 0 })
	h.ents.set("sensor.new", entity.StateOff)
	h.ents.set("sensor.new", entity.StateOn)
	if tr := h.finished(); tr.Automation != "unknown_entity" {
		t.Errorf("trace = %+v", tr)
	}
}

func TestForHold(t *testing.T) {
	doc := milestone()
	doc.Triggers[0].For = ms(80)
	doc.Conditions = nil
	doc.Actions = []Action{{Type: ActionLog, Message: "held"}}
	h := newHarness(t, doc)
	h.ents.set("block.t2", entity.StateOccupied)
	time.Sleep(20 * time.Millisecond)
	h.ents.set("block.t2", entity.StateClear) // too short
	h.quiet(150 * time.Millisecond)
	h.ents.set("block.t2", entity.StateOccupied)
	start := time.Now()
	tr := h.finished()
	if tr.Result != ResultFinished || time.Since(start) < 80*time.Millisecond {
		t.Errorf("hold: %s after %v", tr.Result, time.Since(start))
	}
	if !strings.Contains(tr.Trigger.Description, "stayed so for 80ms") {
		t.Errorf("description = %q", tr.Trigger.Description)
	}
}

func TestEventAndNodeAndStartTriggers(t *testing.T) {
	ev := openlcb.EventID(0x0501010122000007)
	docs := []Automation{
		{Name: "On event", Enabled: true, Triggers: []Trigger{{Type: TriggerEvent, Event: ev}}, Actions: []Action{{Type: ActionEvent, Event: ev + 1}}},
		{Name: "On node", Enabled: true, Triggers: []Trigger{{Type: TriggerNode, Node: peer, To: "online"}}, Actions: []Action{{Type: ActionLog, Message: "up"}}},
		{Name: "On start", Enabled: true, Triggers: []Trigger{{Type: TriggerStart}}, Actions: []Action{{Type: ActionLog, Message: "hello"}}},
	}
	h := newHarness(t, docs...)
	// Start fired when Run began.
	if tr := h.finished(); tr.Automation != "on_start" || tr.Trigger.Type != TriggerStart {
		t.Errorf("start trace = %+v", tr)
	}
	h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: ev.AsSlice()})
	tr := h.finished()
	if tr.Automation != "on_event" || tr.Trigger.Event != ev {
		t.Errorf("event trace = %+v", tr)
	}
	h.bus.mu.Lock()
	sent := slices.Clone(h.bus.events)
	h.bus.mu.Unlock()
	if !slices.Equal(sent, []openlcb.EventID{ev + 1}) {
		t.Errorf("bus got %v", sent)
	}
	h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: ev.AsSlice()})
	h.quiet(30 * time.Millisecond)

	h.e.HandleNode(registry.Info{ID: peer, Online: false})
	h.quiet(30 * time.Millisecond)
	h.e.HandleNode(registry.Info{ID: peer, Online: true, SNIP: &openlcb.SNIP{UserName: "Yard"}})
	tr = h.finished()
	if tr.Automation != "on_node" || tr.Trigger.Description != "Yard came online" || tr.Trigger.NodeName != "Yard" {
		t.Errorf("node trace = %+v", tr.Trigger)
	}
}

func TestLocomotiveTrigger(t *testing.T) {
	loco := entity.Locomotive{Address: detector.Address{Type: detector.Long, Number: 1234}, Name: "1234"}
	other := entity.Locomotive{Address: detector.Address{Type: detector.Short, Number: 3}, Name: "3 (short)"}
	docs := []Automation{
		{Name: "Any enters", Enabled: true, Triggers: []Trigger{{Type: TriggerLocomotive, Entity: "reporter.t2", Direction: "entered"}}, Actions: []Action{{Type: ActionLog, Message: "in"}}},
		{Name: "1234 leaves", Enabled: true, Triggers: []Trigger{{Type: TriggerLocomotive, Entity: "reporter.t2", Address: 1234, Direction: "left"}}, Actions: []Action{{Type: ActionLog, Message: "out"}}},
	}
	h := newHarness(t, docs...)
	h.ents.locomotives(other)
	tr := h.finished()
	if tr.Automation != "any_enters" || tr.Trigger.Locomotive != "3 (short)" {
		t.Errorf("trace = %+v", tr.Trigger)
	}
	h.ents.locomotives(other, loco)
	if tr := h.finished(); tr.Automation != "any_enters" || tr.Trigger.Locomotive != "1234" {
		t.Errorf("trace = %+v", tr.Trigger)
	}
	h.ents.locomotives(loco) // 3 left: nobody cares
	h.quiet(40 * time.Millisecond)
	h.ents.locomotives()
	if tr := h.finished(); tr.Automation != "1234_leaves" || tr.Trigger.Description != "Locomotive 1234 left Track 2 reporter" {
		t.Errorf("trace = %+v", tr.Trigger)
	}
}

func TestIntervalAndTimeTriggers(t *testing.T) {
	docs := []Automation{
		{Name: "Tick", Enabled: true, Triggers: []Trigger{{Type: TriggerInterval, Every: ms(40)}}, Actions: []Action{{Type: ActionLog, Message: "tick"}}},
		{Name: "Later", Enabled: true, Triggers: []Trigger{{Type: TriggerTime, At: "23:59"}}, Actions: []Action{{Type: ActionLog, Message: "late"}}},
	}
	h := newHarness(t, docs...)
	first := h.finished()
	second := h.finished()
	if first.Automation != "tick" || second.Automation != "tick" || second.Started.Sub(first.Started) < 30*time.Millisecond {
		t.Errorf("ticks = %v then %v", first.Started, second.Started)
	}
	if first.Trigger.Description != "40ms passed" {
		t.Errorf("description = %q", first.Trigger.Description)
	}
	if _, err := h.e.SetEnabled("tick", false); err != nil {
		t.Fatal(err)
	}
	// Drain what was in flight, then expect silence.
	time.Sleep(60 * time.Millisecond)
	for len(h.done) > 0 {
		<-h.done
	}
	h.quiet(100 * time.Millisecond)
}

func TestModes(t *testing.T) {
	slow := func(name, mode string) Automation {
		return Automation{Name: name, Mode: mode, Enabled: true,
			Triggers: []Trigger{{Type: TriggerEvent, Event: 0x10}},
			Actions:  []Action{{Type: ActionDelay, For: ms(60)}}}
	}
	h := newHarness(t, slow("Single", ModeSingle), slow("Restart", ModeRestart), slow("Queued", ModeQueued), slow("Parallel", ModeParallel))
	fire := func() {
		h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventID(0x10).AsSlice()})
	}
	fire()
	time.Sleep(20 * time.Millisecond)
	fire()
	results := map[string][]string{}
	for range 8 {
		tr := h.finished()
		results[tr.Automation] = append(results[tr.Automation], tr.Result)
	}
	want := map[string][]string{
		"single":   {ResultSkipped, ResultFinished},
		"restart":  {ResultCanceled, ResultFinished},
		"queued":   {ResultFinished, ResultFinished},
		"parallel": {ResultFinished, ResultFinished},
	}
	for id, w := range want {
		if !slices.Equal(results[id], w) {
			t.Errorf("%s: %v, want %v", id, results[id], w)
		}
	}
	list, _ := h.e.Traces(context.Background(), "queued", 0)
	if len(list) != 2 || list[0].Started.Sub(list[1].Finished) < 0 {
		t.Errorf("queued runs overlapped: %v", list)
	}
	list, _ = h.e.Traces(context.Background(), "parallel", 0)
	if len(list) != 2 || list[0].Started.After(list[1].Finished) {
		t.Errorf("parallel runs did not overlap: %v", list)
	}
	list, _ = h.e.Traces(context.Background(), "single", 0)
	skipped := list[slices.IndexFunc(list, func(tr Trace) bool { return tr.Result == ResultSkipped })]
	if !strings.Contains(skipped.Error, "already running") {
		t.Errorf("skipped trace error = %q", skipped.Error)
	}
}

func TestWaitIfChooseRepeatStop(t *testing.T) {
	doc := Automation{Name: "Complex", Enabled: true,
		Triggers: []Trigger{{Type: TriggerEvent, Event: 0x20}},
		Actions: []Action{
			{Type: ActionWait, Triggers: []Trigger{{Type: TriggerState, Entity: "block.t2", To: "clear"}}, Timeout: ms(500)},
			{Type: ActionIf, Conditions: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "closed"}},
				Then: []Action{{Type: ActionCommand, Entity: "turnout.1", Command: "throw"}}, Else: []Action{{Type: ActionStop, Reason: "nope"}}},
			{Type: ActionChoose, Choices: []Choice{
				{Name: "thrown", Conditions: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "thrown"}}, Actions: []Action{{Type: ActionLog, Message: "is thrown"}}},
			}, Default: []Action{{Type: ActionLog, Message: "default"}}},
			{Type: ActionRepeat, Count: 2, Actions: []Action{{Type: ActionCommand, Entity: "turnout.1", Command: "close"}, {Type: ActionCommand, Entity: "turnout.1", Command: "throw"}}},
			{Type: ActionRepeat, Until: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "closed"}}, Actions: []Action{{Type: ActionCommand, Entity: "turnout.1", Command: "close"}}},
			{Type: ActionLog, Message: "disabled", Disabled: true},
			{Type: ActionStop, Reason: "all done"},
			{Type: ActionLog, Message: "never"},
		}}
	h := newHarness(t, doc)
	h.ents.set("block.t2", entity.StateOccupied)
	h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventID(0x20).AsSlice()})
	time.Sleep(30 * time.Millisecond)
	eventually(t, "the wait", func() bool { s, _ := h.e.Get("complex"); return s.Running == 1 })
	h.ents.set("block.t2", entity.StateClear)
	tr := h.finished()
	if tr.Result != ResultStopped || tr.Error != "all done" {
		t.Fatalf("result = %s %q", tr.Result, tr.Error)
	}
	// A block's step comes before the steps inside it.
	want := []string{
		"actions[0]=ok",
		"actions[1]=ok", "actions[1].conditions[0]=ok", "actions[1].then[0]=ok",
		"actions[2]=ok", "actions[2].choices[0].conditions[0]=ok", "actions[2].choices[0].actions[0]=ok",
		"actions[3]=ok", "actions[3].repeat[0]=ok", "actions[3].repeat[0].actions[0]=ok", "actions[3].repeat[0].actions[1]=ok",
		"actions[3].repeat[1]=ok", "actions[3].repeat[1].actions[0]=ok", "actions[3].repeat[1].actions[1]=ok",
		"actions[4]=ok", "actions[4].repeat[0]=ok", "actions[4].repeat[0].actions[0]=ok", "actions[4].repeat[0].until[0]=ok",
		"actions[5]=skipped", "actions[6]=ok",
	}
	if got := stepResults(tr); !slices.Equal(got, want) {
		t.Errorf("steps =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.HasPrefix(tr.Steps[0].Detail, "Track 2 became clear after ") {
		t.Errorf("wait detail = %q", tr.Steps[0].Detail)
	}
	if tr.Steps[1].Detail != "took then" || tr.Steps[4].Detail != "chose thrown" || tr.Steps[7].Detail != "ran 2 times" || tr.Steps[14].Detail != "ran 1 time" {
		t.Errorf("details = %q %q %q %q", tr.Steps[1].Detail, tr.Steps[4].Detail, tr.Steps[7].Detail, tr.Steps[14].Detail)
	}
	if got := h.ents.sent(); !slices.Equal(got, []string{"turnout.1:throw", "turnout.1:close", "turnout.1:throw", "turnout.1:close", "turnout.1:throw", "turnout.1:close"}) {
		t.Errorf("commands = %v", got)
	}

	// A wait that times out with stop_on_timeout stops the run; one that
	// fails on an unknown command errors.
	timeout := Automation{Name: "Timeout", Enabled: true, Triggers: []Trigger{{Type: TriggerEvent, Event: 0x21}},
		Actions: []Action{{Type: ActionWait, Triggers: []Trigger{{Type: TriggerState, Entity: "block.t2", To: "occupied"}}, Timeout: ms(30), StopOnTimeout: true}}}
	bad := Automation{Name: "Bad", Enabled: true, Triggers: []Trigger{{Type: TriggerEvent, Event: 0x22}},
		Actions: []Action{{Type: ActionCommand, Entity: "block.t2", Command: "close"}}}
	for _, d := range []Automation{timeout, bad} {
		if _, err := h.e.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := h.e.Get("bad"); len(s.Problems) != 1 || !strings.Contains(s.Problems[0], "takes no commands") {
		t.Errorf("bad problems = %v", s.Problems)
	}
	h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventID(0x21).AsSlice()})
	if tr := h.finished(); tr.Result != ResultStopped || tr.Steps[0].Result != StepTimeout {
		t.Errorf("timeout = %s %v", tr.Result, stepResults(tr))
	}
	h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventID(0x22).AsSlice()})
	if tr := h.finished(); tr.Result != ResultError || tr.Steps[0].Result != StepError || tr.Steps[0].Error == "" {
		t.Errorf("bad = %s %v", tr.Result, stepResults(tr))
	}
}

func TestManualRunAndStop(t *testing.T) {
	doc := milestone()
	doc.Actions[0].For = ms(500)
	h := newHarness(t, doc)
	// Conditions are skipped by default: the turnout is closed.
	tr, err := h.e.Trigger("close_turnout_1", false)
	if err != nil || tr.Result != ResultRunning || tr.Trigger.Type != "manual" {
		t.Fatalf("Trigger = %+v, %v", tr, err)
	}
	var live Trace
	eventually(t, "the delay step", func() bool {
		var err error
		live, err = h.e.Trace(context.Background(), "close_turnout_1", tr.ID)
		return err == nil && len(live.Steps) == 1
	})
	if live.Result != ResultRunning || live.Steps[0].Result != StepRunning {
		t.Errorf("live trace = %+v", live)
	}
	if err := h.e.Stop("close_turnout_1"); err != nil {
		t.Fatal(err)
	}
	done := h.finished()
	if done.Result != ResultCanceled || done.Error != "Stopped by hand" || done.Steps[0].Result != StepCanceled {
		t.Errorf("stopped = %s %q %v", done.Result, done.Error, stepResults(done))
	}
	if err := h.e.Stop("close_turnout_1"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Stop when idle = %v", err)
	}
	// With the conditions checked, the closed turnout keeps it from running.
	if tr, err := h.e.Trigger("close_turnout_1", true); err != nil || tr.Result != ResultRunning {
		t.Fatalf("Trigger = %+v, %v", tr, err)
	}
	if tr := h.finished(); tr.Result != ResultNotRun {
		t.Errorf("checked = %s", tr.Result)
	}
	if _, err := h.e.Trigger("nope", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("Trigger(nope) = %v", err)
	}
}

func TestCreateUpdateDelete(t *testing.T) {
	h := newHarness(t, milestone())
	dup, err := h.e.Create(milestone())
	if err != nil || dup.ID != "close_turnout_1_2" {
		t.Errorf("second Create = %s, %v", dup.ID, err)
	}
	explicit := milestone()
	explicit.ID = "close_turnout_1"
	if _, err := h.e.Create(explicit); !errors.Is(err, ErrExists) {
		t.Errorf("explicit duplicate = %v", err)
	}
	broken := milestone()
	broken.Triggers = nil
	if _, err := h.e.Create(broken); err == nil {
		t.Error("invalid Create succeeded")
	}

	var statuses []Status
	var mu sync.Mutex
	h.e.Subscribe(func(s Status) {
		mu.Lock()
		statuses = append(statuses, s)
		mu.Unlock()
	})

	// Disabling cancels a run in progress.
	long := milestone()
	long.Actions[0].For = ms(500)
	if _, err := h.e.Update("close_turnout_1", long); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Trigger("close_turnout_1", false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running", func() bool { s, _ := h.e.Get("close_turnout_1"); return s.Running == 1 })
	if _, err := h.e.SetEnabled("close_turnout_1", false); err != nil {
		t.Fatal(err)
	}
	if tr := h.finished(); tr.Result != ResultCanceled || tr.Error != "The automation was turned off" {
		t.Errorf("after disable = %s %q", tr.Result, tr.Error)
	}
	h.ents.set("turnout.1", entity.StateThrown)
	h.ents.set("block.t2", entity.StateOccupied)
	if tr := h.finished(); tr.Automation != "close_turnout_1_2" { // the copy still runs
		t.Errorf("trace = %+v", tr)
	}
	h.quiet(50 * time.Millisecond) // the disabled original does not

	if _, err := h.e.Update("nope", milestone()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(nope) = %v", err)
	}
	if err := h.e.Delete("close_turnout_1_2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.e.Get("close_turnout_1_2"); ok {
		t.Error("deleted automation still listed")
	}
	if err := h.e.Delete("close_turnout_1_2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v", err)
	}
	mu.Lock()
	last := statuses[len(statuses)-1]
	mu.Unlock()
	if last.ID != "close_turnout_1_2" || !last.Removed {
		t.Errorf("last status = %+v", last)
	}
	if list := h.e.List(); len(list) != 1 || list[0].ID != "close_turnout_1" || list[0].Enabled {
		t.Errorf("List = %+v", list)
	}
}

func TestTracesPrunedAndRestored(t *testing.T) {
	doc := Automation{Name: "Quick", Enabled: true, Triggers: []Trigger{{Type: TriggerEvent, Event: 0x30}}, Actions: []Action{{Type: ActionLog, Message: "x"}}}
	h := newHarness(t, doc)
	for range 7 {
		h.e.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: openlcb.EventID(0x30).AsSlice()})
		h.finished()
	}
	eventually(t, "pruning", func() bool { return h.store.count("quick") == 5 })
	list, _ := h.e.Traces(context.Background(), "quick", 3)
	if len(list) != 3 {
		t.Errorf("Traces(limit 3) = %d", len(list))
	}
	var tr Trace
	if err := json.Unmarshal(h.store.sorted("quick")[0].JSON, &tr); err != nil || tr.Result != ResultFinished {
		t.Errorf("stored JSON: %v %v", tr.Result, err)
	}

	// A new engine over the same store restores the last result.
	f, _ := OpenFile(t.TempDir() + "/a.yaml")
	_ = f.Put(doc.Normalize())
	e2 := New(Options{Entities: h.ents, Bus: h.bus, Store: h.store, File: f})
	e2.prime()
	s, _ := e2.Get("quick")
	if s.LastResult != ResultFinished || s.LastTrace == "" || s.LastTriggered.IsZero() {
		t.Errorf("restored status = %+v", s)
	}
}

func TestTrainAction(t *testing.T) {
	h := newHarness(t, Automation{
		Name:     "Stop the Challenger",
		Enabled:  true,
		Triggers: []Trigger{{Type: TriggerLocomotive, Entity: "reporter.t2", Address: 1234, Direction: "entered"}},
		Actions: []Action{
			{Type: ActionTrain, Address: 1234, Command: train.CommandSpeed, Speed: 30, Direction: train.Reverse},
			{Type: ActionTrain, Address: 1234, Command: train.CommandFunction, Function: 2, State: "on"},
			{Type: ActionTrain, Address: 3, AddressType: "short", Command: train.CommandStop},
			{Type: ActionTrain, Address: 1234, Command: train.CommandEStop},
		},
	})
	h.ents.locomotives(entity.Locomotive{Address: detector.Address{Type: detector.Long, Number: 1234}, Name: "1234"})
	tr := h.finished()
	if tr.Result != ResultFinished {
		t.Fatalf("result = %s (%s)", tr.Result, tr.Error)
	}
	if len(tr.Steps) != 4 || tr.Steps[0].Description != "Set Challenger to speed 30 reverse" || tr.Steps[0].Detail != "speed 30 reverse" ||
		tr.Steps[1].Description != "Turn on F2 of Challenger" || tr.Steps[2].Description != "Stop locomotive 3 (short)" ||
		tr.Steps[3].Description != "Emergency stop Challenger" || tr.Steps[3].Detail != "emergency stopped" {
		for _, s := range tr.Steps {
			t.Logf("%s: %q %q", s.Path, s.Description, s.Detail)
		}
		t.Fatal("unexpected steps")
	}
	h.trains.mu.Lock()
	cmds := h.trains.commands
	h.trains.mu.Unlock()
	if len(cmds) != 4 || cmds[0] != (train.Command{Address: 1234, Long: true, Kind: train.CommandSpeed, Speed: 30, Direction: train.Reverse}) ||
		cmds[1] != (train.Command{Address: 1234, Long: true, Kind: train.CommandFunction, Function: 2, On: true}) ||
		cmds[2] != (train.Command{Address: 3, Kind: train.CommandStop}) || cmds[3].Kind != train.CommandEStop {
		t.Errorf("commands = %+v", cmds)
	}

	// A failing command fails the run.
	h.trains.mu.Lock()
	h.trains.err = errors.New("no command station answered")
	h.trains.mu.Unlock()
	if _, err := h.e.Trigger("stop_the_challenger", false); err != nil {
		t.Fatal(err)
	}
	if tr = h.finished(); tr.Result != ResultError || !strings.Contains(tr.Error, "no command station") {
		t.Errorf("result = %s (%s)", tr.Result, tr.Error)
	}

	// Without a train manager the action is reported as a problem.
	e := New(Options{Entities: h.ents, Bus: h.bus, Nodes: h.nodes, Store: newMemStore()})
	s, err := e.Create(Automation{Name: "x", Triggers: []Trigger{{Type: TriggerStart}}, Actions: []Action{{Type: ActionTrain, Address: 5, Command: train.CommandStop}}})
	if err != nil || len(s.Problems) != 1 {
		t.Errorf("status = %+v, %v", s, err)
	}
}

func TestClockTriggerAndCondition(t *testing.T) {
	h := newHarness(t, Automation{
		Name: "Morning freight", Enabled: true,
		Triggers:   []Trigger{{Type: TriggerClock, At: "06:42", Weekdays: []string{"fri"}}},
		Conditions: []Condition{{Type: ConditionClock, After: "06:00", Before: "07:00"}},
		Actions:    []Action{{Type: ActionLog, Message: "go"}},
	}, Automation{
		Name: "Night freight", Enabled: true,
		Triggers:   []Trigger{{Type: TriggerClock, At: "22:00"}},
		Conditions: []Condition{{Type: ConditionClock, After: "06:00", Before: "07:00"}},
		Actions:    []Action{{Type: ActionLog, Message: "go"}},
	})
	// 1957-06-21 was a Friday.
	h.clock.set(h.e, time.Date(1957, 6, 21, 6, 41, 0, 0, time.UTC))
	h.quiet(100 * time.Millisecond)
	h.clock.set(h.e, time.Date(1957, 6, 21, 6, 42, 0, 0, time.UTC))
	tr := h.finished()
	if tr.Result != ResultFinished || tr.Trigger.Description != "The fast clock read 06:42" {
		t.Fatalf("trace = %s %q trigger %q", tr.Result, tr.Error, tr.Trigger.Description)
	}
	if len(tr.Steps) < 1 || tr.Steps[0].Detail != "the fast clock read 06:42 Fri" {
		t.Errorf("condition step = %+v", tr.Steps[0])
	}
	// The next day is a Saturday: the trigger stays quiet.
	h.clock.set(h.e, time.Date(1957, 6, 22, 6, 42, 0, 0, time.UTC))
	h.quiet(100 * time.Millisecond)
	// At night the other automation fires, and its window condition fails.
	h.clock.set(h.e, time.Date(1957, 6, 22, 22, 0, 0, 0, time.UTC))
	tr = h.finished()
	if tr.Automation != "night_freight" || tr.Result != ResultNotRun {
		t.Errorf("night trace = %s %s", tr.Automation, tr.Result)
	}
	// Without a clock, the automation shows a problem.
	e := New(Options{Entities: h.ents, Bus: h.bus, Nodes: h.nodes, Store: newMemStore()})
	st, err := e.Create(Automation{Name: "No clock", Enabled: true, Triggers: []Trigger{{Type: TriggerClock, At: "07:00"}}, Actions: []Action{{Type: ActionLog, Message: "x"}}})
	if err != nil || len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "no fast clock") {
		t.Errorf("problems without a clock = %v, %v", st.Problems, err)
	}
}
