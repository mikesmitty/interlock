package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/decoder"
	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/openlcb"
)

type fakeDecoders struct {
	idx *decoder.Index
	err error
	def *decoder.Definition
}

func (f *fakeDecoders) Status() decoder.Status {
	return decoder.Status{Source: decoder.SourceGitHub, Ref: "v5.16"}
}

func (f *fakeDecoders) Index() (*decoder.Index, error) { return f.idx, f.err }

func (f *fakeDecoders) Definition(file, model string) (*decoder.Definition, error) {
	if f.def == nil || file != f.def.File || model != f.def.Model {
		return nil, decoder.ErrUnknownDecoder
	}
	return f.def, nil
}

// cvProgrammer is a decoder on the programming track, logging every CV
// operation.
type cvProgrammer struct {
	fakeProgrammer
	cvs map[int]int
	log []string
}

func (p *cvProgrammer) Read(_ context.Context, cv int) (programmer.Op, error) {
	p.log = append(p.log, fmt.Sprintf("R%d", cv))
	v, ok := p.cvs[cv]
	if !ok {
		return programmer.Op{CV: cv}, &openlcb.Error{Code: programmer.CodeNoLocomotive}
	}
	return programmer.Op{CV: cv, Value: v, OK: true}, nil
}

func (p *cvProgrammer) Write(_ context.Context, cv, value int) (programmer.Op, error) {
	p.log = append(p.log, fmt.Sprintf("W%d=%d", cv, value))
	p.cvs[cv] = value
	return programmer.Op{CV: cv, Value: value, OK: true}, nil
}

func (p *cvProgrammer) WritePOM(_ context.Context, train string, cv, value int) (programmer.Op, error) {
	p.log = append(p.log, fmt.Sprintf("POM %s W%d=%d", train, cv, value))
	p.cvs[cv] = value
	return programmer.Op{CV: cv, Value: value, OK: true}, nil
}

// ReadPOM reads the same decoder, as though it had moved to the main.
func (p *cvProgrammer) ReadPOM(_ context.Context, train string, cv int) (programmer.Op, error) {
	p.log = append(p.log, fmt.Sprintf("POM %s R%d", train, cv))
	v, ok := p.cvs[cv]
	if !ok {
		return programmer.Op{CV: cv}, &openlcb.Error{Code: programmer.CodeNoLocomotive}
	}
	return programmer.Op{CV: cv, Value: v, OK: true}, nil
}

func newDecoderAPI(t *testing.T) (*testAPI, *fakeDecoders, *cvProgrammer) {
	t.Helper()
	fd := &fakeDecoders{
		idx: &decoder.Index{
			Version:       "717",
			Manufacturers: []decoder.Manufacturer{{Name: "Electronic Solutions Ulm GmbH", ID: 151}},
			Families: []decoder.Family{{
				Name: "ESU LokSound 5", Manufacturer: "Electronic Solutions Ulm GmbH", File: "ESU_LokSound5.xml", LowVersion: 255, HighVersion: 255,
				Models: []decoder.Model{{Name: "LokSound 5", ProductIDs: []string{"33554582"}, LowVersion: -1, HighVersion: -1}},
			}},
		},
		def: &decoder.Definition{
			File: "ESU_LokSound5.xml", Model: "LokSound 5",
			Programming: decoder.Programming{Index: &decoder.Indexing{PI: 31, SI: 32, Cache: true}},
			CVs:         []decoder.CV{{Name: "29"}, {Name: "16.0.259"}, {Name: "16.0.260"}, {Name: "8", ReadOnly: true}, {Name: "64", WriteOnly: true}, {Name: "99", InfoOnly: true}},
		},
	}
	prog := &cvProgrammer{cvs: map[int]int{8: 151, 7: 255, 261: 0x96, 262: 0, 263: 0, 264: 2, 29: 6, 1: 3, 259: 40, 260: 41}}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, Decoders: fd, Programmer: prog})
	mux := http.NewServeMux()
	a.Register(mux)
	return &testAPI{api: a, mux: mux}, fd, prog
}

func TestDecoderIndexEndpoints(t *testing.T) {
	ta, fd, _ := newDecoderAPI(t)
	if rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders", ""); rec.Code != http.StatusOK || body["source"] != "github" || body["ref"] != "v5.16" {
		t.Fatalf("GET decoders: %d %v", rec.Code, body)
	}
	rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders/index", "")
	if rec.Code != http.StatusOK || body["version"] != "717" || len(body["families"].([]any)) != 1 {
		t.Fatalf("GET index: %d %v", rec.Code, body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/decoders/index", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip, br")
	zrec := httptest.NewRecorder()
	ta.mux.ServeHTTP(zrec, req)
	if zrec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("index not compressed: %v", zrec.Header())
	}
	zr, err := gzip.NewReader(zrec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var unzipped decoder.Index
	if err := json.NewDecoder(zr).Decode(&unzipped); err != nil || unzipped.Families[0].File != "ESU_LokSound5.xml" {
		t.Fatalf("compressed index = %+v, %v", unzipped, err)
	}

	if rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders/definition?file=ESU_LokSound5.xml&model=LokSound%205", ""); rec.Code != http.StatusOK || body["model"] != "LokSound 5" {
		t.Errorf("GET definition: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/decoders/definition?file=ESU_LokSound5.xml&model=Nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown model: %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/decoders/definition?file=ESU_LokSound5.xml", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no model: %d", rec.Code)
	}

	fd.idx, fd.err = nil, errors.New("fetch from GitHub: 503 Service Unavailable")
	if rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders/index", ""); rec.Code != http.StatusBadGateway || body["error"] == nil {
		t.Errorf("index failure: %d %v", rec.Code, body)
	}
}

