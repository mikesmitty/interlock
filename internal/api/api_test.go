package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/version"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/hub"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

const (
	localID openlcb.NodeID = 0x050101012200
	peerID  openlcb.NodeID = 0x050101012201
)

type fakeNode struct{}

func (fakeNode) ID() openlcb.NodeID   { return localID }
func (fakeNode) Alias() openlcb.Alias { return 0x123 }
func (fakeNode) State() node.State    { return node.StateInitialized }

type fakeRegistry struct {
	mu        sync.Mutex
	nodes     map[openlcb.NodeID]registry.Info
	subs      []registry.Subscriber
	refreshed []openlcb.NodeID
	cdiXML    []byte
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{nodes: map[openlcb.NodeID]registry.Info{
		localID: {ID: localID, Alias: 0x123, Online: true, Local: true},
		peerID:  {ID: peerID, Alias: 0x456, Online: true, SNIP: &openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", UserName: "Yard"}},
	}}
}

func (r *fakeRegistry) List() []registry.Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []registry.Info{r.nodes[localID], r.nodes[peerID]}
}

func (r *fakeRegistry) Get(id openlcb.NodeID) (registry.Info, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.nodes[id]
	return info, ok
}

func (r *fakeRegistry) Refresh(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshed = append(r.refreshed, 0)
	return nil
}

func (r *fakeRegistry) RefreshNode(_ context.Context, id openlcb.NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return node.ErrNotFound
	}
	r.refreshed = append(r.refreshed, id)
	return nil
}

func (r *fakeRegistry) Subscribe(sub registry.Subscriber) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs = append(r.subs, sub)
	return func() {}
}

func (r *fakeRegistry) Forget(_ context.Context, id openlcb.NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.nodes[id]
	if !ok {
		return node.ErrNotFound
	}
	if info.Online {
		return registry.ErrNodeOnline
	}
	delete(r.nodes, id)
	return nil
}

func (r *fakeRegistry) CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != peerID || r.cdiXML == nil {
		return nil, nil, false
	}
	doc, err := cdi.Parse(r.cdiXML)
	if err != nil {
		return nil, nil, false
	}
	return doc, r.cdiXML, true
}

func (r *fakeRegistry) RefreshCDI(_ context.Context, id openlcb.NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return node.ErrNotFound
	}
	if id != peerID {
		return registry.ErrNoCDI
	}
	r.refreshed = append(r.refreshed, id)
	return nil
}

func (r *fakeRegistry) publish(info registry.Info) {
	r.mu.Lock()
	subs := append([]registry.Subscriber(nil), r.subs...)
	r.mu.Unlock()
	for _, sub := range subs {
		sub(info)
	}
}

type fakeBus struct {
	mu    sync.Mutex
	sent  []can.Frame
	links []transport.Status
}

func (b *fakeBus) Links() []transport.Status { return b.links }
func (b *fakeBus) Send(f can.Frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, f)
}

type testAPI struct {
	api *API
	reg *fakeRegistry
	mon *monitor.Monitor
	bus *fakeBus
	mux *http.ServeMux
}

func newTestAPI() *testAPI {
	reg := newFakeRegistry()
	mon := monitor.New(100, nil)
	bus := &fakeBus{links: []transport.Status{{Name: "a", Kind: "serial", Connected: true}, {Name: "b", Kind: "tcp"}}}
	a := New(Deps{Node: fakeNode{}, Registry: reg, Monitor: mon, Bus: bus, SerialPorts: func() ([]string, error) { return []string{"/dev/cu.usbmodem1"}, nil }})
	mux := http.NewServeMux()
	a.Register(mux)
	return &testAPI{api: a, reg: reg, mon: mon, bus: bus, mux: mux}
}

func (ta *testAPI) do(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader = http.NoBody
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	ta.mux.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: body is not JSON: %q", method, path, rec.Body)
		}
	}
	return rec, decoded
}

