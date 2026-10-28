package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/train"
)

func newRosterAPI(t *testing.T) (*testAPI, *cvProgrammer) {
	t.Helper()
	ta, fd, prog := newDecoderAPI(t)
	store := train.OpenCVStore(filepath.Join(t.TempDir(), train.CVDirName), nil)
	trains := train.New(train.Options{Bus: &fakeTrainBus{}, SearchWindow: time.Millisecond, Functions: 3, CVs: store})
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, Decoders: fd, Programmer: prog, Trains: trains})
	mux := http.NewServeMux()
	a.Register(mux)
	ta.api, ta.mux = a, mux
	return ta, prog
}

func TestRosterDecoderEndpoints(t *testing.T) {
	ta, prog := newRosterAPI(t)
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/trains", `{"name": "GP40", "address": 6641}`); rec.Code != http.StatusCreated || body["id"] != "gp40" {
		t.Fatalf("POST trains = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/trains/gp40/decoders", `{"file": "Nope.xml", "model": "X"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown definition = %d", rec.Code)
	}
	rec, body := ta.do(t, http.MethodPost, "/api/v1/trains/gp40/decoders",
		`{"file": "ESU_LokSound5.xml", "family": "ESU LokSound 5", "model": "LokSound 5", "identity": {"manufacturer": 151, "version": 255, "product_id": 33554582}}`)
	if rec.Code != http.StatusCreated || body["decoder"].(map[string]any)["id"] != "loksound-5" || body["train"].(map[string]any)["installed"] != "loksound-5" {
		t.Fatalf("POST decoders = %d %v", rec.Code, body)
	}

	// Values read and written for a roster decoder are saved for it, once
	// a check has found it on the programming track.
	const def = `"file":"ESU_LokSound5.xml","model":"LokSound 5","locomotive":"gp40","decoder":"loksound-5",`
	const check = "/api/v1/trains/gp40/decoders/loksound-5/check"
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"16.0.259"}`); rec.Code != http.StatusConflict {
		t.Fatalf("read before a check = %d %v", rec.Code, body)
	}
	// The decoder answers at its short address, not the roster's.
	rec, body = ta.do(t, http.MethodPost, check, "")
	if rec.Code != http.StatusOK || body["match"] != false || body["session"] != nil || body["manufacturer"] != "Electronic Solutions Ulm GmbH" || body["model"] != "LokSound 5" {
		t.Fatalf("check at another address = %d %v", rec.Code, body)
	}
	if diffs := body["differences"].([]any); len(diffs) != 1 || fmt.Sprint(diffs[0]) != "map[roster:6641 roster_long:true track:3 what:address]" {
		t.Errorf("differences = %v", diffs)
	}
	prog.cvs[29], prog.cvs[17], prog.cvs[18] = 0x26, 0xD9, 0xF1
	rec, body = ta.do(t, http.MethodPost, check, "")
	if rec.Code != http.StatusOK || body["match"] != true || body["session"].(map[string]any)["check"] != "matched" {
		t.Fatalf("check = %d %v", rec.Code, body)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"16.0.259"}`); rec.Code != http.StatusOK || body["ok"] != true || body["error"] != nil {
		t.Fatalf("read = %d %v", rec.Code, body)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"29","write":true,"value":38}`); rec.Code != http.StatusOK || body["ok"] != true {
		t.Fatalf("write = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{"file":"ESU_LokSound5.xml","model":"LokSound 5","locomotive":"gp40","decoder":"nope","cv":"29"}`); rec.Code != http.StatusNotFound {
		t.Errorf("another decoder = %d", rec.Code)
	}
	// What the check read is saved too.
	rec, body = ta.do(t, http.MethodGet, "/api/v1/trains/gp40/decoders/loksound-5/cvs", "")
	if cvs := body["cvs"].(map[string]any); rec.Code != http.StatusOK || cvs["16.0.259"] != float64(40) || cvs["29"] != float64(38) || cvs["8"] != float64(151) || body["updated"] == nil {
		t.Fatalf("GET cvs = %d %v", rec.Code, body)
	}
	// They are the decoder's session, until it is ended by hand or a
	// decoder is identified on the programming track.
	session := func() map[string]any {
		t.Helper()
		_, body := ta.do(t, http.MethodGet, "/api/v1/trains/gp40/decoders/loksound-5/cvs", "")
		s, _ := body["session"].(map[string]any)
		return s
	}
	if s := session(); s == nil || fmt.Sprint(s["read"]) != "[16.0.259 8]" || fmt.Sprint(s["written"]) != "[29]" || s["started"] == nil || s["check"] != "matched" {
		t.Errorf("session = %v", s)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/trains/gp40/decoders/loksound-5/session", ""); rec.Code != http.StatusNoContent || session() != nil {
		t.Errorf("DELETE session = %d, then %v", rec.Code, session())
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/trains/gp40/decoders/nope/session", ""); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE another decoder's session = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"29"}`); rec.Code != http.StatusConflict {
		t.Errorf("read after the session ended = %d", rec.Code)
	}
	// On the main the address picks the decoder, so a read there needs no
	// check, and begins a session without one.
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"29","train":"gp40"}`); rec.Code != http.StatusOK || body["ok"] != true {
		t.Fatalf("read on the main = %d %v", rec.Code, body)
	}
	if s := session(); s == nil || fmt.Sprint(s["read"]) != "[29]" || s["check"] != nil {
		t.Errorf("session after a read on the main = %v", s)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/trains/gp40/decoders/loksound-5/session", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE session = %d", rec.Code)
	}
	// A session can begin without a match, when asked to.
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/trains/gp40/decoders/loksound-5/session", ""); rec.Code != http.StatusOK || body["check"] != "accepted" {
		t.Errorf("POST session = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"29"}`); rec.Code != http.StatusOK || session() == nil {
		t.Fatalf("read = %d, session %v", rec.Code, session())
	}
	// Identify ends it, and begins a new one when the decoder checks out.
	rec, body = ta.do(t, http.MethodPost, "/api/v1/programming/identify", "")
	if s := session(); rec.Code != http.StatusOK || s == nil || s["check"] != "matched" || fmt.Sprint(s["read"]) != "[29 8]" || body["roster"].([]any)[0].(map[string]any)["checked"] != true {
		t.Errorf("identify = %d %v, session %v", rec.Code, body, s)
	}
	prog.cvs[29] = 6
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/programming/identify", ""); rec.Code != http.StatusOK || session() != nil {
		t.Errorf("identify of another decoder = %d, session %v", rec.Code, session())
	}
	if rec, body = ta.do(t, http.MethodPatch, "/api/v1/trains/gp40/decoders/loksound-5/cvs", `{"cvs": {"3": 10}}`); rec.Code != http.StatusOK || len(body["cvs"].(map[string]any)) != 4 {
		t.Errorf("PATCH cvs = %d %v", rec.Code, body)
	}
	if rec, body = ta.do(t, http.MethodPut, "/api/v1/trains/gp40/decoders/loksound-5/cvs", `{"cvs": {"3": 11}}`); rec.Code != http.StatusOK || len(body["cvs"].(map[string]any)) != 1 {
		t.Errorf("PUT cvs = %d %v", rec.Code, body)
	}

	// Changes not written yet are kept until a write puts them in the
	// decoder.
	if rec, _ = ta.do(t, http.MethodPut, "/api/v1/trains/gp40/decoders/loksound-5/changes", `{"changes": {"29": {"value": 0, "bits": 32}}}`); rec.Code != http.StatusNoContent {
		t.Errorf("PUT changes = %d", rec.Code)
	}
	if rec, _ = ta.do(t, http.MethodPut, "/api/v1/trains/gp40/decoders/loksound-5/changes", `{"changes": {"29": {"value": 0, "bits": 0}}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT a change of no bits = %d", rec.Code)
	}
	if _, body = ta.do(t, http.MethodGet, "/api/v1/trains/gp40/decoders/loksound-5/cvs", ""); body["changes"].(map[string]any)["29"].(map[string]any)["bits"] != float64(32) {
		t.Errorf("GET cvs with changes = %v", body)
	}
	ta.do(t, http.MethodPost, "/api/v1/trains/gp40/decoders/loksound-5/session", "")
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", `{`+def+`"cv":"29","write":true,"value":6}`); rec.Code != http.StatusOK || body["ok"] != true {
		t.Fatalf("write = %d %v", rec.Code, body)
	}
	if _, body = ta.do(t, http.MethodGet, "/api/v1/trains/gp40/decoders/loksound-5/cvs", ""); body["changes"] != nil {
		t.Errorf("changes after writing them = %v", body["changes"])
	}

	// Decoder profiles keep a copy of the values under a name.
	const profiles = "/api/v1/trains/gp40/decoders/loksound-5/profiles"
	if rec, body := ta.do(t, http.MethodPost, profiles, `{"name": "As delivered"}`); rec.Code != http.StatusCreated || body["name"] != "As delivered" || body["cvs"].(map[string]any)["29"] != float64(6) {
		t.Errorf("POST profiles = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, profiles, `{"name": "As delivered"}`); rec.Code != http.StatusConflict {
		t.Errorf("POST profiles twice = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, profiles, `{"name": "As delivered", "replace": true}`); rec.Code != http.StatusCreated {
		t.Errorf("POST profiles to replace = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodPost, profiles, `{"name": " "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("POST profiles without a name = %d", rec.Code)
	}
	if _, body = ta.do(t, http.MethodGet, "/api/v1/trains/gp40/decoders/loksound-5/cvs", ""); len(body["profiles"].([]any)) != 1 {
		t.Errorf("GET cvs with a profile = %v", body["profiles"])
	}
	if rec, _ := ta.do(t, http.MethodDelete, profiles+"/As%20delivered", ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE profile = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodDelete, profiles+"/As%20delivered", ""); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE profile twice = %d", rec.Code)
	}

	// Identify names the roster decoder on the track.
	prog.cvs[29], prog.cvs[17], prog.cvs[18] = 0x26, 0xD9, 0xF1
	rec, body = ta.do(t, http.MethodPost, "/api/v1/programming/identify", "")
	roster := body["roster"].([]any)
	if rec.Code != http.StatusOK || len(roster) != 1 {
		t.Fatalf("identify = %d %v", rec.Code, body)
	}
	if m := roster[0].(map[string]any); m["train"] != "gp40" || m["decoder"] != "loksound-5" || m["installed"] != true || m["identity"] != true {
		t.Errorf("roster match = %v", m)
	}

	if rec, body = ta.do(t, http.MethodPatch, "/api/v1/trains/gp40/decoders/loksound-5", `{"name": "Sound"}`); rec.Code != http.StatusOK || body["decoders"].([]any)[0].(map[string]any)["name"] != "Sound" {
		t.Errorf("PATCH decoder = %d %v", rec.Code, body)
	}
	if rec, body = ta.do(t, http.MethodPost, "/api/v1/trains/gp40/install", `{"decoder": ""}`); rec.Code != http.StatusOK || body["installed"] != nil {
		t.Errorf("install none = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodPost, "/api/v1/trains/gp40/install", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("install without decoder = %d", rec.Code)
	}
	if rec, body = ta.do(t, http.MethodDelete, "/api/v1/trains/gp40/decoders/loksound-5", ""); rec.Code != http.StatusOK || body["decoders"] != nil {
		t.Errorf("DELETE decoder = %d %v", rec.Code, body)
	}
	if rec, _ = ta.do(t, http.MethodGet, "/api/v1/trains/gp40/decoders/loksound-5/cvs", ""); rec.Code != http.StatusNotFound {
		t.Errorf("cvs of a removed decoder = %d", rec.Code)
	}
}

