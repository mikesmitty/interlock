package trackplan

import (
	"math"
	"strings"
	"testing"
)

// station is two tracks 33 mm apart with a crossover between them that
// takes trains from the upper track down to the lower one heading east,
// and a block at each end of each track:
//
//	a1 ===== x1 ===== b1
//	a2 =====    ===== b2
func station() Plan {
	return Plan{
		Grid: DefaultGrid,
		Points: []Point{
			{ID: "u0", X: 0, Y: 0, Bumper: true}, {ID: "u1", X: 300, Y: 0}, {ID: "u2", X: 610, Y: 0}, {ID: "u3", X: 910, Y: 0, Bumper: true},
			{ID: "l0", X: 0, Y: 33, Bumper: true}, {ID: "l1", X: 300, Y: 33}, {ID: "l2", X: 610, Y: 33}, {ID: "l3", X: 910, Y: 33, Bumper: true},
		},
		Tracks: []Track{
			{ID: "t1", A: "u0", B: "u1", Block: "a1"},
			{ID: "t2", A: "u2", B: "u3", Block: "b1"},
			{ID: "t3", A: "l0", B: "l1", Block: "a2"},
			{ID: "t4", A: "l2", B: "l3", Block: "b2"},
		},
		Junctions: []Junction{{ID: "x1", Kind: KindCrossover, Ends: []string{"u1", "u2", "l1", "l2"}, Switch: "turnout.t1"}},
		Blocks:    []Block{{ID: "a1", Name: "A1"}, {ID: "a2", Name: "A2"}, {ID: "b1", Name: "B1"}, {ID: "b2", Name: "B2"}},
	}
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
	for _, b := range top.Blocks {
		if b.Length != 300 || len(b.Ends) != 1 {
			t.Errorf("block %s: length %v with ends %+v, want 300 mm with one end", b.ID, b.Length, b.Ends)
		}
	}
	if got := top.Blocks[0].Ends[0]; got != (Port{Block: "a1", Point: "u1", Heading: 0}) {
		t.Errorf("A1's end = %+v, want at u1 heading east", got)
	}
	if got := top.Blocks[2].Ends[0].Heading; got != 180 {
		t.Errorf("B1's end heads %v, want 180 (west)", got)
	}

	var got []string
	for _, l := range top.Links {
		var via []string
		for _, s := range l.Turnouts {
			via = append(via, s.Junction+"="+string(s.Position)+"("+s.Switch+")")
		}
		got = append(got, l.From.Block+">"+l.To.Block+" "+strings.Join(via, ","))
	}
	want := []string{"a1>b1 x1=closed(turnout.t1)", "a1>b2 x1=thrown(turnout.t1)", "a2>b2 x1=closed(turnout.t1)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("links:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if top.Links[0].Length != 310 {
		t.Errorf("the straight way through is %v mm, want 310", top.Links[0].Length)
	}
	if l := top.Links[1].Length; l <= 310 || l > 315 {
		t.Errorf("the way across is %v mm, want a little over 310", l)
	}
}

func TestTopologyDoubleCrossover(t *testing.T) {
	p := station()
	p.Junctions[0].Kind = KindDoubleCrossover
	links := p.Topology().Links
	if len(links) != 4 {
		t.Fatalf("links = %+v, want four", links)
	}
	if links[2].From.Block != "a2" || links[2].To.Block != "b1" || links[2].Turnouts[0].Position != Thrown {
		t.Errorf("the second way across = %+v, want a2 to b1 thrown", links[2])
	}
}

// branch is a turnout at the end of block "in", leading straight on into
// block "main" and off to the right along a curve into block "siding".
func branch() Plan {
	// A #4 turnout: 126 mm straight on, the branch along 481 mm radius
	// for 15 degrees; the routes part where the lines along its ends
	// meet.
	r, a := 481.0, 15.0
	thrown := Vec{r * math.Sin(radians(a)), r * (1 - math.Cos(radians(a)))}
	at := Vec{r * math.Tan(radians(a/2)), 0}
	side := thrown.add(unit(a).scale(200))
	return Plan{
		Grid: DefaultGrid,
		Points: []Point{
			{ID: "p0", X: -200, Y: 0}, {ID: "p1", X: 0, Y: 0}, {ID: "p2", X: 126, Y: 0}, {ID: "p3", X: thrown.X, Y: thrown.Y},
			{ID: "p4", X: 400, Y: 0, Bumper: true}, {ID: "p5", X: side.X, Y: side.Y},
		},
		Tracks: []Track{
			{ID: "t1", A: "p0", B: "p1", Block: "in"},
			{ID: "t2", A: "p2", B: "p4", Block: "main"},
			{ID: "t3", A: "p3", B: "p5", Block: "siding"},
		},
		Junctions: []Junction{{ID: "w1", Kind: KindTurnout, Ends: []string{"p1", "p2", "p3"}, At: &at, Switch: "turnout.w1"}},
		Blocks:    []Block{{ID: "in", Name: "In"}, {ID: "main", Name: "Main"}, {ID: "siding", Name: "Siding"}, {ID: "spare", Name: "Spare"}},
	}
}

func TestTopologyProblems(t *testing.T) {
	p := branch()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	top := p.Topology()
	var got []string
	for _, pr := range top.Problems {
		got = append(got, pr.Kind+" "+pr.Point+pr.Block)
	}
	// p0 and p5 lead nowhere; p4 ends at a buffer stop; the turnout's
	// ends meet their track without a kink.
	want := []string{"open_end p0", "open_end p5", "block_empty spare"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("problems = %q, want %q", got, want)
	}
	if len(top.Links) != 2 || top.Links[0].To.Block != "main" || top.Links[1].To.Block != "siding" || top.Links[1].Turnouts[0].Position != Thrown {
		t.Errorf("links = %+v", top.Links)
	}
	if l := top.Links[1].Length; math.Abs(l-126) > 1 {
		t.Errorf("the branch through the turnout is %v mm, want the 126 of its curve", l)
	}

	// Bend the siding's track at the turnout and split the main line.
	p.Points[5] = Point{ID: "p5", X: p.Points[3].X + 200, Y: p.Points[3].Y}
	p.Points = append(p.Points, Point{ID: "p6", X: 600, Y: 0, Bumper: true}, Point{ID: "p7", X: 500, Y: 0})
	p.Tracks = append(p.Tracks, Track{ID: "t4", A: "p7", B: "p6", Block: "main"})
	top = p.Topology()
	got = nil
	for _, pr := range top.Problems {
		got = append(got, pr.Kind)
		if pr.Kind == ProblemKink && !strings.Contains(pr.Message, "bends by 15°") {
			t.Errorf("kink message = %q", pr.Message)
		}
	}
	if !strings.Contains(strings.Join(got, " "), "kink") || !strings.Contains(strings.Join(got, " "), "block_split") {
		t.Errorf("problems = %q, want a kink and a split block", got)
	}
}

func TestTopologyCurveHeadings(t *testing.T) {
	// A quarter circle of 216 mm radius from heading east to heading
	// south, then straight on south.
	p := Plan{
		Grid:   DefaultGrid,
		Points: []Point{{ID: "a", X: 0, Y: 0, Bumper: true}, {ID: "b", X: 216, Y: 216}, {ID: "c", X: 216, Y: 400, Bumper: true}},
		Tracks: []Track{{ID: "t1", A: "a", B: "b", Angle: 90, Block: "k"}, {ID: "t2", A: "b", B: "c"}},
		Blocks: []Block{{ID: "k", Name: "K"}},
	}
	top := p.Topology()
	if len(top.Problems) != 0 {
		t.Errorf("problems: %+v", top.Problems)
	}
	if b := top.Blocks[0]; b.Length != math.Round(216*math.Pi/2) || b.Ends[0].Heading != 90 {
		t.Errorf("block = %+v, want %v mm leaving south", b, math.Round(216*math.Pi/2))
	}
	p.Tracks[0].Angle = -90 // the same chord bowed the other way
	if top := p.Topology(); len(top.Problems) != 1 || top.Problems[0].Kind != ProblemKink {
		t.Errorf("problems = %+v, want a kink", top.Problems)
	}
}
