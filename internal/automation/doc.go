// Package automation runs Home Assistant style automations over the
// entities and the bus: triggers start a run, conditions gate it, actions
// do things, and every run leaves a trace.
//
// An automation is a declarative document, JSON in the API and YAML in
// automations.yaml. There is no template language; a small expression
// language can be added to conditions later without changing the shape.
package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/internal/yamlstrict"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Modes say what a new trigger does while the automation is already
// running.
const (
	// ModeSingle ignores the new trigger.
	ModeSingle = "single"
	// ModeRestart stops the running run and starts over.
	ModeRestart = "restart"
	// ModeQueued runs again after the current run finishes.
	ModeQueued = "queued"
	// ModeParallel runs alongside.
	ModeParallel = "parallel"
)

// DefaultMax is how many runs queued and parallel modes allow before new
// triggers are dropped, when the automation does not say.
const DefaultMax = 10

// Trigger types.
const (
	TriggerState      = "state"      // an entity's state changed
	TriggerEvent      = "event"      // an event report was received
	TriggerLocomotive = "locomotive" // a reporter saw a locomotive arrive or leave
	TriggerNode       = "node"       // a node came online or went offline
	TriggerTime       = "time"       // a time of day
	TriggerInterval   = "interval"   // every so often
	TriggerStart      = "start"      // Interlock started
)

// Condition types.
const (
	ConditionState   = "state"
	ConditionTime    = "time"
	ConditionTrigger = "trigger" // which trigger started the run
	ConditionAnd     = "and"
	ConditionOr      = "or"
	ConditionNot     = "not"
)

// Action types.
const (
	ActionCommand = "command"
	ActionTrain   = "train"
	ActionEvent   = "event"
	ActionDelay   = "delay"
	ActionWait    = "wait"
	ActionIf      = "if"
	ActionChoose  = "choose"
	ActionRepeat  = "repeat"
	ActionStop    = "stop"
	ActionLog     = "log"
)

// Errors.
var (
	ErrNotFound   = errors.New("automation: no such automation")
	ErrExists     = errors.New("automation: an automation with that ID exists")
	ErrNotRunning = errors.New("automation: nothing is running")
)

// InvalidError says what is wrong with a document and where.
type InvalidError struct {
	// Path points at the field: "triggers[0].entity", "actions[2].for".
	Path   string
	Reason string
}

func (e *InvalidError) Error() string {
	if e.Path == "" {
		return "automation: " + e.Reason
	}
	return "automation: " + e.Path + ": " + e.Reason
}

func invalid(path, format string, args ...any) error {
	return &InvalidError{Path: path, Reason: fmt.Sprintf(format, args...)}
}

// Automation is one automation as written.
type Automation struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	// Mode is one of the Mode constants; single when empty.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// Max limits queued and parallel runs; DefaultMax when zero.
	Max        int         `json:"max,omitempty" yaml:"max,omitempty"`
	Triggers   []Trigger   `json:"triggers" yaml:"triggers"`
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Actions    []Action    `json:"actions" yaml:"actions"`
}

// Trigger starts a run. Which fields matter depends on Type.
type Trigger struct {
	// ID names the trigger for a "trigger" condition and in traces.
	ID   string `json:"id,omitempty" yaml:"id,omitempty"`
	Type string `json:"type" yaml:"type"`
	// Entity is the entity watched by state and locomotive triggers.
	Entity string `json:"entity,omitempty" yaml:"entity,omitempty"`
	// From and To narrow a state trigger to a transition; either may be
	// empty. To also says "online" or "offline" for a node trigger.
	From string `json:"from,omitempty" yaml:"from,omitempty"`
	To   string `json:"to,omitempty" yaml:"to,omitempty"`
	// For makes a state trigger wait until the state has held this long.
	For Duration `json:"for,omitempty" yaml:"for,omitempty"`
	// Event is the event ID an event trigger listens for.
	Event openlcb.EventID `json:"event,omitempty" yaml:"event,omitempty"`
	// Address narrows a locomotive trigger to one DCC address; zero means
	// any. AddressType is long (default), short or consist.
	Address     uint16 `json:"address,omitempty" yaml:"address,omitempty"`
	AddressType string `json:"address_type,omitempty" yaml:"address_type,omitempty"`
	// Direction is "entered" or "left" for a locomotive trigger; empty
	// means either.
	Direction string `json:"direction,omitempty" yaml:"direction,omitempty"`
	// Node is the node a node trigger watches; zero means any node.
	Node openlcb.NodeID `json:"node,omitempty" yaml:"node,omitempty"`
	// At is the time of day for a time trigger, "HH:MM" or "HH:MM:SS".
	At string `json:"at,omitempty" yaml:"at,omitempty"`
	// Weekdays limits a time trigger: mon, tue, wed, thu, fri, sat, sun.
	Weekdays []string `json:"weekdays,omitempty" yaml:"weekdays,omitempty"`
	// Every is the period of an interval trigger.
	Every Duration `json:"every,omitempty" yaml:"every,omitempty"`
}

