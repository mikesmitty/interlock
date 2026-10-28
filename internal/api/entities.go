package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/profile"
	"github.com/mikesmitty/interlock/openlcb"
)

// Entities is the entity manager.
type Entities interface {
	List() []entity.Entity
	Get(id string) (entity.Entity, bool)
	Command(ctx context.Context, id, command string) (entity.Entity, error)
	Update(id string, change entity.Change) (entity.Entity, error)
	Create(man entity.Manual) (entity.Entity, error)
	Delete(id string) error
	IdentifyAll()
	Subscribe(entity.Subscriber) func()
}

// EventNames is the named event table.
type EventNames interface {
	Entries() []eventtable.Entry
	NodeEntries(id openlcb.NodeID) []eventtable.Entry
	Refresh(id openlcb.NodeID)
	Subscribe(eventtable.Subscriber) func()
}

// Profiles lists the loaded node profiles.
type Profiles interface {
	List() []*profile.Profile
}

// The entity endpoints:
//
//	GET    /api/v1/entities                       every entity
//	POST   /api/v1/entities                       build one by hand {kind, name, events}
//	POST   /api/v1/entities/refresh               ask nodes for their states again
//	GET    /api/v1/entities/kinds                 the kinds and their roles
//	GET    /api/v1/entities/{id}
//	PATCH  /api/v1/entities/{id}                  {name, hidden, confirmed, events}
//	DELETE /api/v1/entities/{id}                  manual entities only
//	POST   /api/v1/entities/{id}/command          {command}
//	GET    /api/v1/event-names?node=              the named event table
//	POST   /api/v1/nodes/{id}/event-names/refresh read a node's events again
//	GET    /api/v1/profiles

func (a *API) entitiesReady(w http.ResponseWriter) bool {
	if a.deps.Entities == nil {
		writeError(w, http.StatusServiceUnavailable, "entities are not available")
		return false
	}
	return true
}

func (a *API) listEntities(w http.ResponseWriter, _ *http.Request) {
	list := []entity.Entity{}
	if a.deps.Entities != nil {
		if l := a.deps.Entities.List(); l != nil {
			list = l
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": list})
}

func (a *API) entityKinds(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": entity.Definitions()})
}

func (a *API) getEntity(w http.ResponseWriter, r *http.Request) {
	if !a.entitiesReady(w) {
		return
	}
	ent, ok := a.deps.Entities.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no entity "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, ent)
}

func (a *API) createEntity(w http.ResponseWriter, r *http.Request) {
	if !a.entitiesReady(w) {
		return
	}
	var man entity.Manual
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&man); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with kind, name and events")
		return
	}
	ent, err := a.deps.Entities.Create(man)
	if err != nil {
		a.entityError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ent)
}

func (a *API) updateEntity(w http.ResponseWriter, r *http.Request) {
	if !a.entitiesReady(w) {
		return
	}
	var change entity.Change
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&change); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of name, hidden, confirmed and events")
		return
	}
	ent, err := a.deps.Entities.Update(r.PathValue("id"), change)
	if err != nil {
		a.entityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ent)
}

func (a *API) deleteEntity(w http.ResponseWriter, r *http.Request) {
	if !a.entitiesReady(w) {
		return
	}
	if err := a.deps.Entities.Delete(r.PathValue("id")); err != nil {
		a.entityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) commandEntity(w http.ResponseWriter, r *http.Request) {
	if !a.entitiesReady(w) {
		return
	}
	var body struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Command == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with a command")
		return
	}
	ent, err := a.deps.Entities.Command(r.Context(), r.PathValue("id"), body.Command)
	if err != nil {
		a.entityError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, ent)
}

func (a *API) refreshEntities(w http.ResponseWriter, _ *http.Request) {
	if !a.entitiesReady(w) {
		return
	}
	a.deps.Entities.IdentifyAll()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "identifying"})
}

func (a *API) entityError(w http.ResponseWriter, err error) {
	var invalid *entity.InvalidError
	var locked *entity.LockedError
	switch {
	case errors.Is(err, entity.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, entity.ErrUnavailable):
		writeError(w, http.StatusConflict, "the entity's node is offline")
	case errors.Is(err, entity.ErrUnknownCommand):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, entity.ErrNotManual), errors.Is(err, entity.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case errors.As(err, &locked):
		writeError(w, http.StatusConflict, locked.Reason)
	default:
		a.busError(w, err)
	}
}

func (a *API) eventNames(w http.ResponseWriter, r *http.Request) {
	entries := []eventtable.Entry{}
	if a.deps.EventNames != nil {
		var list []eventtable.Entry
		if s := r.URL.Query().Get("node"); s != "" {
			id, err := openlcb.ParseNodeID(s)
			if err != nil {
				writeError(w, http.StatusBadRequest, "node must be six dotted hex bytes")
				return
			}
			list = a.deps.EventNames.NodeEntries(id)
		} else {
			list = a.deps.EventNames.Entries()
		}
		if list != nil {
			entries = list
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": entries})
}

func (a *API) refreshEventNames(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeIDParam(w, r)
	if !ok {
		return
	}
	if a.deps.EventNames == nil {
		writeError(w, http.StatusServiceUnavailable, "the event table is not available")
		return
	}
	if _, found := a.deps.Registry.Get(id); !found {
		writeError(w, http.StatusNotFound, "unknown node "+id.String())
		return
	}
	a.deps.EventNames.Refresh(id)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reading"})
}

func (a *API) listProfiles(w http.ResponseWriter, _ *http.Request) {
	list := []*profile.Profile{}
	if a.deps.Profiles != nil {
		if l := a.deps.Profiles.List(); l != nil {
			list = l
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}
