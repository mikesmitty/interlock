package api

import (
	"encoding/json"
	"net/http"

	"github.com/mikesmitty/interlock/internal/train"
)

// The consist endpoints:
//
//	GET    /api/v1/consists                   {consists}
//	POST   /api/v1/consists                   {name, kind, address, members: [{train, reverse, f0, functions}]}, the lead first
//	GET    /api/v1/consists/{id}
//	PATCH  /api/v1/consists/{id}              any of {name, kind, address, members}; a made-up consist can only be renamed
//	DELETE /api/v1/consists/{id}              only when it is apart
//	POST   /api/v1/consists/{id}/make-up      put it together on the layout
//	POST   /api/v1/consists/{id}/break-up     take it apart; it has to be stopped

func (a *API) listConsists(w http.ResponseWriter, _ *http.Request) {
	list := []train.ConsistState{}
	if a.deps.Trains != nil {
		list = append(list, a.deps.Trains.Consists()...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"consists": list})
}

func (a *API) getConsist(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	c, ok := a.deps.Trains.Consist(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no consist "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (a *API) createConsist(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var c train.Consist
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a name and members")
		return
	}
	state, err := a.deps.Trains.CreateConsist(c)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, state)
}

// consistPatch is a consist change: only the fields present change.
type consistPatch struct {
	Name    *string         `json:"name"`
	Kind    *string         `json:"kind"`
	Address *uint16         `json:"address"`
	Members *[]train.Member `json:"members"`
}

func (a *API) updateConsist(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var p consistPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with the consist fields to change")
		return
	}
	state, err := a.deps.Trains.UpdateConsist(r.PathValue("id"), func(c *train.Consist) error {
		if p.Name != nil {
			c.Name = *p.Name
		}
		if p.Kind != nil {
			c.Kind = *p.Kind
		}
		if p.Address != nil {
			c.Address = *p.Address
		}
		if p.Members != nil {
			c.Members = *p.Members
		}
		return nil
	})
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *API) deleteConsist(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	if err := a.deps.Trains.DeleteConsist(r.PathValue("id")); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) makeUpConsist(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	state, err := a.deps.Trains.MakeUp(r.Context(), r.PathValue("id"))
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *API) breakUpConsist(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	state, err := a.deps.Trains.BreakUp(r.Context(), r.PathValue("id"))
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}
