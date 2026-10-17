package profile

import (
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
// 0x00-0x13, occupied and clear 0x20-0x27, reporters the next node IDs.
func outpostValues(t *testing.T) (*cdi.CDI, map[string]string) {
	t.Helper()
	doc, err := cdi.Parse(sim.OutpostCDI)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	servo, track := 0, 0
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
	if len(specs) != 12 {
		t.Fatalf("specs = %d, want 4 turnouts, 4 blocks, 4 reporters", len(specs))
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
	if len(specs) != 10 {
		t.Errorf("specs after changes = %d, want 10", len(specs))
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
	for id, s := range ids {
		if s.Kind == entity.KindSensor {
			t.Errorf("unexpected sensor guess %s: %v", id, s.Events)
		}
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
		"no name":        "match: {any: true}\nentities: [{kind: block, events: {occupied: a, clear: b}}]\n",
		"no match":       "name: x\nentities: [{kind: block, events: {occupied: a, clear: b}}]\n",
		"unknown kind":   "name: x\nmatch: {any: true}\nentities: [{kind: lamp, events: {on: a}}]\n",
		"unknown role":   "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: a, clear: b, press: c}}]\n",
		"missing role":   "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: a}}]\n",
		"bad regexp":     "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: '~(', clear: b}}]\n",
		"zero event":     "name: x\nmatch: {any: true}\nentities: [{kind: block, events: {occupied: 00.00.00.00.00.00.00.00, clear: b}}]\n",
		"events as list": "name: x\nmatch: {any: true}\nentities: [{kind: block, events: [occupied, clear]}]\n",
		"no entities":    "name: x\nmatch: {any: true}\n",
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
