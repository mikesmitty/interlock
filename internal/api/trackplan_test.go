package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mikesmitty/interlock/internal/trackplan"
)

func TestTrackPlanEndpoints(t *testing.T) {
	file, err := trackplan.OpenFile(filepath.Join(t.TempDir(), trackplan.FileName))
	if err != nil {
		t.Fatal(err)
	}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, TrackPlan: file})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}

	rec, body := ta.do(t, http.MethodGet, "/api/v1/trackplan", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %v", rec.Code, body)
	}
	first, _ := body["revision"].(string)
	if first == "" {
		t.Fatalf("GET gave no revision: %v", body)
	}

	plan := `{"tiles":[{"x":0,"y":0,"track":["e"],"block":"a"},{"x":1,"y":0,"track":["w","e"]},{"x":2,"y":0,"track":["w"],"block":"b"}],` +
		`"blocks":[{"id":"a","name":"A"},{"id":"b","name":"B","length_mm":450}],"stations":[]}`
	rec, body = ta.do(t, http.MethodPut, "/api/v1/trackplan", fmt.Sprintf(`{"plan":%s,"revision":%q}`, plan, first))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %v", rec.Code, body)
	}
	saved, _ := body["revision"].(string)
	topology, _ := body["topology"].(map[string]any)
	links, _ := topology["links"].([]any)
	if saved == first || len(links) != 1 {
		t.Errorf("PUT gave revision %q and links %v", saved, links)
	}

	rec, body = ta.do(t, http.MethodPut, "/api/v1/trackplan", fmt.Sprintf(`{"plan":%s,"revision":%q}`, plan, first))
	if rec.Code != http.StatusConflict || body["revision"] != saved {
		t.Errorf("PUT from an old revision: %d %v, want 409 with the current revision", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPut, "/api/v1/trackplan", fmt.Sprintf(`{"plan":{"tiles":[{"x":0,"y":0,"track":["e","ne"]}]},"revision":%q}`, saved))
	if rec.Code != http.StatusBadRequest || body["error"] == nil {
		t.Errorf("PUT of a bad piece: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPut, "/api/v1/trackplan", `{"plan":{},"revision":"x","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT with an unknown field: %d %v", rec.Code, body)
	}
}

func TestTrackPlanUnavailable(t *testing.T) {
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/trackplan", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET without a track plan: %d", rec.Code)
	}
}
