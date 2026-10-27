package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mikesmitty/interlock/internal/replace"
	"github.com/mikesmitty/interlock/openlcb"
)

// Replacer moves what names one node over to another.
type Replacer interface {
	Replace(ctx context.Context, from, to openlcb.NodeID, apply, forget bool) (replace.Result, error)
}

// The replace endpoint:
//
//	POST /api/v1/nodes/{id}/replace   {with, apply, forget} -> the changes
//
// Node {id} is the one replaced and may already be forgotten; "with" is
// the node taking its place. Without apply nothing changes, and the
// response lists what would. With forget, node {id} is forgotten after.

func (a *API) replaceNode(w http.ResponseWriter, r *http.Request) {
	if a.deps.Replace == nil {
		writeError(w, http.StatusServiceUnavailable, "replacing nodes is not available")
		return
	}
	from, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		With   openlcb.NodeID `json:"with"`
		Apply  bool           `json:"apply"`
		Forget bool           `json:"forget"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with with (the new node's ID), apply and forget: "+err.Error())
		return
	}
	res, err := a.deps.Replace.Replace(r.Context(), from, body.With, body.Apply, body.Forget)
	var invalid *replace.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "result": res})
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
