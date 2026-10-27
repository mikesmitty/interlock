// Package api serves Interlock's HTTP API under /api/v1. The web interface
// is its only built-in client, so the API is the full surface of the server.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/internal/version"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// LocalNode is what the API shows about Interlock's own node.
type LocalNode interface {
	ID() openlcb.NodeID
	Alias() openlcb.Alias
	State() node.State
}

// Registry is the node table.
type Registry interface {
	List() []registry.Info
	Get(id openlcb.NodeID) (registry.Info, bool)
	Refresh(ctx context.Context) error
	RefreshNode(ctx context.Context, id openlcb.NodeID) error
	Subscribe(registry.Subscriber) func()
	Forget(ctx context.Context, id openlcb.NodeID) error
	CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool)
	RefreshCDI(ctx context.Context, id openlcb.NodeID) error
}

// Monitor is the traffic log.
type Monitor interface {
	Since(seq uint64, limit int) []monitor.Entry
	Recent(limit int) []monitor.Entry
	LastSeq() uint64
	Subscribe(monitor.Subscriber) func()
}

// Bus is the hub: link status and raw frame sending.
type Bus interface {
	Links() []transport.Status
	Send(can.Frame)
}

// Deps are the parts of the server the API exposes.
type Deps struct {
	Node     LocalNode
	Registry Registry
	Monitor  Monitor
	Bus      Bus
	// Config reads and writes node configuration; nil disables it.
	Config Config
	// Entities is the entity manager; nil disables the entity endpoints.
	Entities Entities
	// EventNames is the named event table; may be nil.
	EventNames EventNames
	// Profiles lists node profiles; may be nil.
	Profiles Profiles
	// Automations is the automation engine; nil disables the endpoints.
	Automations Automations
	// Trains is the train manager; nil disables the train endpoints.
	Trains Trains
	// Programmer reads and writes CVs; nil disables the programming endpoints.
	Programmer Programmer
	// Decoders reads the DCC decoder definitions; nil disables their
	// endpoints.
	Decoders Decoders
	// Firmware upgrades nodes; nil disables the firmware endpoints.
	Firmware Firmware
	// Clock is the fast clock; nil disables the clock endpoints.
	Clock Clock
	// Backups writes and restores backups; nil disables those endpoints.
	Backups Backups
	// TrackPlan is the track diagram; nil disables its endpoints.
	TrackPlan TrackPlan
	// Replace moves what names one node over to another; nil disables it.
	Replace Replacer
	// Restart restarts the server; nil means it cannot.
	Restart func()
	// SerialPorts lists serial devices for the settings page; may be nil.
	SerialPorts func() ([]string, error)
	Logger      *slog.Logger
}

// API holds what the handlers need.
type API struct {
	deps     Deps
	started  time.Time
	mux      *http.ServeMux
	sessions decoderSessions
}

