package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mikesmitty/interlock/internal/driving"
)

// Driving drives trains on their own.
type Driving interface {
	List() []driving.Drive
	Get(id string) (driving.Drive, bool)
	Drive(ctx context.Context, id string, req driving.Request) (driving.Drive, error)
	Measure(ctx context.Context, id string, req driving.MeasureRequest) (driving.Drive, error)
	Stop(ctx context.Context, id string) (driving.Drive, error)
	Subscribe(driving.Subscriber) func()
}

// The driving endpoints, by tracked train ID:
//
//	GET  /api/v1/driving                 {"drives": [...]} the drives going, and each train's last
//	GET  /api/v1/driving/{train}         a train's drive
//	POST /api/v1/driving/{train}         drive it to a block: {"to", "marker", "speed", "direction", "braking"}; 409 with the reason if it can't
//	POST /api/v1/driving/{train}/measure run it around to measure its speeds: {"steps": [...], "direction"}
//	POST /api/v1/driving/{train}/stop    brake it to a stand and end the drive
//
// Changes arrive as "drive" server-sent events.

func (a *API) drivingReady(w http.ResponseWriter) bool {
	if a.deps.Driving == nil {
		writeError(w, http.StatusServiceUnavailable, "driving is not available")
		return false
	}
	return true
}

func (a *API) listDrives(w http.ResponseWriter, _ *http.Request) {
	if !a.drivingReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drives": a.deps.Driving.List()})
}

func (a *API) getDrive(w http.ResponseWriter, r *http.Request) {
	if !a.drivingReady(w) {
		return
	}
	d, ok := a.deps.Driving.Get(r.PathValue("train"))
	if !ok {
		writeError(w, http.StatusNotFound, "the train hasn't been driven")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// decodeBody reads a small JSON body into v; an empty body leaves it as
// it is.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return false
	}
	return true
}

func (a *API) startDrive(w http.ResponseWriter, r *http.Request) {
	if !a.drivingReady(w) {
		return
	}
	var req driving.Request
	if !decodeBody(w, r, &req) {
		return
	}
	d, err := a.deps.Driving.Drive(r.Context(), r.PathValue("train"), req)
	if err != nil {
		a.driveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) measureSpeeds(w http.ResponseWriter, r *http.Request) {
	if !a.drivingReady(w) {
		return
	}
	var req driving.MeasureRequest
	if !decodeBody(w, r, &req) {
		return
	}
	d, err := a.deps.Driving.Measure(r.Context(), r.PathValue("train"), req)
	if err != nil {
		a.driveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) stopDrive(w http.ResponseWriter, r *http.Request) {
	if !a.drivingReady(w) {
		return
	}
	d, err := a.deps.Driving.Stop(r.Context(), r.PathValue("train"))
	if err != nil {
		a.driveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) driveError(w http.ResponseWriter, err error) {
	var refused *driving.RefusedError
	switch {
	case errors.Is(err, driving.ErrNoTrain):
		writeError(w, http.StatusNotFound, "tracking doesn't follow that train")
	case errors.Is(err, driving.ErrNotDriving):
		writeError(w, http.StatusConflict, "the train isn't being driven")
	case errors.As(err, &refused):
		writeError(w, http.StatusConflict, refused.Reason)
	default:
		a.entityError(w, err)
	}
}
