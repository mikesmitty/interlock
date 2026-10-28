package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mikesmitty/interlock/internal/tracking"
)

// Tracking follows the trains on the layout.
type Tracking interface {
	List() []tracking.Train
	Subscribe(tracking.Subscriber) func()
	Forget(ctx context.Context, id string) error
}

// The train tracking endpoints:
//
//	GET    /api/v1/tracking        {"trains": [...]} the trains detectors have seen, where they are
//	DELETE /api/v1/tracking/{id}   forget where a train is, as when it was taken off the layout; 204
//
// Changes arrive as "tracked" server-sent events, a few times a second
// for a train on the move.

func (a *API) getTracking(w http.ResponseWriter, _ *http.Request) {
	if a.deps.Tracking == nil {
		writeError(w, http.StatusServiceUnavailable, "train tracking is not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trains": a.deps.Tracking.List()})
}

func (a *API) forgetTracked(w http.ResponseWriter, r *http.Request) {
	if a.deps.Tracking == nil {
		writeError(w, http.StatusServiceUnavailable, "train tracking is not available")
		return
	}
	switch err := a.deps.Tracking.Forget(r.Context(), r.PathValue("id")); {
	case errors.Is(err, tracking.ErrNotTracked):
		writeError(w, http.StatusNotFound, "tracking doesn't follow that train")
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
