// Package train is the throttle side of Train Control. It keeps a roster
// of locomotives in trains.yaml, finds the train node for each one with
// the Train Search protocol (having the command station create it when
// it has to), takes the controller role, and sends speed, direction and
// function commands. What it set, and what it read back from the train,
// is the train's state; the throttle page and the automations work from
// it. The well-known emergency events, which stop every train or cut
// track power, are handled here too.
package train

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Limits.
const (
	// MaxSpeed is the top of the speed range: DCC's 126 steps, one per
	// protocol unit.
	MaxSpeed = 126
	// MaxAddress is the highest DCC long address.
	MaxAddress = 10239
	// MaxFunction is the highest function number.
	MaxFunction = traction.MaxFunction
)

// Directions.
const (
	Forward = "forward"
	Reverse = "reverse"
)

// Commands an automation's train action can give.
const (
	CommandSpeed    = "speed"
	CommandStop     = "stop"
	CommandEStop    = "estop"
	CommandFunction = "function"
)

// Errors.
var (
	ErrNotFound = errors.New("train: no such train")
	ErrExists   = errors.New("train: a train with that address is already in the roster")
	ErrNoNode   = errors.New("train: no command station answered for the locomotive")
	ErrRefused  = errors.New("train: the train refused to be controlled")
)

// InvalidError reports a roster entry or command that cannot be used.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return "train: " + e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}

// Entry is a locomotive in the roster, as kept in trains.yaml.
type Entry struct {
	// ID is the address as text, with an "l" after a long address of 127
	// or less: "1234", "3", "3l".
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Address uint16 `yaml:"address" json:"address"`
	// Long is the address kind; addresses above 127 are always long.
	Long bool `yaml:"long,omitempty" json:"long"`
	// Functions labels the function keys: {0: Headlight, 2: Horn}.
	Functions map[int]string `yaml:"functions,omitempty" json:"functions,omitempty"`
	// Momentary lists functions that act only while held, such as a horn.
	Momentary []int `yaml:"momentary,omitempty" json:"momentary,omitempty"`
}

// IDFor returns the ID of a locomotive at an address.
func IDFor(address uint16, long bool) string {
	s := strconv.Itoa(int(address))
	if long && address <= traction.ShortAddressMax {
		return s + "l"
	}
	return s
}

// ParseID reads an ID, or a bare address, back into an address.
func ParseID(id string) (address uint16, long, ok bool) {
	s := strings.TrimSpace(strings.ToLower(id))
	if strings.HasSuffix(s, "l") {
		long = true
		s = strings.TrimSuffix(s, "l")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxAddress {
		return 0, false, false
	}
	if n > traction.ShortAddressMax {
		long = true
	}
	return uint16(n), long, true
}

// Normalize fills in what an entry may leave out: the long flag for high
// addresses, the ID, and a name.
func (e Entry) Normalize() Entry {
	e.Name = strings.TrimSpace(e.Name)
	if e.Address > traction.ShortAddressMax {
		e.Long = true
	}
	e.ID = IDFor(e.Address, e.Long)
	if e.Name == "" {
		e.Name = traction.AddressName(e.Address, e.Long)
	}
	for k, v := range e.Functions {
		e.Functions[k] = strings.TrimSpace(v)
		if e.Functions[k] == "" {
			delete(e.Functions, k)
		}
	}
	if len(e.Functions) == 0 {
		e.Functions = nil
	}
	sort.Ints(e.Momentary)
	return e
}

// Validate checks an entry.
func (e Entry) Validate() error {
	if e.Address < 1 || e.Address > MaxAddress {
		return invalid("the address must be between 1 and %d", MaxAddress)
	}
	if len(e.Name) > 64 {
		return invalid("the name is too long; keep it under 64 characters")
	}
	for k := range e.Functions {
		if k < 0 || k > MaxFunction {
			return invalid("there is no function F%d; functions run from F0 to F%d", k, MaxFunction)
		}
	}
	for _, k := range e.Momentary {
		if k < 0 || k > MaxFunction {
			return invalid("there is no function F%d; functions run from F0 to F%d", k, MaxFunction)
		}
	}
	return nil
}

// Train is a locomotive as the throttle sees it.
type Train struct {
	Entry
	// Roster is false for a train driven without a roster entry, found by
	// a search or named by an automation.
	Roster bool `json:"roster"`
	// Node is the train node on the bus, zero until found.
	Node openlcb.NodeID `json:"node,omitempty"`
	// Online is whether the train node is on the bus now.
	Online bool `json:"online"`
	// Controlled is whether Interlock holds the controller role.
	Controlled bool `json:"controlled"`
	// Controller is the node controlling the train when known.
	Controller openlcb.NodeID `json:"controller,omitempty"`
	// Speed is 0 to MaxSpeed; Direction is forward or reverse.
	Speed     float32 `json:"speed"`
	Direction string  `json:"direction"`
	// EStop is set after an emergency stop until a speed is set.
	EStop bool `json:"estop"`
	// On lists the functions that are on.
	On []int `json:"on"`
	// Known is true once the state was read back from the train.
	Known   bool      `json:"known"`
	Updated time.Time `json:"updated"`
	// Removed is set on the last update about a deleted train.
	Removed bool `json:"removed,omitempty"`
}

// Found is a train node a search turned up.
type Found struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Node    openlcb.NodeID `json:"node"`
	Address uint16         `json:"address"`
	Long    bool           `json:"long"`
	// Roster is whether the roster already has the locomotive.
	Roster bool `json:"roster"`
}

