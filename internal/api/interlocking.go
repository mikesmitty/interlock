package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mikesmitty/interlock/internal/interlocking"
)

// Interlocking sets and releases routes.
type Interlocking interface {
	List() []interlocking.Route
	Get(id string) (interlocking.Route, bool)
	Set(ctx context.Context, id string) (interlocking.Route, error)
	Cancel(ctx context.Context, id string, force bool) (interlocking.Route, error)
	Subscribe(interlocking.Subscriber) func()
}

// The interlocking's endpoints:
//
//	GET  /api/v1/routes             {"routes": [...]} every route the plan has, set or not
//	GET  /api/v1/routes/{id}        one route
//	POST /api/v1/routes/{id}/set    set it: lock its turnouts and move them; 409 with the reason if something is in the way
//	POST /api/v1/routes/{id}/cancel release it; {"force": true} even with a train on it, else 409
//
// Changes arrive as "route" server-sent events, and as one "routes"
// event with all of them when the plan changes them.

func (a *API) interlockingReady(w http.ResponseWriter) bool {
	if a.deps.Interlocking == nil {
		writeError(w, http.StatusServiceUnavailable, "the interlocking is not available")
		return false
	}
	return true
}

func (a *API) listRoutes(w http.ResponseWriter, _ *http.Request) {
	if !a.interlockingReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": a.deps.Interlocking.List()})
}

func (a *API) getRoute(w http.ResponseWriter, r *http.Request) {
	if !a.interlockingReady(w) {
		return
	}
	route, ok := a.deps.Interlocking.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "the plan has no such route")
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (a *API) setRoute(w http.ResponseWriter, r *http.Request) {
	if !a.interlockingReady(w) {
		return
	}
	route, err := a.deps.Interlocking.Set(r.Context(), r.PathValue("id"))
	if err != nil {
		a.routeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (a *API) cancelRoute(w http.ResponseWriter, r *http.Request) {
	if !a.interlockingReady(w) {
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	route, err := a.deps.Interlocking.Cancel(r.Context(), r.PathValue("id"), body.Force)
	if err != nil {
		a.routeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (a *API) routeError(w http.ResponseWriter, err error) {
	var refused *interlocking.RefusedError
	switch {
	case errors.Is(err, interlocking.ErrNoRoute):
		writeError(w, http.StatusNotFound, "the plan has no such route")
	case errors.As(err, &refused):
		writeError(w, http.StatusConflict, refused.Reason)
	default:
		a.entityError(w, err)
	}
}
