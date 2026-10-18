package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/decoder"
	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/internal/train"
)

// Decoders reads the DCC decoder definitions.
type Decoders interface {
	Status() decoder.Status
	Index() (*decoder.Index, error)
	Definition(file, model string) (*decoder.Definition, error)
}

// The decoder definition endpoints:
//
//	GET  /api/v1/decoders                         where the definitions come from
//	GET  /api/v1/decoders/index                   every manufacturer and decoder family
//	GET  /api/v1/decoders/definition?file=&model= one model's settings and panes
//	POST /api/v1/programming/identify             {identity, manufacturer, candidates, address, roster}
//	POST /api/v1/programming/decoder-cv           {file, model, cv, write, value, train, locomotive, decoder} -> {value}
//
// decoder-cv reads or writes one CV by the name a definition uses for it
// ("29", "16.0.259", "T2CV.1.05"), on the programming track or, with a
// train, on the main. The browser walks a pane's CVs one request at a
// time, so it can show progress and stop. With a roster locomotive and
// one of its decoders, each value read or written is saved for it.
//
// identify also reads the address the decoder answers to and lists the
// roster decoders it could be.

func (a *API) decoderStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.deps.Decoders.Status())
}

func (a *API) decoderIndex(w http.ResponseWriter, r *http.Request) {
	idx, err := a.deps.Decoders.Index()
	if err != nil {
		writeError(w, http.StatusBadGateway, "The decoder definitions could not be read: "+err.Error())
		return
	}
	writeJSONCompressed(w, r, idx)
}

func (a *API) decoderDefinition(w http.ResponseWriter, r *http.Request) {
	file, model := r.URL.Query().Get("file"), r.URL.Query().Get("model")
	if file == "" || model == "" {
		writeError(w, http.StatusBadRequest, "file and model are required")
		return
	}
	d, err := a.deps.Decoders.Definition(file, model)
	switch {
	case errors.Is(err, decoder.ErrUnknownDecoder):
		writeError(w, http.StatusNotFound, "No decoder definition "+file+" has a model "+model)
	case err != nil:
		writeError(w, http.StatusBadGateway, "The decoder definition could not be read: "+err.Error())
	default:
		writeJSONCompressed(w, r, d)
	}
}

type identifyResponse struct {
	Identity     decoder.Identity    `json:"identity"`
	Manufacturer string              `json:"manufacturer,omitempty"`
	Candidates   []decoder.Candidate `json:"candidates"`
	// Note explains why there are no candidates when the definitions
	// could not be read.
	Note string `json:"note,omitempty"`
	// Address is the address the decoder answers to, when it could be
	// read.
	Address *decoder.Address `json:"address,omitempty"`
	// Roster lists the roster decoders it could be, best first.
	Roster []train.DecoderMatch `json:"roster"`
}

