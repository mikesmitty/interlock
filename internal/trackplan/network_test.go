package trackplan

import (
	"errors"
	"math"
	"testing"
)

// turnoutPlan is a #4 turnout at p2 with track on all three ends: t1 to
// its points, t2 on from its closed end and t3 from its thrown end, which
// ends at a buffer stop.
func turnoutPlan() Plan {
	return Plan{
		Points: []Point{
			{ID: "p1", X: 0, Y: 0}, {ID: "p2", X: 248, Y: 0}, {ID: "p3", X: 374, Y: 0},
			{ID: "p4", X: 372.49, Y: 16.39}, {ID: "p5", X: 496.98, Y: 32.8, Bumper: true}, {ID: "p6", X: 622, Y: 0},
		},
		Tracks: []Track{
			{ID: "t1", A: "p1", B: "p2", Block: "a"},
			{ID: "t2", A: "p3", B: "p6", Block: "b"},
			{ID: "t3", A: "p4", B: "p5", Angle: -15},
		},
		Junctions: []Junction{{ID: "j1", Kind: KindTurnout, Ends: []string{"p2", "p3", "p4"}, At: &Vec{311.32, 0}, Switch: "turnout.x.servo_1"}},
		Grid:      DefaultGrid,
	}
}

func TestNetworkFollowsTheTurnout(t *testing.T) {
	n := turnoutPlan().Network()
	set := func(p Position) func(string) Position { return func(string) Position { return p } }
	t1 := Leg{Piece: "t1"}

	tests := []struct {
		name string
		from Leg
		set  Position
		want Leg
		err  error
	}{
		{"facing the points, closed", t1, Closed, Leg{Piece: "j1", Junction: true, Route: 0}, nil},
		{"facing the points, thrown", t1, Thrown, Leg{Piece: "j1", Junction: true, Route: 1}, nil},
		{"out of the closed route", Leg{Piece: "j1", Junction: true, Route: 0}, Closed, Leg{Piece: "t2"}, nil},
		{"trailing the closed end, closed", Leg{Piece: "t2", Reverse: true}, Closed, Leg{Piece: "j1", Junction: true, Route: 0, Reverse: true}, nil},
		{"to the buffer stop", Leg{Piece: "t3"}, Closed, Leg{}, ErrTrackEnds},
		{"off the end of t2", Leg{Piece: "t2"}, Closed, Leg{}, ErrTrackEnds},
	}
	for _, tt := range tests {
		got, err := n.Next(tt.from, set(tt.set))
		if !errors.Is(err, tt.err) || got != tt.want {
			t.Errorf("%s: Next = %+v, %v; want %+v, %v", tt.name, got, err, tt.want, tt.err)
		}
	}

	// Trailing the closed end while the turnout is thrown, or while it is
	// moving, has no way on.
	for _, p := range []Position{Thrown, ""} {
		_, err := n.Next(Leg{Piece: "t2", Reverse: true}, set(p))
		var against *SetAgainstError
		if !errors.As(err, &against) || against.Junction != "j1" || against.Position != p {
			t.Errorf("trailing closed end with the turnout %q: err = %v", p, err)
		}
	}
	if n.Switch("j1") != "turnout.x.servo_1" || n.Block(t1) != "a" || n.Block(Leg{Piece: "j1", Junction: true}) != "" {
		t.Error("Switch or Block wrong")
	}
	if legs := n.Legs("b"); len(legs) != 1 || legs[0].Piece != "t2" {
		t.Errorf("Legs(b) = %v", legs)
	}
}

func TestNetworkMeasuresAndPlaces(t *testing.T) {
	n := turnoutPlan().Network()
	t1 := Leg{Piece: "t1"}
	if l := n.Length(t1); l != 248 {
		t.Errorf("t1 length = %v", l)
	}
	if at := n.At(t1, 48); at != (Vec{48, 0}) {
		t.Errorf("48 mm along t1 = %v", at)
	}
	if at := n.At(t1.Reversed(), 48); at != (Vec{200, 0}) {
		t.Errorf("48 mm back along t1 = %v", at)
	}
	closed, thrown := Leg{Piece: "j1", Junction: true}, Leg{Piece: "j1", Junction: true, Route: 1}
	if l := n.Length(closed); math.Abs(l-126) > 0.01 {
		t.Errorf("closed route = %v mm", l)
	}
	if l := n.Length(thrown); l <= 124.49 || l > 128 {
		t.Errorf("thrown route = %v mm", l)
	}
	if end := n.At(thrown, n.Length(thrown)); math.Abs(end.X-372.49) > 0.01 || math.Abs(end.Y-16.39) > 0.01 {
		t.Errorf("thrown route ends at %v", end)
	}
	if n.Valid(Leg{Piece: "j1"}) || n.Valid(Leg{Piece: "j1", Junction: true, Route: 2}) {
		t.Error("a junction taken as a track, or a route it doesn't have, is valid")
	}
}

