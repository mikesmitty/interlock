package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/traction"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// fakeTrainBus plays a command station for the train manager: every
// search finds the DCC node for the address, and every train obeys.
type fakeTrainBus struct {
	mu       sync.Mutex
	commands [][]byte
	handler  node.TractionHandler
}

func (b *fakeTrainBus) ID() openlcb.NodeID                               { return 0x0A0000000001 }
func (b *fakeTrainBus) SendEvent(context.Context, openlcb.EventID) error { return nil }
func (b *fakeTrainBus) Send(context.Context, openlcb.Message) error      { return nil }
func (b *fakeTrainBus) HandleTraction(h node.TractionHandler)            { b.handler = h }
func (b *fakeTrainBus) VerifyAlias(context.Context, openlcb.Alias) (openlcb.NodeID, error) {
	return 0, errors.New("unknown")
}

func (b *fakeTrainBus) Producers(_ context.Context, ev openlcb.EventID, _ time.Duration, _ bool) ([]node.Producer, error) {
	query, flags, ok := traction.ParseSearch(ev)
	if !ok || query == "" {
		return nil, nil
	}
	var address uint16
	for _, r := range query {
		address = address*10 + uint16(r-'0')
	}
	long := flags&traction.SearchLong != 0 || address > 127
	return []node.Producer{{Node: traction.DCCNodeID(address, long), Alias: 0x123, State: "valid"}}, nil
}

func (b *fakeTrainBus) TractionCommand(_ context.Context, _ openlcb.NodeID, payload []byte, reply bool) ([]byte, error) {
	b.mu.Lock()
	b.commands = append(b.commands, append([]byte(nil), payload...))
	b.mu.Unlock()
	if !reply {
		return nil, nil
	}
	switch payload[0] {
	case traction.CmdQuerySpeeds:
		return []byte{0x10, 0, 0, 0, 0, 0, 0, 0}, nil
	case traction.CmdQueryFunction:
		return []byte{0x11, payload[1], payload[2], payload[3], 0, 0}, nil
	case traction.CmdController:
		if payload[1] == traction.ControllerAssign {
			return []byte{0x20, 0x01, 0x00}, nil
		}
		return append([]byte{0x20, 0x03, 0}, b.ID().AsSlice()...), nil
	case traction.CmdConsist:
		return append(append([]byte{0x30, payload[1]}, payload[3:9]...), 0, 0), nil
	}
	return nil, errors.New("unexpected command")
}

type fakeProgrammer struct {
	track programmer.Track
	ops   []programmer.Op
}

func (p *fakeProgrammer) Track() programmer.Track                        { return p.track }
func (p *fakeProgrammer) Find(context.Context) (programmer.Track, error) { return p.track, nil }
func (p *fakeProgrammer) History() []programmer.Op                       { return p.ops }
func (p *fakeProgrammer) Subscribe(programmer.Subscriber) func()         { return func() {} }
func (p *fakeProgrammer) Read(_ context.Context, cv int) (programmer.Op, error) {
	if cv < 1 {
		return programmer.Op{}, &train.InvalidError{Reason: "CV numbers run from 1 to 1024"}
	}
	op := programmer.Op{ID: len(p.ops) + 1, Kind: "read", CV: cv, Value: 13, OK: true}
	p.ops = append(p.ops, op)
	return op, nil
}

func (p *fakeProgrammer) Write(_ context.Context, cv, value int) (programmer.Op, error) {
	op := programmer.Op{ID: len(p.ops) + 1, Kind: "write", CV: cv, Value: value, Error: "No decoder answered. Is a locomotive on the programming track?"}
	p.ops = append(p.ops, op)
	return op, &openlcb.Error{Code: programmer.CodeNoLocomotive}
}

func (p *fakeProgrammer) WritePOM(_ context.Context, id string, cv, value int) (programmer.Op, error) {
	if id == "nope" {
		return programmer.Op{}, train.ErrNotFound
	}
	op := programmer.Op{ID: len(p.ops) + 1, Kind: "pom", Train: id, CV: cv, Value: value, OK: true}
	p.ops = append(p.ops, op)
	return op, nil
}

func (p *fakeProgrammer) WriteLongAddressPOM(_ context.Context, id string, cv17, cv18 int) (programmer.Op, error) {
	op := programmer.Op{ID: len(p.ops) + 1, Kind: "pom_address", Train: id, CV: 17, Value: (cv17&0x3F)<<8 | cv18, OK: true}
	p.ops = append(p.ops, op)
	return op, nil
}

