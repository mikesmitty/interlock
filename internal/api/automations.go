package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/mikesmitty/interlock/internal/automation"
)

// Automations is the automation engine.
type Automations interface {
	List() []automation.Status
	Get(id string) (automation.Status, bool)
	Create(doc automation.Automation) (automation.Status, error)
	Update(id string, doc automation.Automation) (automation.Status, error)
	SetEnabled(id string, enabled bool) (automation.Status, error)
	Delete(id string) error
	Trigger(id string, checkConditions bool) (automation.Trace, error)
	Stop(id string) error
	Traces(ctx context.Context, id string, limit int) ([]automation.Trace, error)
	Trace(ctx context.Context, id, traceID string) (automation.Trace, error)
	Subscribe(func(automation.Status)) func()
	SubscribeTraces(func(automation.Trace)) func()
}

// The automation endpoints:
//
//	GET    /api/v1/automations                        every automation with its status
//	POST   /api/v1/automations                        create one (the document; id optional)
//	GET    /api/v1/automations/{id}
//	PUT    /api/v1/automations/{id}                   replace the document
//	PATCH  /api/v1/automations/{id}                   {enabled}
//	DELETE /api/v1/automations/{id}
//	GET    /api/v1/automations/{id}/yaml              the document as it is written to the file
//	POST   /api/v1/automations/{id}/run               {check_conditions} run it by hand -> the trace
//	POST   /api/v1/automations/{id}/stop              cancel its runs
//	GET    /api/v1/automations/{id}/traces?limit=     newest first
//	GET    /api/v1/automations/{id}/traces/{trace}
//
// Invalid documents get 400 with {"error": ..., "path": "actions[1].for"}.

const maxAutomationBody = 1 << 20

func (a *API) automationsReady(w http.ResponseWriter) bool {
	if a.deps.Automations == nil {
		writeError(w, http.StatusServiceUnavailable, "automations are not available")
		return false
	}
	return true
}

func (a *API) listAutomations(w http.ResponseWriter, _ *http.Request) {
	list := []automation.Status{}
	if a.deps.Automations != nil {
		if l := a.deps.Automations.List(); l != nil {
			list = l
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"automations": list})
}

func (a *API) getAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	s, ok := a.deps.Automations.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no automation "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func decodeAutomation(w http.ResponseWriter, r *http.Request) (automation.Automation, bool) {
	var doc automation.Automation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAutomationBody)).Decode(&doc); err != nil {
		writeError(w, http.StatusBadRequest, "body must be an automation document: "+err.Error())
		return doc, false
	}
	return doc, true
}

func (a *API) createAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	doc, ok := decodeAutomation(w, r)
	if !ok {
		return
	}
	s, err := a.deps.Automations.Create(doc)
	if err != nil {
		a.automationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

func (a *API) updateAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	doc, ok := decodeAutomation(w, r)
	if !ok {
		return
	}
	s, err := a.deps.Automations.Update(r.PathValue("id"), doc)
	if err != nil {
		a.automationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) patchAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with enabled true or false")
		return
	}
	s, err := a.deps.Automations.SetEnabled(r.PathValue("id"), *body.Enabled)
	if err != nil {
		a.automationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	if err := a.deps.Automations.Delete(r.PathValue("id")); err != nil {
		a.automationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) automationYAML(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	s, ok := a.deps.Automations.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no automation "+r.PathValue("id"))
		return
	}
	body, err := automation.MarshalYAML(s.Automation)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *API) runAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	var body struct {
		CheckConditions bool `json:"check_conditions"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "body must be JSON, optionally with check_conditions")
			return
		}
	}
	tr, err := a.deps.Automations.Trigger(r.PathValue("id"), body.CheckConditions)
	if err != nil {
		a.automationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, tr)
}

func (a *API) stopAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	err := a.deps.Automations.Stop(r.PathValue("id"))
	switch {
	case errors.Is(err, automation.ErrNotRunning):
		writeJSON(w, http.StatusOK, map[string]string{"status": "idle"})
	case err != nil:
		a.automationError(w, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

func (a *API) listTraces(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	limit := 0
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = n
	}
	traces, err := a.deps.Automations.Traces(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		a.automationError(w, err)
		return
	}
	if traces == nil {
		traces = []automation.Trace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces})
}

func (a *API) getTrace(w http.ResponseWriter, r *http.Request) {
	if !a.automationsReady(w) {
		return
	}
	tr, err := a.deps.Automations.Trace(r.Context(), r.PathValue("id"), r.PathValue("trace"))
	if err != nil {
		a.automationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (a *API) automationError(w http.ResponseWriter, err error) {
	var invalid *automation.InvalidError
	switch {
	case errors.Is(err, automation.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, automation.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": invalid.Reason, "path": invalid.Path})
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
