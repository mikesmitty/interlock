package api

import (
	"net/http"

	"github.com/mikesmitty/interlock/internal/signals"
)

// Signals are what the track plan's signals show.
type Signals interface {
	List() []signals.Signal
	Subscribe(signals.Subscriber) func()
}

// The signals' endpoint:
//
//	GET /api/v1/signals    {"signals": [...]} what every signal on the plan shows
//
// Changes arrive as "signal" server-sent events, and as one "signals"
// event with all of them when the plan changes them.

func (a *API) listSignals(w http.ResponseWriter, _ *http.Request) {
	if a.deps.Signals == nil {
		writeError(w, http.StatusServiceUnavailable, "the signals are not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": a.deps.Signals.List()})
}
