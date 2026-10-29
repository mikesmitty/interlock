package entity

import (
	"errors"
	"fmt"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Source says where an entity came from.
type Source string

// Sources.
const (
	// SourceProfile: built by a shipped or user profile for the node.
	SourceProfile Source = "profile"
	// SourceGuess: guessed from event names on a node without a profile;
	// shown as a suggestion until confirmed.
	SourceGuess Source = "guess"
	// SourceManual: built by hand from event IDs.
	SourceManual Source = "manual"
)

// Errors.
var (
	ErrNotFound       = errors.New("entity: no such entity")
	ErrUnavailable    = errors.New("entity: the node is offline")
	ErrUnknownCommand = errors.New("entity: the entity has no such command")
	ErrNotManual      = errors.New("entity: only entities built by hand can be deleted; hide this one instead")
	ErrExists         = errors.New("entity: an entity with that ID exists")
)

// InvalidError reports a manual entity that cannot be built.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return "entity: " + e.Reason }

// LockedError is a command a guard refused: something holds the entity
// where it is, as a route holds the turnouts it is set through.
type LockedError struct {
	Reason string
}

func (e *LockedError) Error() string { return e.Reason }

// Spec is the blueprint a builder produces: everything about an entity
// except its live state.
type Spec struct {
	ID   string
	Kind Kind
	Name string
	// Node is the node whose configuration defines the entity; zero for
	// manual entities.
	Node openlcb.NodeID
	// Group is the CDI group the entity came from, such as "Servo 1".
	Group  string
	Events map[string]openlcb.EventID
	// Keys maps roles to the configuration variable each event came from.
	Keys      map[string]string
	Source    Source
	Profile   string
	Suggested bool
	// Accessory is the number of the DCC accessory decoder output a
	// turnout built by hand is on; its throw and close events come from
	// it.
	Accessory int
}

// NodeInfo is what a builder is told about the node.
type NodeInfo struct {
	ID   openlcb.NodeID
	Name string
	SNIP *openlcb.SNIP
}

// Locomotive is one reported by a detector.
type Locomotive struct {
	Address detector.Address `json:"address"`
	// Name is how the address reads: "1234", "3 (short)", "consist 12".
	Name string `json:"name"`
	// Facing is "west", "east" or "" when the detector does not report it.
	Facing string    `json:"facing,omitempty"`
	Since  time.Time `json:"since"`
}

// Entity is a thing on the layout with a state.
type Entity struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// DefaultName is the name before the user renamed it.
	DefaultName string         `json:"default_name,omitempty"`
	Node        openlcb.NodeID `json:"node,omitempty"`
	NodeName    string         `json:"node_name,omitempty"`
	Group       string         `json:"group,omitempty"`
	// Accessory is the DCC accessory number of a turnout on an accessory
	// decoder.
	Accessory int    `json:"accessory,omitempty"`
	Source    Source `json:"source"`
	Profile   string `json:"profile,omitempty"`
	// Suggested marks a guess the user has not confirmed.
	Suggested bool `json:"suggested,omitempty"`
	// Hidden entities stay out of the main list and of automations.
	Hidden bool `json:"hidden,omitempty"`
	// Available is false while the entity's node is offline.
	Available bool   `json:"available"`
	State     string `json:"state"`
	// Commanded is the last command a turnout was given, which may differ
	// from State while it moves.
	Commanded string `json:"commanded,omitempty"`
	// Locomotives are the ones a reporter currently sees.
	Locomotives []Locomotive `json:"locomotives,omitempty"`
	// LastEvent is when an event last changed the entity.
	LastEvent time.Time                  `json:"last_event,omitzero"`
	Commands  []string                   `json:"commands,omitempty"`
	Events    map[string]openlcb.EventID `json:"events,omitempty"`
	Keys      map[string]string          `json:"keys,omitempty"`
	Updated   time.Time                  `json:"updated"`
	// Removed is set on the last update about an entity that went away.
	Removed bool `json:"removed,omitempty"`
}

