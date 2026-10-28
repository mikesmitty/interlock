package trackplan

import (
	"fmt"
	"slices"

	"github.com/mikesmitty/interlock/internal/refs"
)

const area = "Track plan"

// Rewrite renames the entities the plan links: its turnouts' switch
// machines and its blocks' detectors and reporters. With apply false it
// only reports what would change; with apply true it saves the changed
// plan as a new revision.
func (f *File) Rewrite(rw refs.Rewrite, apply bool) ([]refs.Change, error) {
	st := f.Get()
	plan := st.Plan
	plan.Junctions = slices.Clone(plan.Junctions)
	plan.Blocks = slices.Clone(plan.Blocks)
	var changes []refs.Change
	points := make(map[string]Point, len(plan.Points))
	for _, pt := range plan.Points {
		points[pt.ID] = pt
	}
	for i := range plan.Junctions {
		j := &plan.Junctions[i]
		if c, ok := rw.RenameEntity(&j.Switch); ok {
			changes = append(changes, c.In(area, j.describe(points), "switch machine"))
		}
	}
	for i := range plan.Blocks {
		b := &plan.Blocks[i]
		if c, ok := rw.RenameEntity(&b.Occupancy); ok {
			changes = append(changes, c.In(area, "Block "+b.Name, "detector"))
		}
		if c, ok := rw.RenameEntity(&b.Reporter); ok {
			changes = append(changes, c.In(area, "Block "+b.Name, "reporter"))
		}
	}
	if !apply || len(changes) == 0 {
		return changes, nil
	}
	if _, err := f.Put(plan, st.Revision); err != nil {
		return nil, fmt.Errorf("save the track plan: %w", err)
	}
	return changes, nil
}
