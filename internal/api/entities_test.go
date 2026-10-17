package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/profile"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

type fakeEntities struct {
	mu       sync.Mutex
	ents     map[string]entity.Entity
	commands []string
	subs     []entity.Subscriber
	refresh  int
}

func newFakeEntities() *fakeEntities {
	return &fakeEntities{ents: map[string]entity.Entity{
		"turnout.050101012201.servo_1": {ID: "turnout.050101012201.servo_1", Kind: entity.KindTurnout, Name: "Yard Servo 1", DefaultName: "Yard Servo 1",
			Node: peerID, Source: entity.SourceProfile, Available: true, State: entity.StateClosed, Commands: []string{"throw", "close"}},
		"block.050101012201.track_1": {ID: "block.050101012201.track_1", Kind: entity.KindBlock, Name: "Yard Track 1", DefaultName: "Yard Track 1",
			Node: peerID, Source: entity.SourceProfile, Available: false, State: entity.StateUnknown},
	}}
}

func (f *fakeEntities) List() []entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []entity.Entity
	for _, e := range f.ents {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b entity.Entity) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (f *fakeEntities) Get(id string) (entity.Entity, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	return e, ok
}

func (f *fakeEntities) Command(_ context.Context, id, command string) (entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return entity.Entity{}, entity.ErrNotFound
	}
	if !e.Available {
		return e, entity.ErrUnavailable
	}
	if !slices.Contains(e.Commands, command) {
		return e, entity.ErrUnknownCommand
	}
	f.commands = append(f.commands, id+":"+command)
	e.State = entity.StateMoving
	f.ents[id] = e
	return e, nil
}

func (f *fakeEntities) Update(id string, change entity.Change) (entity.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return entity.Entity{}, entity.ErrNotFound
	}
	if change.Name != nil {
		e.Name = *change.Name
	}
	if change.Hidden != nil {
		e.Hidden = *change.Hidden
	}
	f.ents[id] = e
	return e, nil
}

func (f *fakeEntities) Create(man entity.Manual) (entity.Entity, error) {
	spec, err := man.Spec()
	if err != nil {
		return entity.Entity{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.ents[spec.ID]; exists {
		return entity.Entity{}, entity.ErrExists
	}
	e := entity.Entity{ID: spec.ID, Kind: spec.Kind, Name: spec.Name, Source: entity.SourceManual, Available: true, State: entity.StateUnknown, Events: spec.Events}
	f.ents[spec.ID] = e
	return e, nil
}

func (f *fakeEntities) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ents[id]
	if !ok {
		return entity.ErrNotFound
	}
	if e.Source != entity.SourceManual {
		return entity.ErrNotManual
	}
	delete(f.ents, id)
	return nil
}

func (f *fakeEntities) IdentifyAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh++
}

func (f *fakeEntities) Subscribe(sub entity.Subscriber) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, sub)
	return func() {}
}

func (f *fakeEntities) publish(e entity.Entity) {
	f.mu.Lock()
	subs := append([]entity.Subscriber(nil), f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		sub(e)
	}
}

type fakeEventNames struct {
	mu        sync.Mutex
	refreshed []openlcb.NodeID
	subs      []eventtable.Subscriber
}

func (f *fakeEventNames) entries() []eventtable.Entry {
	return []eventtable.Entry{
		{Node: peerID, NodeName: "Yard", Key: "253:132", Path: []string{"Switch Machines", "Servo 1", "Throw Command Event"}, Name: "Switch Machines / Servo 1 / Throw Command Event", Event: peerID.Event(0), Consumed: true},
		{Node: peerID, NodeName: "Yard", Key: "253:140", Path: []string{"Switch Machines", "Servo 1", "Close Command Event"}, Name: "Switch Machines / Servo 1 / Close Command Event", Event: peerID.Event(1)},
	}
}

func (f *fakeEventNames) Entries() []eventtable.Entry { return f.entries() }

func (f *fakeEventNames) NodeEntries(id openlcb.NodeID) []eventtable.Entry {
	if id != peerID {
		return nil
	}
	return f.entries()[:1]
}

func (f *fakeEventNames) Refresh(id openlcb.NodeID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshed = append(f.refreshed, id)
}

func (f *fakeEventNames) Subscribe(sub eventtable.Subscriber) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, sub)
	return func() {}
}

func (f *fakeEventNames) publish(id openlcb.NodeID) {
	f.mu.Lock()
	subs := append([]eventtable.Subscriber(nil), f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		sub(id)
	}
}

type fakeProfiles struct{}

