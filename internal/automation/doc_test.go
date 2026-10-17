package automation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/train"

	"gopkg.in/yaml.v3"
)

func TestDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		text string
	}{
		{"3s", 3 * time.Second, "3s"},
		{"1m30s", 90 * time.Second, "1m30s"},
		{"250ms", 250 * time.Millisecond, "250ms"},
		{"2h", 2 * time.Hour, "2h"},
		{"61m", 61 * time.Minute, "1h1m"},
		{"1.5", 1500 * time.Millisecond, "1.5s"},
		{"10", 10 * time.Second, "10s"},
		{"0", 0, "0s"},
	}
	for _, c := range cases {
		d, err := ParseDuration(c.in)
		if err != nil {
			t.Errorf("ParseDuration(%q): %v", c.in, err)
			continue
		}
		if d.Duration() != c.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", c.in, d.Duration(), c.want)
		}
		if d.String() != c.text {
			t.Errorf("Duration(%v).String() = %q, want %q", c.want, d.String(), c.text)
		}
	}
	if _, err := ParseDuration("soon"); err == nil {
		t.Error("ParseDuration(soon) succeeded")
	}

	var v struct {
		A Duration `json:"a" yaml:"a"`
		B Duration `json:"b" yaml:"b"`
	}
	if err := json.Unmarshal([]byte(`{"a":"3s","b":2}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Duration() != 3*time.Second || v.B.Duration() != 2*time.Second {
		t.Errorf("JSON durations = %v, %v", v.A, v.B)
	}
	out, _ := json.Marshal(v)
	if string(out) != `{"a":"3s","b":"2s"}` {
		t.Errorf("JSON = %s", out)
	}
	if err := yaml.Unmarshal([]byte("a: 1m\nb: 5\n"), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Duration() != time.Minute || v.B.Duration() != 5*time.Second {
		t.Errorf("YAML durations = %v, %v", v.A, v.B)
	}
	if err := yaml.Unmarshal([]byte("a: [1]\n"), &v); err == nil {
		t.Error("YAML list accepted as a duration")
	}
}

func TestClock(t *testing.T) {
	for _, s := range []string{"07:30", "23:59:59", "00:00"} {
		if _, err := parseClock(s); err != nil {
			t.Errorf("parseClock(%q): %v", s, err)
		}
	}
	for _, s := range []string{"7:30", "24:00", "07:60", "noon", "07:30:00:00", ""} {
		if _, err := parseClock(s); err == nil {
			t.Errorf("parseClock(%q) succeeded", s)
		}
	}
	// Thursday 2026-10-15 10:00 local.
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.Local)
	cases := []struct {
		at   string
		days []string
		want time.Time
	}{
		{"10:30", nil, time.Date(2026, 10, 15, 10, 30, 0, 0, time.Local)},
		{"09:00", nil, time.Date(2026, 10, 16, 9, 0, 0, 0, time.Local)},
		{"10:00", nil, time.Date(2026, 10, 16, 10, 0, 0, 0, time.Local)},
		{"09:00", []string{"sat", "sun"}, time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local)},
		{"12:00", []string{"Thu"}, time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)},
		{"09:00", []string{"thu"}, time.Date(2026, 10, 22, 9, 0, 0, 0, time.Local)},
	}
	for _, c := range cases {
		got := nextAt(now, mustClock(c.at), c.days)
		if !got.Equal(c.want) {
			t.Errorf("nextAt(%s, %v) = %v, want %v", c.at, c.days, got, c.want)
		}
	}
	windows := []struct {
		after, before string
		days          []string
		want          bool
	}{
		{"", "", nil, true},
		{"06:00", "22:00", nil, true},
		{"11:00", "22:00", nil, false},
		{"22:00", "06:00", nil, false},
		{"22:00", "11:00", nil, true},
		{"", "10:00", nil, false},
		{"10:00", "", nil, true},
		{"", "", []string{"thu"}, true},
		{"", "", []string{"fri"}, false},
	}
	for _, w := range windows {
		if got := inWindow(now, w.after, w.before, w.days); got != w.want {
			t.Errorf("inWindow(%q, %q, %v) = %v", w.after, w.before, w.days, got)
		}
	}
}

func valid() Automation {
	return Automation{
		ID: "close_turnout_1", Name: "Close Turnout 1", Enabled: true,
		Triggers:   []Trigger{{Type: TriggerState, Entity: "block.t2", To: "occupied"}},
		Conditions: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "thrown"}},
		Actions:    []Action{{Type: ActionDelay, For: Duration(3 * time.Second)}, {Type: ActionCommand, Entity: "turnout.1", Command: "close"}},
	}
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Automation)
		path string
	}{
		{"no name", func(a *Automation) { a.Name = "" }, "name"},
		{"bad id", func(a *Automation) { a.ID = "Close Turnout" }, "id"},
		{"bad mode", func(a *Automation) { a.Mode = "sometimes" }, "mode"},
		{"no triggers", func(a *Automation) { a.Triggers = nil }, "triggers"},
		{"no actions", func(a *Automation) { a.Actions = nil }, "actions"},
		{"trigger type", func(a *Automation) { a.Triggers[0].Type = "when" }, "triggers[0].type"},
		{"state without entity", func(a *Automation) { a.Triggers[0].Entity = "" }, "triggers[0].entity"},
		{"state with event", func(a *Automation) { a.Triggers[0].Event = 1 }, "triggers[0].event"},
		{"event without event", func(a *Automation) { a.Triggers[0] = Trigger{Type: TriggerEvent} }, "triggers[0].event"},
		{"bad direction", func(a *Automation) {
			a.Triggers[0] = Trigger{Type: TriggerLocomotive, Entity: "reporter.1", Direction: "arrived"}
		}, "triggers[0].direction"},
		{"bad address type", func(a *Automation) {
			a.Triggers[0] = Trigger{Type: TriggerLocomotive, Entity: "reporter.1", Address: 3, AddressType: "tiny"}
		}, "triggers[0].address_type"},
		{"node to", func(a *Automation) { a.Triggers[0] = Trigger{Type: TriggerNode, To: "gone"} }, "triggers[0].to"},
		{"time at", func(a *Automation) { a.Triggers[0] = Trigger{Type: TriggerTime, At: "7am"} }, "triggers[0].at"},
		{"time weekday", func(a *Automation) {
			a.Triggers[0] = Trigger{Type: TriggerTime, At: "07:00", Weekdays: []string{"someday"}}
		}, "triggers[0].weekdays"},
		{"interval", func(a *Automation) { a.Triggers[0] = Trigger{Type: TriggerInterval} }, "triggers[0].every"},
		{"duplicate trigger ids", func(a *Automation) {
			a.Triggers[0].ID = "a"
			a.Triggers = append(a.Triggers, Trigger{ID: "a", Type: TriggerStart})
		}, "triggers[1].id"},
		{"condition type", func(a *Automation) { a.Conditions[0].Type = "maybe" }, "conditions[0].type"},
		{"condition state", func(a *Automation) { a.Conditions[0].State = "" }, "conditions[0].state"},
		{"empty time condition", func(a *Automation) { a.Conditions[0] = Condition{Type: ConditionTime} }, "conditions[0]"},
		{"not with two", func(a *Automation) {
			a.Conditions[0] = Condition{Type: ConditionNot, Conditions: []Condition{{Type: ConditionTime, After: "01:00"}, {Type: ConditionTime, After: "02:00"}}}
		}, "conditions[0].conditions"},
		{"nested condition", func(a *Automation) {
			a.Conditions[0] = Condition{Type: ConditionOr, Conditions: []Condition{{Type: ConditionState, Entity: "x"}}}
		}, "conditions[0].conditions[0].state"},
		{"action type", func(a *Automation) { a.Actions[0].Type = "do" }, "actions[0].type"},
		{"command without command", func(a *Automation) { a.Actions[1].Command = "" }, "actions[1].command"},
		{"delay without for", func(a *Automation) { a.Actions[0].For = 0 }, "actions[0].for"},
		{"wait without triggers", func(a *Automation) { a.Actions[0] = Action{Type: ActionWait} }, "actions[0].triggers"},
		{"wait for start", func(a *Automation) {
			a.Actions[0] = Action{Type: ActionWait, Triggers: []Trigger{{Type: TriggerStart}}}
		}, "actions[0].triggers[0].type"},
		{"if without branch", func(a *Automation) {
			a.Actions[0] = Action{Type: ActionIf, Conditions: []Condition{{Type: ConditionTime, After: "01:00"}}}
		}, "actions[0].then"},
		{"nested action", func(a *Automation) {
			a.Actions[0] = Action{Type: ActionIf, Conditions: []Condition{{Type: ConditionTime, After: "01:00"}}, Then: []Action{{Type: ActionLog}}}
		}, "actions[0].then[0].message"},
		{"choose without choices", func(a *Automation) { a.Actions[0] = Action{Type: ActionChoose} }, "actions[0].choices"},
		{"repeat two kinds", func(a *Automation) {
			a.Actions[0] = Action{Type: ActionRepeat, Count: 2, Until: []Condition{{Type: ConditionTime, After: "01:00"}}, Actions: []Action{{Type: ActionStop}}}
		}, "actions[0]"},
		{"repeat empty", func(a *Automation) { a.Actions[0] = Action{Type: ActionRepeat, Count: 2} }, "actions[0].actions"},
	}
	for _, c := range cases {
		a := valid()
		c.edit(&a)
		err := a.Validate()
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: err = %v, want an InvalidError", c.name, err)
			continue
		}
		if inv.Path != c.path {
			t.Errorf("%s: path = %q (%s), want %q", c.name, inv.Path, inv.Reason, c.path)
		}
	}
}

func TestNormalize(t *testing.T) {
	a := Automation{Name: "  Close Turnout 1 "}.Normalize()
	if a.ID != "close_turnout_1" || a.Name != "Close Turnout 1" || a.Mode != ModeSingle {
		t.Errorf("Normalize = %+v", a)
	}
	if (Automation{Max: 3}).MaxRuns() != 3 || (Automation{}).MaxRuns() != DefaultMax {
		t.Error("MaxRuns")
	}
}

func TestDefaultsWhenDecoding(t *testing.T) {
	var a Automation
	if err := json.Unmarshal([]byte(`{"name":"x","triggers":[{"type":"start"}],"actions":[{"type":"stop"}]}`), &a); err != nil {
		t.Fatal(err)
	}
	if !a.Enabled {
		t.Error("JSON: enabled should default to true")
	}
	if err := json.Unmarshal([]byte(`{"name":"x","enabled":false}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.Enabled {
		t.Error("JSON: enabled: false ignored")
	}
	if err := yaml.Unmarshal([]byte("name: y\ntriggers:\n  - type: start\n"), &a); err != nil {
		t.Fatal(err)
	}
	if !a.Enabled || a.Name != "y" {
		t.Errorf("YAML defaults: %+v", a)
	}
}

func TestDescribe(t *testing.T) {
	n := names{entity: func(id string) string {
		return map[string]string{"block.t2": "Track 2", "turnout.1": "Turnout 1", "reporter.t2": "Track 2 reporter"}[id]
	}}
	triggers := []struct {
		t    Trigger
		want string
	}{
		{Trigger{Type: TriggerState, Entity: "block.t2", To: "occupied"}, "When Track 2 becomes occupied"},
		{Trigger{Type: TriggerState, Entity: "block.t2", From: "clear", To: "occupied", For: Duration(5 * time.Second)}, "When Track 2 changes from clear to occupied for 5s"},
		{Trigger{Type: TriggerState, Entity: "block.t2", From: "clear"}, "When Track 2 leaves clear"},
		{Trigger{Type: TriggerState, Entity: "turnout.9"}, "When turnout.9 changes state"},
		{Trigger{Type: TriggerEvent, Event: 0x0501010122000001}, "When event 05.01.01.01.22.00.00.01 is received"},
		{Trigger{Type: TriggerLocomotive, Entity: "reporter.t2", Address: 1234, Direction: "entered"}, "When locomotive 1234 enters Track 2 reporter"},
		{Trigger{Type: TriggerLocomotive, Entity: "reporter.t2", Address: 3, AddressType: "short", Direction: "left"}, "When locomotive 3 (short) leaves Track 2 reporter"},
		{Trigger{Type: TriggerLocomotive, Entity: "reporter.t2"}, "When a locomotive enters or leaves Track 2 reporter"},
		{Trigger{Type: TriggerNode, To: "offline"}, "When any node goes offline"},
		{Trigger{Type: TriggerNode, Node: 0x050101012200, To: "online"}, "When 05.01.01.01.22.00 comes online"},
		{Trigger{Type: TriggerTime, At: "07:30", Weekdays: []string{"mon", "fri"}}, "At 07:30 on Mon, Fri"},
		{Trigger{Type: TriggerInterval, Every: Duration(10 * time.Minute)}, "Every 10m"},
		{Trigger{Type: TriggerStart}, "When Interlock starts"},
	}
	for _, c := range triggers {
		if got := describeTrigger(c.t, n); got != c.want {
			t.Errorf("describeTrigger = %q, want %q", got, c.want)
		}
	}
	conditions := []struct {
		c    Condition
		want string
	}{
		{Condition{Type: ConditionState, Entity: "turnout.1", State: "thrown"}, "Turnout 1 is thrown"},
		{Condition{Type: ConditionTime, After: "06:00", Before: "22:00"}, "Between 06:00 and 22:00"},
		{Condition{Type: ConditionTime, Weekdays: []string{"sat"}}, "Only on Sat"},
		{Condition{Type: ConditionTrigger, Trigger: "night"}, "Started by trigger night"},
		{Condition{Type: ConditionNot, Conditions: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "moving"}}}, "Not: Turnout 1 is moving"},
		{Condition{Type: ConditionOr, Conditions: make([]Condition, 2)}, "Any of 2 conditions"},
	}
	for _, c := range conditions {
		if got := describeCondition(c.c, n); got != c.want {
			t.Errorf("describeCondition = %q, want %q", got, c.want)
		}
	}
	actions := []struct {
		a    Action
		want string
	}{
		{Action{Type: ActionCommand, Entity: "turnout.1", Command: "close"}, "Close Turnout 1"},
		{Action{Type: ActionEvent, Event: 0x0501010122000001}, "Send event 05.01.01.01.22.00.00.01"},
		{Action{Type: ActionDelay, For: Duration(3 * time.Second)}, "Wait 3s"},
		{Action{Type: ActionWait, Triggers: []Trigger{{Type: TriggerState, Entity: "block.t2", To: "clear"}}, Timeout: Duration(10 * time.Second)}, "Wait until Track 2 becomes clear (up to 10s)"},
		{Action{Type: ActionIf, Conditions: []Condition{{Type: ConditionState, Entity: "turnout.1", State: "thrown"}}}, "If Turnout 1 is thrown"},
		{Action{Type: ActionChoose, Choices: make([]Choice, 3)}, "Choose between 3 options"},
		{Action{Type: ActionRepeat, Count: 3}, "Repeat 3 times"},
		{Action{Type: ActionRepeat, Until: []Condition{{Type: ConditionState, Entity: "block.t2", State: "clear"}}}, "Repeat until Track 2 is clear"},
		{Action{Type: ActionStop, Reason: "done"}, "Stop: done"},
		{Action{Type: ActionLog, Message: "hi"}, `Log "hi"`},
	}
	for _, c := range actions {
		if got := describeAction(c.a, n); got != c.want {
			t.Errorf("describeAction = %q, want %q", got, c.want)
		}
	}
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/automations.yaml"
	f, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.List()) != 0 {
		t.Fatal("a missing file should be empty")
	}
	a := valid()
	a.Mode = ModeRestart
	if err := f.Put(a); err != nil {
		t.Fatal(err)
	}
	b := valid()
	b.ID, b.Name, b.Enabled = "second", "Second", false
	if err := f.Put(b); err != nil {
		t.Fatal(err)
	}
	again, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list := again.List()
	if len(list) != 2 || list[0].ID != "close_turnout_1" || list[0].Mode != ModeRestart || list[1].ID != "second" || list[1].Enabled {
		t.Fatalf("reloaded: %+v", list)
	}
	if list[0].Actions[0].For.Duration() != 3*time.Second {
		t.Errorf("duration lost: %v", list[0].Actions[0].For)
	}
	if err := again.Remove("second"); err != nil {
		t.Fatal(err)
	}
	third, _ := OpenFile(path)
	if len(third.List()) != 1 {
		t.Fatal("remove did not persist")
	}

	// Hand-written files get defaults and strict checking.
	hand := dir + "/hand.yaml"
	writeFile(t, hand, `
automations:
  - name: Lights on
    triggers:
      - type: time
        at: "18:00"
    actions:
      - type: command
        entity: switch.lights
        command: "on"
      - type: delay
        for: 90
`)
	h, err := OpenFile(hand)
	if err != nil {
		t.Fatal(err)
	}
	got := h.List()[0]
	if got.ID != "lights_on" || !got.Enabled || got.Mode != ModeSingle || got.Actions[1].For.Duration() != 90*time.Second {
		t.Errorf("hand-written: %+v", got)
	}
	writeFile(t, hand, "# nothing yet\n")
	if empty, err := OpenFile(hand); err != nil || len(empty.List()) != 0 {
		t.Errorf("empty file: %v, %v", empty, err)
	}
	writeFile(t, hand, "automations:\n  - name: Typo\n    trigers: []\n    actions: []\n")
	if _, err := OpenFile(hand); err == nil || !strings.Contains(err.Error(), "trigers") {
		t.Errorf("unknown field: %v", err)
	}
	writeFile(t, hand, "automations:\n  - name: Broken\n    triggers:\n      - type: start\n    actions: []\n")
	if _, err := OpenFile(hand); err == nil || !strings.Contains(err.Error(), "Broken") {
		t.Errorf("invalid automation: %v", err)
	}
	writeFile(t, hand, "automations:\n  - id: a\n    name: A\n    triggers: [{type: start}]\n    actions: [{type: stop}]\n  - id: a\n    name: B\n    triggers: [{type: start}]\n    actions: [{type: stop}]\n")
	if _, err := OpenFile(hand); err == nil || !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("duplicate IDs: %v", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := writeText(path, body); err != nil {
		t.Fatal(err)
	}
}

