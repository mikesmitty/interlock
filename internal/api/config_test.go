package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mikesmitty/interlock/internal/nodeconfig"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

type fakeConfig struct {
	mu      sync.Mutex
	reads   []string
	written nodeconfig.Values
	reboots int
	resets  int
	fail    error
}

func (f *fakeConfig) Read(_ context.Context, _ openlcb.NodeID, space uint8, address uint32, size int) (*nodeconfig.ReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.reads = append(f.reads, memconfig.SpaceName(space))
	_ = address
	return &nodeconfig.ReadResult{Values: nodeconfig.Values{"253:172": "16384"}, Bytes: size}, nil
}

func (f *fakeConfig) Write(_ context.Context, _ openlcb.NodeID, values nodeconfig.Values) (*nodeconfig.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return &nodeconfig.WriteResult{Written: []string{}, Failed: "253:172", FailedError: f.fail.Error()}, f.fail
	}
	if _, bad := values["253:999999"]; bad {
		inv := map[string]string{"253:999999": "no such variable in the node's layout"}
		return &nodeconfig.WriteResult{Invalid: inv}, &nodeconfig.InvalidError{Invalid: inv}
	}
	f.written = values
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	return &nodeconfig.WriteResult{Written: keys}, nil
}

func (f *fakeConfig) Reboot(context.Context, openlcb.NodeID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reboots++
	return f.fail
}

func (f *fakeConfig) FactoryReset(context.Context, openlcb.NodeID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets++
	return f.fail
}

func newConfigAPI() (*testAPI, *fakeConfig) {
	ta := newTestAPI()
	cfg := &fakeConfig{}
	ta.api.deps.Config = cfg
	ta.reg.cdiXML = sim.OutpostCDI
	return ta, cfg
}

