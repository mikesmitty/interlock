package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/internal/train"
)

// Trains is the train manager.
type Trains interface {
	List() []train.Train
	Get(id string) (train.Train, bool)
	Entry(id string) (train.Entry, bool)
	Create(e train.Entry) (train.Train, error)
	Update(ctx context.Context, id string, change func(*train.Entry) error) (train.Train, error)
	Delete(ctx context.Context, id string) error
	AddDecoder(ctx context.Context, id string, d train.Decoder, install bool) (train.Train, train.Decoder, error)
	UpdateDecoder(ctx context.Context, id, decoder string, change func(*train.Decoder)) (train.Train, error)
	RemoveDecoder(ctx context.Context, id, decoder string) (train.Train, error)
	Install(ctx context.Context, id, decoder string) (train.Train, error)
	CVs(id, decoder string) (train.CVValues, error)
	SetCVs(id, decoder string, values map[string]int, replace bool) error
	SetChanges(id, decoder string, changes map[string]train.CVChange) error
	Programmed(id, decoder, cv string, value int, written bool) error
	KeepProfile(id, decoder, name string, replace bool) (train.DecoderProfile, error)
	DeleteProfile(id, decoder, name string) error
	StartSession(id, decoder, check string) error
	SessionChecked(decoder string) bool
	CheckDecoder(id, decoder string, track train.OnTrack, opts train.CheckOptions) (train.DecoderCheck, error)
	EndSession(id, decoder string) error
	EndSessions()
	FindDecoder(id train.Identity, address uint16, long bool) []train.DecoderMatch
	PlanImport(items []train.Import) []train.ImportStep
	ApplyImport(ctx context.Context, items []train.Import, plan []train.ImportStep) (train.ImportResult, error)
	Search(ctx context.Context, query string) ([]train.Found, error)
	Acquire(ctx context.Context, id string) (train.Train, error)
	Release(ctx context.Context, id string) (train.Train, error)
	SetSpeed(ctx context.Context, id string, speed float32, direction string) (train.Train, error)
	EmergencyStop(ctx context.Context, id string) (train.Train, error)
	SetFunction(ctx context.Context, id string, fn int, on bool) (train.Train, error)
	Refresh(ctx context.Context, id string) (train.Train, error)
	Layout() train.Layout
	SetPower(ctx context.Context, on bool) (train.Layout, error)
	SetEmergencyStop(ctx context.Context, stop bool) (train.Layout, error)
	Subscribe(train.Subscriber) func()
	SubscribeLayout(train.LayoutSubscriber) func()
	Consists() []train.ConsistState
	Consist(id string) (train.ConsistState, bool)
	CreateConsist(c train.Consist) (train.ConsistState, error)
	UpdateConsist(id string, change func(*train.Consist) error) (train.ConsistState, error)
	DeleteConsist(id string) error
	MakeUp(ctx context.Context, id string) (train.ConsistState, error)
	BreakUp(ctx context.Context, id string) (train.ConsistState, error)
	SubscribeConsists(train.ConsistSubscriber) func()
	Cars() []train.CarState
	Car(id string) (train.CarState, bool)
	CreateCar(c train.Car) (train.CarState, error)
	UpdateCar(id string, change func(*train.Car) error) (train.CarState, error)
	DeleteCar(id string) error
	SubscribeCars(train.CarSubscriber) func()
	Formations() []train.FormationState
	Formation(id string) (train.FormationState, bool)
	CreateFormation(f train.Formation) (train.FormationState, error)
	UpdateFormation(id string, change func(*train.Formation) error) (train.FormationState, error)
	DeleteFormation(id string) error
	SubscribeFormations(train.FormationSubscriber) func()
}

// Programmer reads and writes CVs.
type Programmer interface {
	Track() programmer.Track
	Find(ctx context.Context) (programmer.Track, error)
	Read(ctx context.Context, cv int) (programmer.Op, error)
	Write(ctx context.Context, cv, value int) (programmer.Op, error)
	WritePOM(ctx context.Context, trainID string, cv, value int) (programmer.Op, error)
	ReadPOM(ctx context.Context, trainID string, cv int) (programmer.Op, error)
	History() []programmer.Op
	Subscribe(programmer.Subscriber) func()
}

