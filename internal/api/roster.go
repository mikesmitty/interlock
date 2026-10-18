package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/mikesmitty/interlock/internal/decoder"
	"github.com/mikesmitty/interlock/internal/jmri"
	"github.com/mikesmitty/interlock/internal/train"
)

// The roster's decoder endpoints:
//
//	POST   /api/v1/trains/{id}/decoders                    {name, file, family, model, identity, install} -> {train, decoder}
//	PATCH  /api/v1/trains/{id}/decoders/{decoder}          any of {name, file, family, model, identity} -> train
//	DELETE /api/v1/trains/{id}/decoders/{decoder}          forgets its CV values too -> train
//	POST   /api/v1/trains/{id}/install                     {decoder} fits it ("" for none) -> train
//	GET    /api/v1/trains/{id}/decoders/{decoder}/cvs      {cvs, imported, updated, changes, session}
//	PATCH  /api/v1/trains/{id}/decoders/{decoder}/cvs      {cvs} saves these values, keeping the others
//	PUT    /api/v1/trains/{id}/decoders/{decoder}/cvs      {cvs} replaces them all
//	PUT    /api/v1/trains/{id}/decoders/{decoder}/changes  {changes} keeps the changes not written yet
//	POST   /api/v1/trains/{id}/decoders/{decoder}/setups   {name, replace} keeps a copy of its values -> setup
//	DELETE /api/v1/trains/{id}/decoders/{decoder}/setups/{name}  forgets one
//	POST   /api/v1/trains/{id}/decoders/{decoder}/session  begins the session though a check did not match -> session
//	DELETE /api/v1/trains/{id}/decoders/{decoder}/session  ends the session on it
//	POST   /api/v1/trains/import                           multipart: file (JMRI roster files or zips), plan
//
// Without a plan the import answers with what it found and a suggested
// plan, changing nothing; with one it carries the plan out.
//
// Changes are by CV: {"29": {"value": 34, "bits": 32}} sets bit 5 of CV
// 29 and leaves the rest to the decoder. A change is dropped once a value
// read from or written to the decoder holds it.

// maxImportSize bounds an import upload; a JMRI roster file for a sound
// decoder is under a megabyte.
const maxImportSize = 64 << 20

type decoderBody struct {
	Name     *string         `json:"name"`
	File     *string         `json:"file"`
	Family   *string         `json:"family"`
	Model    *string         `json:"model"`
	Identity *train.Identity `json:"identity"`
	Install  bool            `json:"install"`
}

func (b decoderBody) apply(d *train.Decoder) {
	for _, f := range []struct {
		dst *string
		src *string
	}{{&d.Name, b.Name}, {&d.File, b.File}, {&d.Family, b.Family}, {&d.Model, b.Model}} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if b.Identity != nil {
		d.Identity = b.Identity
	}
}

// checkDefinition makes sure a decoder names a definition the index has.
func (a *API) checkDefinition(w http.ResponseWriter, d train.Decoder) bool {
	if d.File == "" || a.deps.Decoders == nil {
		return true
	}
	idx, err := a.deps.Decoders.Index()
	if err != nil {
		// Unreachable definitions do not stop the roster.
		return true
	}
	for _, f := range idx.Families {
		if f.File == d.File && (f.Name == d.Model || slices.ContainsFunc(f.Models, func(m decoder.Model) bool { return m.Name == d.Model })) {
			return true
		}
	}
	writeError(w, http.StatusBadRequest, "No decoder definition "+d.File+" has a model "+d.Model)
	return false
}

func (a *API) addDecoder(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body decoderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with the decoder's name and definition")
		return
	}
	var d train.Decoder
	body.apply(&d)
	if !a.checkDefinition(w, d) {
		return
	}
	tr, added, err := a.deps.Trains.AddDecoder(r.Context(), r.PathValue("id"), d, body.Install)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"train": tr, "decoder": added})
}

func (a *API) updateDecoder(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body decoderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with the decoder fields to change")
		return
	}
	if e, ok := a.deps.Trains.Entry(r.PathValue("id")); ok {
		if d, ok := e.Decoder(r.PathValue("decoder")); ok {
			body.apply(&d)
			if !a.checkDefinition(w, d) {
				return
			}
		}
	}
	tr, err := a.deps.Trains.UpdateDecoder(r.Context(), r.PathValue("id"), r.PathValue("decoder"), body.apply)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (a *API) removeDecoder(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	tr, err := a.deps.Trains.RemoveDecoder(r.Context(), r.PathValue("id"), r.PathValue("decoder"))
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (a *API) installDecoder(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Decoder *string `json:"decoder"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Decoder == nil {
		writeError(w, http.StatusBadRequest, `body must be JSON with decoder, "" for none`)
		return
	}
	tr, err := a.deps.Trains.Install(r.Context(), r.PathValue("id"), *body.Decoder)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (a *API) decoderCVs(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	values, err := a.deps.Trains.CVs(r.PathValue("id"), r.PathValue("decoder"))
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSONCompressed(w, r, values)
}

// setDecoderCVs saves values: PATCH keeps the others, PUT replaces them.
func (a *API) setDecoderCVs(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		CVs map[string]int `json:"cvs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil || body.CVs == nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with cvs")
		return
	}
	id, dec := r.PathValue("id"), r.PathValue("decoder")
	if err := a.deps.Trains.SetCVs(id, dec, body.CVs, r.Method == http.MethodPut); err != nil {
		a.trainError(w, err)
		return
	}
	values, err := a.deps.Trains.CVs(id, dec)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSONCompressed(w, r, values)
}

