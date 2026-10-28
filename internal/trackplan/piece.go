// The grid of cells that plans were drawn on before they were to scale:
// a cell's track is the list of directions it leaves the cell by, through
// the middle of a side or through a corner, and the piece follows from
// that list. OpenFile reads such plans and converts them (legacy.go).

package trackplan

import (
	"fmt"
	"slices"
)

// Direction is one of the eight ways out of a cell, clockwise from north.
// The sides are north, east, south and west; the corners lie between them.
type Direction uint8

// The directions. North is up on the diagram.
const (
	North Direction = iota
	NorthEast
	East
	SouthEast
	South
	SouthWest
	West
	NorthWest
)

var directionNames = [...]string{"n", "ne", "e", "se", "s", "sw", "w", "nw"}

func (d Direction) String() string {
	if int(d) < len(directionNames) {
		return directionNames[d]
	}
	return fmt.Sprintf("Direction(%d)", d)
}

// ParseDirection reads a direction's short name: n, ne, e, se, s, sw, w
// or nw.
func ParseDirection(s string) (Direction, error) {
	for i, name := range directionNames {
		if name == s {
			return Direction(i), nil
		}
	}
	return 0, fmt.Errorf("%q is not a direction; use n, ne, e, se, s, sw, w or nw", s)
}

// MarshalText writes the short name.
func (d Direction) MarshalText() ([]byte, error) {
	if int(d) >= len(directionNames) {
		return nil, fmt.Errorf("no direction %d", d)
	}
	return []byte(directionNames[d]), nil
}

// UnmarshalText reads the short name.
func (d *Direction) UnmarshalText(text []byte) error {
	parsed, err := ParseDirection(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Opposite is the direction back.
func (d Direction) Opposite() Direction { return d.Turn(4) }

// Turn returns the direction steps eighths of a turn clockwise from d;
// negative steps turn counterclockwise.
func (d Direction) Turn(steps int) Direction {
	return Direction(((int(d)+steps)%8 + 8) % 8)
}

// Step is the offset to the neighboring cell in direction d. Y grows
// southward, as on the screen.
func (d Direction) Step() (dx, dy int) {
	switch d {
	case North:
		return 0, -1
	case NorthEast:
		return 1, -1
	case East:
		return 1, 0
	case SouthEast:
		return 1, 1
	case South:
		return 0, 1
	case SouthWest:
		return -1, 1
	case West:
		return -1, 0
	default:
		return -1, -1
	}
}

// apart is how many eighths of a turn separate two directions, 0 to 4.
func apart(a, b Direction) int {
	n := (int(b) - int(a) + 8) % 8
	if n > 4 {
		n = 8 - n
	}
	return n
}

// Piece is what a cell's track is.
type Piece string

// The pieces.
const (
	Bumper       Piece = "bumper"        // one way out: the end of the track
	Straight     Piece = "straight"      // in one side, out the opposite one
	Curve        Piece = "curve"         // a bend of 45 degrees
	SharpCurve   Piece = "sharp_curve"   // a bend of 90 degrees
	TurnoutLeft  Piece = "turnout_left"  // straight on, or branching left
	TurnoutRight Piece = "turnout_right" // straight on, or branching right
	Wye          Piece = "wye"           // branching left or right
	Crossing     Piece = "crossing"      // two tracks crossing at 90 degrees
	Crossing45   Piece = "crossing_45"   // two tracks crossing at 45 degrees
)

// Switches reports whether the piece has a turnout to set.
func (p Piece) Switches() bool {
	return p == TurnoutLeft || p == TurnoutRight || p == Wye
}

// Path is a way through a piece between two of its ends, and the turnout
// position it needs, if any.
type Path struct {
	A, B     Direction
	Position Position
}

// Shape is a classified cell of track.
type Shape struct {
	Piece Piece
	// Points is a turnout's or wye's single end; trains branch going away
	// from it. ClosedLeg and ThrownLeg are the ends the closed and thrown
	// positions lead to: straight on and the branch for a turnout, the
	// left and right legs for a wye.
	Points, ClosedLeg, ThrownLeg Direction
	Paths                        []Path
}

// Exit returns where a train that enters the piece through end in can
// leave it, one path for each way.
func (s Shape) Exit(in Direction) []Path {
	var out []Path
	for _, p := range s.Paths {
		switch in {
		case p.A:
			out = append(out, p)
		case p.B:
			out = append(out, Path{A: p.B, B: p.A, Position: p.Position})
		}
	}
	return out
}

// Classify works out the piece that leaves a cell by the given
// directions, in any order.
func Classify(exits []Direction) (Shape, error) {
	var mask uint8
	for _, d := range exits {
		if d > NorthWest {
			return Shape{}, fmt.Errorf("no direction %d", d)
		}
		if mask&(1<<d) != 0 {
			return Shape{}, fmt.Errorf("the direction %s is listed twice", d)
		}
		mask |= 1 << d
	}
	has := func(d Direction) bool { return mask&(1<<d) != 0 }
	sorted := slices.Clone(exits)
	slices.Sort(sorted)

	switch len(sorted) {
	case 0:
		return Shape{}, fmt.Errorf("track needs at least one way out of its cell")
	case 1:
		return Shape{Piece: Bumper}, nil
	case 2:
		a, b := sorted[0], sorted[1]
		path := []Path{{A: a, B: b}}
		switch apart(a, b) {
		case 4:
			return Shape{Piece: Straight, Paths: path}, nil
		case 3:
			return Shape{Piece: Curve, Paths: path}, nil
		case 2:
			return Shape{Piece: SharpCurve, Paths: path}, nil
		}
		return Shape{}, fmt.Errorf("track can't turn as sharply as %s to %s", a, b)
	case 3:
		for _, points := range sorted {
			ahead := points.Opposite()
			if has(ahead) {
				for _, branch := range sorted {
					if branch == points || branch == ahead {
						continue
					}
					switch branch {
					case ahead.Turn(-1):
						return turnout(TurnoutLeft, points, ahead, branch), nil
					case ahead.Turn(1):
						return turnout(TurnoutRight, points, ahead, branch), nil
					}
				}
			}
			if has(ahead.Turn(-1)) && has(ahead.Turn(1)) {
				return turnout(Wye, points, ahead.Turn(-1), ahead.Turn(1)), nil
			}
		}
		return Shape{}, fmt.Errorf("three tracks meeting at %s, %s and %s are not a turnout", sorted[0], sorted[1], sorted[2])
	case 4:
		first := sorted[0]
		var second Direction
		found := false
		for _, d := range sorted {
			if d != first && d != first.Opposite() {
				second, found = d, true
				break
			}
		}
		if has(first.Opposite()) && found && has(second.Opposite()) {
			paths := []Path{{A: first, B: first.Opposite()}, {A: second, B: second.Opposite()}}
			if apart(first, second) == 2 {
				return Shape{Piece: Crossing, Paths: paths}, nil
			}
			return Shape{Piece: Crossing45, Paths: paths}, nil
		}
		return Shape{}, fmt.Errorf("four tracks meeting in a cell must be two straight ones crossing")
	}
	return Shape{}, fmt.Errorf("at most four tracks can meet in a cell")
}

func turnout(piece Piece, points, closed, thrown Direction) Shape {
	return Shape{
		Piece:  piece,
		Points: points, ClosedLeg: closed, ThrownLeg: thrown,
		Paths: []Path{{A: points, B: closed, Position: Closed}, {A: points, B: thrown, Position: Thrown}},
	}
}
