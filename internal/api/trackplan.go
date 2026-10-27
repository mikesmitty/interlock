package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikesmitty/interlock/internal/trackplan"
)

// TrackPlan is the track diagram.
type TrackPlan interface {
	Get() trackplan.State
	Put(plan trackplan.Plan, revision string) (trackplan.State, error)
	Subscribe(func(trackplan.State)) func()
	SaveBackground(data []byte) (string, error)
	BackgroundFile(name string) (string, error)
}

// The track plan endpoints:
//
//	GET  /api/v1/trackplan                     the plan, its revision and its topology
//	PUT  /api/v1/trackplan                     {plan, revision} -> the saved state
//	POST /api/v1/trackplan/backgrounds         multipart field "file": store an image -> {image}
//	GET  /api/v1/trackplan/backgrounds/{name}  a stored image
//
// A save names the revision it was based on and gets 409 when the plan
// was saved by someone else since. Saves arrive as "trackplan"
// server-sent events. A stored image becomes the plan's background when a
// plan naming it is saved; saving deletes the images the plan doesn't
// name.

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

func (a *API) uploadBackground(w http.ResponseWriter, r *http.Request) {
	if !a.trackPlanReady(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, trackplan.MaxBackgroundSize+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "send the image as a multipart form field named file: "+err.Error())
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "the form needs a file field holding the image")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the image: "+err.Error())
		return
	}
	name, err := a.deps.TrackPlan.SaveBackground(data)
	var invalid *trackplan.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusCreated, map[string]string{"image": name})
	}
}

func (a *API) getBackground(w http.ResponseWriter, r *http.Request) {
	if !a.trackPlanReady(w) {
		return
	}
	p, err := a.deps.TrackPlan.BackgroundFile(r.PathValue("name"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "no such background image")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", trackplan.BackgroundTypes[filepath.Ext(p)])
	// The name follows the contents, so it never changes.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	// An SVG opened on its own must not run scripts as this site.
	h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; sandbox")
	http.ServeFile(w, r, p)
}
