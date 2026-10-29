package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
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
//	POST /api/v1/trains/{id}/decoders/{decoder}/check  {match, differences, told, alike, others, identity, address, session}
//
// decoder-cv reads or writes one CV by the name a definition uses for it
// ("29", "16.0.259", "T2CV.1.05"), on the programming track or, with a
// train, on the main, where reads come back over RailCom. The browser
// walks a pane's CVs one request at a time, so it can show progress and
// stop. With a roster locomotive and one of its decoders, each value read
// or written is saved for it; on the programming track that needs the
// decoder's session to have begun with a check (409 until then). On the
// main the address picks the decoder, so there is no check.
//
// identify also reads the address the decoder answers to and lists the
// roster decoders it could be. When the first of them checks out, its
// session begins.
//
// check compares the decoder on the programming track with a roster
// decoder (see train.Manager.CheckDecoder) and, when it is that one,
// begins its session with the values read for the check. When it is not,
// nothing changes; POST .../session begins the session anyway.

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
	found, err := a.readOnTrack(r.Context())
	if err == nil && a.deps.Trains != nil {
		// The locomotive on the track may have been swapped, so what was
		// read or written before no longer counts as current.
		a.deps.Trains.EndSessions()
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "The decoder could not be identified: "+programmer.Explain(err))
		return
	}
	id := found.identity
	resp := identifyResponse{Identity: id, Candidates: []decoder.Candidate{}, Roster: []train.DecoderMatch{}}
	if found.addressErr == nil {
		resp.Address = &found.address
	}
	if a.deps.Trains != nil {
		var address uint16
		var long bool
		if resp.Address != nil {
			address, long = uint16(resp.Address.Address), resp.Address.Long
		}
		if m := a.deps.Trains.FindDecoder(trainIdentity(id), address, long); m != nil {
			resp.Roster = m
		}
		// The roster decoder that fits best begins its session if it
		// checks out, so its settings open ready to read.
		if len(resp.Roster) > 0 && resp.Roster[0].Decoder != "" && resp.Address != nil {
			top := &resp.Roster[0]
			if def, err := a.rosterDefinition(top.Train, top.Decoder); err == nil {
				if check, err := a.check(r.Context(), top.Train, top.Decoder, def, found, false); err == nil && check.Match {
					top.Checked = a.startChecked(top.Train, top.Decoder, def, check.Read) == nil
				}
			}
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
	// Next is CV 18's value when CV 17 is written on the main with it:
	// the two go as one long address.
	Next *int `json:"next"`
	// Locomotive and Decoder name a roster decoder to save the value for.
	Locomotive string `json:"locomotive"`
	Decoder    string `json:"decoder"`
}

type decoderCVResponse struct {
	CV    string `json:"cv"`
	Value int    `json:"value"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// CannotRead is set when a read on the main failed because the
	// command station cannot read there at all.
	CannotRead bool `json:"cannot_read,omitempty"`
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
	if body.Next != nil && (!body.Write || body.CV != "17" || body.Train == "" || !slices.ContainsFunc(d.CVs, func(c decoder.CV) bool { return c.Name == "18" })) {
		writeError(w, http.StatusBadRequest, "next goes with writing CV 17 on the main, for a definition that has CV 18")
		return
	}
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
		if body.Train == "" && !a.deps.Trains.SessionChecked(body.Decoder) {
			writeError(w, http.StatusConflict, "Check that the decoder on the programming track is "+e.Name+"'s "+saved.Title()+" first")
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
	switch {
	case body.Next != nil:
		// One Long Address instruction, so the decoder never answers a
		// half-changed address.
		_, err = a.deps.Programmer.WriteLongAddressPOM(r.Context(), body.Train, *body.Value, *body.Next)
		resp.Value = *body.Value
	case body.Write:
		err = session.Write(r.Context(), bus, access, *body.Value, cache)
		resp.Value = *body.Value
	default:
		resp.Value, err = session.Read(r.Context(), bus, access, cache)
	}
	resp.OK = err == nil
	if err != nil {
		resp.Error = programmer.Explain(err)
		resp.CannotRead = !body.Write && programmer.CannotReadOnMain(err)
	}
	if resp.OK && body.Decoder != "" {
		saved := map[string]int{body.CV: resp.Value}
		if body.Next != nil {
			saved["18"] = *body.Next
			// The decoder set CV 29 bit 5 itself.
			if v, err := a.deps.Trains.CVs(body.Locomotive, body.Decoder); err == nil {
				if cv29, ok := v.CVs["29"]; ok {
					saved["29"] = cv29 | 0x20
				}
			}
		}
		for _, name := range slices.Sorted(maps.Keys(saved)) {
			if err := a.deps.Trains.Programmed(body.Locomotive, body.Decoder, name, saved[name], body.Write); err != nil {
				resp.Error = "The value could not be saved in the roster: " + err.Error()
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// onTrack is what the decoder on the programming track said about
// itself.
type onTrack struct {
	identity decoder.Identity
	// address is valid when addressErr is nil.
	address    decoder.Address
	addressErr error
	// cvs are the plain CVs read, by name.
	cvs map[string]int
}

// readOnTrack identifies the decoder on the programming track and reads
// the address it answers to.
func (a *API) readOnTrack(ctx context.Context) (onTrack, error) {
	bus := &recordingBus{CVBus: serviceBus{a.deps.Programmer}, cvs: map[string]int{}}
	var found onTrack
	var err error
	found.identity, err = decoder.Identify(ctx, bus)
	// Identify can leave index CVs pointing elsewhere.
	a.sessions.forget("")
	if err != nil {
		return found, err
	}
	found.address, found.addressErr = decoder.ReadAddress(ctx, bus)
	found.cvs = bus.cvs
	return found, nil
}

// trainIdentity is an identity as the roster keeps it.
func trainIdentity(id decoder.Identity) train.Identity {
	t := train.Identity{Manufacturer: id.Manufacturer, Version: id.Version}
	if id.HasProductID {
		p := id.ProductID
		t.ProductID = &p
	}
	return t
}

// plainCVs are the CVs Identify and ReadAddress read that every
// definition names by number and no index CV moves.
var plainCVs = map[int]bool{1: true, 7: true, 8: true, 17: true, 18: true, 29: true}

// recordingBus keeps the values of the plain CVs read through it.
type recordingBus struct {
	decoder.CVBus
	cvs map[string]int
}

func (b *recordingBus) ReadCV(ctx context.Context, cv int) (int, error) {
	v, err := b.CVBus.ReadCV(ctx, cv)
	if err == nil && plainCVs[cv] {
		b.cvs[strconv.Itoa(cv)] = v
	}
	return v, err
}

type checkResponse struct {
	train.DecoderCheck
	// Identity, Manufacturer and Address are the decoder on the track's.
	Identity     decoder.Identity `json:"identity"`
	Manufacturer string           `json:"manufacturer,omitempty"`
	// Model is the model its product ID names, when the index lists one.
	Model   string           `json:"model,omitempty"`
	Address decoder.Address  `json:"address"`
	Session *train.CVSession `json:"session,omitempty"`
}

func (a *API) checkDecoder(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) || !a.programmerReady(w) {
		return
	}
	id, dec := r.PathValue("id"), r.PathValue("decoder")
	e, ok := a.deps.Trains.Entry(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no locomotive "+id+" in the roster")
		return
	}
	d, ok := e.Decoder(dec)
	if !ok {
		writeError(w, http.StatusNotFound, e.Name+" has no decoder "+dec)
		return
	}
	if d.File == "" {
		writeError(w, http.StatusBadRequest, e.Name+"'s decoder "+d.Title()+" has no definition yet")
		return
	}
	def, err := a.deps.Decoders.Definition(d.File, d.Model)
	if err != nil {
		writeError(w, http.StatusBadGateway, "The decoder definition could not be read: "+err.Error())
		return
	}
	found, err := a.readOnTrack(r.Context())
	if err == nil {
		err = found.addressErr
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "The decoder on the programming track could not be read: "+programmer.Explain(err))
		return
	}
	check, err := a.check(r.Context(), id, dec, def, found, true)
	if err != nil {
		a.trainError(w, err)
		return
	}
	if check.Match {
		if err := a.startChecked(id, dec, def, check.Read); err != nil {
			a.trainError(w, err)
			return
		}
	}
	resp := checkResponse{DecoderCheck: check, Identity: found.identity, Address: found.address}
	if idx, err := a.deps.Decoders.Index(); err == nil {
		if m, ok := idx.Manufacturer(found.identity.Manufacturer); ok {
			resp.Manufacturer = m.Name
		}
		// A product ID can stand for several models; only one is named.
		var models []string
		for _, c := range idx.Match(found.identity) {
			if c.Exact && !slices.Contains(models, c.Model) {
				models = append(models, c.Model)
			}
		}
		if len(models) == 1 {
			resp.Model = models[0]
		}
		for i, diff := range resp.Differences {
			if diff.What == "manufacturer" {
				if m, ok := idx.Manufacturer(int(diff.Roster)); ok {
					resp.Differences[i].RosterName = m.Name
				}
				resp.Differences[i].TrackName = resp.Manufacturer
			}
		}
	}
	if v, err := a.deps.Trains.CVs(id, dec); err == nil {
		resp.Session = v.Session
	}
	writeJSON(w, http.StatusOK, resp)
}

// rosterDefinition is the definition of a roster decoder.
func (a *API) rosterDefinition(id, dec string) (*decoder.Definition, error) {
	e, _ := a.deps.Trains.Entry(id)
	d, ok := e.Decoder(dec)
	if !ok || d.File == "" {
		return nil, decoder.ErrUnknownDecoder
	}
	return a.deps.Decoders.Definition(d.File, d.Model)
}

// check compares the decoder on the programming track with a roster
// decoder whose definition is def; with tell it reads CVs to tell it
// from others like it.
func (a *API) check(ctx context.Context, id, dec string, def *decoder.Definition, found onTrack, tell bool) (train.DecoderCheck, error) {
	opts := train.CheckOptions{}
	if idx, err := a.deps.Decoders.Index(); err == nil {
		opts.Manufacturer, opts.ProductIDs = definitionIdentity(idx, def.File, def.Model)
	}
	if tell {
		session := a.sessions.get("")
		cache := def.Programming.Index != nil && def.Programming.Index.Cache
		opts.Read = func(name string) (int, error) {
			if !readableCV(def, name) {
				return 0, errors.New("CV " + name + " cannot be read")
			}
			access, err := def.Programming.Resolve(name)
			if err != nil {
				return 0, err
			}
			return session.Read(ctx, serviceBus{a.deps.Programmer}, access, cache)
		}
	}
	track := train.OnTrack{Identity: trainIdentity(found.identity), Address: uint16(found.address.Address), Long: found.address.Long, CVs: found.cvs}
	return a.deps.Trains.CheckDecoder(id, dec, track, opts)
}

// startChecked begins a decoder's session after a check found it on the
// programming track, with what the check read as read in it.
func (a *API) startChecked(id, dec string, def *decoder.Definition, read map[string]int) error {
	if err := a.deps.Trains.StartSession(id, dec, train.CheckMatched); err != nil {
		return err
	}
	for _, cv := range slices.Sorted(maps.Keys(read)) {
		if !readableCV(def, cv) {
			continue
		}
		if err := a.deps.Trains.Programmed(id, dec, cv, read[cv], false); err != nil {
			return err
		}
	}
	return nil
}

// readableCV reports whether a definition has a CV that can be read.
func readableCV(def *decoder.Definition, name string) bool {
	i := slices.IndexFunc(def.CVs, func(c decoder.CV) bool { return c.Name == name })
	return i >= 0 && !def.CVs[i].InfoOnly && !def.CVs[i].WriteOnly
}

// definitionIdentity is what a definition lets a decoder report: its
// manufacturer's ID, and the model's product IDs (every model's, for the
// family as a whole).
func definitionIdentity(idx *decoder.Index, file, model string) (int, []uint32) {
	for _, f := range idx.Families {
		if f.File != file {
			continue
		}
		maker := 0
		for _, m := range idx.Manufacturers {
			if m.Name == f.Manufacturer {
				maker = m.ID
				break
			}
		}
		var ids []uint32
		for _, m := range f.Models {
			if m.Name != model && f.Name != model {
				continue
			}
			for _, p := range m.ProductIDs {
				if n, err := strconv.ParseUint(p, 10, 32); err == nil {
					ids = append(ids, uint32(n))
				}
			}
		}
		return maker, ids
	}
	return 0, nil
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

// pomBus reaches a decoder on the main through its train's node, waiting
// delay after each write as the definition asks.
type pomBus struct {
	p     Programmer
	train string
	delay time.Duration
}

func (b pomBus) ReadCV(ctx context.Context, cv int) (int, error) {
	op, err := b.p.ReadPOM(ctx, b.train, cv)
	return op.Value, err
}

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
