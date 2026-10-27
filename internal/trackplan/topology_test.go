package trackplan

import (
	"strconv"
	"strings"
	"testing"
)

func tile(x, y int, block string, dirs ...Direction) Tile {
	return Tile{X: x, Y: y, Track: dirs, Block: block}
}

// station is two parallel tracks with a crossover in the middle, from the
// upper track down to the lower one heading east, and a block at each end
// of each track:
//
//	a1 a1 a1 .  T1 .  .  b1 b1 b1
//	a2 a2 a2 .  .  T2 .  b2 b2 b2
func station() Plan {
	p := Plan{Blocks: []Block{
		{ID: "a1", Name: "A1"}, {ID: "a2", Name: "A2"}, {ID: "b1", Name: "B1"}, {ID: "b2", Name: "B2"},
	}}
	for y, row := range []string{"1", "2"} {
		for x := range 10 {
			block := ""
			switch {
			case x <= 2:
				block = "a" + row
			case x >= 7:
				block = "b" + row
			}
			dirs := []Direction{West, East}
			switch x {
			case 0:
				dirs = []Direction{East}
			case 9:
				dirs = []Direction{West}
			}
			p.Tiles = append(p.Tiles, tile(x, y, block, dirs...))
		}
	}
	p.Tiles[4].Track = []Direction{West, East, SouthEast}
	p.Tiles[4].Turnout = "turnout.t1"
	p.Tiles[15].Track = []Direction{West, East, NorthWest}
	return p
}

func TestTopologyStation(t *testing.T) {
	p := station()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	top := p.Topology()
	if len(top.Problems) != 0 {
		t.Errorf("problems: %+v", top.Problems)
	}
	ends := map[string][]Port{}
	for _, b := range top.Blocks {
		ends[b.ID] = b.Ends
		if b.Cells != 3 {
			t.Errorf("block %s has %d cells, want 3", b.ID, b.Cells)
		}
	}
	if got := ends["a1"]; len(got) != 1 || got[0] != (Port{Block: "a1", X: 2, Y: 0, Dir: East}) {
		t.Errorf("a1 ends = %+v, want only its east end (the west one is a bumper)", got)
	}

	want := []string{
		"a1 2,0 e > b1 7,0 w via 4,0 closed (4 cells)",
		"a1 2,0 e > b2 7,1 w via 4,0 thrown 5,1 thrown (4 cells)",
		"a2 2,1 e > b2 7,1 w via 5,1 closed (4 cells)",
	}
	var got []string
	for _, l := range top.Links {
		got = append(got, describe(l))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("links:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if top.Links[0].Turnouts[0].Turnout != "turnout.t1" {
		t.Errorf("the closed setting names %q, want the linked turnout", top.Links[0].Turnouts[0].Turnout)
	}
}

func describe(l Link) string {
	var b strings.Builder
	b.WriteString(l.From.Block + " " + Cell{l.From.X, l.From.Y}.String() + " " + l.From.Dir.String())
	b.WriteString(" > " + l.To.Block + " " + Cell{l.To.X, l.To.Y}.String() + " " + l.To.Dir.String())
	if len(l.Turnouts) > 0 {
		b.WriteString(" via")
		for _, s := range l.Turnouts {
			b.WriteString(" " + Cell{s.X, s.Y}.String() + " " + string(s.Position))
		}
	}
	b.WriteString(" (" + strconv.Itoa(l.Cells) + " cells)")
	return b.String()
}

func TestTopologyLoop(t *testing.T) {
	// A loop of sharp curves with one block on its top side: the block's
	// two ends link to each other around the loop.
	p := Plan{
		Blocks: []Block{{ID: "loop", Name: "Loop"}},
		Tiles: []Tile{
			tile(0, 0, "", East, South), tile(1, 0, "loop", West, East), tile(2, 0, "", West, South),
			tile(0, 1, "", North, South), tile(2, 1, "", North, South),
			tile(0, 2, "", North, East), tile(1, 2, "", West, East), tile(2, 2, "", North, West),
		},
	}
	top := p.Topology()
	if len(top.Problems) != 0 {
		t.Errorf("problems: %+v", top.Problems)
	}
	if len(top.Links) != 1 {
		t.Fatalf("links = %+v, want one around the loop", top.Links)
	}
	if got := describe(top.Links[0]); got != "loop 1,0 e > loop 1,0 w (7 cells)" {
		t.Errorf("link = %s", got)
	}
}

func TestTopologyReversingLoopWithoutBlock(t *testing.T) {
	// A balloon loop entirely outside blocks comes back through its own
	// turnout on the other leg, so it leads nowhere, and the walk ends.
	p := Plan{
		Blocks: []Block{{ID: "a", Name: "A"}},
		Tiles: []Tile{
			tile(0, 1, "a", East), tile(1, 1, "", West, East, SouthEast),
			tile(2, 1, "", West, East), tile(3, 1, "", West, South),
			tile(3, 2, "", North, West), tile(2, 2, "", East, NorthWest),
		},
	}
	top := p.Topology()
	if len(top.Problems) != 0 {
		t.Errorf("problems: %+v", top.Problems)
	}
	if len(top.Links) != 0 {
		t.Errorf("links = %+v, want none", top.Links)
	}
}

func TestTopologyAdjacentBlocks(t *testing.T) {
	p := Plan{
		Blocks: []Block{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}},
		Tiles:  []Tile{tile(0, 0, "a", East), tile(1, 0, "a", West, East), tile(2, 0, "b", West, East), tile(3, 0, "b", West)},
	}
	top := p.Topology()
	if len(top.Links) != 1 || describe(top.Links[0]) != "a 1,0 e > b 2,0 w (0 cells)" {
		t.Errorf("links = %+v", top.Links)
	}
}