// Condition gates a run or an action.
type Condition struct {
	Type string `json:"type" yaml:"type"`
	// Entity and State for a state condition.
	Entity string `json:"entity,omitempty" yaml:"entity,omitempty"`
	State  string `json:"state,omitempty" yaml:"state,omitempty"`
	// After, Before and Weekdays for a time condition. The window wraps
	// past midnight when After is later than Before.
	After    string   `json:"after,omitempty" yaml:"after,omitempty"`
	Before   string   `json:"before,omitempty" yaml:"before,omitempty"`
	Weekdays []string `json:"weekdays,omitempty" yaml:"weekdays,omitempty"`
	// Trigger is the ID of the trigger a trigger condition requires.
	Trigger string `json:"trigger,omitempty" yaml:"trigger,omitempty"`
	// Conditions are the operands of and, or and not.
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// Action is one step of a run.
type Action struct {
	Type string `json:"type" yaml:"type"`
	// Disabled actions are skipped, which is handy while debugging.
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	// Entity and Command for a command action.
	Entity  string `json:"entity,omitempty" yaml:"entity,omitempty"`
	Command string `json:"command,omitempty" yaml:"command,omitempty"`
	// Address, AddressType, Speed, Direction, Function and State for a
	// train action, whose Command is speed, stop, estop or function.
	Address     uint16  `json:"address,omitempty" yaml:"address,omitempty"`
	AddressType string  `json:"address_type,omitempty" yaml:"address_type,omitempty"`
	Speed       float32 `json:"speed,omitempty" yaml:"speed,omitempty"`
	Direction   string  `json:"direction,omitempty" yaml:"direction,omitempty"`
	Function    int     `json:"function,omitempty" yaml:"function,omitempty"`
	State       string  `json:"state,omitempty" yaml:"state,omitempty"`
	// Event for an event action.
	Event openlcb.EventID `json:"event,omitempty" yaml:"event,omitempty"`
	// For is how long a delay waits.
	For Duration `json:"for,omitempty" yaml:"for,omitempty"`
	// Triggers and Timeout for a wait action. The run goes on after a
	// timeout unless StopOnTimeout is set.
	Triggers      []Trigger `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	Timeout       Duration  `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	StopOnTimeout bool      `json:"stop_on_timeout,omitempty" yaml:"stop_on_timeout,omitempty"`
	// Conditions, Then and Else for an if action.
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Then       []Action    `json:"then,omitempty" yaml:"then,omitempty"`
	Else       []Action    `json:"else,omitempty" yaml:"else,omitempty"`
	// Choices and Default for a choose action: the first choice whose
	// conditions hold runs.
	Choices []Choice `json:"choices,omitempty" yaml:"choices,omitempty"`
	Default []Action `json:"default,omitempty" yaml:"default,omitempty"`
	// Count, While or Until, and Actions for a repeat action.
	Count   int         `json:"count,omitempty" yaml:"count,omitempty"`
	While   []Condition `json:"while,omitempty" yaml:"while,omitempty"`
	Until   []Condition `json:"until,omitempty" yaml:"until,omitempty"`
	Actions []Action    `json:"actions,omitempty" yaml:"actions,omitempty"`
	// Reason for a stop action; Error marks the run as failed.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
	Error  bool   `json:"error,omitempty" yaml:"error,omitempty"`
	// Message for a log action.
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

// trainCommand is the train action as the train manager takes it.
func (a Action) trainCommand() train.Command {
	return train.Command{
		Address:   a.Address,
		Long:      a.AddressType == "long" || a.Address > traction.ShortAddressMax,
		Kind:      a.Command,
		Speed:     a.Speed,
		Direction: a.Direction,
		Function:  a.Function,
		On:        a.State == "on",
	}
}

// Choice is one branch of a choose action.
type Choice struct {
	Name       string      `json:"name,omitempty" yaml:"name,omitempty"`
	Conditions []Condition `json:"conditions" yaml:"conditions"`
	Actions    []Action    `json:"actions" yaml:"actions"`
}

// UnmarshalJSON applies the defaults a document may leave out.
func (a *Automation) UnmarshalJSON(data []byte) error {
	type plain Automation
	p := plain{Enabled: true}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*a = Automation(p)
	return nil
}

// UnmarshalYAML applies the defaults a document may leave out.
func (a *Automation) UnmarshalYAML(node *yaml.Node) error {
	type plain Automation
	p := plain{Enabled: true}
	if err := yamlstrict.Check(node, reflect.TypeOf(p)); err != nil {
		return err
	}
	if err := node.Decode(&p); err != nil {
		return err
	}
	*a = Automation(p)
	return nil
}

// Normalize trims text, fills the ID from the name and the mode's
// default, and returns the result.
func (a Automation) Normalize() Automation {
	a.Name = strings.TrimSpace(a.Name)
	a.Description = strings.TrimSpace(a.Description)
	a.ID = strings.TrimSpace(a.ID)
	if a.ID == "" {
		a.ID = entity.Slug(a.Name)
	}
	if a.Mode == "" {
		a.Mode = ModeSingle
	}
	return a
}

// MaxRuns is the queue or parallel limit in force.
func (a Automation) MaxRuns() int {
	if a.Max > 0 {
		return a.Max
	}
	return DefaultMax
}

var (
	modes          = []string{ModeSingle, ModeRestart, ModeQueued, ModeParallel}
	triggerTypes   = []string{TriggerState, TriggerEvent, TriggerLocomotive, TriggerNode, TriggerTime, TriggerInterval, TriggerStart}
	conditionTypes = []string{ConditionState, ConditionTime, ConditionTrigger, ConditionAnd, ConditionOr, ConditionNot}
	actionTypes    = []string{ActionCommand, ActionTrain, ActionEvent, ActionDelay, ActionWait, ActionIf, ActionChoose, ActionRepeat, ActionStop, ActionLog}
	weekdayNames   = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	addressTypes   = []string{"", "long", "short", "consist"}
)

// Validate checks the document's shape. It does not check that entities
// exist, since they may appear later; the engine reports those as
// problems instead.
func (a Automation) Validate() error {
	if a.ID == "" {
		return invalid("id", "the automation needs an ID")
	}
	if a.ID != entity.Slug(a.ID) {
		return invalid("id", "%q must be lowercase letters, digits and underscores", a.ID)
	}
	if a.Name == "" {
		return invalid("name", "the automation needs a name")
	}
	if a.Mode != "" && !slices.Contains(modes, a.Mode) {
		return invalid("mode", "%q is not a mode; use single, restart, queued or parallel", a.Mode)
	}
	if a.Max < 0 {
		return invalid("max", "must not be negative")
	}
	if len(a.Triggers) == 0 {
		return invalid("triggers", "the automation needs at least one trigger")
	}
	seen := make(map[string]bool)
	for i, t := range a.Triggers {
		path := "triggers[" + strconv.Itoa(i) + "]"
		if err := t.validate(path); err != nil {
			return err
		}
		if t.ID != "" {
			if seen[t.ID] {
				return invalid(path+".id", "two triggers are named %q", t.ID)
			}
			seen[t.ID] = true
		}
	}
	if err := validateConditions(a.Conditions, "conditions"); err != nil {
		return err
	}
	if len(a.Actions) == 0 {
		return invalid("actions", "the automation needs at least one action")
	}
	return validateActions(a.Actions, "actions", 0)
}

// validate checks a trigger; inWait is set for wait action triggers,
// which cannot use start.
func (t Trigger) validate(path string) error {
	if !slices.Contains(triggerTypes, t.Type) {
		return invalid(path+".type", "%q is not a trigger type; use %s", t.Type, strings.Join(triggerTypes, ", "))
	}
	allowed := map[string][]string{
		TriggerState:      {"entity", "from", "to", "for"},
		TriggerEvent:      {"event"},
		TriggerLocomotive: {"entity", "address", "address_type", "direction"},
		TriggerNode:       {"node", "to"},
		TriggerTime:       {"at", "weekdays"},
		TriggerInterval:   {"every"},
		TriggerStart:      {},
	}[t.Type]
	for _, f := range t.fieldsSet() {
		if !slices.Contains(allowed, f) {
			return invalid(path+"."+f, "a %s trigger does not use %s", t.Type, f)
		}
	}
	switch t.Type {
	case TriggerState:
		if t.Entity == "" {
			return invalid(path+".entity", "say which entity to watch")
		}
		if t.For < 0 {
			return invalid(path+".for", "must not be negative")
		}
	case TriggerEvent:
		if t.Event == 0 {
			return invalid(path+".event", "say which event to listen for")
		}
	case TriggerLocomotive:
		if t.Entity == "" {
			return invalid(path+".entity", "say which reporter to watch")
		}
		if !slices.Contains(addressTypes, t.AddressType) {
			return invalid(path+".address_type", "%q must be long, short or consist", t.AddressType)
		}
		if t.Direction != "" && t.Direction != "entered" && t.Direction != "left" {
			return invalid(path+".direction", "%q must be entered or left", t.Direction)
		}
	case TriggerNode:
		if t.To != "" && t.To != "online" && t.To != "offline" {
			return invalid(path+".to", "%q must be online or offline", t.To)
		}
	case TriggerTime:
		if _, err := parseClock(t.At); err != nil {
			return invalid(path+".at", "%v", err)
		}
		if err := validateWeekdays(t.Weekdays); err != nil {
			return invalid(path+".weekdays", "%v", err)
		}
	case TriggerInterval:
		if t.Every <= 0 {
			return invalid(path+".every", "say how often, such as 10m")
		}
	}
	return nil
}

// fieldsSet lists the optional fields a trigger has values in.
func (t Trigger) fieldsSet() []string {
	var set []string
	add := func(name string, ok bool) {
		if ok {
			set = append(set, name)
		}
	}
	add("entity", t.Entity != "")
	add("from", t.From != "")
	add("to", t.To != "")
	add("for", t.For != 0)
	add("event", t.Event != 0)
	add("address", t.Address != 0)
	add("address_type", t.AddressType != "")
	add("direction", t.Direction != "")
	add("node", t.Node != 0)
	add("at", t.At != "")
	add("weekdays", len(t.Weekdays) > 0)
	add("every", t.Every != 0)
	return set
}

func validateConditions(conds []Condition, path string) error {
	for i, c := range conds {
		if err := c.validate(path + "[" + strconv.Itoa(i) + "]"); err != nil {
			return err
		}
	}
	return nil
}

func (c Condition) validate(path string) error {
	if !slices.Contains(conditionTypes, c.Type) {
		return invalid(path+".type", "%q is not a condition type; use %s", c.Type, strings.Join(conditionTypes, ", "))
	}
	switch c.Type {
	case ConditionState:
		if c.Entity == "" {
			return invalid(path+".entity", "say which entity to check")
		}
		if c.State == "" {
			return invalid(path+".state", "say which state it must be in")
		}
	case ConditionTime:
		if c.After == "" && c.Before == "" && len(c.Weekdays) == 0 {
			return invalid(path, "a time condition needs after, before or weekdays")
		}
		if c.After != "" {
			if _, err := parseClock(c.After); err != nil {
				return invalid(path+".after", "%v", err)
			}
		}
		if c.Before != "" {
			if _, err := parseClock(c.Before); err != nil {
				return invalid(path+".before", "%v", err)
			}
		}
		if err := validateWeekdays(c.Weekdays); err != nil {
			return invalid(path+".weekdays", "%v", err)
		}
	case ConditionTrigger:
		if c.Trigger == "" {
			return invalid(path+".trigger", "name the trigger")
		}
	case ConditionAnd, ConditionOr:
		if len(c.Conditions) == 0 {
			return invalid(path+".conditions", "%s needs conditions to combine", c.Type)
		}
		return validateConditions(c.Conditions, path+".conditions")
	case ConditionNot:
		if len(c.Conditions) != 1 {
			return invalid(path+".conditions", "not takes exactly one condition")
		}
		return validateConditions(c.Conditions, path+".conditions")
	}
	return nil
}

// maxNesting keeps a document from nesting blocks without end.
const maxNesting = 8

func validateActions(actions []Action, path string, depth int) error {
	if depth > maxNesting {
		return invalid(path, "actions nest too deep")
	}
	for i, a := range actions {
		if err := a.validate(path+"["+strconv.Itoa(i)+"]", depth); err != nil {
			return err
		}
	}
	return nil
}

func (a Action) validate(path string, depth int) error {
	if !slices.Contains(actionTypes, a.Type) {
		return invalid(path+".type", "%q is not an action type; use %s", a.Type, strings.Join(actionTypes, ", "))
	}
	switch a.Type {
	case ActionCommand:
		if a.Entity == "" {
			return invalid(path+".entity", "say which entity to command")
		}
		if a.Command == "" {
			return invalid(path+".command", "say which command to send")
		}
	case ActionTrain:
		if a.Address < 1 || a.Address > train.MaxAddress {
			return invalid(path+".address", "say which locomotive, by DCC address 1 to %d", train.MaxAddress)
		}
		if a.AddressType != "" && a.AddressType != "long" && a.AddressType != "short" {
			return invalid(path+".address_type", "%q must be long or short", a.AddressType)
		}
		if a.AddressType == "short" && a.Address > traction.ShortAddressMax {
			return invalid(path+".address_type", "a short address is 127 or less")
		}
		switch a.Command {
		case train.CommandSpeed:
			if a.Speed < 0 || a.Speed > train.MaxSpeed {
				return invalid(path+".speed", "the speed runs from 0 to %d", train.MaxSpeed)
			}
			if a.Direction != "" && a.Direction != train.Forward && a.Direction != train.Reverse {
				return invalid(path+".direction", "%q must be forward or reverse", a.Direction)
			}
		case train.CommandStop, train.CommandEStop:
		case train.CommandFunction:
			if a.Function < 0 || a.Function > train.MaxFunction {
				return invalid(path+".function", "functions run from F0 to F%d", train.MaxFunction)
			}
			if a.State != "on" && a.State != "off" {
				return invalid(path+".state", "say whether the function goes on or off")
			}
		default:
			return invalid(path+".command", "%q is not a train command; use speed, stop, estop or function", a.Command)
		}
	case ActionEvent:
		if a.Event == 0 {
			return invalid(path+".event", "say which event to send")
		}
	case ActionDelay:
		if a.For <= 0 {
			return invalid(path+".for", "say how long to wait, such as 3s")
		}
	case ActionWait:
		if len(a.Triggers) == 0 {
			return invalid(path+".triggers", "say what to wait for")
		}
		for i, t := range a.Triggers {
			tp := path + ".triggers[" + strconv.Itoa(i) + "]"
			if err := t.validate(tp); err != nil {
				return err
			}
			if t.Type == TriggerStart {
				return invalid(tp+".type", "a wait cannot wait for Interlock to start")
			}
		}
		if a.Timeout < 0 {
			return invalid(path+".timeout", "must not be negative")
		}
	case ActionIf:
		if len(a.Conditions) == 0 {
			return invalid(path+".conditions", "an if needs conditions")
		}
		if err := validateConditions(a.Conditions, path+".conditions"); err != nil {
			return err
		}
		if len(a.Then) == 0 && len(a.Else) == 0 {
			return invalid(path+".then", "an if needs something to do")
		}
		if err := validateActions(a.Then, path+".then", depth+1); err != nil {
			return err
		}
		return validateActions(a.Else, path+".else", depth+1)
	case ActionChoose:
		if len(a.Choices) == 0 {
			return invalid(path+".choices", "a choose needs choices")
		}
		for i, ch := range a.Choices {
			cp := path + ".choices[" + strconv.Itoa(i) + "]"
			if len(ch.Conditions) == 0 {
				return invalid(cp+".conditions", "a choice needs conditions")
			}
			if err := validateConditions(ch.Conditions, cp+".conditions"); err != nil {
				return err
			}
			if len(ch.Actions) == 0 {
				return invalid(cp+".actions", "a choice needs actions")
			}
			if err := validateActions(ch.Actions, cp+".actions", depth+1); err != nil {
				return err
			}
		}
		return validateActions(a.Default, path+".default", depth+1)
	case ActionRepeat:
		kinds := 0
		if a.Count > 0 {
			kinds++
		}
		if len(a.While) > 0 {
			kinds++
		}
		if len(a.Until) > 0 {
			kinds++
		}
		if kinds != 1 {
			return invalid(path, "a repeat needs exactly one of count, while or until")
		}
		if err := validateConditions(a.While, path+".while"); err != nil {
			return err
		}
		if err := validateConditions(a.Until, path+".until"); err != nil {
			return err
		}
		if len(a.Actions) == 0 {
			return invalid(path+".actions", "a repeat needs actions to repeat")
		}
		return validateActions(a.Actions, path+".actions", depth+1)
	case ActionStop:
	case ActionLog:
		if a.Message == "" {
			return invalid(path+".message", "say what to log")
		}
	}
	return nil
}

func validateWeekdays(days []string) error {
	for _, d := range days {
		if !slices.Contains(weekdayNames, strings.ToLower(d)) {
			return fmt.Errorf("%q is not a weekday; use %s", d, strings.Join(weekdayNames, ", "))
		}
	}
	return nil
}