const importLocomotive = `<?xml version="1.0" encoding="UTF-8"?>
<locomotive-config>
  <locomotive id="GP40 1" fileName="GP40_1.xml" roadNumber="6641" roadName="CSX" dccAddress="6641">
    <decoder model="LokSound 5" family="ESU LokSound 5" />
    <locoaddress><dcclocoaddress number="6641" longaddress="yes" /></locoaddress>
    <functionlabels><functionlabel num="2" lockable="false">Horn</functionlabel></functionlabels>
    <values><CVvalue name="8" value="151" /><CVvalue name="16.0.259" value="12" /></values>
  </locomotive>
</locomotive-config>`

func postImport(t *testing.T, ta *testAPI, plan string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "GP40_1.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(importLocomotive))
	if plan != "" {
		_ = mw.WriteField("plan", plan)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/trains/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	ta.mux.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("import: %q", rec.Body)
	}
	return rec.Code, body
}

func TestImportEndpoint(t *testing.T) {
	ta, _ := newRosterAPI(t)
	code, body := postImport(t, ta, "")
	if code != http.StatusOK {
		t.Fatalf("preview = %d %v", code, body)
	}
	locos := body["locomotives"].([]any)
	l := locos[0].(map[string]any)
	if len(locos) != 1 || l["key"] != "GP40 1" || l["cv_count"] != float64(2) || l["definition"] != true || l["decoder"].(map[string]any)["file"] != "ESU_LokSound5.xml" {
		t.Fatalf("preview = %v", body)
	}
	if rec, body := ta.do(t, http.MethodGet, "/api/v1/trains", ""); len(body["trains"].([]any)) != 0 {
		t.Fatalf("the preview changed the roster: %d %v", rec.Code, body)
	}
	plan, _ := json.Marshal(body["plan"])
	code, body = postImport(t, ta, string(plan))
	if code != http.StatusOK || body["error"] != nil {
		t.Fatalf("import = %d %v", code, body)
	}
	res := body["result"].(map[string]any)
	tr := res["trains"].([]any)[0].(map[string]any)
	if res["decoders"] != float64(1) || res["cvs"] != float64(2) || tr["id"] != "gp40-1" || tr["road_name"] != "CSX" {
		t.Fatalf("result = %v", res)
	}
	if code, body = postImport(t, ta, `[{"key": "GP40 1", "action": "fly"}]`); code != http.StatusBadRequest {
		t.Errorf("bad plan = %d %v", code, body)
	}
}
