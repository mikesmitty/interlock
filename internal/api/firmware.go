package api

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/mikesmitty/interlock/internal/firmware"
	"github.com/mikesmitty/interlock/openlcb"
)

// Firmware upgrades nodes.
type Firmware interface {
	Start(ctx context.Context, id openlcb.NodeID, file string, data []byte) (firmware.Job, error)
	Cancel(id openlcb.NodeID) error
	Job(id openlcb.NodeID) (firmware.Job, bool)
	Jobs() []firmware.Job
	Subscribe(func(firmware.Job)) func()
}

// The firmware endpoints:
//
//	GET    /api/v1/firmware               {jobs} the latest upgrade of every node
//	GET    /api/v1/nodes/{id}/firmware    the node's latest upgrade
//	POST   /api/v1/nodes/{id}/firmware    multipart form with "file" -> 202 job
//	DELETE /api/v1/nodes/{id}/firmware    cancel the upgrade in progress
//
// Progress arrives as "firmware" server-sent events.

// maxFirmwareUpload bounds an uploaded file; images are a few hundred KB.
const maxFirmwareUpload = firmware.MaxImage + 1<<20

func (a *API) listFirmware(w http.ResponseWriter, _ *http.Request) {
	jobs := a.deps.Firmware.Jobs()
	if jobs == nil {
		jobs = []firmware.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (a *API) getFirmware(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	job, ok := a.deps.Firmware.Job(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no firmware upgrade has been started for "+id.String())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) startFirmware(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFirmwareUpload)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "send the firmware as a multipart form field named file: "+err.Error())
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "the form needs a file field holding the firmware")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the file: "+err.Error())
		return
	}
	job, err := a.deps.Firmware.Start(r.Context(), id, header.Filename, data)
	if err != nil {
		a.firmwareError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) cancelFirmware(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	if err := a.deps.Firmware.Cancel(id); err != nil {
		a.firmwareError(w, err)
		return
	}
	job, _ := a.deps.Firmware.Job(id)
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) firmwareError(w http.ResponseWriter, err error) {
	var invalid *firmware.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case errors.Is(err, firmware.ErrNotFound):
		writeError(w, http.StatusNotFound, "unknown node")
	case errors.Is(err, firmware.ErrNoJob):
		writeError(w, http.StatusNotFound, "no upgrade is in progress for that node")
	case errors.Is(err, firmware.ErrBusy):
		writeError(w, http.StatusConflict, "an upgrade is already in progress; one runs at a time")
	case errors.Is(err, firmware.ErrOffline):
		writeError(w, http.StatusConflict, "the node is offline")
	default:
		a.busError(w, err)
	}
}
