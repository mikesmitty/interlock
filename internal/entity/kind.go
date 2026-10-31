// Package entity models the layout as things with a state and commands:
// turnouts, blocks, reporters, sensors, switches, buttons and signal heads.
// Entities are
// built from node profiles over the named event table, or by hand from
// any events, and follow the bus: an Event Report or an Identified reply
// for one of their events changes their state, and a command sends one.
package entity

import (
	"strings"
	"unicode"
)

// Kind is what sort of thing an entity is.
type Kind string

// Entity kinds.
const (
	KindTurnout  Kind = "turnout"
	KindBlock    Kind = "block"
	KindReporter Kind = "reporter"
	KindSensor   Kind = "sensor"
	KindSwitch   Kind = "switch"
	KindButton   Kind = "button"
	// KindSignalHead is one head of a signal: a set of lamps that shows
	// one aspect at a time, each chosen by an event of its own.
	KindSignalHead Kind = "signal_head"
)

// States.
const (
	StateUnknown  = "unknown"
	StateThrown   = "thrown"
	StateClosed   = "closed"
	StateMoving   = "moving"
	StateOccupied = "occupied"
	StateClear    = "clear"
	StateOn       = "on"
	StateOff      = "off"
	StateIdle     = "idle"
)

// A signal head's aspects, which are its states too. S, D and Number are
// the lit letters and speed number of NYC subway time signals.
const (
	AspectDark           = "dark"
	AspectRed            = "red"
	AspectYellow         = "yellow"
	AspectGreen          = "green"
	AspectLunar          = "lunar"
	AspectS              = "s"
	AspectD              = "d"
	AspectNumber         = "number"
	AspectFlashingRed    = "flashing_red"
	AspectFlashingYellow = "flashing_yellow"
	AspectFlashingGreen  = "flashing_green"
)

// Role is the part an event plays in an entity: the turnout's throw
// command, the block's occupied report.
type Role struct {
	Name string `json:"name"`
	// Required roles must be bound for the entity to exist.
	Required bool `json:"required,omitempty"`
	// Command roles are events Interlock sends on the entity's behalf;
	// the others are reports the entity listens for.
	Command bool `json:"command,omitempty"`
	// State is the state the event puts the entity in. For a command role
	// it applies when the entity has no feedback roles bound.
	State string `json:"state,omitempty"`
	// Range marks a role bound to an event range rather than one event.
	Range bool `json:"range,omitempty"`
	// Label describes the role for the UI.
	Label string `json:"label"`
}

// Definition describes a kind: its roles and states.
type Definition struct {
	Kind   Kind     `json:"kind"`
	Label  string   `json:"label"`
	Plural string   `json:"plural"`
	Roles  []Role   `json:"roles"`
	States []string `json:"states"`
}