func (a *API) identifyDecoder(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	id, err := decoder.Identify(r.Context(), serviceBus{a.deps.Programmer})
	a.sessions.forget("")
	if err == nil && a.deps.Trains != nil {
		// The locomotive on the track may have been swapped, so what was
		// read or written before no longer counts as current.
		a.deps.Trains.EndSessions()
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "The decoder could not be identified: "+programmer.Explain(err))
		return
	}
	resp := identifyResponse{Identity: id, Candidates: []decoder.Candidate{}, Roster: []train.DecoderMatch{}}
	if addr, err := decoder.ReadAddress(r.Context(), serviceBus{a.deps.Programmer}); err == nil {
		resp.Address = &addr
	}
	if a.deps.Trains != nil {
		tid := train.Identity{Manufacturer: id.Manufacturer, Version: id.Version}
		if id.HasProductID {
			p := id.ProductID
			tid.ProductID = &p
		}
		var address uint16
		var long bool
		if resp.Address != nil {
			address, long = uint16(resp.Address.Address), resp.Address.Long
		}
		if m := a.deps.Trains.FindDecoder(tid, address, long); m != nil {
			resp.Roster = m
		}
	}
	idx, err := a.deps.Decoders.Index()
	if err != nil {
		resp.Note = "The decoder definitions could not be read: " + err.Error()
	} else {
		if m, ok := idx.Manufacturer(id.Manufacturer); ok {
			resp.Manufacturer = m.Name
		}
		if c := idx.Match(id); c != nil {
			resp.Candidates = c
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type decoderCVBody struct {
	File  string `json:"file"`
	Model string `json:"model"`
	CV    string `json:"cv"`
	Write bool   `json:"write"`
	Value *int   `json:"value"`
	// Train is set to program on the main through that train's node.
	Train string `json:"train"`
	// Locomotive and Decoder name a roster decoder to save the value for.
	Locomotive string `json:"locomotive"`
	Decoder    string `json:"decoder"`
}

type decoderCVResponse struct {
	CV    string `json:"cv"`
	Value int    `json:"value"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (a *API) decoderCV(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	var body decoderCVBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.File == "" || body.Model == "" || body.CV == "" || (body.Write && body.Value == nil) {
		writeError(w, http.StatusBadRequest, "body must be JSON with file, model and cv, and value when writing")
		return
	}
	d, err := a.deps.Decoders.Definition(body.File, body.Model)
	switch {
	case errors.Is(err, decoder.ErrUnknownDecoder):
		writeError(w, http.StatusNotFound, "No decoder definition "+body.File+" has a model "+body.Model)
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "The decoder definition could not be read: "+err.Error())
		return
	}
	i := slices.IndexFunc(d.CVs, func(c decoder.CV) bool { return c.Name == body.CV })
	if i < 0 {
		writeError(w, http.StatusBadRequest, "The definition has no CV "+body.CV)
		return
	}
	cv := d.CVs[i]
	if body.Locomotive != "" || body.Decoder != "" {
		if !a.trainsReady(w) {
			return
		}
		e, ok := a.deps.Trains.Entry(body.Locomotive)
		if !ok {
			writeError(w, http.StatusNotFound, "no locomotive "+body.Locomotive+" in the roster")
			return
		}
		saved, ok := e.Decoder(body.Decoder)
		if !ok {
			writeError(w, http.StatusNotFound, e.Name+" has no decoder "+body.Decoder)
			return
		}
		if saved.File != body.File || saved.Model != body.Model {
			writeError(w, http.StatusBadRequest, e.Name+"'s decoder "+saved.Title()+" uses another definition")
			return
		}
	}
	switch {
	case cv.InfoOnly:
		writeError(w, http.StatusBadRequest, "CV "+body.CV+" is for information only and is never read or written")
		return
	case body.Write && cv.ReadOnly:
		writeError(w, http.StatusBadRequest, "CV "+body.CV+" is read-only")
		return
	case !body.Write && cv.WriteOnly:
		writeError(w, http.StatusBadRequest, "CV "+body.CV+" cannot be read back")
		return
	case !body.Write && body.Train != "":
		writeError(w, http.StatusBadRequest, "Reading on the main needs RailCom, which is not available yet")
		return
	}
	access, err := d.Programming.Resolve(body.CV)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var bus decoder.CVBus = serviceBus{a.deps.Programmer}
	if body.Train != "" {
		bus = pomBus{p: a.deps.Programmer, train: body.Train, delay: time.Duration(d.Programming.OpsWriteDelay) * time.Millisecond}
	}
	session := a.sessions.get(body.Train)
	cache := d.Programming.Index != nil && d.Programming.Index.Cache
	resp := decoderCVResponse{CV: body.CV}
	if body.Write {
		err = session.Write(r.Context(), bus, access, *body.Value, cache)
		resp.Value = *body.Value
	} else {
		resp.Value, err = session.Read(r.Context(), bus, access, cache)
	}
	resp.OK = err == nil
	if err != nil {
		resp.Error = programmer.Explain(err)
	}
	if resp.OK && body.Decoder != "" {
		if err := a.deps.Trains.Programmed(body.Locomotive, body.Decoder, body.CV, resp.Value, body.Write); err != nil {
			resp.Error = "The value could not be saved in the roster: " + err.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// serviceBus reaches the decoder on the programming track.
type serviceBus struct{ p Programmer }

func (b serviceBus) ReadCV(ctx context.Context, cv int) (int, error) {
	op, err := b.p.Read(ctx, cv)
	return op.Value, err
}

func (b serviceBus) WriteCV(ctx context.Context, cv, value int) error {
	_, err := b.p.Write(ctx, cv, value)
	return err
}

// pomBus reaches a decoder on the main through its train's node; writes
// only.
type pomBus struct {
	p     Programmer
	train string
	delay time.Duration
}

var errReadOnMain = errors.New("reading on the main needs RailCom, which is not available yet")

func (b pomBus) ReadCV(context.Context, int) (int, error) { return 0, errReadOnMain }

func (b pomBus) WriteCV(ctx context.Context, cv, value int) error {
	if _, err := b.p.WritePOM(ctx, b.train, cv, value); err != nil {
		return err
	}
	if b.delay > 0 {
		t := time.NewTimer(b.delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// decoderSessions keeps one session per decoder being programmed: the one
// on the programming track ("") and one per train on the main, so index
// writes are not repeated needlessly.
type decoderSessions struct {
	mu       sync.Mutex
	sessions map[string]*decoder.Session
}

func (s *decoderSessions) get(target string) *decoder.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*decoder.Session)
	}
	sess, ok := s.sessions[target]
	if !ok {
		sess = decoder.NewSession()
		s.sessions[target] = sess
	}
	return sess
}

func (s *decoderSessions) forget(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[target]; ok {
		sess.Forget()
	}
}

// writeJSONCompressed writes a large body gzipped when the client takes
// it; the decoder index and definitions run to hundreds of kilobytes as
// JSON and a tenth of that compressed.
func writeJSONCompressed(w http.ResponseWriter, r *http.Request, body any) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		writeJSON(w, http.StatusOK, body)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.WriteHeader(http.StatusOK)
	gz := gzip.NewWriter(w)
	if err := json.NewEncoder(gz).Encode(body); err != nil {
		return
	}
	_ = gz.Close()
}