func (p *fakeProgrammer) ReadPOM(_ context.Context, id string, cv int) (programmer.Op, error) {
	if id == "nope" {
		return programmer.Op{}, train.ErrNotFound
	}
	op := programmer.Op{ID: len(p.ops) + 1, Kind: "pom_read", Train: id, CV: cv, Value: 13, OK: true}
	p.ops = append(p.ops, op)
	return op, nil
}

func newTrainAPI(t *testing.T) (*testAPI, *fakeTrainBus) {
	t.Helper()
	reg := newFakeRegistry()
	mon := monitor.New(100, nil)
	bus := &fakeBus{links: []transport.Status{{Name: "a", Kind: "serial", Connected: true}}}
	tb := &fakeTrainBus{}
	trains := train.New(train.Options{Bus: tb, SearchWindow: time.Millisecond, Functions: 3})
	prog := &fakeProgrammer{track: programmer.Track{Node: 0x0A0000000077, Name: "Bench", Online: true}}
	a := New(Deps{Node: fakeNode{}, Registry: reg, Monitor: mon, Bus: bus, Trains: trains, Programmer: prog})
	mux := http.NewServeMux()
	a.Register(mux)
	return &testAPI{api: a, reg: reg, mon: mon, bus: bus, mux: mux}, tb
}

