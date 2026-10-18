// Package train is the throttle side of Train Control. It keeps a roster
// of locomotives in trains.yaml, finds the train node for each one with
// the Train Search protocol (having the command station create it when
// it has to), takes the controller role, and sends speed, direction and
// function commands. What it set, and what it read back from the train,
// is the train's state; the throttle page and the automations work from
// it. The well-known emergency events, which stop every train or cut
// track power, are handled here too.
//
// A roster entry is a locomotive. It can have several decoders, one of
// them installed, each with the CV values last read from or written to
// it (kept by a CVStore). Locomotives may share an address; to the
// command station they are then one train, and they share its state.
package train

import (
	"errors"
	"fmt"
	"regexp"
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
	ErrNoNode   = errors.New("train: no command station answered for the locomotive")
	ErrRefused  = errors.New("train: the train refused to be controlled")
	// ErrProfileExists means a decoder already keeps a decoder profile by
	// that name.
	ErrProfileExists = errors.New("train: a decoder profile by that name is kept already")
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
	// ID names the entry in the API and the web interface. New entries
	// get one made from their name; entries from before locomotives could
	// share an address have their address as their ID: "1234", "3", or
	// "3l" for a long address of 127 or less.
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Address uint16 `yaml:"address" json:"address"`
	// Long is the address kind; addresses above 127 are always long.
	Long bool `yaml:"long,omitempty" json:"long"`
	// Functions labels the function keys: {0: Headlight, 2: Horn}.
	Functions map[int]string `yaml:"functions,omitempty" json:"functions,omitempty"`
	// Momentary lists functions that act only while held, such as a horn.
	Momentary []int `yaml:"momentary,omitempty" json:"momentary,omitempty"`

	// What the locomotive is, for the roster.
	RoadName     string `yaml:"road_name,omitempty" json:"road_name,omitempty"`
	RoadNumber   string `yaml:"road_number,omitempty" json:"road_number,omitempty"`
	Manufacturer string `yaml:"manufacturer,omitempty" json:"manufacturer,omitempty"`
	Model        string `yaml:"model,omitempty" json:"model,omitempty"`
	Owner        string `yaml:"owner,omitempty" json:"owner,omitempty"`
	Comment      string `yaml:"comment,omitempty" json:"comment,omitempty"`

	// Decoders are the decoders the locomotive has, fitted or not.
	Decoders []Decoder `yaml:"decoders,omitempty" json:"decoders,omitempty"`
	// Installed is the ID of the decoder fitted now; empty when none is.
	// The address and function labels above are that decoder's.
	Installed string `yaml:"installed,omitempty" json:"installed,omitempty"`
}

// Decoder is one of a locomotive's decoders. Its settings come from a
// JMRI decoder definition; its CV values are kept in a CVStore under the
// decoder's ID, which is unique in the roster.
type Decoder struct {
	ID string `yaml:"id" json:"id"`
	// Name tells a locomotive's decoders apart; the model when empty.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// File, Family and Model name the decoder definition.
	File   string `yaml:"file,omitempty" json:"file,omitempty"`
	Family string `yaml:"family,omitempty" json:"family,omitempty"`
	Model  string `yaml:"model,omitempty" json:"model,omitempty"`
	// Identity is what the decoder reported about itself.
	Identity *Identity `yaml:"identity,omitempty" json:"identity,omitempty"`
	// Address, Long, Functions and Momentary are what a decoder that is
	// not installed brings back to the locomotive when it is installed
	// again. An address of zero keeps the locomotive's.
	Address   uint16         `yaml:"address,omitempty" json:"address,omitempty"`
	Long      bool           `yaml:"long,omitempty" json:"long,omitempty"`
	Functions map[int]string `yaml:"functions,omitempty" json:"functions,omitempty"`
	Momentary []int          `yaml:"momentary,omitempty" json:"momentary,omitempty"`
}

// Title is how a decoder is called: its name, else its model.
func (d Decoder) Title() string {
	switch {
	case d.Name != "":
		return d.Name
	case d.Model != "":
		return d.Model
	}
	return "decoder " + d.ID
}

// Identity is what a decoder says about itself: its manufacturer (CV 8),
// its version (CV 7) and, for makers that publish one, a product ID.
type Identity struct {
	Manufacturer int     `yaml:"manufacturer" json:"manufacturer"`
	Version      int     `yaml:"version" json:"version"`
	ProductID    *uint32 `yaml:"product_id,omitempty" json:"product_id,omitempty"`
}

// Decoder returns one of the entry's decoders by ID.
func (e Entry) Decoder(id string) (Decoder, bool) {
	for _, d := range e.Decoders {
		if d.ID == id {
			return d, true
		}
	}
	return Decoder{}, false
}

// key is the address the entry drives, as a train key.
func (e Entry) key() string { return IDFor(e.Address, e.Long) }

// clone copies an entry deeply enough to change it.
func (e Entry) clone() Entry {
	e.Functions = cloneLabels(e.Functions)
	e.Momentary = append([]int(nil), e.Momentary...)
	ds := make([]Decoder, len(e.Decoders))
	for i, d := range e.Decoders {
		d.Functions = cloneLabels(d.Functions)
		d.Momentary = append([]int(nil), d.Momentary...)
		if d.Identity != nil {
			id := *d.Identity
			d.Identity = &id
		}
		ds[i] = d
	}
	if e.Decoders != nil {
		e.Decoders = ds
	}
	return e
}

