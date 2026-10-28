package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/mikesmitty/interlock/internal/driving"
)

type fakeDriving struct {
	drives map[string]driving.Drive
	asked  driving.Request
	steps  []int
}

func (f *fakeDriving) List() []driving.Drive {
	out := []driving.Drive{}
	for _, d := range f.drives {
		out = append(out, d)
	}
	return out
}

func (f *fakeDriving) Get(id string) (driving.Drive, bool) {
	d, ok := f.drives[id]
	return d, ok
}

func (f *fakeDriving) Drive(_ context.Context, id string, req driving.Request) (driving.Drive, error) {
	switch {
	case id != "local-1":
		return driving.Drive{}, driving.ErrNoTrain
	case f.drives[id].Active():
		return driving.Drive{}, &driving.RefusedError{Reason: "Local 1 is being driven already."}
	}
	f.asked = req
	d := driving.Drive{Train: id, Kind: driving.KindDrive, To: req.To, State: driving.StateStarting}
	f.drives[id] = d
	return d, nil
}

func (f *fakeDriving) Measure(_ context.Context, id string, req driving.MeasureRequest) (driving.Drive, error) {
	f.steps = req.Steps
	d := driving.Drive{Train: id, Kind: driving.KindMeasure, State: driving.StateRunning}
	f.drives[id] = d
	return d, nil
}

func (f *fakeDriving) Stop(_ context.Context, id string) (driving.Drive, error) {
	d, ok := f.drives[id]
	if !ok || !d.Active() {
		return driving.Drive{}, driving.ErrNotDriving
	}
	d.State = driving.StateStopping
	f.drives[id] = d
	return d, nil
}

func (f *fakeDriving) Subscribe(driving.Subscriber) func() { return func() {} }

func TestDrivingEndpoints(t *testing.T) {
	ta := newTestAPI()
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/driving", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without driving = %d", rec.Code)
	}
	fake := &fakeDriving{drives: map[string]driving.Drive{}}
	ta.api.deps.Driving = fake

	if rec, body := ta.do(t, http.MethodPost, "/api/v1/driving/local-1", `{"to": "park-outer", "speed": 80}`); rec.Code != http.StatusOK || body["state"] != "starting" || fake.asked.Speed != 80 {
		t.Errorf("drive = %d %v", rec.Code, body)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/driving/local-1", `{"to": "park-outer"}`); rec.Code != http.StatusConflict || body["error"] != "Local 1 is being driven already." {
		t.Errorf("drive again = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/driving/nobody", `{"to": "park-outer"}`); rec.Code != http.StatusNotFound {
		t.Errorf("drive a train not tracked = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/driving/local-1", `{"to": `); rec.Code != http.StatusBadRequest {
		t.Errorf("a broken body = %d", rec.Code)
	}
	rec, body := ta.do(t, http.MethodGet, "/api/v1/driving", "")
	if list, _ := body["drives"].([]any); rec.Code != http.StatusOK || len(list) != 1 {
		t.Errorf("GET driving = %d %v", rec.Code, body)
	}
	if rec, body := ta.do(t, http.MethodGet, "/api/v1/driving/local-1", ""); rec.Code != http.StatusOK || body["to"] != "park-outer" {
		t.Errorf("GET a drive = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/driving/local-2", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET a train never driven = %d", rec.Code)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/driving/local-1/stop", ""); rec.Code != http.StatusOK || body["state"] != "stopping" {
		t.Errorf("stop = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/driving/local-2/stop", ""); rec.Code != http.StatusConflict {
		t.Errorf("stop a train not driven = %d", rec.Code)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/driving/local-2/measure", `{"steps": [20, 40]}`); rec.Code != http.StatusOK || body["kind"] != "measure" || len(fake.steps) != 2 {
		t.Errorf("measure = %d %v", rec.Code, body)
	}
}
