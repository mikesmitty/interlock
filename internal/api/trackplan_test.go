package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

	plan := `{"points":[{"id":"p1","x":0,"y":0,"bumper":true},{"id":"p2","x":100,"y":0},{"id":"p3","x":200,"y":0},{"id":"p4","x":300,"y":0,"bumper":true}],` +
		`"tracks":[{"id":"t1","a":"p1","b":"p2","block":"a"},{"id":"t2","a":"p2","b":"p3"},{"id":"t3","a":"p3","b":"p4","block":"b"}],` +
		`"blocks":[{"id":"a","name":"A"},{"id":"b","name":"B","length_mm":450}],"stations":[],"grid":33}`
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
	rec, body = ta.do(t, http.MethodPut, "/api/v1/trackplan", fmt.Sprintf(`{"plan":{"tracks":[{"id":"t1","a":"p1","b":"p2"}],"grid":33},"revision":%q}`, saved))
	if rec.Code != http.StatusBadRequest || body["error"] == nil {
		t.Errorf("PUT of track between missing points: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPut, "/api/v1/trackplan", `{"plan":{},"revision":"x","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT with an unknown field: %d %v", rec.Code, body)
	}
}

func TestTrackPlanBackground(t *testing.T) {
	file, err := trackplan.OpenFile(filepath.Join(t.TempDir(), trackplan.FileName))
	if err != nil {
		t.Fatal(err)
	}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, TrackPlan: file})
	mux := http.NewServeMux()
	a.Register(mux)

	upload := func(content string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("file", "plan.svg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/trackplan/backgrounds", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="4" height="3"/>`
	rec := upload(svg)
	var got struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusCreated || err != nil || !strings.HasSuffix(got.Image, ".svg") {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if rec := upload("not an image"); rec.Code != http.StatusBadRequest {
		t.Errorf("upload of text: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/trackplan/backgrounds/"+got.Image, http.NoBody))
	if rec.Code != http.StatusOK || rec.Body.String() != svg {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want a sandbox", csp)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/trackplan/backgrounds/background-0123456789abcdef.png", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Errorf("get of a missing image: %d", rec.Code)
	}
}

func TestTrackPlanImport(t *testing.T) {
	file, err := trackplan.OpenFile(filepath.Join(t.TempDir(), trackplan.FileName))
	if err != nil {
		t.Fatal(err)
	}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, TrackPlan: file})
	mux := http.NewServeMux()
	a.Register(mux)

	post := func(content, scale string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		if scale != "" {
			if err := mw.WriteField("scale", scale); err != nil {
				t.Fatal(err)
			}
		}
		part, err := mw.CreateFormFile("file", "layout.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/trackplan/import", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	panel := `<layout-config><LayoutEditor>` +
		`<positionablepoint ident="A1" type="ANCHOR" x="0" y="0"/><positionablepoint ident="EB2" type="END_BUMPER" x="24.8" y="0"/>` +
		`<tracksegment ident="T1" connect1name="A1" type1="POS_POINT" connect2name="EB2" type2="POS_POINT"/>` +
		`</LayoutEditor></layout-config>`
	rec := post(panel, "")
	var got trackplan.Import
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || len(got.Plan.Tracks) != 1 || got.Plan.Points[1].X != 248 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if rec := post(panel, "1"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"x":24.8`) {
		t.Errorf("import a millimeter to the unit: %d %s", rec.Code, rec.Body)
	}
	if rec := post(panel, "a lot"); rec.Code != http.StatusBadRequest {
		t.Errorf("import with a bad scale: %d %s", rec.Code, rec.Body)
	}
	if rec := post("tiles: []", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("import of YAML: %d %s", rec.Code, rec.Body)
	}
	if file.Get().Plan.Empty() != true {
		t.Error("an import changed the saved plan")
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