func cloneLabels(m map[int]string) map[int]string {
	if m == nil {
		return nil
	}
	out := make(map[int]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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

// Normalize fills in what an entry may leave out (the long flag for high
// addresses, a name) and tidies the rest. It leaves the ID alone.
func (e Entry) Normalize() Entry {
	e.Name = strings.TrimSpace(e.Name)
	if e.Address > traction.ShortAddressMax {
		e.Long = true
	}
	if e.Name == "" {
		e.Name = traction.AddressName(e.Address, e.Long)
	}
	e.Functions, e.Momentary = tidyLabels(e.Functions, e.Momentary)
	for _, p := range []*string{&e.RoadName, &e.RoadNumber, &e.Manufacturer, &e.Model, &e.Owner} {
		*p = strings.TrimSpace(*p)
	}
	e.Comment = strings.TrimSpace(e.Comment)
	for i := range e.Decoders {
		d := &e.Decoders[i]
		d.Name = strings.TrimSpace(d.Name)
		if d.Address > traction.ShortAddressMax {
			d.Long = true
		}
		d.Functions, d.Momentary = tidyLabels(d.Functions, d.Momentary)
	}
	if len(e.Decoders) == 0 {
		e.Decoders = nil
	}
	return e
}

func tidyLabels(fns map[int]string, momentary []int) (map[int]string, []int) {
	for k, v := range fns {
		fns[k] = strings.TrimSpace(v)
		if fns[k] == "" {
			delete(fns, k)
		}
	}
	if len(fns) == 0 {
		fns = nil
	}
	sort.Ints(momentary)
	if len(momentary) == 0 {
		momentary = nil
	}
	return fns, momentary
}

// Validate checks an entry on its own; the manager and the file check
// that IDs are unique.
func (e Entry) Validate() error {
	if !validID(e.ID) {
		return invalid("%q cannot be a train ID; use letters, digits and dashes", e.ID)
	}
	if _, _, ok := ParseID(e.ID); ok && e.ID != e.key() {
		return invalid("the ID %s is an address, but not this locomotive's", e.ID)
	}
	if e.Address < 1 || e.Address > MaxAddress {
		return invalid("the address must be between 1 and %d", MaxAddress)
	}
	if len(e.Name) > 64 {
		return invalid("the name is too long; keep it under 64 characters")
	}
	if err := validLabels(e.Functions, e.Momentary); err != nil {
		return err
	}
	for _, f := range []string{e.RoadName, e.RoadNumber, e.Manufacturer, e.Model, e.Owner} {
		if len(f) > 100 {
			return invalid("%q is too long; keep it under 100 characters", f[:20]+"…")
		}
	}
	if len(e.Comment) > 4000 {
		return invalid("the comment is too long; keep it under 4000 characters")
	}
	seen := make(map[string]bool, len(e.Decoders))
	for _, d := range e.Decoders {
		if !validDecoderID(d.ID) {
			return invalid("%q cannot be a decoder ID; use letters, digits and dashes", d.ID)
		}
		if seen[d.ID] {
			return invalid("two decoders have the ID %s", d.ID)
		}
		seen[d.ID] = true
		if len(d.Name) > 64 {
			return invalid("the decoder name %q is too long; keep it under 64 characters", d.Name)
		}
		if d.File != "" && (strings.ContainsAny(d.File, `/\`) || !strings.HasSuffix(d.File, ".xml")) {
			return invalid("%q is not a decoder definition file", d.File)
		}
		if d.Address > MaxAddress {
			return invalid("the address must be between 1 and %d", MaxAddress)
		}
		if err := validLabels(d.Functions, d.Momentary); err != nil {
			return err
		}
	}
	if e.Installed != "" && !seen[e.Installed] {
		return invalid("the installed decoder %s is not one of the locomotive's", e.Installed)
	}
	return nil
}

func validLabels(fns map[int]string, momentary []int) error {
	for k := range fns {
		if k < 0 || k > MaxFunction {
			return invalid("there is no function F%d; functions run from F0 to F%d", k, MaxFunction)
		}
	}
	for _, k := range momentary {
		if k < 0 || k > MaxFunction {
			return invalid("there is no function F%d; functions run from F0 to F%d", k, MaxFunction)
		}
	}
	return nil
}

var (
	idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	// reservedNames are file names Windows refuses; decoder IDs name files.
	reservedNames = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com\d|lpt\d)$`)
)

func validID(id string) bool { return idPattern.MatchString(id) }

func validDecoderID(id string) bool { return validID(id) && !reservedNames.MatchString(id) }

// slug makes an ID from a name: lower-case letters and digits, with
// dashes for whatever lies between them.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// uniqueID returns base, or base-2, base-3 and so on, whichever is not
// taken.
func uniqueID(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		if id := base + "-" + strconv.Itoa(i); !taken(id) {
			return id
		}
	}
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
	// ID is the roster entry's when the roster has the locomotive, else
	// its address.
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
