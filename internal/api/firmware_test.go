package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mikesmitty/interlock/internal/firmware"
	"github.com/mikesmitty/interlock/openlcb"
)

type fakeFirmware struct {
	mu      sync.Mutex
	jobs    map[openlcb.NodeID]firmware.Job
	subs    []func(firmware.Job)
	started []string
}

func (f *fakeFirmware) Start(_ context.Context, id openlcb.NodeID, file string, data []byte) (firmware.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != peerID {
		return firmware.Job{}, firmware.ErrNotFound
	}
	if len(data) == 0 {
		return firmware.Job{}, &firmware.InvalidError{Reason: "the file is empty"}
	}
	if j, ok := f.jobs[id]; ok && j.Phase.Active() {
		return firmware.Job{}, firmware.ErrBusy
	}
	j := firmware.Job{ID: "j1", Node: id, File: file, Size: len(data), Phase: firmware.PhaseWriting, Message: "Sending the image"}
	f.jobs[id] = j
	f.started = append(f.started, file)
	return j, nil
}

func (f *fakeFirmware) Cancel(id openlcb.NodeID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok || !j.Phase.Active() {
		return firmware.ErrNoJob
	}
	j.Phase, j.Message = firmware.PhaseFailed, "Canceled"
	f.jobs[id] = j
	return nil
}

func (f *fakeFirmware) Job(id openlcb.NodeID) (firmware.Job, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	return j, ok
}

func (f *fakeFirmware) Jobs() []firmware.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []firmware.Job
	for _, j := range f.jobs {
		out = append(out, j)
	}
	return out
}

func (f *fakeFirmware) Subscribe(fn func(firmware.Job)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fn)
	return func() {}
}

func upload(t *testing.T, mux *http.ServeMux, path, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestFirmwareEndpoints(t *testing.T) {
	fw := &fakeFirmware{jobs: make(map[openlcb.NodeID]firmware.Job)}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, Firmware: fw})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}
	peer := "/api/v1/nodes/" + peerID.String() + "/firmware"

	rec, body := ta.do(t, http.MethodGet, "/api/v1/firmware", "")
	if rec.Code != http.StatusOK || len(body["jobs"].([]any)) != 0 {
		t.Fatalf("empty list: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, peer, ""); rec.Code != http.StatusNotFound {
		t.Errorf("job before any upload: %d", rec.Code)
	}

	rec = upload(t, mux, peer, "outpost-2.0.0.uf2", []byte("image bytes"))
	if rec.Code != http.StatusAccepted || !bytes.Contains(rec.Body.Bytes(), []byte(`"phase":"writing"`)) {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if len(fw.started) != 1 || fw.started[0] != "outpost-2.0.0.uf2" {
		t.Errorf("started = %v", fw.started)
	}
	if rec := upload(t, mux, peer, "again.bin", []byte("more")); rec.Code != http.StatusConflict {
		t.Errorf("second upload: %d %s", rec.Code, rec.Body)
	}
	if rec := upload(t, mux, "/api/v1/nodes/"+localID.String()+"/firmware", "x.bin", []byte("more")); rec.Code != http.StatusNotFound {
		t.Errorf("unknown node: %d %s", rec.Code, rec.Body)
	}
	rec, body = ta.do(t, http.MethodGet, peer, "")
	if rec.Code != http.StatusOK || body["file"] != "outpost-2.0.0.uf2" || body["size"] != float64(11) {
		t.Errorf("job: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodDelete, peer, "")
	if rec.Code != http.StatusAccepted || body["phase"] != "failed" {
		t.Errorf("cancel: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodDelete, peer, ""); rec.Code != http.StatusNotFound {
		t.Errorf("cancel again: %d", rec.Code)
	}
	if rec := upload(t, mux, peer, "empty.bin", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("empty file: %d %s", rec.Code, rec.Body)
	}
	// Not a multipart form at all.
	if rec, _ := ta.do(t, http.MethodPost, peer, `{"file":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("JSON body: %d", rec.Code)
	}
}
