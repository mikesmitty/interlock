package api

import (
	"net/http"

	"github.com/mikesmitty/interlock/internal/tracking"
)

// Tracking follows the trains on the layout.
type Tracking interface {
	List() []tracking.Train
	Subscribe(tracking.Subscriber) func()
}

// The train tracking endpoint:
//
//	GET /api/v1/tracking   {"trains": [...]} the trains detectors have seen, where they are
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
