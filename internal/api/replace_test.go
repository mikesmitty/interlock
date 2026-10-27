package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/mikesmitty/interlock/internal/refs"
	"github.com/mikesmitty/interlock/internal/replace"
	"github.com/mikesmitty/interlock/openlcb"
)

type fakeReplacer struct {
	from, to      openlcb.NodeID
	apply, forget bool
}

func (f *fakeReplacer) Replace(_ context.Context, from, to openlcb.NodeID, apply, forget bool) (replace.Result, error) {
	if to == 0 {
		return replace.Result{}, &replace.InvalidError{Reason: "no new node"}
	}
	f.from, f.to, f.apply, f.forget = from, to, apply, forget
	return replace.Result{From: from, To: to, Applied: apply, Changes: []refs.Change{{Area: "Track plan", Kind: refs.KindEntity, From: "a", To: "b"}}}, nil
}

func TestReplaceNode(t *testing.T) {
	fr := &fakeReplacer{}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, Replace: fr})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}

	rec, body := ta.do(t, http.MethodPost, "/api/v1/nodes/0A.FF.90.24.A6.34/replace", `{"with":"05.01.01.01.22.00","apply":true,"forget":true}`)
	if rec.Code != http.StatusOK || body["applied"] != true || body["to"] != "05.01.01.01.22.00" {
		t.Fatalf("replace: %d %v", rec.Code, body)
	}
	if fr.from != 0x0aff9024a634 || fr.to != 0x050101012200 || !fr.apply || !fr.forget {
		t.Errorf("replacer got %+v", fr)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/nodes/0A.FF.90.24.A6.34/replace", `{}`); rec.Code != http.StatusBadRequest || body["error"] != "no new node" {
		t.Errorf("no new node: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/nodes/0A.FF.90.24.A6.34/replace", `{"with":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad node ID: %d", rec.Code)
	}
}