func TestHealth(t *testing.T) {
	ta := newTestAPI()
	rec, body := ta.do(t, http.MethodGet, "/api/v1/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	if body["status"] != "ok" || body["version"] != version.Version || body["node_id"] != "05.01.01.01.22.00" {
		t.Errorf("body = %v", body)
	}
	if body["alias"] != float64(0x123) || body["node_state"] != "initialized" || body["nodes"] != float64(2) {
		t.Errorf("node fields = %v", body)
	}
	if body["links_total"] != float64(2) || body["links_connected"] != float64(1) {
		t.Errorf("link counts = %v", body)
	}
	if body["os"] != runtime.GOOS {
		t.Errorf("os = %v, want %s", body["os"], runtime.GOOS)
	}
}

func TestRouting(t *testing.T) {
	ta := newTestAPI()
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantAllow  string
		check      func(t *testing.T, body map[string]any)
	}{
		{name: "health wrong method", method: http.MethodPost, path: "/api/v1/health", wantStatus: 405, wantAllow: "GET, HEAD"},
		{name: "unknown endpoint", method: http.MethodGet, path: "/api/v1/nope", wantStatus: 404},
		{name: "api root", method: http.MethodGet, path: "/api/", wantStatus: 404},
		{name: "nodes", method: http.MethodGet, path: "/api/v1/nodes", wantStatus: 200, check: func(t *testing.T, body map[string]any) {
			nodes := body["nodes"].([]any)
			if len(nodes) != 2 || nodes[1].(map[string]any)["id"] != "05.01.01.01.22.01" {
				t.Errorf("nodes = %v", nodes)
			}
			if snip := nodes[1].(map[string]any)["snip"].(map[string]any); snip["UserName"] != "Yard" {
				t.Errorf("snip = %v", snip)
			}
		}},
		{name: "node", method: http.MethodGet, path: "/api/v1/nodes/05.01.01.01.22.01", wantStatus: 200, check: func(t *testing.T, body map[string]any) {
			if body["alias"] != float64(0x456) || body["online"] != true {
				t.Errorf("node = %v", body)
			}
		}},
		{name: "node bare hex", method: http.MethodGet, path: "/api/v1/nodes/050101012201", wantStatus: 200},
		{name: "node unknown", method: http.MethodGet, path: "/api/v1/nodes/05.01.01.01.22.99", wantStatus: 404},
		{name: "node bad id", method: http.MethodGet, path: "/api/v1/nodes/xyz", wantStatus: 400},
		{name: "refresh all", method: http.MethodPost, path: "/api/v1/nodes/refresh", wantStatus: 202},
		{name: "refresh one", method: http.MethodPost, path: "/api/v1/nodes/05.01.01.01.22.01/refresh", wantStatus: 202},
		{name: "refresh unknown", method: http.MethodPost, path: "/api/v1/nodes/05.01.01.01.22.99/refresh", wantStatus: 404},
		{name: "links", method: http.MethodGet, path: "/api/v1/links", wantStatus: 200, check: func(t *testing.T, body map[string]any) {
			if links := body["links"].([]any); len(links) != 2 || links[0].(map[string]any)["name"] != "a" {
				t.Errorf("links = %v", links)
			}
		}},
		{name: "serial ports", method: http.MethodGet, path: "/api/v1/serial-ports", wantStatus: 200, check: func(t *testing.T, body map[string]any) {
			if ports := body["ports"].([]any); len(ports) != 1 || ports[0] != "/dev/cu.usbmodem1" {
				t.Errorf("ports = %v", ports)
			}
		}},
		{name: "monitor send", method: http.MethodPost, path: "/api/v1/monitor/send", body: `{"frame":":x19490abcn;"}`, wantStatus: 202, check: func(t *testing.T, body map[string]any) {
			if body["frame"] != ":X19490ABCN;" {
				t.Errorf("frame = %v", body["frame"])
			}
		}},
		{name: "monitor send bad frame", method: http.MethodPost, path: "/api/v1/monitor/send", body: `{"frame":"hello"}`, wantStatus: 400},
		{name: "monitor send bad json", method: http.MethodPost, path: "/api/v1/monitor/send", body: `{`, wantStatus: 400},
		{name: "monitor bad since", method: http.MethodGet, path: "/api/v1/monitor?since=x", wantStatus: 400},
		{name: "monitor bad limit", method: http.MethodGet, path: "/api/v1/monitor?limit=-1", wantStatus: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, body := ta.do(t, tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("%s %s: status = %d, want %d; body %s", tt.method, tt.path, rec.Code, tt.wantStatus, rec.Body)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q", ct)
			}
			if tt.wantAllow != "" {
				if allow := rec.Header().Get("Allow"); allow != tt.wantAllow {
					t.Errorf("Allow = %q, want %q", allow, tt.wantAllow)
				}
			}
			if tt.wantStatus >= 400 && body["error"] == "" {
				t.Errorf("error body missing: %s", rec.Body)
			}
			if tt.check != nil {
				tt.check(t, body)
			}
		})
	}
	if len(ta.bus.sent) != 1 || ta.bus.sent[0].ID != 0x19490ABC {
		t.Errorf("bus received %v", ta.bus.sent)
	}
	if len(ta.reg.refreshed) != 2 || ta.reg.refreshed[1] != peerID {
		t.Errorf("refreshed = %v", ta.reg.refreshed)
	}
}