func TestWayFollowsEachLink(t *testing.T) {
	for _, kind := range []string{KindCrossover, KindDoubleCrossover} {
		p := station()
		p.Junctions[0].Kind = kind
		n := p.Network()
		for _, l := range p.Topology().Links {
			for _, way := range []Link{l, l.reversed()} {
				legs, err := n.Way(way.From, way.To, way.Turnouts)
				if err != nil {
					t.Errorf("%s: %s to %s: %v", kind, way.From.Block, way.To.Block, err)
					continue
				}
				length := 0.0
				for _, leg := range legs {
					length += n.Length(leg)
				}
				if len(legs) != 1 || !legs[0].Junction || math.Round(length) != way.Length {
					t.Errorf("%s: %s to %s covers %+v, %v mm; want the crossover, %v mm", kind, way.From.Block, way.To.Block, legs, length, way.Length)
				}
			}
		}
	}

	// From A1 east with the crossover thrown is B2, not B1.
	n := station().Network()
	thrown := []Setting{{Junction: "x1", Position: Thrown}}
	if _, err := n.Way(Port{Block: "a1", Point: "u1"}, Port{Block: "b1", Point: "u2"}, thrown); err == nil {
		t.Error("A1 to B1 with the crossover thrown has a way")
	}
	if legs, err := n.Way(Port{Block: "a1", Point: "u1"}, Port{Block: "b2", Point: "l2"}, thrown); err != nil || len(legs) != 1 || legs[0].Route != 2 {
		t.Errorf("A1 to B2 thrown = %+v, %v", legs, err)
	}
	// Blocks that meet end to end have nothing between them.
	p := Plan{
		Grid:   DefaultGrid,
		Points: []Point{{ID: "p1", X: 0, Y: 0, Bumper: true}, {ID: "p2", X: 100, Y: 0}, {ID: "p3", X: 200, Y: 0, Bumper: true}},
		Tracks: []Track{{ID: "t1", A: "p1", B: "p2", Block: "a"}, {ID: "t2", A: "p2", B: "p3", Block: "b"}},
		Blocks: []Block{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}},
	}
	if legs, err := p.Network().Way(Port{Block: "a", Point: "p2"}, Port{Block: "b", Point: "p2"}, nil); err != nil || len(legs) != 0 {
		t.Errorf("A to B end to end = %+v, %v", legs, err)
	}
}

func TestFouls(t *testing.T) {
	x := func(route int) Leg { return Leg{Piece: "x1", Junction: true, Route: route} }
	tests := []struct {
		name string
		kind string
		a, b Leg
		want bool
	}{
		{"one track, either way", KindCrossover, Leg{Piece: "t1"}, Leg{Piece: "t1", Reverse: true}, true},
		{"two tracks", KindCrossover, Leg{Piece: "t1"}, Leg{Piece: "t2"}, false},
		{"a crossover's two straight tracks", KindCrossover, x(0), x(1), false},
		{"across a crossover and straight on", KindCrossover, x(2), x(0), true},
		{"a double crossover's straight tracks", KindDoubleCrossover, x(0), x(1), false},
		{"a double crossover both ways across", KindDoubleCrossover, x(2), x(3), true},
		{"a crossing's two tracks", KindCrossing, x(0), x(1), true},
	}
	for _, tt := range tests {
		p := station()
		p.Junctions[0].Kind = tt.kind
		if tt.kind == KindCrossing {
			p.Junctions[0].Switch = ""
		}
		if got := p.Network().Fouls(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: Fouls = %v, want %v", tt.name, got, tt.want)
		}
	}
}