// The train and programming endpoints:
//
//	GET    /api/v1/trains                     {trains, layout}
//	POST   /api/v1/trains                     add to the roster {name, address, long, functions, momentary, ...}
//	POST   /api/v1/trains/search              {query} -> {trains: [found]}
//	GET    /api/v1/trains/layout              {power_off, power_known, emergency_stop}
//	POST   /api/v1/trains/layout              {power} or {emergency_stop} -> layout
//	GET    /api/v1/trains/{id}                id is a roster ID, or an address: "1234", "3", or "3l" for a long address under 128
//	PATCH  /api/v1/trains/{id}                any of {name, address, long, functions, momentary, road_name, road_number,
//	                                          manufacturer, model, owner, comment}
//	DELETE /api/v1/trains/{id}
//	POST   /api/v1/trains/{id}/acquire        find the train node and take control
//	POST   /api/v1/trains/{id}/release
//	POST   /api/v1/trains/{id}/speed          {speed, direction}; takes control first if need be
//	POST   /api/v1/trains/{id}/estop
//	POST   /api/v1/trains/{id}/function       {function, on}
//	POST   /api/v1/trains/{id}/refresh        read the train's state back
//	POST   /api/v1/trains/{id}/cv             {cv, value} write a CV on the main -> the operation
//	POST   /api/v1/trains/{id}/cv/read        {cv} read a CV on the main over RailCom -> the operation, with value
//	GET    /api/v1/programming                {track, history}
//	POST   /api/v1/programming/find           look for the programming track again -> track
//	POST   /api/v1/programming/read           {cv} -> the operation, with value
//	POST   /api/v1/programming/write          {cv, value} -> the operation
//
// A CV operation that ran but failed (no decoder answered, say) is 200
// with ok false and the reason in error; only bad input is 400.

func (a *API) trainsReady(w http.ResponseWriter) bool {
	if a.deps.Trains == nil {
		writeError(w, http.StatusServiceUnavailable, "trains are not available")
		return false
	}
	return true
}

func (a *API) listTrains(w http.ResponseWriter, _ *http.Request) {
	list := []train.Train{}
	var layout train.Layout
	if a.deps.Trains != nil {
		if l := a.deps.Trains.List(); l != nil {
			list = l
		}
		layout = a.deps.Trains.Layout()
	}
	writeJSON(w, http.StatusOK, map[string]any{"trains": list, "layout": layout})
}

func (a *API) getTrain(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	tr, ok := a.deps.Trains.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no train "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (a *API) createTrain(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var e train.Entry
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with name and address")
		return
	}
	tr, err := a.deps.Trains.Create(e)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tr)
}

func (a *API) updateTrain(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var p trainPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with the roster fields to change")
		return
	}
	tr, err := a.deps.Trains.Update(r.Context(), r.PathValue("id"), func(e *train.Entry) error {
		p.apply(e)
		return nil
	})
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// trainPatch is a roster change: only the fields present change.
type trainPatch struct {
	Name         *string         `json:"name"`
	Address      *uint16         `json:"address"`
	Long         *bool           `json:"long"`
	Functions    *map[int]string `json:"functions"`
	Momentary    *[]int          `json:"momentary"`
	RoadName     *string         `json:"road_name"`
	RoadNumber   *string         `json:"road_number"`
	Manufacturer *string         `json:"manufacturer"`
	Model        *string         `json:"model"`
	Owner        *string         `json:"owner"`
	Comment      *string         `json:"comment"`
	Length       *float64        `json:"length"`
}

func (p trainPatch) apply(e *train.Entry) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&e.Name, p.Name)
	set(&e.RoadName, p.RoadName)
	set(&e.RoadNumber, p.RoadNumber)
	set(&e.Manufacturer, p.Manufacturer)
	set(&e.Model, p.Model)
	set(&e.Owner, p.Owner)
	set(&e.Comment, p.Comment)
	if p.Address != nil {
		e.Address = *p.Address
		e.Long = false
	}
	if p.Long != nil {
		e.Long = *p.Long
	}
	if p.Length != nil {
		e.Length = *p.Length
	}
	if p.Functions != nil {
		e.Functions = *p.Functions
	}
	if p.Momentary != nil {
		e.Momentary = *p.Momentary
	}
}

func (a *API) deleteTrain(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	if err := a.deps.Trains.Delete(r.Context(), r.PathValue("id")); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) searchTrains(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a query")
		return
	}
	found, err := a.deps.Trains.Search(r.Context(), body.Query)
	if err != nil {
		a.trainError(w, err)
		return
	}
	if found == nil {
		found = []train.Found{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"trains": found})
}

