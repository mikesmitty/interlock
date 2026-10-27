package trackplan

import (
	"encoding/json"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		exits  []Direction
		piece  Piece
		points Direction
		closed Direction
		thrown Direction
		paths  int
	}{
		{"end of track", []Direction{East}, Bumper, 0, 0, 0, 0},
		{"straight across", []Direction{West, East}, Straight, 0, 0, 0, 1},
		{"straight diagonal", []Direction{SouthWest, NorthEast}, Straight, 0, 0, 0, 1},
		{"45 degree curve", []Direction{West, NorthEast}, Curve, 0, 0, 0, 1},
		{"90 degree curve", []Direction{West, South}, SharpCurve, 0, 0, 0, 1},
		{"left turnout heading east", []Direction{West, East, NorthEast}, TurnoutLeft, West, East, NorthEast, 2},
		{"right turnout heading east", []Direction{East, SouthEast, West}, TurnoutRight, West, East, SouthEast, 2},
		{"right turnout heading west", []Direction{East, West, NorthWest}, TurnoutRight, East, West, NorthWest, 2},
		{"left turnout on a diagonal", []Direction{SouthWest, NorthEast, North}, TurnoutLeft, SouthWest, NorthEast, North, 2},
		{"wye", []Direction{West, NorthEast, SouthEast}, Wye, West, NorthEast, SouthEast, 2},
		{"90 degree crossing", []Direction{North, East, South, West}, Crossing, 0, 0, 0, 2},
		{"diagonal 90 degree crossing", []Direction{NorthEast, SouthEast, SouthWest, NorthWest}, Crossing, 0, 0, 0, 2},
		{"45 degree crossing", []Direction{West, East, NorthEast, SouthWest}, Crossing45, 0, 0, 0, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shape, err := Classify(tt.exits)
			if err != nil {
				t.Fatalf("Classify(%v): %v", tt.exits, err)
			}
			if shape.Piece != tt.piece {
				t.Fatalf("piece = %s, want %s", shape.Piece, tt.piece)
			}
			if len(shape.Paths) != tt.paths {
				t.Errorf("paths = %v, want %d", shape.Paths, tt.paths)
			}
			if !tt.piece.Switches() {
				return
			}
			if shape.Points != tt.points || shape.ClosedLeg != tt.closed || shape.ThrownLeg != tt.thrown {
				t.Errorf("points %s closed %s thrown %s, want %s %s %s", shape.Points, shape.ClosedLeg, shape.ThrownLeg, tt.points, tt.closed, tt.thrown)
			}
		})
	}
}

func TestClassifyRejects(t *testing.T) {
	tests := []struct {
		name  string
		exits []Direction
	}{
		{"nothing", nil},
		{"too sharp", []Direction{East, NorthEast}},
		{"listed twice", []Direction{East, East}},
		{"a T", []Direction{West, East, North}},
		{"three corners", []Direction{NorthEast, SouthEast, North}},
		{"a turnout and a branch", []Direction{West, East, NorthEast, SouthEast}},
		{"five", []Direction{North, NorthEast, East, South, West}},
		{"no such direction", []Direction{8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if shape, err := Classify(tt.exits); err == nil {
				t.Errorf("Classify(%v) = %s, want an error", tt.exits, shape.Piece)
			}
		})
	}
}

func TestShapeExit(t *testing.T) {
	shape, err := Classify([]Direction{West, East, SouthEast})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in   Direction
		want []Path
	}{
		{West, []Path{{West, East, Closed}, {West, SouthEast, Thrown}}},
		{East, []Path{{East, West, Closed}}},
		{SouthEast, []Path{{SouthEast, West, Thrown}}},
		{North, nil},
	}
	for _, tt := range tests {
		got := shape.Exit(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("Exit(%s) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("Exit(%s) = %v, want %v", tt.in, got, tt.want)
			}
		}
	}
}

func TestDirection(t *testing.T) {
	if got := West.Opposite(); got != East {
		t.Errorf("West.Opposite() = %s", got)
	}
	if got := North.Turn(-1); got != NorthWest {
		t.Errorf("North.Turn(-1) = %s", got)
	}
	if got := NorthWest.Turn(3); got != East {
		t.Errorf("NorthWest.Turn(3) = %s", got)
	}
	if dx, dy := SouthWest.Step(); dx != -1 || dy != 1 {
		t.Errorf("SouthWest.Step() = %d,%d", dx, dy)
	}
	body, err := json.Marshal([]Direction{North, SouthEast})
	if err != nil || string(body) != `["n","se"]` {
		t.Errorf("json = %s, %v", body, err)
	}
	var back []Direction
	if err := json.Unmarshal([]byte(`["nw","s"]`), &back); err != nil || back[0] != NorthWest || back[1] != South {
		t.Errorf("parsed %v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`["north"]`), &back); err == nil {
		t.Error("parsed north, want an error")
	}
}