var definitions = map[Kind]Definition{
	KindTurnout: {
		Kind: KindTurnout, Label: "Turnout", Plural: "Turnouts",
		Roles: []Role{
			{Name: "thrown", State: StateThrown, Label: "Thrown feedback"},
			{Name: "closed", State: StateClosed, Label: "Closed feedback"},
			{Name: "throw", Required: true, Command: true, State: StateThrown, Label: "Throw command"},
			{Name: "close", Required: true, Command: true, State: StateClosed, Label: "Close command"},
			{Name: "center", Command: true, State: StateUnknown, Label: "Center command"},
		},
		States: []string{StateThrown, StateClosed, StateMoving, StateUnknown},
	},
	KindBlock: {
		Kind: KindBlock, Label: "Block", Plural: "Blocks",
		Roles: []Role{
			{Name: "occupied", Required: true, State: StateOccupied, Label: "Occupied"},
			{Name: "clear", Required: true, State: StateClear, Label: "Clear"},
		},
		States: []string{StateOccupied, StateClear, StateUnknown},
	},
	KindReporter: {
		Kind: KindReporter, Label: "Reporter", Plural: "Reporters",
		Roles: []Role{
			{Name: "range", Required: true, Range: true, Label: "Detector event range"},
		},
		States: []string{StateOccupied, StateClear, StateUnknown},
	},
	KindSensor: {
		Kind: KindSensor, Label: "Sensor", Plural: "Sensors",
		Roles: []Role{
			{Name: "on", Required: true, State: StateOn, Label: "On"},
			{Name: "off", Required: true, State: StateOff, Label: "Off"},
		},
		States: []string{StateOn, StateOff, StateUnknown},
	},
	KindSwitch: {
		Kind: KindSwitch, Label: "Switch", Plural: "Switches",
		Roles: []Role{
			{Name: "on", Required: true, Command: true, State: StateOn, Label: "On"},
			{Name: "off", Required: true, Command: true, State: StateOff, Label: "Off"},
		},
		States: []string{StateOn, StateOff, StateUnknown},
	},
	KindButton: {
		Kind: KindButton, Label: "Button", Plural: "Buttons",
		Roles: []Role{
			{Name: "press", Required: true, Command: true, State: StateIdle, Label: "Press"},
		},
		States: []string{StateIdle},
	},
	KindSignalHead: {
		Kind: KindSignalHead, Label: "Signal head", Plural: "Signal heads",
		Roles: []Role{
			{Name: AspectDark, Command: true, State: AspectDark, Label: "Dark"},
			{Name: AspectRed, Command: true, State: AspectRed, Label: "Red"},
			{Name: AspectYellow, Command: true, State: AspectYellow, Label: "Yellow"},
			{Name: AspectGreen, Command: true, State: AspectGreen, Label: "Green"},
			{Name: AspectLunar, Command: true, State: AspectLunar, Label: "Lunar"},
			{Name: AspectS, Command: true, State: AspectS, Label: "S"},
			{Name: AspectD, Command: true, State: AspectD, Label: "D"},
			{Name: AspectNumber, Command: true, State: AspectNumber, Label: "Number"},
			{Name: AspectFlashingRed, Command: true, State: AspectFlashingRed, Label: "Flashing red"},
			{Name: AspectFlashingYellow, Command: true, State: AspectFlashingYellow, Label: "Flashing yellow"},
			{Name: AspectFlashingGreen, Command: true, State: AspectFlashingGreen, Label: "Flashing green"},
		},
		States: []string{
			AspectDark, AspectRed, AspectYellow, AspectGreen, AspectLunar, AspectS, AspectD, AspectNumber,
			AspectFlashingRed, AspectFlashingYellow, AspectFlashingGreen, StateUnknown,
		},
	},
}

// kindOrder is how kinds are listed.
var kindOrder = []Kind{KindTurnout, KindBlock, KindReporter, KindSensor, KindSwitch, KindButton, KindSignalHead}

// Definitions lists every kind in display order.
func Definitions() []Definition {
	out := make([]Definition, 0, len(kindOrder))
	for _, k := range kindOrder {
		out = append(out, definitions[k])
	}
	return out
}

// Lookup returns a kind's definition.
func Lookup(kind Kind) (Definition, bool) {
	d, ok := definitions[kind]
	return d, ok
}

// Role returns the named role.
func (d Definition) Role(name string) (Role, bool) {
	for _, r := range d.Roles {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}

// Missing lists the required roles not among bound.
func (d Definition) Missing(bound map[string]bool) []string {
	var missing []string
	for _, r := range d.Roles {
		if r.Required && !bound[r.Name] {
			missing = append(missing, r.Name)
		}
	}
	return missing
}

// Commands lists the command roles among bound, in definition order.
func (d Definition) Commands(bound map[string]bool) []string {
	var out []string
	for _, r := range d.Roles {
		if r.Command && bound[r.Name] {
			out = append(out, r.Name)
		}
	}
	return out
}

// hasFeedback reports whether any non-command role is bound.
func (d Definition) hasFeedback(bound map[string]bool) bool {
	for _, r := range d.Roles {
		if !r.Command && bound[r.Name] {
			return true
		}
	}
	return false
}

// Slug turns a name into an identifier: lower case, runs of anything but
// letters and digits become one underscore.
func Slug(s string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pending && b.Len() > 0 {
				b.WriteByte('_')
			}
			pending = false
			b.WriteRune(r)
			continue
		}
		pending = true
	}
	return b.String()
}