// trainAction runs one of the per-train operations and answers with the
// train as it stands.
func (a *API) trainAction(status int, act func(ctx context.Context, id string) (train.Train, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.trainsReady(w) {
			return
		}
		tr, err := act(r.Context(), r.PathValue("id"))
		if err != nil {
			a.trainError(w, err)
			return
		}
		writeJSON(w, status, tr)
	}
}

func (a *API) trainSpeed(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Speed     float32 `json:"speed"`
		Direction string  `json:"direction"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with speed and direction")
		return
	}
	tr, err := a.deps.Trains.SetSpeed(r.Context(), r.PathValue("id"), body.Speed, body.Direction)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, tr)
}

func (a *API) trainFunction(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Function *int `json:"function"`
		On       bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Function == nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with function and on")
		return
	}
	tr, err := a.deps.Trains.SetFunction(r.Context(), r.PathValue("id"), *body.Function, body.On)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, tr)
}

func (a *API) getLayout(w http.ResponseWriter, _ *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, a.deps.Trains.Layout())
}

func (a *API) setLayout(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var body struct {
		Power         *bool `json:"power"`
		EmergencyStop *bool `json:"emergency_stop"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || (body.Power == nil && body.EmergencyStop == nil) {
		writeError(w, http.StatusBadRequest, "body must be JSON with power or emergency_stop")
		return
	}
	var layout train.Layout
	var err error
	if body.EmergencyStop != nil {
		layout, err = a.deps.Trains.SetEmergencyStop(r.Context(), *body.EmergencyStop)
	}
	if err == nil && body.Power != nil {
		layout, err = a.deps.Trains.SetPower(r.Context(), *body.Power)
	}
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, layout)
}

func (a *API) trainError(w http.ResponseWriter, err error) {
	var invalid *train.InvalidError
	switch {
	case errors.Is(err, train.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, train.ErrNoNode), errors.Is(err, train.ErrRefused), errors.Is(err, train.ErrConsistRefused):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	default:
		a.busError(w, err)
	}
}

// --- programming ---------------------------------------------------------

func (a *API) programmerReady(w http.ResponseWriter) bool {
	if a.deps.Programmer == nil {
		writeError(w, http.StatusServiceUnavailable, "programming is not available")
		return false
	}
	return true
}

func (a *API) programming(w http.ResponseWriter, _ *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	history := a.deps.Programmer.History()
	if history == nil {
		history = []programmer.Op{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"track": a.deps.Programmer.Track(), "history": history})
}

func (a *API) findProgrammingTrack(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	track, err := a.deps.Programmer.Find(r.Context())
	if err != nil {
		if errors.Is(err, programmer.ErrNoTrack) {
			writeError(w, http.StatusNotFound, programmer.Explain(err))
			return
		}
		a.busError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, track)
}

type cvBody struct {
	CV    *int `json:"cv"`
	Value *int `json:"value"`
}

func readCVBody(w http.ResponseWriter, r *http.Request, needValue bool) (cvBody, bool) {
	var body cvBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.CV == nil || (needValue && body.Value == nil) {
		what := "cv"
		if needValue {
			what = "cv and value"
		}
		writeError(w, http.StatusBadRequest, "body must be JSON with "+what)
		return body, false
	}
	return body, true
}

// writeOp answers with a CV operation: bad input is 400, anything that
// ran is 200 with the outcome inside.
func (a *API) writeOp(w http.ResponseWriter, op programmer.Op, err error) {
	var invalid *train.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case errors.Is(err, train.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeJSON(w, http.StatusOK, op)
	}
}

func (a *API) readCV(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	body, ok := readCVBody(w, r, false)
	if !ok {
		return
	}
	op, err := a.deps.Programmer.Read(r.Context(), *body.CV)
	a.writeOp(w, op, err)
}

func (a *API) writeCV(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	body, ok := readCVBody(w, r, true)
	if !ok {
		return
	}
	op, err := a.deps.Programmer.Write(r.Context(), *body.CV, *body.Value)
	a.writeOp(w, op, err)
}

func (a *API) writePOM(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	body, ok := readCVBody(w, r, true)
	if !ok {
		return
	}
	op, err := a.deps.Programmer.WritePOM(r.Context(), r.PathValue("id"), *body.CV, *body.Value)
	a.writeOp(w, op, err)
}

func (a *API) readPOM(w http.ResponseWriter, r *http.Request) {
	if !a.programmerReady(w) {
		return
	}
	body, ok := readCVBody(w, r, false)
	if !ok {
		return
	}
	op, err := a.deps.Programmer.ReadPOM(r.Context(), r.PathValue("id"), *body.CV)
	a.writeOp(w, op, err)
}
