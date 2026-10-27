package entity

import (
	"fmt"
	"maps"
	"slices"

	"github.com/mikesmitty/interlock/internal/refs"
)

const area = "Entities"

// Rewrite moves what was set for entities that are renamed (a name of
// your own, hidden, a confirmed guess) to their new IDs, unless the new
// entity has settings of its own, and renames the events of entities
// built by hand. With apply false it only reports what would change.
func (m *Manager) Rewrite(rw refs.Rewrite, apply bool) ([]refs.Change, error) {
	var changes []refs.Change
	overrides := m.opts.File.Overrides()
	for _, id := range slices.Sorted(maps.Keys(overrides)) {
		o := overrides[id]
		to := id
		c, ok := rw.RenameEntity(&to)
		if !ok {
			continue
		}
		if _, taken := overrides[to]; taken {
			continue
		}
		changes = append(changes, c.In(area, m.nameOf(id, o), "name and visibility set here"))
		if !apply {
			continue
		}
		if err := m.opts.File.SetOverride(to, o); err != nil {
			return nil, err
		}
		if err := m.opts.File.SetOverride(id, Override{}); err != nil {
			return nil, err
		}
		if _, ok := m.Get(to); ok {
			if _, err := m.Update(to, Change{Name: &o.Name, Hidden: &o.Hidden, Confirmed: &o.Confirmed}); err != nil {
				return nil, fmt.Errorf("entity %s: %w", to, err)
			}
		}
	}
	for _, man := range m.opts.File.Manuals() {
		events := maps.Clone(man.Events)
		var mine []refs.Change
		for _, role := range slices.Sorted(maps.Keys(events)) {
			ev := events[role]
			if c, ok := rw.RenameEvent(&ev); ok {
				events[role] = ev
				mine = append(mine, c.In(area, man.Name, role+" event"))
			}
		}
		changes = append(changes, mine...)
		if apply && len(mine) > 0 {
			if _, err := m.Update(man.ID, Change{Events: events}); err != nil {
				return nil, fmt.Errorf("entity %s: %w", man.Name, err)
			}
		}
	}
	return changes, nil
}

// nameOf names an entity for a list of changes, while it still exists.
func (m *Manager) nameOf(id string, o Override) string {
	if ent, ok := m.Get(id); ok {
		return ent.Name
	}
	if o.Name != "" {
		return o.Name
	}
	return id
}
