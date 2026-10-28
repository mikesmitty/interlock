package trackplan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	f, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	empty := f.Get()
	if empty.Revision == "" || !empty.Plan.Empty() || empty.Plan.Tracks == nil || empty.Plan.Grid != DefaultGrid {
		t.Fatalf("a new plan = %+v, want an empty one with a revision, non-nil lists and the default grid", empty)
	}

	var heard []State
	unsubscribe := f.Subscribe(func(s State) { heard = append(heard, s) })
	defer unsubscribe()

	plan := station()
	plan.Stations = []Station{{ID: "times-sq", Name: " Times Sq "}}
	plan.Blocks[0].Station = "times-sq"
	saved, err := f.Put(plan, empty.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision == empty.Revision {
		t.Error("the revision did not change")
	}
	if len(saved.Topology.Links) != 3 {
		t.Errorf("topology links = %d, want 3", len(saved.Topology.Links))
	}
	if got := saved.Plan.Stations[0].Name; got != "Times Sq" {
		t.Errorf("station name = %q, want it trimmed", got)
	}
	if len(heard) != 1 || heard[0].Revision != saved.Revision {
		t.Errorf("subscribers heard %+v", heard)
	}

	if _, err := f.Put(plan, empty.Revision); !errors.Is(err, ErrConflict) {
		t.Errorf("a save from an old revision = %v, want ErrConflict", err)
	}
	bad := station()
	bad.Tracks[0].Block = "nope"
	var invalidErr *InvalidError
	if _, err := f.Put(bad, saved.Revision); !errors.As(err, &invalidErr) {
		t.Errorf("an invalid plan = %v, want an *InvalidError", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"# The track plan, in millimeters",
		`- {id: u1, x: 300, "y": 0}`,
		`- {id: t1, a: u0, b: u1, block: a1}`,
		`- {id: x1, kind: crossover, ends: [u1, u2, l1, l2], switch: turnout.t1}`,
		"station: times-sq",
		"grid: 33",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the file lacks %q:\n%s", want, text)
		}
	}

	again, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get(); got.Revision != saved.Revision || len(got.Plan.Tracks) != len(plan.Tracks) {
		t.Errorf("reopened: revision %s with %d tracks, want %s with %d", got.Revision, len(got.Plan.Tracks), saved.Revision, len(plan.Tracks))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), LegacyFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a plan in millimeters left a grid copy behind: %v", err)
	}
}

func TestOpenFileRejects(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"unknown field", "tracks:\n  - {id: t1, a: p1, b: p2, shade: red}\n", "shade"},
		{"missing point", "tracks:\n  - {id: t1, a: p1, b: p2}\n", "isn't in the plan"},
		{"bad old grid", "tiles:\n  - {x: 0, \"y\": 0, track: [east]}\n", "not a direction"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenFile(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("OpenFile = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
