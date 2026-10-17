package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mikesmitty/interlock/internal/backup"
)

// Backups writes and stages backups of the data directory.
type Backups interface {
	Write(ctx context.Context, w io.Writer) error
	Stage(r io.ReaderAt, size int64) (backup.Manifest, error)
}

// The backup endpoints:
//
//	GET  /api/v1/backup    download a zip of the data directory
//	POST /api/v1/restore   multipart form with "file": stage it, then restart
//	POST /api/v1/restart   restart Interlock (reloads interlock.yaml too)
//
// Restart answers 202 and then stops the server; it comes back on the
// same address within a few seconds.

func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	// Build it first, so an error can still be reported as one.
	var buf bytes.Buffer
	if err := a.deps.Backups.Write(r.Context(), &buf); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write the backup: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+backup.FileName(time.Now())+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (a *API) restore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxArchive+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "send the backup as a multipart form field named file: "+err.Error())
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "the form needs a file field holding the backup")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the file: "+err.Error())
		return
	}
	manifest, err := a.deps.Backups.Stage(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		var invalid *backup.InvalidError
		if errors.As(err, &invalid) {
			writeError(w, http.StatusBadRequest, invalid.Reason)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"manifest": manifest, "restarting": a.deps.Restart != nil})
	a.restartSoon()
}

func (a *API) restart(w http.ResponseWriter, _ *http.Request) {
	if a.deps.Restart == nil {
		writeError(w, http.StatusNotImplemented, "this server cannot restart itself")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
	a.restartSoon()
}

// restartSoon restarts after the response has gone out.
func (a *API) restartSoon() {
	if a.deps.Restart == nil {
		return
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.deps.Restart()
	}()
}