func TestTrainEndpoints(t *testing.T) {
	ta, tb := newTrainAPI(t)

	rec, body := ta.do(t, http.MethodGet, "/api/v1/trains", "")
	if rec.Code != http.StatusOK || len(body["trains"].([]any)) != 0 || body["layout"] == nil {
		t.Fatalf("GET trains = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains", `{"name": "Challenger", "address": 1234, "functions": {"0": "Headlight"}}`)
	if rec.Code != http.StatusCreated || body["id"] != "challenger" || body["long"] != true || body["roster"] != true {
		t.Fatalf("POST trains = %d %v", rec.Code, body)
	}
	// Locomotives may share an address.
	if rec, body = ta.do(t, http.MethodPost, "/api/v1/trains", `{"name": "Again", "address": 1234}`); rec.Code != http.StatusCreated || body["id"] != "again" {
		t.Errorf("same address = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains", `{"name": "Bad", "address": 0}`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPatch, "/api/v1/trains/challenger", `{"name": "UP 3985", "road_name": "Union Pacific"}`)
	if rec.Code != http.StatusOK || body["name"] != "UP 3985" || body["road_name"] != "Union Pacific" || body["functions"] == nil {
		t.Fatalf("PATCH = %d %v", rec.Code, body)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/acquire", "")
	if rec.Code != http.StatusOK || body["controlled"] != true || body["node"] != "06.01.00.00.C4.D2" || body["known"] != true {
		t.Fatalf("acquire = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/speed", `{"speed": 30, "direction": "reverse"}`)
	if rec.Code != http.StatusAccepted || body["speed"] != 30.0 || body["direction"] != "reverse" {
		t.Fatalf("speed = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/1234/speed", `{"speed": 500}`); rec.Code != http.StatusBadRequest {
		t.Errorf("speed 500 = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/function", `{"function": 0, "on": true}`)
	if rec.Code != http.StatusAccepted || len(body["on"].([]any)) != 1 {
		t.Fatalf("function = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/1234/function", `{"on": true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("function without number = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/estop", "")
	if rec.Code != http.StatusAccepted || body["estop"] != true {
		t.Fatalf("estop = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/refresh", "")
	if rec.Code != http.StatusOK || body["controlled"] != true {
		t.Fatalf("refresh = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/release", "")
	if rec.Code != http.StatusOK || body["controlled"] != false {
		t.Fatalf("release = %d %v", rec.Code, body)
	}

	// A train outside the roster, by address.
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/3/speed", `{"speed": 5}`)
	if rec.Code != http.StatusAccepted || body["roster"] != false || body["id"] != "3" {
		t.Fatalf("speed for 3 = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodGet, "/api/v1/trains/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET nope = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/search", `{"query": "12"}`)
	if rec.Code != http.StatusOK || len(body["trains"].([]any)) != 1 {
		t.Fatalf("search = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/search", `{"query": "x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad search = %d", rec.Code)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/layout", `{"power": false}`)
	if rec.Code != http.StatusAccepted || body["power_off"] != true {
		t.Fatalf("layout = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/trains/layout", "")
	if rec.Code != http.StatusOK || body["power_off"] != true {
		t.Fatalf("GET layout = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/layout", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty layout = %d", rec.Code)
	}
	// Every train here answers that it is stopped.
	rec, body = ta.do(t, http.MethodGet, "/api/v1/trains/layout/resume", "")
	if list, ok := body["trains"].([]any); rec.Code != http.StatusOK || !ok || len(list) != 0 {
		t.Errorf("resume = %d %v", rec.Code, body)
	}

	for _, id := range []string{"challenger", "again"} {
		if rec, _ = ta.do(t, http.MethodDelete, "/api/v1/trains/"+id, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s = %d", id, rec.Code)
		}
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/trains", "")
	if list := body["trains"].([]any); rec.Code != http.StatusOK || len(list) != 1 {
		t.Fatalf("after delete = %d %v", rec.Code, body)
	}
	tb.mu.Lock()
	n := len(tb.commands)
	tb.mu.Unlock()
	if n == 0 {
		t.Error("no traction commands were sent")
	}
}

func TestConsistEndpoints(t *testing.T) {
	ta, _ := newTrainAPI(t)
	for _, body := range []string{`{"name": "Lead", "address": 1001}`, `{"name": "Trailer", "address": 1002}`} {
		if rec, _ := ta.do(t, http.MethodPost, "/api/v1/trains", body); rec.Code != http.StatusCreated {
			t.Fatalf("add train = %d", rec.Code)
		}
	}
	rec, body := ta.do(t, http.MethodPost, "/api/v1/consists", `{"name": "Pair", "members": [{"train": "lead"}, {"train": "trailer", "reverse": true}]}`)
	if rec.Code != http.StatusCreated || body["id"] != "pair" || body["kind"] != "lcc" {
		t.Fatalf("create = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/consists", `{"name": "One", "members": [{"train": "lead"}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a consist of one = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/consists/pair/make-up", "")
	if rec.Code != http.StatusOK || body["made_up"] != true || body["attached"] != true {
		t.Fatalf("make up = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/consists/pair", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("delete while made up = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/trains/trailer", "")
	if rec.Code != http.StatusOK || body["consist"] != "pair" {
		t.Errorf("member = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPatch, "/api/v1/consists/pair", `{"name": "Matched Pair"}`)
	if rec.Code != http.StatusOK || body["name"] != "Matched Pair" {
		t.Errorf("rename = %d %v", rec.Code, body)
	}
	if rec, body = ta.do(t, http.MethodPost, "/api/v1/consists/pair/break-up", ""); rec.Code != http.StatusOK || body["made_up"] != false {
		t.Errorf("break up = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/consists", "")
	if list, _ := body["consists"].([]any); rec.Code != http.StatusOK || len(list) != 1 {
		t.Errorf("list = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/consists/pair", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/consists/pair", ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d", rec.Code)
	}
}

func TestProgrammingEndpoints(t *testing.T) {
	ta, _ := newTrainAPI(t)
	rec, body := ta.do(t, http.MethodGet, "/api/v1/programming", "")
	if rec.Code != http.StatusOK || body["track"].(map[string]any)["name"] != "Bench" {
		t.Fatalf("GET programming = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/programming/read", `{"cv": 8}`)
	if rec.Code != http.StatusOK || body["value"] != 13.0 || body["ok"] != true {
		t.Fatalf("read = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/programming/read", `{"cv": 0}`); rec.Code != http.StatusBadRequest {
		t.Errorf("read CV 0 = %d", rec.Code)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/programming/read", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("read without cv = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/programming/write", `{"cv": 3, "value": 5}`)
	if rec.Code != http.StatusOK || body["ok"] != false || !strings.Contains(body["error"].(string), "No decoder") {
		t.Fatalf("write = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/cv", `{"cv": 3, "value": 5}`)
	if rec.Code != http.StatusOK || body["kind"] != "pom" || body["train"] != "1234" {
		t.Fatalf("pom = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/nope/cv", `{"cv": 3, "value": 5}`); rec.Code != http.StatusNotFound {
		t.Errorf("pom for unknown train = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/1234/cv/read", `{"cv": 3}`)
	if rec.Code != http.StatusOK || body["kind"] != "pom_read" || body["value"] != 13.0 {
		t.Fatalf("pom read = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/1234/cv/read", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("pom read without cv = %d", rec.Code)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/programming/find", "")
	if rec.Code != http.StatusOK || body["name"] != "Bench" {
		t.Fatalf("find = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/programming", "")
	if rec.Code != http.StatusOK || len(body["history"].([]any)) != 4 {
		t.Fatalf("history = %d %v", rec.Code, body)
	}
}

func TestTrainsUnavailable(t *testing.T) {
	ta := newTestAPI()
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/trains/1/speed", `{"speed": 1}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("speed without trains = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/programming/read", `{"cv": 1}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("read without programmer = %d", rec.Code)
	}
	if rec, body := ta.do(t, http.MethodGet, "/api/v1/trains", ""); rec.Code != http.StatusOK || len(body["trains"].([]any)) != 0 {
		t.Errorf("GET trains without trains = %d %v", rec.Code, body)
	}
}
