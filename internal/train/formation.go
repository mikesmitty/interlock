package train

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MaxVehicles bounds how many locomotives and cars a formation has.
const MaxVehicles = 200

// Car is a car in the roster: anything a train is made up of besides its
// locomotives.
type Car struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`

	// What the car is, for the roster.
	RoadName     string `yaml:"road_name,omitempty" json:"road_name,omitempty"`
	RoadNumber   string `yaml:"road_number,omitempty" json:"road_number,omitempty"`
	Manufacturer string `yaml:"manufacturer,omitempty" json:"manufacturer,omitempty"`
	Model        string `yaml:"model,omitempty" json:"model,omitempty"`
	Comment      string `yaml:"comment,omitempty" json:"comment,omitempty"`

	// Length is the car's length over its couplers in millimeters; 0
	// until it is measured.
	Length float64 `yaml:"length,omitempty" json:"length,omitempty"`
}

// CarState is a car as subscribers hear of it.
type CarState struct {
	Car
	// Removed is set on the last update about a deleted car.
	Removed bool `json:"removed,omitempty"`
}

// CarSubscriber is told whenever a car changes.
type CarSubscriber func(CarState)

// Normalize tidies a car and names it by its road name and number when
// it has no name.
func (c Car) Normalize() Car {
	for _, p := range []*string{&c.Name, &c.RoadName, &c.RoadNumber, &c.Manufacturer, &c.Model, &c.Comment} {
		*p = strings.TrimSpace(*p)
	}
	if c.Name == "" {
		c.Name = strings.TrimSpace(c.RoadName + " " + c.RoadNumber)
	}
	return c
}

// Validate checks a car on its own; the manager checks that IDs are
// unique.
func (c Car) Validate() error {
	if !validID(c.ID) {
		return invalid("%q cannot be a car ID; use letters, digits and dashes", c.ID)
	}
	if c.Name == "" {
		return invalid("a car needs a name, or its road name and number")
	}
	if len(c.Name) > 64 {
		return invalid("the name is too long; keep it under 64 characters")
	}
	for _, f := range []string{c.RoadName, c.RoadNumber, c.Manufacturer, c.Model} {
		if len(f) > 100 {
			return invalid("%q is too long; keep it under 100 characters", f[:20]+"…")
		}
	}
	if len(c.Comment) > 4000 {
		return invalid("the comment is too long; keep it under 4000 characters")
	}
	if c.Length < 0 || c.Length > MaxLength {
		return invalid("the length must be between 0 and %d mm", MaxLength)
	}
	return nil
}

// Formation is a train as it is made up: locomotives and cars in order
// from its front, the end that leads when it goes forward. The web
// interface calls it a train; in this package a train is what runs at a
// DCC address, as Train Control has it. A locomotive or car is in one
// formation at most.
type Formation struct {
	ID       string    `yaml:"id" json:"id"`
	Name     string    `yaml:"name" json:"name"`
	Vehicles []Vehicle `yaml:"vehicles" json:"vehicles"`
	Comment  string    `yaml:"comment,omitempty" json:"comment,omitempty"`
}

// Vehicle is one place in a formation: a locomotive or a car.
type Vehicle struct {
	// Locomotive is a roster entry's ID and Car a car's; one of them is
	// set.
	Locomotive string `yaml:"locomotive,omitempty" json:"locomotive,omitempty"`
	Car        string `yaml:"car,omitempty" json:"car,omitempty"`
	// Reverse is set for one turned around in the train, its own front
	// toward the train's rear.
	Reverse bool `yaml:"reverse,omitempty" json:"reverse"`
}

// FormationState is a formation as the API shows it.
type FormationState struct {
	Formation
	// Length is the train's length, its locomotives' and cars' lengths
	// added up, and Unmeasured how many of them have no length given.
	Length     float64 `json:"length"`
	Unmeasured int     `json:"unmeasured"`
	// Removed is set on the last update about a deleted formation.
	Removed bool `json:"removed,omitempty"`
}

// FormationSubscriber is told whenever a formation changes, including
// when one of its locomotives or cars is given a new length.
type FormationSubscriber func(FormationState)

// Normalize tidies a formation.
func (f Formation) Normalize() Formation {
	f.Name = strings.TrimSpace(f.Name)
	f.Comment = strings.TrimSpace(f.Comment)
	return f.clone()
}

// Validate checks a formation on its own; the manager checks its
// locomotives and cars against the roster.
func (f Formation) Validate() error {
	if !validID(f.ID) {
		return invalid("%q cannot be a train ID; use letters, digits and dashes", f.ID)
	}
	if _, _, ok := ParseID(f.ID); ok {
		return invalid("%s is an address, so it cannot be a train's ID", f.ID)
	}
	if f.Name == "" {
		return invalid("a train needs a name")
	}
	if len(f.Name) > 64 {
		return invalid("the name is too long; keep it under 64 characters")
	}
	if len(f.Comment) > 4000 {
		return invalid("the comment is too long; keep it under 4000 characters")
	}
	if len(f.Vehicles) > MaxVehicles {
		return invalid("a train can have %d locomotives and cars at most", MaxVehicles)
	}
	seen := make(map[Vehicle]bool, len(f.Vehicles))
	for _, v := range f.Vehicles {
		if (v.Locomotive == "") == (v.Car == "") {
			return invalid("each place in a train needs a locomotive or a car")
		}
		if !validID(v.Locomotive + v.Car) {
			return invalid("%q cannot be a locomotive or car ID", v.Locomotive+v.Car)
		}
		key := Vehicle{Locomotive: v.Locomotive, Car: v.Car}
		if seen[key] {
			return invalid("%s is in the train twice", v.Locomotive+v.Car)
		}
		seen[key] = true
	}
	return nil
}

func (f Formation) clone() Formation {
	f.Vehicles = append(make([]Vehicle, 0, len(f.Vehicles)), f.Vehicles...)
	return f
}

// --- the manager's cars and formations ---------------------------------------

// loadFormations takes the cars and formations from the file, leaving out
// of each formation the locomotives and cars no longer in the roster or
// in a formation before it. New calls it.
func (m *Manager) loadFormations() {
	for _, c := range m.opts.File.Cars() {
		m.cars[c.ID] = c
	}
	for _, f := range m.opts.File.Formations() {
		kept := f.Vehicles[:0]
		for _, v := range f.Vehicles {
			switch _, in := m.formationOfLocked(v, f.ID); {
			case !m.vehicleKnownLocked(v):
				m.log.Warn("train names a locomotive or car not in the roster", "train", f.ID, "vehicle", v.Locomotive+v.Car)
			case in:
				m.log.Warn("train names a locomotive or car in another train", "train", f.ID, "vehicle", v.Locomotive+v.Car)
			default:
				kept = append(kept, v)
			}
		}
		f.Vehicles = kept
		m.formations[f.ID] = f.Normalize()
	}
}

// vehicleKnownLocked reports whether a vehicle's locomotive or car is in
// the roster. The caller holds m.mu.
func (m *Manager) vehicleKnownLocked(v Vehicle) bool {
	if v.Locomotive != "" {
		_, ok := m.roster[v.Locomotive]
		return ok
	}
	_, ok := m.cars[v.Car]
	return ok
}

// formationOfLocked returns the formation other than skip a vehicle's
// locomotive or car is in. The caller holds m.mu.
func (m *Manager) formationOfLocked(v Vehicle, skip string) (Formation, bool) {
	for _, f := range m.formations {
		if f.ID == skip {
			continue
		}
		for _, w := range f.Vehicles {
			if w.Locomotive == v.Locomotive && w.Car == v.Car {
				return f, true
			}
		}
	}
	return Formation{}, false
}

// vehicleNameLocked is how a vehicle is called. The caller holds m.mu.
func (m *Manager) vehicleNameLocked(v Vehicle) string {
	if v.Locomotive != "" {
		return cmp.Or(m.roster[v.Locomotive].Name, v.Locomotive)
	}
	return cmp.Or(m.cars[v.Car].Name, v.Car)
}

// checkFormationLocked checks a formation's locomotives and cars: each in
// the roster and in no other formation. The caller holds m.mu.
func (m *Manager) checkFormationLocked(f Formation) error {
	for _, v := range f.Vehicles {
		if !m.vehicleKnownLocked(v) {
			if v.Locomotive != "" {
				return invalid("%s is not a locomotive in the roster", v.Locomotive)
			}
			return invalid("%s is not a car in the roster", v.Car)
		}
		if other, ok := m.formationOfLocked(v, f.ID); ok {
			return invalid("%s is in %s already; take it out of that train first", m.vehicleNameLocked(v), other.Name)
		}
	}
	return nil
}

// formationStateLocked is a formation as the API shows it, with its
// length. The caller holds m.mu.
func (m *Manager) formationStateLocked(f Formation) FormationState {
	s := FormationState{Formation: f.clone()}
	for _, v := range f.Vehicles {
		var length float64
		if v.Locomotive != "" {
			length = m.roster[v.Locomotive].Length
		} else {
			length = m.cars[v.Car].Length
		}
		if length == 0 {
			s.Unmeasured++
		}
		s.Length += length
	}
	return s
}

// Cars returns the cars by name.
func (m *Manager) Cars() []CarState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CarState, 0, len(m.cars))
	for _, c := range m.cars {
		out = append(out, CarState{Car: c})
	}
	slices.SortFunc(out, func(a, b CarState) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Car returns one car.
func (m *Manager) Car(id string) (CarState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cars[id]
	return CarState{Car: c}, ok
}

// newCarIDLocked makes an ID for a new car from its name.
func (m *Manager) newCarIDLocked(name string) string {
	base := slug(name)
	if base == "" {
		base = "car"
	}
	return uniqueID(base, func(id string) bool {
		_, taken := m.cars[id]
		return taken
	})
}

// CreateCar adds a car to the roster under a new ID made from its name.
func (m *Manager) CreateCar(c Car) (CarState, error) {
	c = c.Normalize()
	m.mu.Lock()
	c.ID = m.newCarIDLocked(c.Name)
	if err := c.Validate(); err != nil {
		m.mu.Unlock()
		return CarState{}, err
	}
	m.cars[c.ID] = c
	m.mu.Unlock()
	if err := m.opts.File.PutCar(c); err != nil {
		return CarState{Car: c}, err
	}
	m.sendCar(CarState{Car: c})
	return CarState{Car: c}, nil
}

// UpdateCar changes a car: change edits a copy, which is checked and
// saved. The formations it is in are told of a new length.
func (m *Manager) UpdateCar(id string, change func(*Car) error) (CarState, error) {
	m.mu.Lock()
	old, ok := m.cars[id]
	if !ok {
		m.mu.Unlock()
		return CarState{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next := old
	if err := change(&next); err != nil {
		m.mu.Unlock()
		return CarState{}, err
	}
	next.ID = old.ID
	next = next.Normalize()
	if err := next.Validate(); err != nil {
		m.mu.Unlock()
		return CarState{}, err
	}
	m.cars[id] = next
	var touched []string
	if next.Length != old.Length {
		touched = m.formationsWithLocked(func(v Vehicle) bool { return v.Car == id })
	}
	m.mu.Unlock()
	if err := m.opts.File.PutCar(next); err != nil {
		return CarState{Car: next}, err
	}
	m.sendCar(CarState{Car: next})
	m.sendFormations(touched)
	return CarState{Car: next}, nil
}

// DeleteCar removes a car from the roster and from its formation.
func (m *Manager) DeleteCar(id string) error {
	m.mu.Lock()
	c, ok := m.cars[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(m.cars, id)
	changed := m.dropVehicleLocked(func(v Vehicle) bool { return v.Car == id })
	m.mu.Unlock()
	err := m.opts.File.RemoveCar(id)
	err = errors.Join(err, m.saveFormations(changed))
	m.sendCar(CarState{Car: c, Removed: true})
	return err
}

// Formations returns the formations by name.
func (m *Manager) Formations() []FormationState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]FormationState, 0, len(m.formations))
	for _, f := range m.formations {
		out = append(out, m.formationStateLocked(f))
	}
	slices.SortFunc(out, func(a, b FormationState) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Formation returns one formation.
func (m *Manager) Formation(id string) (FormationState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.formations[id]
	if !ok {
		return FormationState{}, false
	}
	return m.formationStateLocked(f), true
}

// newFormationIDLocked makes an ID for a new formation from its name,
// never one that reads as an address.
func (m *Manager) newFormationIDLocked(name string) string {
	base := slug(name)
	if base == "" {
		base = "train"
	}
	if _, _, ok := ParseID(base); ok {
		base = "train-" + base
	}
	return uniqueID(base, func(id string) bool {
		_, taken := m.formations[id]
		_, _, address := ParseID(id)
		return taken || address
	})
}

// CreateFormation adds a formation under a new ID made from its name.
func (m *Manager) CreateFormation(f Formation) (FormationState, error) {
	f = f.Normalize()
	m.mu.Lock()
	f.ID = m.newFormationIDLocked(f.Name)
	if err := f.Validate(); err != nil {
		m.mu.Unlock()
		return FormationState{}, err
	}
	if err := m.checkFormationLocked(f); err != nil {
		m.mu.Unlock()
		return FormationState{}, err
	}
	m.formations[f.ID] = f
	state := m.formationStateLocked(f)
	m.mu.Unlock()
	if err := m.opts.File.PutFormation(f); err != nil {
		return state, err
	}
	m.sendFormation(state)
	return state, nil
}

// UpdateFormation changes a formation: change edits a copy, which is
// checked and saved.
func (m *Manager) UpdateFormation(id string, change func(*Formation) error) (FormationState, error) {
	m.mu.Lock()
	old, ok := m.formations[id]
	if !ok {
		m.mu.Unlock()
		return FormationState{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next := old.clone()
	if err := change(&next); err != nil {
		m.mu.Unlock()
		return FormationState{}, err
	}
	next.ID = old.ID
	next = next.Normalize()
	if err := next.Validate(); err != nil {
		m.mu.Unlock()
		return FormationState{}, err
	}
	if err := m.checkFormationLocked(next); err != nil {
		m.mu.Unlock()
		return FormationState{}, err
	}
	m.formations[id] = next
	state := m.formationStateLocked(next)
	m.mu.Unlock()
	if err := m.opts.File.PutFormation(next); err != nil {
		return state, err
	}
	m.sendFormation(state)
	return state, nil
}

// DeleteFormation removes a formation. Its locomotives and cars stay in
// the roster.
func (m *Manager) DeleteFormation(id string) error {
	m.mu.Lock()
	f, ok := m.formations[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(m.formations, id)
	state := m.formationStateLocked(f)
	m.mu.Unlock()
	if err := m.opts.File.RemoveFormation(id); err != nil {
		return err
	}
	state.Removed = true
	m.sendFormation(state)
	return nil
}

// formationsWithLocked returns the IDs of the formations with a vehicle
// that matches. The caller holds m.mu.
func (m *Manager) formationsWithLocked(match func(Vehicle) bool) []string {
	var ids []string
	for id, f := range m.formations {
		if slices.ContainsFunc(f.Vehicles, match) {
			ids = append(ids, id)
		}
	}
	return ids
}

// renameLocomotiveLocked follows a roster entry's new ID into the
// formations. The caller holds m.mu; it returns the formations to save.
func (m *Manager) renameLocomotiveLocked(old, next string) []string {
	ids := m.formationsWithLocked(func(v Vehicle) bool { return v.Locomotive == old })
	for _, id := range ids {
		f := m.formations[id].clone()
		for i := range f.Vehicles {
			if f.Vehicles[i].Locomotive == old {
				f.Vehicles[i].Locomotive = next
			}
		}
		m.formations[id] = f
	}
	return ids
}

// dropVehicleLocked takes the vehicles that match out of the formations.
// The caller holds m.mu; it returns the formations to save.
func (m *Manager) dropVehicleLocked(match func(Vehicle) bool) []string {
	ids := m.formationsWithLocked(match)
	for _, id := range ids {
		f := m.formations[id].clone()
		f.Vehicles = slices.DeleteFunc(f.Vehicles, match)
		m.formations[id] = f
	}
	return ids
}

// saveFormations writes changed formations and tells subscribers.
func (m *Manager) saveFormations(ids []string) error {
	var errs []error
	for _, id := range ids {
		m.mu.RLock()
		f, ok := m.formations[id]
		m.mu.RUnlock()
		if ok {
			errs = append(errs, m.opts.File.PutFormation(f))
		}
	}
	m.sendFormations(ids)
	return errors.Join(errs...)
}

// sendFormations tells subscribers about formations as they are now.
func (m *Manager) sendFormations(ids []string) {
	for _, id := range ids {
		m.mu.RLock()
		f, ok := m.formations[id]
		var state FormationState
		if ok {
			state = m.formationStateLocked(f)
		}
		m.mu.RUnlock()
		if ok {
			m.sendFormation(state)
		}
	}
}

// SubscribeCars registers a car subscriber.
func (m *Manager) SubscribeCars(sub CarSubscriber) (unsubscribe func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.carSubs[id] = sub
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.carSubs, id)
		m.mu.Unlock()
	}
}

// SubscribeFormations registers a formation subscriber.
func (m *Manager) SubscribeFormations(sub FormationSubscriber) (unsubscribe func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.formationSubs[id] = sub
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.formationSubs, id)
		m.mu.Unlock()
	}
}

func (m *Manager) sendCar(c CarState) {
	m.mu.RLock()
	subs := make([]CarSubscriber, 0, len(m.carSubs))
	for _, sub := range m.carSubs {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()
	for _, sub := range subs {
		sub(c)
	}
}

func (m *Manager) sendFormation(f FormationState) {
	m.mu.RLock()
	subs := make([]FormationSubscriber, 0, len(m.formationSubs))
	for _, sub := range m.formationSubs {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()
	for _, sub := range subs {
		sub(f)
	}
}
