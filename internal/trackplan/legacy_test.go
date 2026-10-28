package trackplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gridPlan is a plan as the grid editor saved it: a line ending at a
// buffer stop, curving down into a turnout that goes on southeast or
// branches off east, with a platform and a label.
const gridPlan = `tiles:
  - {x: 0, "y": 0, track: [e]}
  - {x: 1, "y": 0, track: [e, w], block: track_1}
  - {x: 2, "y": 0, track: [se, w], block: track_1}
  - {x: 3, "y": 1, track: [e, se, nw], turnout: turnout.t1}
  - {x: 4, "y": 1, track: [w]}
  - {x: 4, "y": 2, track: [nw]}
  - {x: 1, "y": 1, platform: [e, w]}
  - {x: 0, "y": 3, label: Times Sq}
blocks:
  - {id: track_1, name: Track 1}
background:
  image: background-0123456789abcdef.png
  x: 1
  "y": 2
  width: 10
  height: 5
  opacity: 0.4
  rotation: 90
`

func TestConvertGrid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(gridPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, BackgroundDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, BackgroundDirName, "background-0123456789abcdef.png"), pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st := f.Get()
	p := st.Plan
	if len(p.Tracks) != 5 || len(p.Junctions) != 1 || len(p.Platforms) != 1 || len(p.Labels) != 1 || p.Grid != LegacyCell {
		t.Fatalf("converted to %d tracks, %d junctions, %d platforms, %d labels on a %v mm grid", len(p.Tracks), len(p.Junctions), len(p.Platforms), len(p.Labels), p.Grid)
	}
	j := p.Junctions[0]
	if j.Kind != KindTurnout || j.Switch != "turnout.t1" || *j.At != (Vec{3.5 * LegacyCell, 1.5 * LegacyCell}) {
		t.Errorf("turnout = %+v", j)
	}
	if b := p.Background; b == nil || b.X != LegacyCell || b.Width != 10*LegacyCell || b.Rotation != 90 {
		t.Errorf("background = %+v", p.Background)
	}
	// The cells' track joins up: the two pieces of track that were cells
	// with one way out end at buffer stops, and the 45 degree bend is no
	// kink worth mentioning.
	var problems []string
	for _, pr := range st.Topology.Problems {
		problems = append(problems, pr.Kind)
	}
	if len(problems) != 0 {
		t.Errorf("problems after converting: %q", problems)
	}
	// A cell of straight track and a 45 degree curve a little longer.
	if l := st.Topology.Blocks[0].Length; l < 2*LegacyCell || l > 2.3*LegacyCell {
		t.Errorf("block length = %v, want a little over two cells' worth", l)
	}

	kept, err := os.ReadFile(filepath.Join(dir, LegacyFileName))
	if err != nil || string(kept) != gridPlan {
		t.Errorf("the grid plan wasn't kept as it was: %v", err)
	}
	saved, err := f.Put(p, st.Revision)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "tiles:") || saved.Plan.Grid != LegacyCell {
		t.Errorf("saved in the new form? grid %v:\n%s", saved.Plan.Grid, body)
	}
}
