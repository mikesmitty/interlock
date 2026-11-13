package profile

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

const nodeID = openlcb.NodeID(0x0A0000000042)

// outpostValues builds the values the event table would hold for an
// outpost with default event IDs: node ID then an index, servo events
// 0x00-0x13, occupied and clear 0x20-0x27, reporters the next node IDs,
// sixteen aspect events per signal head from 0x100, every head disabled,
// and the signal test's start and stop at 0x200 and 0x201.
func outpostValues(t *testing.T) (*cdi.CDI, map[string]string) {
	t.Helper()
	doc, err := cdi.Parse(sim.OutpostCDI)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	servo, track, aspect, own := 0, 0, 0, 0
	for _, v := range doc.Variables() {
		path := strings.Join(v.Path, "/")
		switch {
		case strings.HasPrefix(path, "Switch Machines/"):
			switch {
			case v.Element.Kind == cdi.KindEventID:
				index := []string{"Throw Command Event", "Close Command Event", "Thrown Feedback Event", "Closed Feedback Event", "Center Command Event"}
				for i, name := range index {
					if name == v.Element.Name {
						values[v.Key()] = nodeID.Event(uint16(servo*5 + i)).String()
					}
				}
				if v.Element.Name == "Center Command Event" {
					servo++
				}
			case v.Element.Name == "Enabled":
				values[v.Key()] = "1"
			default:
				values[v.Key()] = v.Element.Default
			}
		case strings.HasPrefix(path, "RailCom Detectors/"):
			switch v.Element.Name {
			case "Occupied Event":
				values[v.Key()] = nodeID.Event(uint16(0x20 + 2*track)).String()
			case "Clear Event":
				values[v.Key()] = nodeID.Event(uint16(0x21 + 2*track)).String()
			case "Reporter ID":
				values[v.Key()] = (nodeID + 1 + openlcb.NodeID(track)).Event(0).String()
				track++
			default:
				values[v.Key()] = v.Element.Default
			}
		case path == "Signal Heads/Signal Test/Start Event":
			values[v.Key()] = nodeID.Event(0x200).String()
		case path == "Signal Heads/Signal Test/Stop Event":
			values[v.Key()] = nodeID.Event(0x201).String()
		case strings.HasPrefix(path, "Signal Heads/") && strings.HasSuffix(v.Path[1], " Outputs"):
			// Eight per head, the last two unused
			values[v.Key()] = nodeID.Event(uint16(0x300 + own)).String()
			own++
			if own%8 == 6 {
				own += 2
			}
		case strings.HasPrefix(path, "Signal Heads/"):
			if v.Element.Kind == cdi.KindEventID {
				values[v.Key()] = nodeID.Event(uint16(0x100 + aspect)).String()
				aspect++
				if aspect%16 == 11 {
					aspect += 5
				}
				continue
			}
			values[v.Key()] = v.Element.Default
		}
	}
	return doc, values
}