// Manual is an entity built by hand, as kept in entities.yaml.
type Manual struct {
	ID     string                     `yaml:"id" json:"id"`
	Kind   Kind                       `yaml:"kind" json:"kind"`
	Name   string                     `yaml:"name" json:"name"`
	Events map[string]openlcb.EventID `yaml:"events,omitempty" json:"events"`
	// Accessory puts a turnout on a DCC accessory decoder output, 1 to
	// 2048: its throw and close events are the accessory events for that
	// number, and Events holds at most its feedback.
	Accessory int `yaml:"accessory,omitempty" json:"accessory,omitempty"`
}

// Spec turns a manual entity into a blueprint, checking it first.
func (m Manual) Spec() (Spec, error) {
	def, ok := Lookup(m.Kind)
	if !ok {
		return Spec{}, &InvalidError{Reason: fmt.Sprintf("%q is not a kind of entity", m.Kind)}
	}
	if m.Name == "" {
		return Spec{}, &InvalidError{Reason: "the entity needs a name"}
	}
	accessory, err := m.accessoryEvents()
	if err != nil {
		return Spec{}, err
	}
	events := make(map[string]openlcb.EventID)
	bound := make(map[string]bool)
	for role, ev := range m.Events {
		r, ok := def.Role(role)
		if !ok {
			return Spec{}, &InvalidError{Reason: fmt.Sprintf("a %s has no %q event", def.Label, role)}
		}
		if ev == 0 {
			continue
		}
		if accessory != nil && r.Command && ev != accessory[role] {
			return Spec{}, &InvalidError{Reason: fmt.Sprintf("a turnout on a DCC accessory decoder takes its %s event from its number", role)}
		}
		events[role] = ev
		bound[role] = true
	}
	for role, ev := range accessory {
		events[role] = ev
		bound[role] = true
	}
	if missing := def.Missing(bound); len(missing) > 0 {
		return Spec{}, &InvalidError{Reason: fmt.Sprintf("a %s needs its %s event", def.Label, missing[0])}
	}
	id := m.ID
	switch {
	case id != "":
	case m.Accessory != 0:
		id = AccessoryID(m.Accessory)
	default:
		id = string(m.Kind) + ".manual." + Slug(m.Name)
	}
	return Spec{ID: id, Kind: m.Kind, Name: m.Name, Events: events, Source: SourceManual, Accessory: m.Accessory}, nil
}

// accessoryEvents returns the throw and close events of a turnout on a DCC
// accessory decoder, or nil for an entity that isn't one.
func (m Manual) accessoryEvents() (map[string]openlcb.EventID, error) {
	if m.Accessory == 0 {
		return nil, nil
	}
	if m.Kind != KindTurnout {
		return nil, &InvalidError{Reason: "only a turnout can be on a DCC accessory decoder"}
	}
	throw, ok := traction.AccessoryEvent(m.Accessory, false)
	if !ok {
		return nil, &InvalidError{Reason: fmt.Sprintf("DCC accessory numbers run from %d to %d", traction.AccessoryFirst, traction.AccessoryLast)}
	}
	closeEv, _ := traction.AccessoryEvent(m.Accessory, true)
	return map[string]openlcb.EventID{"throw": throw, "close": closeEv}, nil
}

// stored is the manual entity as entities.yaml keeps it: a turnout on a
// DCC accessory decoder without the events its number gives it.
func (m Manual) stored() Manual {
	if m.Accessory == 0 {
		return m
	}
	def, _ := Lookup(m.Kind)
	events := make(map[string]openlcb.EventID)
	for role, ev := range m.Events {
		if r, ok := def.Role(role); ok && !r.Command {
			events[role] = ev
		}
	}
	m.Events = events
	if len(events) == 0 {
		m.Events = nil
	}
	return m
}

// Change is what Update can alter about an entity.
type Change struct {
	// Name renames the entity; an empty string restores the default.
	Name *string `json:"name,omitempty"`
	// Hidden hides or shows it.
	Hidden *bool `json:"hidden,omitempty"`
	// Confirmed accepts a guess.
	Confirmed *bool `json:"confirmed,omitempty"`
	// Events replaces a manual entity's events.
	Events map[string]openlcb.EventID `json:"events,omitempty"`
}

// binding ties an event to a role of an entity.
type binding struct {
	id   string
	role string
}

// rangeBinding ties an event range to an entity.
type rangeBinding struct {
	id  string
	rng openlcb.EventRange
}