// New returns an API over deps.
func New(deps Deps) *API {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	a := &API{deps: deps, started: time.Now(), mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /api/v1/health", a.health)
	a.mux.HandleFunc("GET /api/v1/nodes", a.listNodes)
	a.mux.HandleFunc("POST /api/v1/nodes/refresh", a.refreshNodes)
	a.mux.HandleFunc("GET /api/v1/nodes/{id}", a.getNode)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/refresh", a.refreshNode)
	a.mux.HandleFunc("DELETE /api/v1/nodes/{id}", a.forgetNode)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/replace", a.replaceNode)
	a.mux.HandleFunc("GET /api/v1/nodes/{id}/cdi", a.getCDI)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/cdi/refresh", a.refreshCDI)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/config/read", a.configRead)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/config/write", a.configWrite)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/config/reboot", a.configReboot)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/config/factory-reset", a.configFactoryReset)
	a.mux.HandleFunc("POST /api/v1/nodes/{id}/event-names/refresh", a.refreshEventNames)
	a.mux.HandleFunc("GET /api/v1/entities", a.listEntities)
	a.mux.HandleFunc("POST /api/v1/entities", a.createEntity)
	a.mux.HandleFunc("POST /api/v1/entities/refresh", a.refreshEntities)
	a.mux.HandleFunc("GET /api/v1/entities/kinds", a.entityKinds)
	a.mux.HandleFunc("GET /api/v1/entities/{id}", a.getEntity)
	a.mux.HandleFunc("PATCH /api/v1/entities/{id}", a.updateEntity)
	a.mux.HandleFunc("DELETE /api/v1/entities/{id}", a.deleteEntity)
	a.mux.HandleFunc("POST /api/v1/entities/{id}/command", a.commandEntity)
	a.mux.HandleFunc("GET /api/v1/event-names", a.eventNames)
	a.mux.HandleFunc("GET /api/v1/profiles", a.listProfiles)
	a.mux.HandleFunc("GET /api/v1/automations", a.listAutomations)
	a.mux.HandleFunc("POST /api/v1/automations", a.createAutomation)
	a.mux.HandleFunc("GET /api/v1/automations/{id}", a.getAutomation)
	a.mux.HandleFunc("PUT /api/v1/automations/{id}", a.updateAutomation)
	a.mux.HandleFunc("PATCH /api/v1/automations/{id}", a.patchAutomation)
	a.mux.HandleFunc("DELETE /api/v1/automations/{id}", a.deleteAutomation)
	a.mux.HandleFunc("GET /api/v1/automations/{id}/yaml", a.automationYAML)
	a.mux.HandleFunc("POST /api/v1/automations/{id}/run", a.runAutomation)
	a.mux.HandleFunc("POST /api/v1/automations/{id}/stop", a.stopAutomation)
	a.mux.HandleFunc("GET /api/v1/automations/{id}/traces", a.listTraces)
	a.mux.HandleFunc("GET /api/v1/automations/{id}/traces/{trace}", a.getTrace)
	a.mux.HandleFunc("GET /api/v1/trains", a.listTrains)
	a.mux.HandleFunc("POST /api/v1/trains", a.createTrain)
	a.mux.HandleFunc("POST /api/v1/trains/search", a.searchTrains)
	a.mux.HandleFunc("GET /api/v1/trains/layout", a.getLayout)
	a.mux.HandleFunc("POST /api/v1/trains/layout", a.setLayout)
	a.mux.HandleFunc("GET /api/v1/trains/{id}", a.getTrain)
	a.mux.HandleFunc("PATCH /api/v1/trains/{id}", a.updateTrain)
	a.mux.HandleFunc("DELETE /api/v1/trains/{id}", a.deleteTrain)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/acquire", a.trainAction(http.StatusOK, func(ctx context.Context, id string) (train.Train, error) { return a.deps.Trains.Acquire(ctx, id) }))
	a.mux.HandleFunc("POST /api/v1/trains/{id}/release", a.trainAction(http.StatusOK, func(ctx context.Context, id string) (train.Train, error) { return a.deps.Trains.Release(ctx, id) }))
	a.mux.HandleFunc("POST /api/v1/trains/{id}/speed", a.trainSpeed)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/estop", a.trainAction(http.StatusAccepted, func(ctx context.Context, id string) (train.Train, error) { return a.deps.Trains.EmergencyStop(ctx, id) }))
	a.mux.HandleFunc("POST /api/v1/trains/{id}/function", a.trainFunction)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/refresh", a.trainAction(http.StatusOK, func(ctx context.Context, id string) (train.Train, error) { return a.deps.Trains.Refresh(ctx, id) }))
	a.mux.HandleFunc("POST /api/v1/trains/{id}/cv", a.writePOM)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/decoders", a.addDecoder)
	a.mux.HandleFunc("PATCH /api/v1/trains/{id}/decoders/{decoder}", a.updateDecoder)
	a.mux.HandleFunc("DELETE /api/v1/trains/{id}/decoders/{decoder}", a.removeDecoder)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/install", a.installDecoder)
	a.mux.HandleFunc("GET /api/v1/trains/{id}/decoders/{decoder}/cvs", a.decoderCVs)
	a.mux.HandleFunc("PATCH /api/v1/trains/{id}/decoders/{decoder}/cvs", a.setDecoderCVs)
	a.mux.HandleFunc("PUT /api/v1/trains/{id}/decoders/{decoder}/cvs", a.setDecoderCVs)
	a.mux.HandleFunc("PUT /api/v1/trains/{id}/decoders/{decoder}/changes", a.setDecoderChanges)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/decoders/{decoder}/profiles", a.keepProfile)
	a.mux.HandleFunc("DELETE /api/v1/trains/{id}/decoders/{decoder}/profiles/{name}", a.deleteProfile)
	a.mux.HandleFunc("POST /api/v1/trains/{id}/decoders/{decoder}/session", a.startDecoderSession)
	a.mux.HandleFunc("DELETE /api/v1/trains/{id}/decoders/{decoder}/session", a.endDecoderSession)
	a.mux.HandleFunc("POST /api/v1/trains/import", a.importTrains)
	a.mux.HandleFunc("GET /api/v1/consists", a.listConsists)
	a.mux.HandleFunc("POST /api/v1/consists", a.createConsist)
	a.mux.HandleFunc("GET /api/v1/consists/{id}", a.getConsist)
	a.mux.HandleFunc("PATCH /api/v1/consists/{id}", a.updateConsist)
	a.mux.HandleFunc("DELETE /api/v1/consists/{id}", a.deleteConsist)
	a.mux.HandleFunc("POST /api/v1/consists/{id}/make-up", a.makeUpConsist)
	a.mux.HandleFunc("POST /api/v1/consists/{id}/break-up", a.breakUpConsist)
	a.mux.HandleFunc("GET /api/v1/programming", a.programming)
	a.mux.HandleFunc("POST /api/v1/programming/find", a.findProgrammingTrack)
	a.mux.HandleFunc("POST /api/v1/programming/read", a.readCV)
	a.mux.HandleFunc("POST /api/v1/programming/write", a.writeCV)
	if deps.Decoders != nil {
		a.mux.HandleFunc("GET /api/v1/decoders", a.decoderStatus)
		a.mux.HandleFunc("GET /api/v1/decoders/index", a.decoderIndex)
		a.mux.HandleFunc("GET /api/v1/decoders/definition", a.decoderDefinition)
		a.mux.HandleFunc("POST /api/v1/programming/identify", a.identifyDecoder)
		a.mux.HandleFunc("POST /api/v1/programming/decoder-cv", a.decoderCV)
		a.mux.HandleFunc("POST /api/v1/trains/{id}/decoders/{decoder}/check", a.checkDecoder)
	}
	if deps.Firmware != nil {
		a.mux.HandleFunc("GET /api/v1/firmware", a.listFirmware)
		a.mux.HandleFunc("GET /api/v1/nodes/{id}/firmware", a.getFirmware)
		a.mux.HandleFunc("POST /api/v1/nodes/{id}/firmware", a.startFirmware)
		a.mux.HandleFunc("DELETE /api/v1/nodes/{id}/firmware", a.cancelFirmware)
	}
	if deps.Clock != nil {
		a.mux.HandleFunc("GET /api/v1/clock", a.getClock)
		a.mux.HandleFunc("POST /api/v1/clock", a.setClock)
	}
	if deps.Backups != nil {
		a.mux.HandleFunc("GET /api/v1/backup", a.downloadBackup)
		a.mux.HandleFunc("POST /api/v1/restore", a.restore)
	}
	a.mux.HandleFunc("GET /api/v1/trackplan", a.getTrackPlan)
	a.mux.HandleFunc("PUT /api/v1/trackplan", a.putTrackPlan)
	a.mux.HandleFunc("POST /api/v1/trackplan/backgrounds", a.uploadBackground)
	a.mux.HandleFunc("GET /api/v1/trackplan/backgrounds/{name}", a.getBackground)
	a.mux.HandleFunc("POST /api/v1/restart", a.restart)
	a.mux.HandleFunc("GET /api/v1/links", a.links)
	a.mux.HandleFunc("GET /api/v1/monitor", a.monitorEntries)
	a.mux.HandleFunc("POST /api/v1/monitor/send", a.monitorSend)
	a.mux.HandleFunc("GET /api/v1/serial-ports", a.serialPorts)
	a.mux.HandleFunc("GET /api/v1/events", a.events)
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
	// Only the mux's own ServeHTTP fills in path values, so route through
	// it either way; Handler just tells us whether it will match.
	if _, pattern := a.mux.Handler(r); pattern == "" {
		jsonErrors(a.mux).ServeHTTP(w, r)
		return
	}
	a.mux.ServeHTTP(w, r)
}