func TestMonitorEntries(t *testing.T) {
	ta := newTestAPI()
	for i := 1; i <= 5; i++ {
		ta.mon.HandleHubEvent(hub.Event{Time: time.Now(), Frame: can.RIDFrame(openlcb.Alias(i)), Direction: hub.In, Link: "a"})
	}
	rec, body := ta.do(t, http.MethodGet, "/api/v1/monitor", "")
	if rec.Code != 200 || len(body["entries"].([]any)) != 5 || body["last_seq"] != float64(5) {
		t.Fatalf("monitor = %d %v", rec.Code, body)
	}
	_, body = ta.do(t, http.MethodGet, "/api/v1/monitor?since=3", "")
	entries := body["entries"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["seq"] != float64(4) {
		t.Fatalf("since=3 entries = %v", entries)
	}
	_, body = ta.do(t, http.MethodGet, "/api/v1/monitor?limit=2", "")
	if entries := body["entries"].([]any); len(entries) != 2 || entries[0].(map[string]any)["seq"] != float64(4) {
		t.Fatalf("limit=2 entries = %v", entries)
	}
	first := body["entries"].([]any)[0].(map[string]any)
	if first["kind"] != "control" || first["decoded"].(map[string]any)["type"] != "RID" || first["frame"] != ":X10700004N;" {
		t.Errorf("entry = %v", first)
	}
}

// readSSE reads one "event:/data:" pair from the stream.
func readSSE(t *testing.T, r *bufio.Reader) (string, string) {
	t.Helper()
	var event, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && event != "":
			return event, data
		}
	}
}

func TestEventStream(t *testing.T) {
	ta := newTestAPI()
	srv := httptest.NewServer(ta.mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events?topics=nodes,monitor,links", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)

	if event, data := readSSE(t, reader); event != "ready" || data != `{"seq":0}` {
		t.Fatalf("first event = %s %s", event, data)
	}
	if event, data := readSSE(t, reader); event != "links" || !strings.Contains(data, `"name":"a"`) {
		t.Fatalf("second event = %s %s", event, data)
	}

	ta.reg.publish(registry.Info{ID: peerID, Alias: 0x456, Online: false})
	event, data := readSSE(t, reader)
	if event != "node" || !strings.Contains(data, `"id":"05.01.01.01.22.01"`) || !strings.Contains(data, `"online":false`) {
		t.Fatalf("node event = %s %s", event, data)
	}

	ta.mon.HandleHubEvent(hub.Event{Time: time.Now(), Frame: can.RIDFrame(7), Direction: hub.Out})
	event, data = readSSE(t, reader)
	if event != "frame" || !strings.Contains(data, `"frame":":X10700007N;"`) {
		t.Fatalf("frame event = %s %s", event, data)
	}
}

func TestEventStreamTopicFilter(t *testing.T) {
	ta := newTestAPI()
	srv := httptest.NewServer(ta.mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events?topics=monitor", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	readSSE(t, reader) // ready
	ta.reg.publish(registry.Info{ID: peerID})
	ta.mon.HandleHubEvent(hub.Event{Time: time.Now(), Frame: can.RIDFrame(1), Direction: hub.In})
	if event, _ := readSSE(t, reader); event != "frame" {
		t.Fatalf("got %s, want frame (node events filtered out)", event)
	}
}
