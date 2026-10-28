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