// setDecoderChanges keeps the settings page's changes that are not
// written yet, replacing those kept before.
func (a *API) setDecoderChanges(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Changes map[string]train.CVChange `json:"changes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with changes")
		return
	}
	if err := a.deps.Trains.SetChanges(r.PathValue("id"), r.PathValue("decoder"), body.Changes); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// endDecoderSession ends the session on a decoder: what was read and
// written in it counts as saved from then on.
func (a *API) keepSetup(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Replace bool   `json:"replace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a name")
		return
	}
	st, err := a.deps.Trains.KeepSetup(r.PathValue("id"), r.PathValue("decoder"), body.Name, body.Replace)
	if errors.Is(err, train.ErrSetupExists) {
		writeError(w, http.StatusConflict, "A setup named "+body.Name+" is kept already")
		return
	}
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

func (a *API) deleteSetup(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	if err := a.deps.Trains.DeleteSetup(r.PathValue("id"), r.PathValue("decoder"), r.PathValue("name")); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) startDecoderSession(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	id, dec := r.PathValue("id"), r.PathValue("decoder")
	if err := a.deps.Trains.StartSession(id, dec, train.CheckAccepted); err != nil {
		a.trainError(w, err)
		return
	}
	v, err := a.deps.Trains.CVs(id, dec)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v.Session)
}

func (a *API) endDecoderSession(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	if err := a.deps.Trains.EndSession(r.PathValue("id"), r.PathValue("decoder")); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// importItem is one locomotive found in an import, for the preview.
type importItem struct {
	train.Import
	// CVCount is how many CV values come with it.
	CVCount int `json:"cv_count"`
	// Definition is whether the decoder definitions have its decoder.
	Definition bool `json:"definition"`
}

func (a *API) importTrains(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportSize)
	if err := r.ParseMultipartForm(maxImportSize); err != nil {
		writeError(w, http.StatusBadRequest, "send the JMRI roster files, or a zip of them, as multipart form data: "+err.Error())
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	var files []jmri.File
	for _, fh := range r.MultipartForm.File["file"] {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		files = append(files, jmri.File{Name: fh.Filename, Data: data})
	}
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no files were sent")
		return
	}
	roster, err := jmri.Read(files)
	if err != nil {
		writeError(w, http.StatusBadRequest, "The roster could not be read: "+err.Error())
		return
	}
	var idx *decoder.Index
	if a.deps.Decoders != nil {
		idx, _ = a.deps.Decoders.Index()
	}
	items := make([]train.Import, 0, len(roster.Locomotives))
	preview := make([]importItem, 0, len(roster.Locomotives))
	for _, l := range roster.Locomotives {
		it := l.Import()
		found := false
		if it.Decoder != nil && idx != nil {
			if f, ok := findDefinition(idx, it.Decoder.Family, it.Decoder.Model); ok {
				it.Decoder.File, it.Decoder.Family, found = f.File, f.Name, true
			}
		}
		items = append(items, it)
		preview = append(preview, importItem{Import: it, CVCount: len(it.CVs), Definition: found})
	}
	problems := roster.Problems
	if problems == nil {
		problems = []string{}
	}

	planText := r.FormValue("plan")
	if planText == "" {
		writeJSON(w, http.StatusOK, map[string]any{"locomotives": preview, "plan": a.deps.Trains.PlanImport(items), "problems": problems})
		return
	}
	var plan []train.ImportStep
	if err := json.Unmarshal([]byte(planText), &plan); err != nil {
		writeError(w, http.StatusBadRequest, "plan must be a JSON list of steps: "+err.Error())
		return
	}
	res, err := a.deps.Trains.ApplyImport(r.Context(), items, plan)
	if err != nil {
		var invalid *train.InvalidError
		if errors.As(err, &invalid) && len(res.Trains) == 0 {
			writeError(w, http.StatusBadRequest, invalid.Reason)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": res, "problems": problems, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "problems": problems})
}

// findDefinition finds the family a roster names a decoder by, as JMRI
// does: the family and model, else the model in any family. A family's
// own name stands for the family as a whole.
func findDefinition(idx *decoder.Index, family, model string) (decoder.Family, bool) {
	has := func(f decoder.Family) bool {
		return f.Name == model || slices.ContainsFunc(f.Models, func(m decoder.Model) bool { return m.Name == model })
	}
	for _, f := range idx.Families {
		if f.Name == family && has(f) {
			return f, true
		}
	}
	for _, f := range idx.Families {
		if slices.ContainsFunc(f.Models, func(m decoder.Model) bool { return m.Name == model }) {
			return f, true
		}
	}
	return decoder.Family{}, false
}