func outpostNode() entity.NodeInfo {
	return entity.NodeInfo{ID: nodeID, Name: "Yard", SNIP: &openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", SoftwareVersion: "0.2.1", UserName: "Yard"}}
}

func byID(specs []entity.Spec) map[string]entity.Spec {
	out := make(map[string]entity.Spec, len(specs))
	for _, s := range specs {
		out[s.ID] = s
	}
	return out
}

func TestBuiltinProfiles(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	list := set.List()
	if len(list) != 3 || list[2].Name != "generic" {
		names := make([]string, len(list))
		for i, p := range list {
			names[i] = p.Name
		}
		t.Fatalf("profiles = %v", names)
	}
	if p := set.Match(&openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost"}); p == nil || p.Name != "canbus-outpost" {
		t.Errorf("outpost match = %v", p)
	}
	if p := set.Match(&openlcb.SNIP{Manufacturer: "SIMPLE-DCC", Model: "dcc command station "}); p == nil || p.Name != "simple-dcc" {
		t.Errorf("command station match = %v", p)
	}
	if p := set.Match(&openlcb.SNIP{Manufacturer: "RR-CirKits", Model: "Tower-LCC"}); p == nil || p.Name != "generic" {
		t.Errorf("unknown node match = %v", p)
	}
	if p := set.Match(nil); p == nil || p.Name != "generic" {
		t.Errorf("nil SNIP match = %v", p)
	}
}

func TestOutpostProfile(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	doc, values := outpostValues(t)
	specs := set.Build(outpostNode(), doc, values)
	if len(specs) != 13 {
		t.Fatalf("specs = %d, want 4 turnouts, 4 blocks, 4 reporters and the signal test", len(specs))
	}
	ids := byID(specs)
	tn, ok := ids["turnout.0a0000000042.servo_2"]
	if !ok {
		t.Fatalf("no servo 2 turnout among %v", ids)
	}
	if tn.Name != "Yard Servo 2" || tn.Group != "Servo 2" || tn.Source != entity.SourceProfile || tn.Profile != "canbus-outpost" || tn.Suggested {
		t.Errorf("turnout = %+v", tn)
	}
	want := map[string]openlcb.EventID{"throw": nodeID.Event(5), "close": nodeID.Event(6), "thrown": nodeID.Event(7), "closed": nodeID.Event(8), "center": nodeID.Event(9)}
	for role, ev := range want {
		if tn.Events[role] != ev {
			t.Errorf("servo 2 %s = %v, want %v", role, tn.Events[role], ev)
		}
	}
	if tn.Keys["throw"] != "253:252" {
		t.Errorf("servo 2 throw key = %s", tn.Keys["throw"])
	}
	block := ids["block.0a0000000042.track_3"]
	if block.Events["occupied"] != nodeID.Event(0x24) || block.Events["clear"] != nodeID.Event(0x25) || block.Name != "Yard Track 3" {
		t.Errorf("block = %+v", block)
	}
	rep := ids["reporter.0a0000000042.track_1"]
	if rep.Events["range"] != (nodeID + 1).Event(0) {
		t.Errorf("reporter = %+v", rep)
	}
	test := ids["switch.0a0000000042.signal_test"]
	if test.Name != "Yard signal test" || test.Kind != entity.KindSwitch ||
		!maps.Equal(test.Events, map[string]openlcb.EventID{"on": nodeID.Event(0x200), "off": nodeID.Event(0x201)}) {
		t.Errorf("signal test = %+v among %v", test, ids)
	}

	// A description names the entity; a disabled servo has none; a zero
	// reporter ID turns the reporter off.
	lookup := doc.Lookup()
	for _, v := range lookup {
		path := strings.Join(v.Path, "/")
		switch path {
		case "Switch Machines/Servo 1/Description":
			values[v.Key()] = "Yard lead"
		case "Switch Machines/Servo 3/Enabled":
			values[v.Key()] = "0"
		case "RailCom Detectors/Track 4/Reporter ID":
			values[v.Key()] = "00.00.00.00.00.00.00.00"
		}
	}
	specs = set.Build(outpostNode(), doc, values)
	ids = byID(specs)
	if len(specs) != 11 {
		t.Errorf("specs after changes = %d, want 11", len(specs))
	}
	if ids["turnout.0a0000000042.servo_1"].Name != "Yard lead" {
		t.Errorf("servo 1 name = %q", ids["turnout.0a0000000042.servo_1"].Name)
	}
	if _, ok := ids["turnout.0a0000000042.servo_3"]; ok {
		t.Error("disabled servo still has a turnout")
	}
	if _, ok := ids["reporter.0a0000000042.track_4"]; ok {
		t.Error("track with no reporter ID still has a reporter")
	}
	if _, ok := ids["block.0a0000000042.track_4"]; !ok {
		t.Error("track 4 block missing")
	}
}

// A servo output that carries the signal chain has no switch machine,
// whatever its Enabled says.
// {n} in a condition's variable path, or a name template's, is the
// replica's number; a template with an empty variable is skipped.
func TestReplicaPaths(t *testing.T) {
	p, err := Parse([]byte(`name: t
match: {any: true}
entities:
  - kind: switch
    group: Own Events
    id: r
    name: "{Signal Heads/Head {n}/Description} lamp"
    unless: {variable: "Signal Heads/Head {n}/Enabled", value: "1"}
    events: {on: R On, off: R Off}
`), "t")
	if err != nil {
		t.Fatal(err)
	}
	doc, values := outpostValues(t)
	for _, v := range doc.Variables() {
		switch strings.Join(v.Path, "/") {
		case "Signal Heads/Head 1/Description":
			values[v.Key()] = "Bay"
		case "Signal Heads/Head 2/Enabled":
			values[v.Key()] = "1"
		}
	}
	ids := byID(p.Build(outpostNode(), doc, values))
	if s, ok := ids["switch.0a0000000042.head_1_outputs_r"]; !ok || s.Name != "Bay lamp" {
		t.Errorf("head 1 = %+v, %v", s, ok)
	}
	if _, ok := ids["switch.0a0000000042.head_2_outputs_r"]; ok {
		t.Error("unless with {n} in its path didn't hold for head 2")
	}
	if s, ok := ids["switch.0a0000000042.head_3_outputs_r"]; !ok || s.Name != "Yard Head 3 Outputs" {
		t.Errorf("head 3 = %+v, %v", s, ok)
	}
}

func TestOutpostChainOnServo(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	doc, values := outpostValues(t)
	connected := ""
	for _, v := range doc.Variables() {
		if strings.Join(v.Path, "/") == "Signal Heads/Connected To" {
			connected = v.Key()
		}
	}
	tests := []struct {
		value string
		gone  string
	}{
		{"0", ""},
		{"2", "turnout.0a0000000042.servo_2"},
		{"4", "turnout.0a0000000042.servo_4"},
	}
	for _, tt := range tests {
		values[connected] = tt.value
		ids := byID(set.Build(outpostNode(), doc, values))
		for s := 1; s <= 4; s++ {
			id := fmt.Sprintf("turnout.0a0000000042.servo_%d", s)
			if _, ok := ids[id]; ok == (id == tt.gone) {
				t.Errorf("chain on %s: %s present = %v", tt.value, id, ok)
			}
		}
	}
}

// An output set to Own Events (lamp 8) on an enabled head is a switch of its
// own; a head whose outputs all are is no signal head.
func TestOutpostOwnEvents(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	doc, values := outpostValues(t)
	put := func(path, value string) {
		t.Helper()
		for _, v := range doc.Variables() {
			if strings.Join(v.Path, "/") == path {
				values[v.Key()] = value
				return
			}
		}
		t.Fatalf("no variable %s", path)
	}
	// Head 5: red on G beside two own outputs. Head 6: own outputs only,
	// no description. Head 7: an own output, disabled.
	put("Signal Heads/Head 5/Enabled", "1")
	put("Signal Heads/Head 5/Description", "Platform")
	put("Signal Heads/Head 5/R Lamp", "8")
	put("Signal Heads/Head 5/G Lamp", "1")
	put("Signal Heads/Head 5/Y Lamp", "8")
	put("Signal Heads/Head 6/Enabled", "1")
	put("Signal Heads/Head 6/R Lamp", "8")
	put("Signal Heads/Head 6/G Lamp", "0")
	put("Signal Heads/Head 6/Y Lamp", "8")
	put("Signal Heads/Head 7/R Lamp", "8")

	ids := byID(set.Build(outpostNode(), doc, values))
	tests := []struct {
		id, name string
		on, off  uint16
	}{
		{"switch.0a0000000042.head_5_outputs_r", "Platform output R", 0x320, 0x321},
		{"switch.0a0000000042.head_5_outputs_y", "Platform output Y", 0x324, 0x325},
		{"switch.0a0000000042.head_6_outputs_r", "Yard Head 6 output R", 0x328, 0x329},
		{"switch.0a0000000042.head_6_outputs_y", "Yard Head 6 output Y", 0x32C, 0x32D},
	}
	for _, tt := range tests {
		spec, ok := ids[tt.id]
		if !ok {
			t.Errorf("no %s", tt.id)
			continue
		}
		want := map[string]openlcb.EventID{"on": nodeID.Event(tt.on), "off": nodeID.Event(tt.off)}
		if spec.Kind != entity.KindSwitch || spec.Name != tt.name || !maps.Equal(spec.Events, want) {
			t.Errorf("%s = %+v", tt.id, spec)
		}
	}
	for _, id := range []string{"switch.0a0000000042.head_5_outputs_g", "switch.0a0000000042.head_7_outputs_r"} {
		if _, ok := ids[id]; ok {
			t.Errorf("%s: an output not on its own events, or on a disabled head, became a switch", id)
		}
	}
	if h, ok := ids["signal_head.0a0000000042.head_5"]; !ok || !maps.Equal(h.Events, map[string]openlcb.EventID{
		"dark": nodeID.Event(0x140), "red": nodeID.Event(0x141), "flashing_red": nodeID.Event(0x148),
	}) {
		t.Errorf("head 5 = %+v, %v", h, ok)
	}
	if _, ok := ids["signal_head.0a0000000042.head_6"]; ok {
		t.Error("a head with only own outputs became a signal head")
	}
}

func TestOutpostSignalHeads(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	doc, values := outpostValues(t)
	put := func(path, value string) {
		t.Helper()
		for _, v := range doc.Variables() {
			if strings.Join(v.Path, "/") == path {
				values[v.Key()] = value
				return
			}
		}
		t.Fatalf("no variable %s", path)
	}
	const (
		redGreenYellow = "signal_head.0a0000000042.head_1"
		lunar          = "signal_head.0a0000000042.head_2"
		noLamps        = "signal_head.0a0000000042.head_3"
		disabled       = "signal_head.0a0000000042.head_4"
	)
	// Head 1 keeps the default red, green and yellow; head 2 is a lunar
	// lamp on its own, wired twice; head 3 has no lamps.
	put("Signal Heads/Head 1/Enabled", "1")
	put("Signal Heads/Head 1/Description", "Times Sq home")
	put("Signal Heads/Head 2/Enabled", "1")
	put("Signal Heads/Head 2/R Lamp", "4")
	put("Signal Heads/Head 2/G Lamp", "4")
	put("Signal Heads/Head 2/Y Lamp", "0")
	put("Signal Heads/Head 3/Enabled", "1")
	put("Signal Heads/Head 3/R Lamp", "0")
	put("Signal Heads/Head 3/G Lamp", "0")
	put("Signal Heads/Head 3/Y Lamp", "0")

	ids := byID(set.Build(outpostNode(), doc, values))
	if _, ok := ids[disabled]; ok {
		t.Error("a disabled head became an entity")
	}
	// Nothing of a head without lamps is on the bus, not even dark
	if _, ok := ids[noLamps]; ok {
		t.Error("a head without lamps became an entity")
	}
	tests := []struct {
		id     string
		name   string
		events map[string]openlcb.EventID
	}{
		{redGreenYellow, "Times Sq home", map[string]openlcb.EventID{
			"dark": nodeID.Event(0x100), "red": nodeID.Event(0x101), "yellow": nodeID.Event(0x102), "green": nodeID.Event(0x103),
			"flashing_red": nodeID.Event(0x108), "flashing_yellow": nodeID.Event(0x109), "flashing_green": nodeID.Event(0x10A),
		}},
		{lunar, "Yard Head 2", map[string]openlcb.EventID{"dark": nodeID.Event(0x110), "lunar": nodeID.Event(0x114)}},
	}
	for _, tt := range tests {
		spec, ok := ids[tt.id]
		if !ok {
			t.Errorf("no %s among %v", tt.id, ids)
			continue
		}
		if spec.Kind != entity.KindSignalHead || spec.Name != tt.name {
			t.Errorf("%s = %+v", tt.id, spec)
		}
		if !maps.Equal(spec.Events, tt.events) {
			t.Errorf("%s events = %v, want %v", tt.id, spec.Events, tt.events)
		}
	}
}

func TestCommandStationProfile(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := cdi.Parse(sim.CommandStationCDI)
	if err != nil {
		t.Fatal(err)
	}
	node := entity.NodeInfo{ID: 0x0A0000000050, Name: "Bench", SNIP: &openlcb.SNIP{Manufacturer: "simple-dcc", Model: "DCC Command Station"}}
	specs := set.Build(node, doc, map[string]string{})
	if len(specs) != 2 {
		t.Fatalf("specs = %+v", specs)
	}
	power := byID(specs)["switch.0a0000000050.track_power"]
	if power.Name != "Bench track power" || power.Events["on"] != openlcb.EventClearEmergencyOff || power.Events["off"] != openlcb.EventEmergencyOff || power.Kind != entity.KindSwitch {
		t.Errorf("track power = %+v", power)
	}
}

func TestGenericProfile(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	// An outpost whose identification is unknown gets guesses.
	doc, values := outpostValues(t)
	node := entity.NodeInfo{ID: nodeID, Name: "Mystery", SNIP: &openlcb.SNIP{Manufacturer: "Someone", Model: "Else"}}
	specs := set.Build(node, doc, values)
	ids := byID(specs)
	tn, ok := ids["turnout.0a0000000042.servo_1"]
	if !ok {
		t.Fatalf("no guessed turnout among %v", ids)
	}
	if !tn.Suggested || tn.Source != entity.SourceGuess || tn.Profile != "generic" || tn.Name != "Mystery Servo 1" {
		t.Errorf("guess = %+v", tn)
	}
	if tn.Events["throw"] != nodeID.Event(0) || tn.Events["close"] != nodeID.Event(1) || tn.Events["thrown"] != nodeID.Event(2) || tn.Events["closed"] != nodeID.Event(3) {
		t.Errorf("guessed events = %v", tn.Events)
	}
	if _, ok := tn.Events["center"]; ok {
		t.Error("the generic turnout has no center role")
	}
	if b, ok := ids["block.0a0000000042.track_2"]; !ok || b.Events["occupied"] != nodeID.Event(0x22) {
		t.Errorf("guessed block = %+v, %v", b, ok)
	}
	if r, ok := ids["reporter.0a0000000042.track_2"]; !ok || r.Events["range"] != (nodeID+2).Event(0) {
		t.Errorf("guessed reporter = %+v, %v", r, ok)
	}
	// An output's On and Off events read as a sensor: a CDI doesn't say
	// which way an event goes
	for id, s := range ids {
		if s.Kind == entity.KindSensor && !strings.HasSuffix(s.Group, " Outputs") {
			t.Errorf("unexpected sensor guess %s: %v", id, s.Events)
		}
	}
	if s, ok := ids["sensor.0a0000000042.head_1_outputs"]; !ok ||
		s.Events["on"] != nodeID.Event(0x300) || s.Events["off"] != nodeID.Event(0x301) {
		t.Errorf("own events guess = %+v, %v", s, ok)
	}
}

func TestUserProfilesAndErrors(t *testing.T) {
	dir := t.TempDir()
	good := `name: canbus-outpost
description: replaced
match: {manufacturer: mikesmitty, model: CAN Bus Outpost}
entities:
  - kind: sensor
    group: Track
    events: {on: Occupied Event, off: Clear Event}
`
	if err := os.WriteFile(filepath.Join(dir, "outpost.yaml"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := set.Match(&openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost"})
	if p == nil || p.Description != "replaced" || p.Source != filepath.Join(dir, "outpost.yaml") {
		t.Errorf("user profile did not replace the built-in: %+v", p)
	}
	if len(set.List()) != 3 {
		t.Errorf("profiles = %d", len(set.List()))
	}
	doc, values := outpostValues(t)
	specs := set.Build(outpostNode(), doc, values)
	if len(specs) != 4 || specs[0].Kind != entity.KindSensor {
		t.Errorf("user profile specs = %+v", specs)
	}

	// A missing directory is fine.
	if _, err := Load(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("missing dir: %v", err)
	}

	bad := map[string]string{
		"no name":          "match: {any: true}\nentities: [{kind: block, events: {occupied: a, clear: b}}]\n",
		"no match":         "name: x\nentities: [{kind: block, events: {occupied: a, clear: b}}]\n",
		"unknown kind":     "name: x\nmatch: {any: true}\nentities: [{kind: lamp, events: {on: a}}]\n",
		"unknown role":     "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: a, clear: b, press: c}}]\n",
		"missing role":     "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: a}}]\n",
		"bad regexp":       "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: '~(', clear: b}}]\n",
		"zero event":       "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: 00.00.00.00.00.00.00.00, clear: b}}]\n",
		"events as list":   "name: x\nmatch: {any: true}\nentities: [{kind: block, events: [occupied, clear]}]\n",
		"no entities":      "name: x\nmatch: {any: true}\n",
		"no events":        "name: x\nmatch: {any: true}\nentities: [{kind: signal_head, group: Head}]\n",
		"needs nothing":    "name: x\nmatch: {any: true}\nentities: [{kind: signal_head, events: {red: a}, needs: {values: {red: '1'}}}]\n",
		"needs a role":     "name: x\nmatch: {any: true}\nentities: [{kind: signal_head, events: {red: a}, needs: {variables: [b], values: {green: '1'}}}]\n",
		"unless no value":  "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: a, clear: b}, unless: {variable: c}}]\n",
		"when no variable": "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: a, clear: b}, when: [{value: '1'}]}]\n",
		"needs a map":      "name: x\nmatch: {any: true}\nentities: [{kind: signal_head, events: {red: a}, needs: {variables: [b], values: {red: {x: y}}}}]\n",
	}
	for name, text := range bad {
		if _, err := Parse([]byte(text), name); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte(bad["bad regexp"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("a broken user profile should fail loading")
	}
}