type healthResponse struct {
	Status         string         `json:"status"`
	Version        string         `json:"version"`
	Commit         string         `json:"commit,omitempty"`
	OS             string         `json:"os"`
	NodeID         openlcb.NodeID `json:"node_id"`
	Alias          uint16         `json:"alias"`
	NodeState      string         `json:"node_state"`
	UptimeSeconds  float64        `json:"uptime_seconds"`
	Nodes          int            `json:"nodes"`
	LinksTotal     int            `json:"links_total"`
	LinksConnected int            `json:"links_connected"`
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{
		Status:        "ok",
		Version:       version.Version,
		Commit:        version.Commit(),
		OS:            runtime.GOOS,
		UptimeSeconds: time.Since(a.started).Seconds(),
	}
	if a.deps.Node != nil {
		resp.NodeID = a.deps.Node.ID()
		resp.Alias = uint16(a.deps.Node.Alias())
		resp.NodeState = a.deps.Node.State().String()
	}
	if a.deps.Registry != nil {
		resp.Nodes = len(a.deps.Registry.List())
	}
	if a.deps.Bus != nil {
		for _, link := range a.deps.Bus.Links() {
			resp.LinksTotal++
			if link.Connected {
				resp.LinksConnected++
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) listNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := a.deps.Registry.List()
	if nodes == nil {
		nodes = []registry.Info{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (a *API) getNode(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	info, found := a.deps.Registry.Get(id)
	if !found {
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *API) refreshNodes(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Registry.Refresh(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "refreshing"})
}

func (a *API) refreshNode(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	err := a.deps.Registry.RefreshNode(r.Context(), id)
	switch {
	case errors.Is(err, node.ErrNotFound):
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "refreshing"})
	}
}

func nodeIDParam(w http.ResponseWriter, r *http.Request) (openlcb.NodeID, bool) {
	id, err := openlcb.ParseNodeID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "node ID must be six dotted hex bytes, for example 05.01.01.01.22.00")
		return 0, false
	}
	return id, true
}

func (a *API) links(w http.ResponseWriter, _ *http.Request) {
	links := a.deps.Bus.Links()
	if links == nil {
		links = []transport.Status{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (a *API) monitorEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 500
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = n
	}
	var entries []monitor.Entry
	if s := q.Get("since"); s != "" {
		seq, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be a sequence number")
			return
		}
		entries = a.deps.Monitor.Since(seq, limit)
	} else {
		entries = a.deps.Monitor.Recent(limit)
	}
	if entries == nil {
		entries = []monitor.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "last_seq": a.deps.Monitor.LastSeq()})
}

func (a *API) monitorSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Frame string `json:"frame"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a frame field")
		return
	}
	f, err := can.ParseGridConnect(body.Frame)
	if err != nil {
		writeError(w, http.StatusBadRequest, "frame must be GridConnect text such as :X19490ABCN;")
		return
	}
	a.deps.Bus.Send(f)
	writeJSON(w, http.StatusAccepted, map[string]string{"frame": f.String()})
}

func (a *API) serialPorts(w http.ResponseWriter, _ *http.Request) {
	ports := []string{}
	if a.deps.SerialPorts != nil {
		list, err := a.deps.SerialPorts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if list != nil {
			ports = list
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": ports})
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
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
