// Package api serves Interlock's HTTP API under /api/v1. The web interface
// is its only built-in client, so the API is the full surface of the server.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mikesmitty/interlock/internal/version"
	"github.com/mikesmitty/interlock/openlcb"
)

// API holds what the handlers need.
type API struct {
	nodeID  openlcb.NodeID
	started time.Time
	mux     *http.ServeMux
}

// New returns an API for a server running as nodeID.
func New(nodeID openlcb.NodeID) *API {
	a := &API{nodeID: nodeID, started: time.Now(), mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /api/v1/health", a.health)
	return a
}

// Register mounts the API on mux under /api/.
func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("/api/", a)
}

// ServeHTTP routes API requests. Paths the API does not know get a JSON 404,
// and known paths with the wrong method a JSON 405 with an Allow header,
// instead of the mux's plain text pages.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handler, pattern := a.mux.Handler(r)
	if pattern == "" {
		handler = jsonErrors(handler)
	}
	handler.ServeHTTP(w, r)
}

type healthResponse struct {
	Status        string         `json:"status"`
	Version       string         `json:"version"`
	Commit        string         `json:"commit,omitempty"`
	NodeID        openlcb.NodeID `json:"node_id"`
	UptimeSeconds float64        `json:"uptime_seconds"`
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:        "ok",
		Version:       version.Version,
		Commit:        version.Commit(),
		NodeID:        a.nodeID,
		UptimeSeconds: time.Since(a.started).Seconds(),
	})
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("writing response", "error", err)
	}
}

// jsonErrors wraps one of the mux's built-in error handlers (not found or
// method not allowed) so its status and Allow header stand but the body is
// a JSON error.
func jsonErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&jsonErrorWriter{ResponseWriter: w, path: r.URL.Path}, r)
	})
}

type jsonErrorWriter struct {
	http.ResponseWriter
	path   string
	status int
}

func (w *jsonErrorWriter) WriteHeader(status int) {
	w.status = status
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Del("X-Content-Type-Options")
	w.ResponseWriter.WriteHeader(status)
}

// Write replaces the plain text body with a JSON one, once.
func (w *jsonErrorWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	message := http.StatusText(w.status)
	if w.status == http.StatusNotFound {
		message = "no such endpoint: " + w.path
	}
	body, err := json.Marshal(errorResponse{Error: message})
	if err != nil {
		return 0, err
	}
	if _, err := w.ResponseWriter.Write(append(body, '\n')); err != nil {
		return 0, err
	}
	w.status = -1 // body written; swallow any further writes
	return len(b), nil
}