// Layout is what the well-known emergency events say about the whole
// layout.
type Layout struct {
	// PowerOff is set after Emergency Off, which command stations answer
	// by cutting track power, until Clear Emergency Off.
	PowerOff bool `json:"power_off"`
	// EmergencyStop is set after Emergency Stop until it is cleared.
	EmergencyStop bool      `json:"emergency_stop"`
	Updated       time.Time `json:"updated"`
}

// Command is what an automation asks of a train.
type Command struct {
	Address uint16 `json:"address"`
	Long    bool   `json:"long"`
	// Kind is speed, stop, estop or function.
	Kind string `json:"command"`
	// Speed and Direction for speed; an empty direction keeps the current one.
	Speed     float32 `json:"speed"`
	Direction string  `json:"direction"`
	// Function and On for function.
	Function int  `json:"function"`
	On       bool `json:"on"`
}

// Validate checks a command.
func (c Command) Validate() error {
	if c.Address < 1 || c.Address > MaxAddress {
		return invalid("the address must be between 1 and %d", MaxAddress)
	}
	switch c.Kind {
	case CommandSpeed:
		if c.Speed < 0 || c.Speed > MaxSpeed {
			return invalid("the speed must be between 0 and %d", MaxSpeed)
		}
		if c.Direction != "" && c.Direction != Forward && c.Direction != Reverse {
			return invalid("the direction must be forward or reverse")
		}
	case CommandStop, CommandEStop:
	case CommandFunction:
		if c.Function < 0 || c.Function > MaxFunction {
			return invalid("there is no function F%d; functions run from F0 to F%d", c.Function, MaxFunction)
		}
	default:
		return invalid("%q is not a train command; use speed, stop, estop or function", c.Kind)
	}
	return nil
}

// Describe reads like "Set locomotive 1234 to 30 forward".
func (c Command) Describe(name string) string {
	switch c.Kind {
	case CommandSpeed:
		s := "Set " + name + " to speed " + strconv.FormatFloat(float64(c.Speed), 'f', -1, 32)
		if c.Direction != "" {
			s += " " + c.Direction
		}
		return s
	case CommandStop:
		return "Stop " + name
	case CommandEStop:
		return "Emergency stop " + name
	case CommandFunction:
		if c.On {
			return fmt.Sprintf("Turn on F%d of %s", c.Function, name)
		}
		return fmt.Sprintf("Turn off F%d of %s", c.Function, name)
	}
	return c.Kind + " " + name
}
