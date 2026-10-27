package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mikesmitty/interlock/internal/trackplan"
)

// TrackPlan is the track diagram.
type TrackPlan interface {
	Get() trackplan.State
	Put(plan trackplan.Plan, revision string) (trackplan.State, error)
	Subscribe(func(trackplan.State)) func()
}

// The track plan endpoints:
//
//	GET /api/v1/trackplan   the plan, its revision and its topology
//	PUT /api/v1/trackplan   {plan, revision} -> the saved state
//
// A save names the revision it was based on and gets 409 when the plan
// was saved by someone else since. Saves arrive as "trackplan"
// server-sent events.

// maxTrackPlan bounds a saved plan's JSON.
const maxTrackPlan = 8 << 20

func (a *API) trackPlanReady(w http.ResponseWriter) bool {
	if a.deps.TrackPlan == nil {
		writeError(w, http.StatusServiceUnavailable, "the track plan is not available")
		return false
	}
	return true
}

func (a *API) getTrackPlan(w http.ResponseWriter, _ *http.Request) {
	if !a.trackPlanReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, a.deps.TrackPlan.Get())
}

func (a *API) putTrackPlan(w http.ResponseWriter, r *http.Request) {
	if !a.trackPlanReady(w) {
		return
	}
	var body struct {
		Plan     trackplan.Plan `json:"plan"`
		Revision string         `json:"revision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTrackPlan))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with plan and revision: "+err.Error())
		return
	}
	st, err := a.deps.TrackPlan.Put(body.Plan, body.Revision)
	var invalid *trackplan.InvalidError
	switch {
	case errors.Is(err, trackplan.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":    "The track plan was saved by someone else since you opened it.",
			"revision": a.deps.TrackPlan.Get().Revision,
		})
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, st)
	}
}