func TestTopologyProblems(t *testing.T) {
	p := Plan{
		Blocks: []Block{{ID: "split", Name: "Split"}, {ID: "empty", Name: "Empty"}},
		Tiles: []Tile{
			tile(0, 0, "", West, East), tile(1, 0, "", West),
			tile(0, 2, "", East), tile(1, 2, "", North, South),
			tile(0, 4, "split", East), tile(1, 4, "split", West),
			tile(3, 4, "split", East), tile(4, 4, "split", West),
		},
	}
	top := p.Topology()
	var got []string
	for _, pr := range top.Problems {
		got = append(got, pr.Kind+" "+pr.Block+" "+pr.Message)
	}
	want := []string{
		"open_end  The track at 0,0 leads w into an empty cell.",
		"misaligned  The track at 0,2 leads e into track that doesn't lead back.",
		"open_end  The track at 1,2 leads n into an empty cell.",
		"open_end  The track at 1,2 leads s into an empty cell.",
		"block_split split Block Split is drawn in 2 pieces that don't join.",
		"block_empty empty Block Empty has no track drawn in it.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestValidate(t *testing.T) {
	good := station()
	tests := []struct {
		name   string
		change func(*Plan)
		want   string
	}{
		{"fine", func(*Plan) {}, ""},
		{"bad piece", func(p *Plan) { p.Tiles[1].Track = []Direction{West, North, East} }, "the track at 1,0"},
		{"turnout on plain track", func(p *Plan) { p.Tiles[1].Turnout = "turnout.x" }, "has a turnout linked but is a straight"},
		{"unknown block", func(p *Plan) { p.Tiles[1].Block = "nope" }, `block "nope"`},
		{"same cell twice", func(p *Plan) { p.Tiles = append(p.Tiles, tile(1, 0, "", West, East)) }, "listed twice"},
		{"two things in a cell", func(p *Plan) { p.Tiles[1].Label = "Hi" }, "more than one"},
		{"empty cell", func(p *Plan) { p.Tiles = append(p.Tiles, Tile{X: 0, Y: 5}) }, "holds nothing"},
		{"off the grid", func(p *Plan) { p.Tiles = append(p.Tiles, Tile{X: MaxCoord + 1, Label: "far"}) }, "outside the grid"},
		{"bad block ID", func(p *Plan) { p.Blocks = append(p.Blocks, Block{ID: "Bad ID", Name: "Bad"}) }, "must be lowercase"},
		{"duplicate block", func(p *Plan) { p.Blocks = append(p.Blocks, Block{ID: "a1", Name: "Again"}) }, "two blocks"},
		{"nameless block", func(p *Plan) { p.Blocks[0].Name = " " }, "needs a name"},
		{"negative length", func(p *Plan) { p.Blocks[0].LengthMM = -1 }, "length"},
		{"unknown station", func(p *Plan) { p.Blocks[0].Station = "grand-central" }, "isn't in the plan"},
		{"block on a label", func(p *Plan) { p.Tiles = append(p.Tiles, Tile{X: 0, Y: 5, Label: "x", Block: "a1"}) }, "no track"},
		{"platform twice", func(p *Plan) { p.Tiles = append(p.Tiles, Tile{X: 0, Y: 5, Platform: []Direction{East, East}}) }, "twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := station()
			p.Tiles = append([]Tile(nil), good.Tiles...)
			p.Blocks = append([]Block(nil), good.Blocks...)
			tt.change(&p)
			err := p.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("Validate: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("Validate = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
