package automation

import (
	"encoding/json"
	"fmt"

	"github.com/mikesmitty/interlock/internal/refs"
)

const area = "Automations"

// Rewrite renames the entities, events and nodes the automations name, in
// their triggers, conditions and actions at any depth. With apply false
// it only reports what would change; with apply true it saves each
// changed automation.
func (e *Engine) Rewrite(rw refs.Rewrite, apply bool) ([]refs.Change, error) {
	var changes []refs.Change
	for _, st := range e.List() {
		doc, err := clone(st.Automation)
		if err != nil {
			return nil, err
		}
		w := walker{rw: rw, item: doc.Name}
		w.triggers(doc.Triggers, "trigger")
		w.conditions(doc.Conditions)
		w.actions(doc.Actions)
		changes = append(changes, w.changes...)
		if apply && len(w.changes) > 0 {
			if _, err := e.Update(doc.ID, doc); err != nil {
				return nil, fmt.Errorf("save automation %s: %w", doc.Name, err)
			}
		}
	}
	return changes, nil
}

// clone copies an automation deeply, so rewriting it leaves the engine's
// copy alone.
func clone(a Automation) (Automation, error) {
	body, err := json.Marshal(a)
	if err != nil {
		return Automation{}, err
	}
	var out Automation
	err = json.Unmarshal(body, &out)
	return out, err
}

type walker struct {
	rw      refs.Rewrite
	item    string
	changes []refs.Change
}

func (w *walker) add(c refs.Change, ok bool, field string) {
	if ok {
		w.changes = append(w.changes, c.In(area, w.item, field))
	}
}

func (w *walker) triggers(ts []Trigger, what string) {
	for i := range ts {
		t := &ts[i]
		c, ok := w.rw.RenameEntity(&t.Entity)
		w.add(c, ok, "entity a "+what+" watches")
		c, ok = w.rw.RenameEvent(&t.Event)
		w.add(c, ok, "event a "+what+" listens for")
		c, ok = w.rw.RenameNode(&t.Node)
		w.add(c, ok, "node a "+what+" watches")
	}
}

func (w *walker) conditions(cs []Condition) {
	for i := range cs {
		c := &cs[i]
		ch, ok := w.rw.RenameEntity(&c.Entity)
		w.add(ch, ok, "entity a condition checks")
		w.conditions(c.Conditions)
	}
}

func (w *walker) actions(as []Action) {
	for i := range as {
		a := &as[i]
		c, ok := w.rw.RenameEntity(&a.Entity)
		w.add(c, ok, "entity an action commands")
		c, ok = w.rw.RenameEvent(&a.Event)
		w.add(c, ok, "event an action sends")
		w.triggers(a.Triggers, "wait trigger")
		w.conditions(a.Conditions)
		w.conditions(a.While)
		w.conditions(a.Until)
		w.actions(a.Then)
		w.actions(a.Else)
		w.actions(a.Default)
		w.actions(a.Actions)
		for j := range a.Choices {
			w.conditions(a.Choices[j].Conditions)
			w.actions(a.Choices[j].Actions)
		}
	}
}
