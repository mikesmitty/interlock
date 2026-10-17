package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/automation"
	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

type fakeEventBus struct{}

func (fakeEventBus) SendEvent(context.Context, openlcb.EventID) error { return nil }

type automationAPI struct {
	*testAPI
	ents   *fakeEntities
	engine *automation.Engine
}

// newAutomationAPI runs the API over a real engine that is not started,
// so nothing fires on its own but runs by hand work.
func newAutomationAPI() *automationAPI {
	reg := newFakeRegistry()
	mon := monitor.New(100, nil)
	bus := &fakeBus{links: []transport.Status{{Name: "a", Kind: "serial", Connected: true}}}
	ents := newFakeEntities()
	engine := automation.New(automation.Options{Entities: ents, Bus: fakeEventBus{}, Nodes: reg})
	a := New(Deps{Node: fakeNode{}, Registry: reg, Monitor: mon, Bus: bus, Entities: ents, Automations: engine})
	mux := http.NewServeMux()
	a.Register(mux)
	return &automationAPI{testAPI: &testAPI{api: a, reg: reg, mon: mon, bus: bus, mux: mux}, ents: ents, engine: engine}
}

const milestoneJSON = `{
  "name": "Close Turnout 1",
  "triggers": [{"type": "state", "entity": "block.050101012201.track_1", "to": "occupied"}],
  "conditions": [{"type": "state", "entity": "turnout.050101012201.servo_1", "state": "thrown"}],
  "actions": [{"type": "delay", "for": "50ms"}, {"type": "command", "entity": "turnout.050101012201.servo_1", "command": "close"}]
}`

func TestAutomationEndpoints(t *testing.T) {
	ta := newAutomationAPI()

	rec, body := ta.do(t, http.MethodGet, "/api/v1/automations", "")
	if rec.Code != http.StatusOK || len(body["automations"].([]any)) != 0 {
		t.Fatalf("empty list: %d %v", rec.Code, body)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/automations", milestoneJSON)
	if rec.Code != http.StatusCreated || body["id"] != "close_turnout_1" || body["enabled"] != true || body["mode"] != "single" {
		t.Fatalf("create: %d %v", rec.Code, body)
	}
	if body["running"] != float64(0) {
		t.Errorf("status fields missing: %v", body)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/automations", `{"name": "Broken", "triggers": [], "actions": []}`)
	if rec.Code != http.StatusBadRequest || body["path"] != "triggers" {
		t.Errorf("invalid create: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/automations", `{"name": "Broken", "triggers": [{"type":"state","entity":"x"}], "actions": [{"type":"delay","for":"soon"}]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "duration") {
		t.Errorf("bad duration: %d %v", rec.Code, body)
	}

	rec, body = ta.do(t, http.MethodGet, "/api/v1/automations/close_turnout_1", "")
	if rec.Code != http.StatusOK || body["name"] != "Close Turnout 1" {
		t.Errorf("get: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/automations/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("get missing = %d", rec.Code)
	}

	rec, body = ta.do(t, http.MethodPut, "/api/v1/automations/close_turnout_1", strings.Replace(milestoneJSON, `"name": "Close Turnout 1"`, `"name": "Renamed", "mode": "restart"`, 1))
	if rec.Code != http.StatusOK || body["name"] != "Renamed" || body["mode"] != "restart" || body["id"] != "close_turnout_1" {
		t.Errorf("put: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPut, "/api/v1/automations/close_turnout_1", `{"name": "x", "triggers": [{"type": "time", "at": "25:00"}], "actions": [{"type": "stop"}]}`)
	if rec.Code != http.StatusBadRequest || body["path"] != "triggers[0].at" {
		t.Errorf("invalid put: %d %v", rec.Code, body)
	}

	rec, body = ta.do(t, http.MethodPatch, "/api/v1/automations/close_turnout_1", `{"enabled": false}`)
	if rec.Code != http.StatusOK || body["enabled"] != false {
		t.Errorf("patch: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPatch, "/api/v1/automations/close_turnout_1", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty patch = %d", rec.Code)
	}

	yamlRec := httptest.NewRecorder()
	ta.mux.ServeHTTP(yamlRec, httptest.NewRequest(http.MethodGet, "/api/v1/automations/close_turnout_1/yaml", http.NoBody))
	if yamlRec.Code != http.StatusOK || !strings.HasPrefix(yamlRec.Header().Get("Content-Type"), "application/yaml") || !strings.Contains(yamlRec.Body.String(), "for: 50ms") {
		t.Errorf("yaml: %d %q", yamlRec.Code, yamlRec.Body.String())
	}

	// Run by hand: the conditions are skipped, so the delay starts.
	rec, body = ta.do(t, http.MethodPost, "/api/v1/automations/close_turnout_1/run", "")
	if rec.Code != http.StatusAccepted || body["result"] != automation.ResultRunning {
		t.Fatalf("run: %d %v", rec.Code, body)
	}
	traceID := body["id"].(string)
	rec, body = ta.do(t, http.MethodGet, "/api/v1/automations/close_turnout_1/traces", "")
	if rec.Code != http.StatusOK || len(body["traces"].([]any)) != 1 {
		t.Errorf("traces while running: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/automations/close_turnout_1/traces/"+traceID, "")
	if rec.Code != http.StatusOK || body["id"] != traceID {
		t.Errorf("trace: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/automations/close_turnout_1/stop", "")
	if rec.Code != http.StatusAccepted || body["status"] != "stopping" {
		t.Errorf("stop: %d %v", rec.Code, body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := ta.engine.Get("close_turnout_1"); s.Running == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/automations/close_turnout_1/stop", "")
	if rec.Code != http.StatusOK || body["status"] != "idle" {
		t.Errorf("stop when idle: %d %v", rec.Code, body)
	}
	// Without a store, a finished trace is gone.
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/automations/close_turnout_1/traces/"+traceID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("finished trace without a store = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/automations/close_turnout_1/traces?limit=x", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/automations/close_turnout_1/run", `{"check_conditions": "yes"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad run body = %d", rec.Code)
	}

	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/automations/close_turnout_1", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/automations/close_turnout_1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d", rec.Code)
	}
}

func TestAutomationsUnavailable(t *testing.T) {
	ta := newTestAPI()
	rec, body := ta.do(t, http.MethodGet, "/api/v1/automations", "")
	if rec.Code != http.StatusOK || len(body["automations"].([]any)) != 0 {
		t.Errorf("list without an engine: %d %v", rec.Code, body)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/automations"},
		{http.MethodGet, "/api/v1/automations/x"},
		{http.MethodPost, "/api/v1/automations/x/run"},
		{http.MethodGet, "/api/v1/automations/x/traces"},
	} {
		if rec, _ := ta.do(t, c.method, c.path, "{}"); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", c.method, c.path, rec.Code)
		}
	}
}

func TestAutomationStatusRoundTrip(t *testing.T) {
	// The status both marshals flat and unmarshals back.
	s := automation.Status{Automation: automation.Automation{ID: "a", Name: "A", Enabled: true}, Running: 2, LastResult: "finished", Problems: []string{"x"}}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back automation.Status
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != "a" || !back.Enabled || back.Running != 2 || back.LastResult != "finished" || len(back.Problems) != 1 {
		t.Errorf("round trip = %+v", back)
	}
}
