package automation

import (
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/refs"
	"github.com/mikesmitty/interlock/openlcb"
)

func TestRewrite(t *testing.T) {
	const oldEv, newEv = openlcb.EventID(0x0a00000000410010), openlcb.EventID(0x0a00000000420010)
	doc := Automation{
		Name: "Throat", Enabled: true,
		Triggers: []Trigger{
			{Type: TriggerState, Entity: "block.0a0000000041.track_1", To: "occupied"},
			{Type: TriggerEvent, Event: oldEv},
			{Type: TriggerNode, Node: 0x0a0000000041, To: "offline"},
		},
		Conditions: []Condition{{Type: ConditionOr, Conditions: []Condition{
			{Type: ConditionState, Entity: "turnout.0a0000000041.servo_1", State: "closed"},
			{Type: ConditionState, Entity: "turnout.1", State: "closed"},
		}}},
		Actions: []Action{
			{Type: ActionEvent, Event: oldEv},
			{Type: ActionWait, Triggers: []Trigger{{Type: TriggerState, Entity: "block.0a0000000041.track_2", To: "clear"}}, Timeout: ms(500)},
			{Type: ActionIf, Conditions: []Condition{{Type: ConditionState, Entity: "block.t2", State: "clear"}},
				Then: []Action{{Type: ActionCommand, Entity: "turnout.0a0000000041.servo_1", Command: "throw"}}},
		},
	}
	h := newHarness(t, doc)
	id := h.e.List()[0].ID
	rw := refs.Rewrite{
		Entity: func(e string) (string, bool) {
			return strings.Replace(e, ".0a0000000041.", ".0a0000000042.", 1), strings.Contains(e, ".0a0000000041.")
		},
		Event: func(ev openlcb.EventID) (openlcb.EventID, bool) { return newEv, ev == oldEv },
		Node:  func(n openlcb.NodeID) (openlcb.NodeID, bool) { return 0x0a0000000042, n == 0x0a0000000041 },
	}

	preview, err := h.e.Rewrite(rw, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range preview {
		got = append(got, c.Item+": "+c.Field)
	}
	want := []string{
		"Throat: entity a trigger watches",
		"Throat: event a trigger listens for",
		"Throat: node a trigger watches",
		"Throat: entity a condition checks",
		"Throat: event an action sends",
		"Throat: entity a wait trigger watches",
		"Throat: entity an action commands",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if st, _ := h.e.Get(id); st.Triggers[0].Entity != "block.0a0000000041.track_1" {
		t.Error("a preview changed the automation")
	}

	if _, err := h.e.Rewrite(rw, true); err != nil {
		t.Fatal(err)
	}
	st, _ := h.e.Get(id)
	if st.Triggers[0].Entity != "block.0a0000000042.track_1" || st.Triggers[1].Event != newEv || st.Triggers[2].Node != 0x0a0000000042 ||
		st.Conditions[0].Conditions[0].Entity != "turnout.0a0000000042.servo_1" || st.Conditions[0].Conditions[1].Entity != "turnout.1" ||
		st.Actions[1].Triggers[0].Entity != "block.0a0000000042.track_2" || st.Actions[2].Then[0].Entity != "turnout.0a0000000042.servo_1" {
		t.Errorf("saved automation = %+v", st.Automation)
	}
}
