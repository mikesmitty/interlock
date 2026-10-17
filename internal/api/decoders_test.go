package api

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mikesmitty/interlock/internal/decoder"
)

type fakeDecoders struct {
	idx *decoder.Index
	err error
}

func (f *fakeDecoders) Status() decoder.Status {
	return decoder.Status{Source: decoder.SourceGitHub, Ref: "v5.16"}
}

func (f *fakeDecoders) Index() (*decoder.Index, error) { return f.idx, f.err }

func TestDecoderEndpoints(t *testing.T) {
	fd := &fakeDecoders{idx: &decoder.Index{
		Version:       "717",
		Manufacturers: []decoder.Manufacturer{{Name: "Electronic Solutions Ulm GmbH", ID: 151}},
		Families:      []decoder.Family{{Name: "ESU LokSound 5", Manufacturer: "Electronic Solutions Ulm GmbH", File: "ESU_LokSound5.xml", LowVersion: 255, HighVersion: 255, Models: []decoder.Model{{Name: "LokSound 5"}}}},
	}}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, Decoders: fd})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}

	if rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders", ""); rec.Code != http.StatusOK || body["source"] != "github" || body["ref"] != "v5.16" {
		t.Fatalf("GET decoders: %d %v", rec.Code, body)
	}
	rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders/index", "")
	if rec.Code != http.StatusOK || body["version"] != "717" || len(body["families"].([]any)) != 1 {
		t.Fatalf("GET index: %d %v", rec.Code, body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/decoders/index", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	zrec := httptest.NewRecorder()
	mux.ServeHTTP(zrec, req)
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

	fd.idx, fd.err = nil, errors.New("fetch from GitHub: 503 Service Unavailable")
	if rec, body := ta.do(t, http.MethodGet, "/api/v1/decoders/index", ""); rec.Code != http.StatusBadGateway || body["error"] == nil {
		t.Errorf("index failure: %d %v", rec.Code, body)
	}
}