func TestIdentifyEndpoint(t *testing.T) {
	ta, _, prog := newDecoderAPI(t)
	rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/identify", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("identify: %d %v", rec.Code, body)
	}
	id := body["identity"].(map[string]any)
	cands := body["candidates"].([]any)
	if id["manufacturer"] != float64(151) || id["product_id"] != float64(33554582) || body["manufacturer"] != "Electronic Solutions Ulm GmbH" ||
		len(cands) != 1 || cands[0].(map[string]any)["model"] != "LokSound 5" || cands[0].(map[string]any)["exact"] != true {
		t.Errorf("identify = %v", body)
	}
	if addr := body["address"].(map[string]any); addr["address"] != float64(3) || addr["long"] != false || len(body["roster"].([]any)) != 0 {
		t.Errorf("address = %v, roster %v", body["address"], body["roster"])
	}
	if got := strings.Join(prog.log, " "); got != "R8 R7 W31=0 W32=255 R261 R262 R263 R264 R29 R1" {
		t.Errorf("identify ran %s", got)
	}

	delete(prog.cvs, 8)
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/programming/identify", ""); rec.Code != http.StatusBadGateway || !strings.Contains(body["error"].(string), "No decoder answered") {
		t.Errorf("no decoder: %d %v", rec.Code, body)
	}
}

func TestDecoderCVEndpoint(t *testing.T) {
	ta, _, prog := newDecoderAPI(t)
	call := func(body string) (int, map[string]any) {
		t.Helper()
		rec, resp := ta.do(t, http.MethodPost, "/api/v1/programming/decoder-cv", body)
		return rec.Code, resp
	}
	const def = `"file":"ESU_LokSound5.xml","model":"LokSound 5",`
	if code, body := call(`{` + def + `"cv":"16.0.259"}`); code != http.StatusOK || body["value"] != float64(40) || body["ok"] != true {
		t.Fatalf("indexed read: %d %v", code, body)
	}
	if code, body := call(`{` + def + `"cv":"16.0.260"}`); code != http.StatusOK || body["value"] != float64(41) {
		t.Fatalf("second indexed read: %d %v", code, body)
	}
	if code, body := call(`{` + def + `"cv":"29","write":true,"value":38}`); code != http.StatusOK || body["ok"] != true || prog.cvs[29] != 38 {
		t.Fatalf("write: %d %v", code, body)
	}
	if code, body := call(`{` + def + `"cv":"29","write":true,"value":5,"train":"3"}`); code != http.StatusOK || body["ok"] != true {
		t.Fatalf("write on the main: %d %v", code, body)
	}
	if code, body := call(`{` + def + `"cv":"29","train":"3"}`); code != http.StatusOK || body["ok"] != true || body["value"] != float64(5) {
		t.Fatalf("read on the main: %d %v", code, body)
	}
	// An indexed read on the main writes the index there, apart from the
	// programming track's.
	if code, body := call(`{` + def + `"cv":"16.0.259","train":"3"}`); code != http.StatusOK || body["value"] != float64(40) {
		t.Fatalf("indexed read on the main: %d %v", code, body)
	}
	// The second indexed read reuses the index the first one wrote.
	if got := strings.Join(prog.log, " "); got != "W31=16 W32=0 R259 R260 W29=38 POM 3 W29=5 POM 3 R29 POM 3 W31=16 POM 3 W32=0 POM 3 R259" {
		t.Errorf("operations: %s", got)
	}
	delete(prog.cvs, 8)
	if code, body := call(`{` + def + `"cv":"8","train":"3"}`); code != http.StatusOK || body["ok"] != false || body["error"] == "" {
		t.Errorf("failed read on the main: %d %v", code, body)
	}
	prog.cvs[8] = 151

	delete(prog.cvs, 29)
	if code, body := call(`{` + def + `"cv":"29"}`); code != http.StatusOK || body["ok"] != false || !strings.Contains(body["error"].(string), "No decoder answered") {
		t.Errorf("failed read: %d %v", code, body)
	}

	bad := []struct {
		name, body string
		code       int
	}{
		{"read-only", `{` + def + `"cv":"8","write":true,"value":8}`, http.StatusBadRequest},
		{"write-only", `{` + def + `"cv":"64"}`, http.StatusBadRequest},
		{"info-only", `{` + def + `"cv":"99"}`, http.StatusBadRequest},
		{"not in the definition", `{` + def + `"cv":"300"}`, http.StatusBadRequest},
		{"write without a value", `{` + def + `"cv":"29","write":true}`, http.StatusBadRequest},
		{"unknown decoder", `{"file":"Nope.xml","model":"X","cv":"29"}`, http.StatusNotFound},
	}
	for _, tt := range bad {
		if code, body := call(tt.body); code != tt.code {
			t.Errorf("%s: %d %v, want %d", tt.name, code, body, tt.code)
		}
	}
}