func (fakeProfiles) List() []*profile.Profile {
	return []*profile.Profile{{Name: "canbus-outpost", Description: "Servo switch machines", Source: "built-in", Match: profile.Match{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost"}}}
}

type entityAPI struct {
	*testAPI
	ents  *fakeEntities
	names *fakeEventNames
}

func newEntityAPI() *entityAPI {
	reg := newFakeRegistry()
	mon := monitor.New(100, nil)
	bus := &fakeBus{links: []transport.Status{{Name: "a", Kind: "serial", Connected: true}}}
	ents := newFakeEntities()
	names := &fakeEventNames{}
	a := New(Deps{Node: fakeNode{}, Registry: reg, Monitor: mon, Bus: bus, Entities: ents, EventNames: names, Profiles: fakeProfiles{}})
	mux := http.NewServeMux()
	a.Register(mux)
	return &entityAPI{testAPI: &testAPI{api: a, reg: reg, mon: mon, bus: bus, mux: mux}, ents: ents, names: names}
}

func TestEntityEndpoints(t *testing.T) {
	ta := newEntityAPI()
	const turnout, block = "turnout.050101012201.servo_1", "block.050101012201.track_1"

	rec, body := ta.do(t, http.MethodGet, "/api/v1/entities", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if list, _ := body["entities"].([]any); len(list) != 2 {
		t.Errorf("entities = %v", body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/entities/kinds", "")
	if kinds, _ := body["kinds"].([]any); rec.Code != 200 || len(kinds) != 6 {
		t.Errorf("kinds: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/entities/"+turnout, "")
	if rec.Code != 200 || body["state"] != "closed" || body["name"] != "Yard Servo 1" {
		t.Errorf("get: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/entities/turnout.nope", ""); rec.Code != 404 {
		t.Errorf("get missing: %d", rec.Code)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/entities/"+turnout+"/command", `{"command":"throw"}`)
	if rec.Code != 202 || body["state"] != "moving" {
		t.Errorf("command: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities/"+turnout+"/command", `{"command":"fly"}`); rec.Code != 400 {
		t.Errorf("unknown command: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities/"+block+"/command", `{"command":"throw"}`); rec.Code != 409 {
		t.Errorf("command while offline: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities/"+turnout+"/command", `nope`); rec.Code != 400 {
		t.Errorf("bad body: %d", rec.Code)
	}
	ta.ents.mu.Lock()
	commands := ta.ents.commands
	ta.ents.mu.Unlock()
	if len(commands) != 1 || commands[0] != turnout+":throw" {
		t.Errorf("commands = %v", commands)
	}

	rec, body = ta.do(t, http.MethodPatch, "/api/v1/entities/"+turnout, `{"name":"Yard lead"}`)
	if rec.Code != 200 || body["name"] != "Yard lead" {
		t.Errorf("rename: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPatch, "/api/v1/entities/turnout.nope", `{"name":"x"}`); rec.Code != 404 {
		t.Errorf("rename missing: %d", rec.Code)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/entities", `{"kind":"sensor","name":"Door","events":{"on":"05.01.01.01.22.01.00.10","off":"05.01.01.01.22.01.00.11"}}`)
	if rec.Code != 201 || body["id"] != "sensor.manual.door" || body["source"] != "manual" {
		t.Errorf("create: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities", `{"kind":"sensor","name":"Door","events":{"on":"05.01.01.01.22.01.00.10","off":"05.01.01.01.22.01.00.11"}}`); rec.Code != 409 {
		t.Errorf("duplicate: %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/entities", `{"kind":"sensor","name":"Half","events":{"on":"05.01.01.01.22.01.00.10"}}`)
	if rec.Code != 400 || !strings.Contains(body["error"].(string), "needs its off event") {
		t.Errorf("invalid: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities", `{"kind":"sensor","name":"Bad","events":{"on":"garbage"}}`); rec.Code != 400 {
		t.Errorf("bad event ID: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/entities/sensor.manual.door", ""); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/entities/"+turnout, ""); rec.Code != 409 {
		t.Errorf("delete profile entity: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities/refresh", ""); rec.Code != 202 {
		t.Errorf("refresh: %d", rec.Code)
	}

	rec, body = ta.do(t, http.MethodGet, "/api/v1/event-names", "")
	if events, _ := body["events"].([]any); rec.Code != 200 || len(events) != 2 {
		t.Errorf("event names: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/event-names?node="+peerID.String(), "")
	if events, _ := body["events"].([]any); rec.Code != 200 || len(events) != 1 {
		t.Errorf("node event names: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/event-names?node=zz", ""); rec.Code != 400 {
		t.Errorf("bad node: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/nodes/"+peerID.String()+"/event-names/refresh", ""); rec.Code != 202 {
		t.Errorf("refresh names: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/nodes/05.01.01.01.22.99/event-names/refresh", ""); rec.Code != 404 {
		t.Errorf("refresh names of unknown node: %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/profiles", "")
	if profiles, _ := body["profiles"].([]any); rec.Code != 200 || len(profiles) != 1 {
		t.Errorf("profiles: %d %v", rec.Code, body)
	}
}

func TestEntityEndpointsWithoutManager(t *testing.T) {
	ta := newTestAPI()
	rec, body := ta.do(t, http.MethodGet, "/api/v1/entities", "")
	if list, _ := body["entities"].([]any); rec.Code != 200 || len(list) != 0 {
		t.Errorf("list: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/entities", `{}`); rec.Code != 503 {
		t.Errorf("create: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/event-names", ""); rec.Code != 200 {
		t.Errorf("event names: %d", rec.Code)
	}
}

func TestEntityEventStream(t *testing.T) {
	ta := newEntityAPI()
	srv := httptest.NewServer(ta.mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events?topics=entities", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	readSSE(t, reader) // ready
	ta.ents.publish(entity.Entity{ID: "turnout.x", Kind: entity.KindTurnout, State: entity.StateThrown})
	if event, data := readSSE(t, reader); event != "entity" || !strings.Contains(data, `"state":"thrown"`) {
		t.Fatalf("entity event = %s %s", event, data)
	}
	ta.names.publish(peerID)
	if event, data := readSSE(t, reader); event != "event_names" || !strings.Contains(data, `"node":"05.01.01.01.22.01"`) {
		t.Fatalf("event_names event = %s %s", event, data)
	}
}