func TestCDIEndpoints(t *testing.T) {
	ta, _ := newConfigAPI()
	peer := "/api/v1/nodes/05.01.01.01.22.01"

	rec, body := ta.do(t, http.MethodGet, peer+"/cdi", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET cdi = %d %s", rec.Code, rec.Body)
	}
	layout, _ := body["layout"].(map[string]any)
	segments, _ := layout["segments"].([]any)
	if len(segments) != 4 {
		t.Errorf("segments = %d", len(segments))
	}
	ident, _ := layout["identification"].(map[string]any)
	if ident["model"] != "CAN Bus Outpost" {
		t.Errorf("identification = %v", ident)
	}

	req, _ := http.NewRequest(http.MethodGet, peer+"/cdi?format=xml", http.NoBody)
	xmlRec := doRaw(ta, req)
	if xmlRec.Code != http.StatusOK || !strings.HasPrefix(xmlRec.Header().Get("Content-Type"), "application/xml") || !strings.Contains(xmlRec.Body.String(), "<cdi") {
		t.Errorf("GET cdi xml = %d %s %q", xmlRec.Code, xmlRec.Header().Get("Content-Type"), xmlRec.Body.String()[:40])
	}

	rec, body = ta.do(t, http.MethodGet, "/api/v1/nodes/05.01.01.01.22.00/cdi", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(body["error"].(string), "not been read") {
		t.Errorf("GET cdi of node without one = %d %v", rec.Code, body)
	}
	rec, _ = ta.do(t, http.MethodGet, "/api/v1/nodes/05.01.01.01.22.99/cdi", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET cdi of unknown node = %d", rec.Code)
	}

	rec, _ = ta.do(t, http.MethodPost, peer+"/cdi/refresh", "")
	if rec.Code != http.StatusAccepted {
		t.Errorf("POST cdi/refresh = %d %s", rec.Code, rec.Body)
	}
	rec, _ = ta.do(t, http.MethodPost, "/api/v1/nodes/05.01.01.01.22.00/cdi/refresh", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("POST cdi/refresh without CDI = %d", rec.Code)
	}
}

func TestConfigEndpoints(t *testing.T) {
	ta, cfg := newConfigAPI()
	peer := "/api/v1/nodes/05.01.01.01.22.01"

	rec, body := ta.do(t, http.MethodPost, peer+"/config/read", `{"space":253,"address":132,"size":120}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("read = %d %s", rec.Code, rec.Body)
	}
	values, _ := body["values"].(map[string]any)
	if values["253:172"] != "16384" || body["bytes"] != float64(120) {
		t.Errorf("read body = %v", body)
	}
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/read", `{"space":253,"address":0,"size":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("read size 0 = %d", rec.Code)
	}
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/read", `nonsense`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("read bad body = %d", rec.Code)
	}

	rec, body = ta.do(t, http.MethodPost, peer+"/config/write", `{"values":{"253:172":"20000","253:185":"Siding"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("write = %d %s", rec.Code, rec.Body)
	}
	if written, _ := body["written"].([]any); len(written) != 2 {
		t.Errorf("written = %v", body)
	}
	if cfg.written["253:185"] != "Siding" {
		t.Errorf("service got %v", cfg.written)
	}
	rec, body = ta.do(t, http.MethodPost, peer+"/config/write", `{"values":{"253:999999":"1"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid write = %d %s", rec.Code, rec.Body)
	}
	if invalid, _ := body["invalid"].(map[string]any); invalid["253:999999"] == nil {
		t.Errorf("invalid body = %v", body)
	}
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/write", `{"values":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty write = %d", rec.Code)
	}

	rec, _ = ta.do(t, http.MethodPost, peer+"/config/reboot", "")
	if rec.Code != http.StatusAccepted || cfg.reboots != 1 {
		t.Errorf("reboot = %d, reboots %d", rec.Code, cfg.reboots)
	}
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/factory-reset", `{"confirm":"05.01.01.01.22.00"}`)
	if rec.Code != http.StatusBadRequest || cfg.resets != 0 {
		t.Errorf("factory reset with wrong confirm = %d, resets %d", rec.Code, cfg.resets)
	}
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/factory-reset", `{"confirm":"05.01.01.01.22.01"}`)
	if rec.Code != http.StatusAccepted || cfg.resets != 1 {
		t.Errorf("factory reset = %d, resets %d", rec.Code, cfg.resets)
	}

	// Node errors map onto gateway statuses.
	cfg.fail = &openlcb.Error{Code: memconfig.ErrCodeWriteReadOnly, Op: "write"}
	rec, body = ta.do(t, http.MethodPost, peer+"/config/write", `{"values":{"253:172":"1"}}`)
	if rec.Code != http.StatusBadGateway || body["failed"] != "253:172" {
		t.Errorf("refused write = %d %v", rec.Code, body)
	}
	cfg.fail = memconfig.ErrNoReply
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/read", `{"space":253,"address":0,"size":8}`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("silent node read = %d", rec.Code)
	}
	cfg.fail = errors.New("boom")
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/reboot", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("other error = %d", rec.Code)
	}

	// Offline nodes and unknown nodes.
	ta.reg.mu.Lock()
	info := ta.reg.nodes[peerID]
	info.Online = false
	ta.reg.nodes[peerID] = info
	ta.reg.mu.Unlock()
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/read", `{"space":253,"address":0,"size":8}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("offline read = %d", rec.Code)
	}
	rec, _ = ta.do(t, http.MethodPost, "/api/v1/nodes/05.01.01.01.22.99/config/read", `{"space":253,"address":0,"size":8}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown node read = %d", rec.Code)
	}

	// Without a config service the endpoints say so.
	ta.api.deps.Config = nil
	rec, _ = ta.do(t, http.MethodPost, peer+"/config/reboot", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no service = %d", rec.Code)
	}
}

func TestForgetNode(t *testing.T) {
	ta, _ := newConfigAPI()
	rec, _ := ta.do(t, http.MethodDelete, "/api/v1/nodes/05.01.01.01.22.01", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("forget online = %d", rec.Code)
	}
	ta.reg.mu.Lock()
	info := ta.reg.nodes[peerID]
	info.Online = false
	ta.reg.nodes[peerID] = info
	ta.reg.mu.Unlock()
	rec, _ = ta.do(t, http.MethodDelete, "/api/v1/nodes/05.01.01.01.22.01", "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("forget offline = %d %s", rec.Code, rec.Body)
	}
	rec, _ = ta.do(t, http.MethodDelete, "/api/v1/nodes/05.01.01.01.22.01", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("forget again = %d", rec.Code)
	}
}

func doRaw(ta *testAPI, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ta.mux.ServeHTTP(rec, req)
	return rec
}
