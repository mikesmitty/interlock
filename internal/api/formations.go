package api

import (
	"encoding/json"
	"net/http"

	"github.com/mikesmitty/interlock/internal/train"
)

// The car and formation endpoints. A formation is a train made up of
// locomotives and cars, from its front; /trains are the locomotives.
//
//	GET    /api/v1/cars                  {cars}
//	POST   /api/v1/cars                  {name, road_name, road_number, manufacturer, model, comment, length}
//	GET    /api/v1/cars/{id}
//	PATCH  /api/v1/cars/{id}             any of the fields above
//	DELETE /api/v1/cars/{id}             also takes it out of its formation
//	GET    /api/v1/formations            {formations}, each with its length
//	POST   /api/v1/formations            {name, comment, vehicles: [{locomotive | car, reverse}]}
//	GET    /api/v1/formations/{id}
//	PATCH  /api/v1/formations/{id}       any of {name, comment, vehicles}
//	DELETE /api/v1/formations/{id}       its locomotives and cars stay in the roster

func (a *API) listCars(w http.ResponseWriter, _ *http.Request) {
	list := []train.CarState{}
	if a.deps.Trains != nil {
		list = append(list, a.deps.Trains.Cars()...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cars": list})
}

func (a *API) getCar(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	c, ok := a.deps.Trains.Car(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no car "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (a *API) createCar(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var c train.Car
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a name")
		return
	}
	state, err := a.deps.Trains.CreateCar(c)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, state)
}

// carPatch is a car change: only the fields present change.
type carPatch struct {
	Name         *string  `json:"name"`
	RoadName     *string  `json:"road_name"`
	RoadNumber   *string  `json:"road_number"`
	Manufacturer *string  `json:"manufacturer"`
	Model        *string  `json:"model"`
	Comment      *string  `json:"comment"`
	Length       *float64 `json:"length"`
}

func (a *API) updateCar(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var p carPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with the car fields to change")
		return
	}
	state, err := a.deps.Trains.UpdateCar(r.PathValue("id"), func(c *train.Car) error {
		set := func(dst *string, src *string) {
			if src != nil {
				*dst = *src
			}
		}
		set(&c.Name, p.Name)
		set(&c.RoadName, p.RoadName)
		set(&c.RoadNumber, p.RoadNumber)
		set(&c.Manufacturer, p.Manufacturer)
		set(&c.Model, p.Model)
		set(&c.Comment, p.Comment)
		if p.Length != nil {
			c.Length = *p.Length
		}
		return nil
	})
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *API) deleteCar(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	if err := a.deps.Trains.DeleteCar(r.PathValue("id")); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listFormations(w http.ResponseWriter, _ *http.Request) {
	list := []train.FormationState{}
	if a.deps.Trains != nil {
		list = append(list, a.deps.Trains.Formations()...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"formations": list})
}

func (a *API) getFormation(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	f, ok := a.deps.Trains.Formation(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no train "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (a *API) createFormation(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var f train.Formation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&f); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a name and vehicles")
		return
	}
	state, err := a.deps.Trains.CreateFormation(f)
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, state)
}

// formationPatch is a formation change: only the fields present change.
type formationPatch struct {
	Name     *string          `json:"name"`
	Comment  *string          `json:"comment"`
	Vehicles *[]train.Vehicle `json:"vehicles"`
}

func (a *API) updateFormation(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	var p formationPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with the train fields to change")
		return
	}
	state, err := a.deps.Trains.UpdateFormation(r.PathValue("id"), func(f *train.Formation) error {
		if p.Name != nil {
			f.Name = *p.Name
		}
		if p.Comment != nil {
			f.Comment = *p.Comment
		}
		if p.Vehicles != nil {
			f.Vehicles = *p.Vehicles
		}
		return nil
	})
	if err != nil {
		a.trainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *API) deleteFormation(w http.ResponseWriter, r *http.Request) {
	if !a.trainsReady(w) {
		return
	}
	if err := a.deps.Trains.DeleteFormation(r.PathValue("id")); err != nil {
		a.trainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
