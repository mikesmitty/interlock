package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mikesmitty/interlock/internal/nodeconfig"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// Config reads and writes node configuration through the CDI layout.
type Config interface {
	Read(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, size int) (*nodeconfig.ReadResult, error)
	Write(ctx context.Context, id openlcb.NodeID, values nodeconfig.Values) (*nodeconfig.WriteResult, error)
	Reboot(ctx context.Context, id openlcb.NodeID) error
	FactoryReset(ctx context.Context, id openlcb.NodeID) error
}

// The configuration endpoints:
//
//	GET    /api/v1/nodes/{id}/cdi                  expanded layout (or ?format=xml)
//	POST   /api/v1/nodes/{id}/cdi/refresh          read the CDI from the node again
//	POST   /api/v1/nodes/{id}/config/read          {space, address, size} -> values
//	POST   /api/v1/nodes/{id}/config/write         {values} -> what was written
//	POST   /api/v1/nodes/{id}/config/reboot
//	POST   /api/v1/nodes/{id}/config/factory-reset {confirm: node id}
//	DELETE /api/v1/nodes/{id}                      forget an offline node

func (a *API) getCDI(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	info, found := a.deps.Registry.Get(id)
	if !found {
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
		return
	}
	doc, xml, ok := a.deps.Registry.CDI(id)
	if !ok {
		msg := "the node's configuration description has not been read"
		if info.CDI != nil && info.CDI.State == registry.CDIError {
			msg = "reading the node's configuration description failed: " + info.CDI.Error
		}
		writeError(w, http.StatusNotFound, msg)
		return
	}
	if r.URL.Query().Get("format") == "xml" {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(xml)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": info.CDI, "layout": doc.Layout()})
}

func (a *API) refreshCDI(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	err := a.deps.Registry.RefreshCDI(r.Context(), id)
	switch {
	case errors.Is(err, node.ErrNotFound):
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
	case errors.Is(err, registry.ErrNoCDI):
		writeError(w, http.StatusConflict, "the node does not offer a configuration description")
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "reading"})
	}
}

func (a *API) forgetNode(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	err := a.deps.Registry.Forget(r.Context(), id)
	switch {
	case errors.Is(err, node.ErrNotFound):
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
	case errors.Is(err, registry.ErrNodeOnline):
		writeError(w, http.StatusConflict, "the node is online; only offline nodes can be forgotten")
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) configNode(w http.ResponseWriter, r *http.Request) (openlcb.NodeID, bool) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return 0, false
	}
	if a.deps.Config == nil {
		writeError(w, http.StatusServiceUnavailable, "node configuration is not available")
		return 0, false
	}
	info, found := a.deps.Registry.Get(id)
	if !found {
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
		return 0, false
	}
	if !info.Online {
		writeError(w, http.StatusConflict, "the node is offline")
		return 0, false
	}
	return id, true
}

func (a *API) configRead(w http.ResponseWriter, r *http.Request) {
	id, ok := a.configNode(w, r)
	if !ok {
		return
	}
	var body struct {
		Space   uint8  `json:"space"`
		Address uint32 `json:"address"`
		Size    int    `json:"size"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with space, address and size")
		return
	}
	if body.Size <= 0 || body.Size > nodeconfig.MaxRead {
		writeError(w, http.StatusBadRequest, "size must be between 1 and 65536")
		return
	}
	result, err := a.deps.Config.Read(r.Context(), id, body.Space, body.Address, body.Size)
	if err != nil {
		a.busError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) configWrite(w http.ResponseWriter, r *http.Request) {
	id, ok := a.configNode(w, r)
	if !ok {
		return
	}
	var body struct {
		Values nodeconfig.Values `json:"values"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || len(body.Values) == 0 {
		writeError(w, http.StatusBadRequest, "body must be JSON with a values object")
		return
	}
	result, err := a.deps.Config.Write(r.Context(), id, body.Values)
	var invalid *nodeconfig.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "some values are not valid", "invalid": invalid.Invalid})
	case errors.Is(err, nodeconfig.ErrNoLayout):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		status := busStatus(err)
		writeJSON(w, status, map[string]any{"error": err.Error(), "written": result.Written, "failed": result.Failed})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

func (a *API) configReboot(w http.ResponseWriter, r *http.Request) {
	id, ok := a.configNode(w, r)
	if !ok {
		return
	}
	if err := a.deps.Config.Reboot(r.Context(), id); err != nil {
		a.busError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "rebooting"})
}

func (a *API) configFactoryReset(w http.ResponseWriter, r *http.Request) {
	id, ok := a.configNode(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with confirm set to the node ID")
		return
	}
	confirm, err := openlcb.ParseNodeID(strings.TrimSpace(body.Confirm))
	if err != nil || confirm != id {
		writeError(w, http.StatusBadRequest, "confirm must repeat the node ID "+id.String())
		return
	}
	if err := a.deps.Config.FactoryReset(r.Context(), id); err != nil {
		a.busError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "resetting"})
}

// busStatus maps errors from talking to a node onto HTTP statuses: the
// node refused (502), did not answer (504), or is not on the bus (404).
func busStatus(err error) int {
	var oerr *openlcb.Error
	switch {
	case errors.Is(err, node.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, node.ErrTimeout), errors.Is(err, memconfig.ErrNoReply), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, nodeconfig.ErrNoLayout):
		return http.StatusConflict
	case errors.As(err, &oerr):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func (a *API) busError(w http.ResponseWriter, err error) {
	writeError(w, busStatus(err), err.Error())
}
