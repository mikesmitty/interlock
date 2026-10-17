package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mikesmitty/interlock/internal/clock"
)

// Clock is the fast clock.
type Clock interface {
	State() clock.State
	Apply(clock.Change) (clock.State, error)
	Subscribe(func(clock.State)) func()
}

// The clock endpoints:
//
//	GET  /api/v1/clock   the fast clock's state
//	POST /api/v1/clock   {running, rate, time, date}, any subset -> new state
//
// Changes and every fast minute arrive as "clock" server-sent events.

func (a *API) getClock(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.deps.Clock.State())
}

func (a *API) setClock(w http.ResponseWriter, r *http.Request) {
	var change clock.Change
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&change); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with running, rate, time or date: "+err.Error())
		return
	}
	st, err := a.deps.Clock.Apply(change)
	if err != nil {
		var invalid *clock.InvalidError
		if errors.As(err, &invalid) {
			writeError(w, http.StatusBadRequest, invalid.Reason)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}
