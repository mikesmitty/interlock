package trackplan

import (
	"fmt"
	"slices"
)

// LegacyCell is the size a cell of the old grid becomes when a plan
// drawn on it is converted: the spacing of Kato Unitrack N double track,
// the grid most such plans were drawn to.
const LegacyCell = 33

// legacyPlan is a plan as saved before plans were to scale: tiles on a
// grid of cells.
type legacyPlan struct {
	Tiles      []legacyTile      `yaml:"tiles"`
	Blocks     []Block           `yaml:"blocks"`
	Stations   []Station         `yaml:"stations"`
	Background *legacyBackground `yaml:"background"`
}

type legacyTile struct {
	X        int         `yaml:"x"`
	Y        int         `yaml:"y"`
	Track    []Direction `yaml:"track"`
	Platform []Direction `yaml:"platform"`
	Label    string      `yaml:"label"`
	Turnout  string      `yaml:"turnout"`
	Block    string      `yaml:"block"`
}

// legacyBackground placed its image in cells.
type legacyBackground struct {
	Image     string  `yaml:"image"`
	X         float64 `yaml:"x"`
	Y         float64 `yaml:"y"`
	Width     float64 `yaml:"width"`
	Height    float64 `yaml:"height"`
	Opacity   float64 `yaml:"opacity"`
	Operating bool    `yaml:"operating"`
	Invert    bool    `yaml:"invert"`
	Rotation  float64 `yaml:"rotation"`
}

// headingOfDirection is a grid direction as a heading.
func headingOfDirection(d Direction) float64 {
	return turnDiff(float64(d)*45-90, 0)
}

// convert turns a grid plan into a plan to scale, each cell cell
// millimeters across. Track becomes track between the middles of the
// cells' sides and corners, so neighboring cells share their points.
func (lp legacyPlan) convert(cell float64) Plan {
	p := Plan{Blocks: lp.Blocks, Stations: lp.Stations, Grid: cell}
	points := map[[2]int]string{}
	// exit is the point where track leaves a cell, kept in half cells so
	// neighbors find the same one.
	exit := func(t legacyTile, d Direction) string {
		dx, dy := d.Step()
		key := [2]int{2*t.X + 1 + dx, 2*t.Y + 1 + dy}
		if id, ok := points[key]; ok {
			return id
		}
		id := fmt.Sprintf("p%d", len(p.Points)+1)
		points[key] = id
		p.Points = append(p.Points, Point{ID: id, X: float64(key[0]) * cell / 2, Y: float64(key[1]) * cell / 2})
		return id
	}
	middle := func(t legacyTile) Vec { return Vec{(float64(t.X) + 0.5) * cell, (float64(t.Y) + 0.5) * cell} }
	addTrack := func(a, b string, angle float64, block string) {
		p.Tracks = append(p.Tracks, Track{ID: fmt.Sprintf("t%d", len(p.Tracks)+1), A: a, B: b, Angle: angle, Block: block})
	}
	addJunction := func(kind string, ends []string, at *Vec, t legacyTile) {
		p.Junctions = append(p.Junctions, Junction{ID: fmt.Sprintf("j%d", len(p.Junctions)+1), Kind: kind, Ends: ends, At: at, Switch: t.Turnout, Block: t.Block})
	}

	tiles := slices.Clone(lp.Tiles)
	slices.SortFunc(tiles, func(a, b legacyTile) int {
		if a.Y != b.Y {
			return a.Y - b.Y
		}
		return a.X - b.X
	})
	for _, t := range tiles {
		switch {
		case len(t.Track) > 0:
			shape, err := Classify(t.Track)
			if err != nil {
				continue
			}
			switch shape.Piece {
			case Bumper:
				// The track ran from the cell's side to a buffer stop in
				// its middle.
				id := fmt.Sprintf("p%d", len(p.Points)+1)
				m := middle(t)
				p.Points = append(p.Points, Point{ID: id, X: m.X, Y: m.Y, Bumper: true})
				addTrack(exit(t, t.Track[0]), id, 0, t.Block)
			case Straight, Curve, SharpCurve:
				a, b := shape.Paths[0].A, shape.Paths[0].B
				angle := turnDiff(headingOfDirection(b), headingOfDirection(a)+180)
				addTrack(exit(t, a), exit(t, b), angle, t.Block)
			case TurnoutLeft, TurnoutRight, Wye:
				kind := KindTurnout
				if shape.Piece == Wye {
					kind = KindWye
				}
				m := middle(t)
				addJunction(kind, []string{exit(t, shape.Points), exit(t, shape.ClosedLeg), exit(t, shape.ThrownLeg)}, &m, t)
			case Crossing, Crossing45:
				a, b := shape.Paths[0], shape.Paths[1]
				addJunction(KindCrossing, []string{exit(t, a.A), exit(t, a.B), exit(t, b.A), exit(t, b.B)}, nil, t)
			}
		case len(t.Platform) > 0:
			m := middle(t)
			p.Platforms = append(p.Platforms, Platform{ID: fmt.Sprintf("f%d", len(p.Platforms)+1), X: m.X, Y: m.Y, Length: cell, Width: cell / 2, Angle: headingOfDirection(t.Platform[0])})
		case t.Label != "":
			p.Labels = append(p.Labels, Label{ID: fmt.Sprintf("l%d", len(p.Labels)+1), X: (float64(t.X) + 0.1) * cell, Y: (float64(t.Y) + 0.5) * cell, Text: t.Label})
		}
	}
	if b := lp.Background; b != nil {
		p.Background = &Background{
			Image: b.Image, X: b.X * cell, Y: b.Y * cell, Width: b.Width * cell, Height: b.Height * cell,
			Opacity: b.Opacity, Operating: b.Operating, Invert: b.Invert, Rotation: b.Rotation,
		}
	}
	return p
}
