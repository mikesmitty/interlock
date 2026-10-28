package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/tracking"
)

type fakeInterlocking struct {
	routes map[string]interlocking.Route
	forced bool
}

func (f *fakeInterlocking) List() []interlocking.Route {
	return []interlocking.Route{f.routes["a~b"]}
}

func (f *fakeInterlocking) Get(id string) (interlocking.Route, bool) {
	r, ok := f.routes[id]
	return r, ok
}

func (f *fakeInterlocking) Set(_ context.Context, id string) (interlocking.Route, error) {
	r, ok := f.routes[id]
	switch {
	case !ok:
		return r, interlocking.ErrNoRoute
	case r.State != "":
		return r, &interlocking.RefusedError{Reason: "Local is in B."}
	}
	r.State = interlocking.StateSetting
	f.routes[id] = r
	return r, nil
}

func (f *fakeInterlocking) Cancel(_ context.Context, id string, force bool) (interlocking.Route, error) {
	r := f.routes[id]
	if !force {
		return r, &interlocking.RefusedError{Reason: "Local is on it.", Occupied: true}
	}
	f.forced = true
	r.State = ""
	return r, nil
}

func (f *fakeInterlocking) Subscribe(interlocking.Subscriber) func() { return func() {} }

type fakeTracking struct{ forgot string }

func (f *fakeTracking) List() []tracking.Train               { return nil }
func (f *fakeTracking) Subscribe(tracking.Subscriber) func() { return func() {} }
func (f *fakeTracking) Forget(_ context.Context, id string) error {
	if id != "local-1" {
		return tracking.ErrNotTracked
	}
	f.forgot = id
	return nil
}

func TestRouteEndpoints(t *testing.T) {
	ta := newTestAPI()
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/routes", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without an interlocking = %d", rec.Code)
	}
	fake := &fakeInterlocking{routes: map[string]interlocking.Route{"a~b": {ID: "a~b"}}}
	ta.api.deps.Interlocking = fake

	rec, body := ta.do(t, http.MethodGet, "/api/v1/routes", "")
	if list, _ := body["routes"].([]any); rec.Code != http.StatusOK || len(list) != 1 {
		t.Errorf("GET routes = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/routes/nowhere", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET a route the plan lacks = %d", rec.Code)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/routes/a~b/set", ""); rec.Code != http.StatusOK || body["state"] != "setting" {
		t.Errorf("set = %d %v", rec.Code, body)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/routes/a~b/set", ""); rec.Code != http.StatusConflict || body["error"] != "Local is in B." {
		t.Errorf("refused = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/routes/nowhere/set", ""); rec.Code != http.StatusNotFound {
		t.Errorf("set a route the plan lacks = %d", rec.Code)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/routes/a~b/cancel", ""); rec.Code != http.StatusConflict || body["error"] != "Local is on it." {
		t.Errorf("cancel = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/routes/a~b/cancel", `{"force": true}`); rec.Code != http.StatusOK || !fake.forced {
		t.Errorf("cancel by force = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/routes/a~b/cancel", `{"force": `); rec.Code != http.StatusBadRequest {
		t.Errorf("cancel with a broken body = %d", rec.Code)
	}
}

func TestForgetTrackedTrain(t *testing.T) {
	ta := newTestAPI()
	tracker := &fakeTracking{}
	ta.api.deps.Tracking = tracker
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/tracking/local-1", ""); rec.Code != http.StatusNoContent || tracker.forgot != "local-1" {
		t.Errorf("forget = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/tracking/nobody", ""); rec.Code != http.StatusNotFound {
		t.Errorf("forget a train not followed = %d", rec.Code)
	}
}
