package trackplan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/refs"
)

func TestRewrite(t *testing.T) {
	f, err := OpenFile(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	plan := station()
	plan.Junctions[0].Switch = "turnout.0aff9024a634.servo_1"
	plan.Blocks[0].Occupancy = "block.0aff9024a634.track_1"
	plan.Blocks[0].Reporter = "reporter.0aff9024a634.track_1"
	plan.Blocks[1].Occupancy = "block.0a737c38ad26.track_1"
	plan.Signals = []Signal{{ID: "s1", Track: plan.Tracks[0].ID, Upper: "signal_head.0aff9024a634.head_1", Lower: "signal_head.0aff9024a634.head_2"}}
	saved, err := f.Put(plan, f.Get().Revision)
	if err != nil {
		t.Fatal(err)
	}
	rw := refs.Rewrite{Entity: func(id string) (string, bool) {
		if !strings.Contains(id, ".0aff9024a634.") {
			return id, false
		}
		return strings.Replace(id, "0aff9024a634", "050101012200", 1), true
	}}

	preview, err := f.Rewrite(rw, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range preview {
		got = append(got, c.Item+": "+c.Field+" → "+c.To)
	}
	want := []string{
		"Crossover at 300, 0 mm: switch machine → turnout.050101012200.servo_1",
		"Block A1: detector → block.050101012200.track_1",
		"Block A1: reporter → reporter.050101012200.track_1",
		"Signal s1: upper head → signal_head.050101012200.head_1",
		"Signal s1: lower head → signal_head.050101012200.head_2",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if f.Get().Revision != saved.Revision {
		t.Error("a preview saved the plan")
	}

	if _, err := f.Rewrite(rw, true); err != nil {
		t.Fatal(err)
	}
	after := f.Get().Plan
	if after.Junctions[0].Switch != "turnout.050101012200.servo_1" || after.Blocks[0].Reporter != "reporter.050101012200.track_1" || after.Blocks[1].Occupancy != "block.0a737c38ad26.track_1" {
		t.Errorf("saved plan: switch %q, A1 reporter %q, A2 detector %q", after.Junctions[0].Switch, after.Blocks[0].Reporter, after.Blocks[1].Occupancy)
	}
	if got := after.Signals[0]; got.Upper != "signal_head.050101012200.head_1" || got.Lower != "signal_head.050101012200.head_2" {
		t.Errorf("saved signal = %+v", got)
	}
	if saved.Plan.Junctions[0].Switch != "turnout.0aff9024a634.servo_1" || saved.Plan.Signals[0].Upper != "signal_head.0aff9024a634.head_1" {
		t.Error("rewriting changed the plan held by an earlier state")
	}
}
