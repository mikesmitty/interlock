package trackplan

import (
	"fmt"
	"strings"
)

// Position is the way a turnout or crossover is set.
type Position string

// The positions. A turnout's closed route is its straight one.
const (
	Closed Position = "closed"
	Thrown Position = "thrown"
)

// The kinds of junction: where track meets other track other than end to
// end.
const (
	// KindTurnout has the ends points, closed (straight on) and thrown
	// (the branch).
	KindTurnout = "turnout"
	// KindWye has the ends points, then the left and the right leg as seen
	// from the points; closed takes the left leg.
	KindWye = "wye"
	// KindCrossing is two tracks crossing, a1 to a2 and b1 to b2, with
	// nothing to set.
	KindCrossing = "crossing"
	// KindCrossover joins two tracks side by side, a1 to a2 and b1 to b2
	// with a1 beside b1. Thrown, it takes trains from a1 across to b2.
	KindCrossover = "crossover"
	// KindDoubleCrossover is a crossover both ways: thrown, a1 leads to
	// b2 and b1 to a2.
	KindDoubleCrossover = "double_crossover"
)

// route is a way through a junction between two of its ends, and the
// position it needs, if any.
type route struct {
	from, to int
	position Position
}

type kindInfo struct {
	name   string
	ends   int
	routes []route
	// switches is true for kinds a switch machine sets.
	switches bool
	// center is true for kinds drawn through the point where their
	// routes part, which the junction keeps.
	center bool
}

var junctionKinds = map[string]kindInfo{
	KindTurnout: {name: "turnout", ends: 3, switches: true, center: true,
		routes: []route{{0, 1, Closed}, {0, 2, Thrown}}},
	KindWye: {name: "wye", ends: 3, switches: true, center: true,
		routes: []route{{0, 1, Closed}, {0, 2, Thrown}}},
	KindCrossing: {name: "crossing", ends: 4,
		routes: []route{{0, 1, ""}, {2, 3, ""}}},
	KindCrossover: {name: "crossover", ends: 4, switches: true,
		routes: []route{{0, 1, Closed}, {2, 3, Closed}, {0, 3, Thrown}}},
	KindDoubleCrossover: {name: "double crossover", ends: 4, switches: true,
		routes: []route{{0, 1, Closed}, {2, 3, Closed}, {0, 3, Thrown}, {2, 1, Thrown}}},
}

// describe names a junction for people: its kind and where it is.
func (j Junction) describe(points map[string]Point) string {
	name := junctionKinds[j.Kind].name
	if name == "" {
		name = "junction"
	}
	name = strings.ToUpper(name[:1]) + name[1:]
	at := j.At
	if at == nil && len(j.Ends) > 0 {
		if pt, ok := points[j.Ends[0]]; ok {
			at = &Vec{pt.X, pt.Y}
		}
	}
	if at == nil {
		return name + " " + j.ID
	}
	return fmt.Sprintf("%s at %.0f, %.0f mm", name, at.X, at.Y)
}

// partner is the end at the other end of the same track in a crossing or
// crossover: a1 with a2, b1 with b2.
func partner(end int) int { return end ^ 1 }

// junctionShape is a junction placed on the plan: where its ends are and
// where its routes part.
type junctionShape struct {
	kind   string
	ends   []Vec
	center Vec
}

func newJunctionShape(j Junction, ends []Vec) junctionShape {
	s := junctionShape{kind: j.Kind, ends: ends}
	switch {
	case j.At != nil:
		s.center = *j.At
	case len(ends) == 4:
		if c, ok := lineCross(ends[0], ends[1], ends[2], ends[3]); ok {
			s.center = c
		} else {
			s.center = ends[0].lerp(ends[3], 0.5)
		}
	}
	return s
}

// curve is the line a route is drawn along, from its from end to its to
// end.
func (s junctionShape) curve(r route) func(float64) Vec {
	from, to := s.ends[r.from], s.ends[r.to]
	switch s.kind {
	case KindTurnout, KindWye:
		return quad(from, s.center, to)
	case KindCrossover, KindDoubleCrossover:
		if r.to != partner(r.from) {
			// Across from one track to the other, leaving and joining
			// each along its own track.
			return cubic(from, from.lerp(s.ends[partner(r.from)], 0.5), to.lerp(s.ends[partner(r.to)], 0.5), to)
		}
	}
	return func(f float64) Vec { return from.lerp(to, f) }
}

// routeLength is how far a train goes along a route.
func (s junctionShape) routeLength(r route) float64 { return curveLength(s.curve(r)) }

// outward is the heading track leaves the junction by at an end.
func (s junctionShape) outward(end int) float64 {
	switch s.kind {
	case KindCrossover, KindDoubleCrossover:
		return headingOf(s.ends[end].sub(s.ends[partner(end)]))
	}
	return headingOf(s.ends[end].sub(s.center))
}