func TestTrainActionValidation(t *testing.T) {
	base := Automation{Name: "t", Triggers: []Trigger{{Type: TriggerStart}}}
	cases := []struct {
		name   string
		action Action
		path   string
	}{
		{"speed ok", Action{Type: ActionTrain, Address: 1234, Command: "speed", Speed: 30, Direction: "forward"}, ""},
		{"stop ok", Action{Type: ActionTrain, Address: 3, AddressType: "short", Command: "stop"}, ""},
		{"function ok", Action{Type: ActionTrain, Address: 3, Command: "function", Function: 0, State: "off"}, ""},
		{"no address", Action{Type: ActionTrain, Command: "stop"}, "actions[0].address"},
		{"bad type", Action{Type: ActionTrain, Address: 300, AddressType: "short", Command: "stop"}, "actions[0].address_type"},
		{"bad command", Action{Type: ActionTrain, Address: 3, Command: "fly"}, "actions[0].command"},
		{"fast", Action{Type: ActionTrain, Address: 3, Command: "speed", Speed: 200}, "actions[0].speed"},
		{"sideways", Action{Type: ActionTrain, Address: 3, Command: "speed", Speed: 2, Direction: "up"}, "actions[0].direction"},
		{"F99", Action{Type: ActionTrain, Address: 3, Command: "function", Function: 99, State: "on"}, "actions[0].function"},
		{"no state", Action{Type: ActionTrain, Address: 3, Command: "function", Function: 1}, "actions[0].state"},
	}
	for _, c := range cases {
		doc := base
		doc.Actions = []Action{c.action}
		err := doc.Normalize().Validate()
		var inv *InvalidError
		switch {
		case c.path == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.path != "" && (!errors.As(err, &inv) || inv.Path != c.path):
			t.Errorf("%s: error = %v, want path %s", c.name, err, c.path)
		}
	}
	n := names{train: func(address uint16, long bool) string {
		if address == 1234 && long {
			return "Challenger"
		}
		return ""
	}}
	for _, c := range []struct {
		action Action
		want   string
	}{
		{Action{Type: ActionTrain, Address: 1234, Command: "speed", Speed: 30, Direction: "forward"}, "Set Challenger to speed 30 forward"},
		{Action{Type: ActionTrain, Address: 1234, Command: "speed", Speed: 0}, "Set Challenger to speed 0"},
		{Action{Type: ActionTrain, Address: 3, Command: "stop"}, "Stop locomotive 3 (short)"},
		{Action{Type: ActionTrain, Address: 3, AddressType: "long", Command: "estop"}, "Emergency stop locomotive 3 (long)"},
		{Action{Type: ActionTrain, Address: 1234, Command: "function", Function: 2, State: "off"}, "Turn off F2 of Challenger"},
	} {
		if got := describeAction(c.action, n); got != c.want {
			t.Errorf("describeAction = %q, want %q", got, c.want)
		}
	}
	_ = train.MaxSpeed
}
