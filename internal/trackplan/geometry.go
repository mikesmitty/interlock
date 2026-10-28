package trackplan

import "math"

// Vec is a position or an offset on the plan, in millimeters, x
// rightward and y downward.
type Vec struct {
	X float64 `json:"x" yaml:"x"`
	Y float64 `json:"y" yaml:"y"`
}

func (a Vec) add(b Vec) Vec             { return Vec{a.X + b.X, a.Y + b.Y} }
func (a Vec) sub(b Vec) Vec             { return Vec{a.X - b.X, a.Y - b.Y} }
func (a Vec) scale(k float64) Vec       { return Vec{a.X * k, a.Y * k} }
func (a Vec) length() float64           { return math.Hypot(a.X, a.Y) }
func (a Vec) dist(b Vec) float64        { return a.sub(b).length() }
func (a Vec) lerp(b Vec, f float64) Vec { return a.add(b.sub(a).scale(f)) }

// Headings are in degrees, clockwise from rightward, as y grows
// downward: 0 is east on the plan, 90 south.

func radians(deg float64) float64 { return deg * math.Pi / 180 }
func degrees(rad float64) float64 { return rad * 180 / math.Pi }

// headingOf is the direction an offset points in.
func headingOf(v Vec) float64 { return degrees(math.Atan2(v.Y, v.X)) }

// unit is one millimeter in the direction of a heading.
func unit(heading float64) Vec {
	r := radians(heading)
	return Vec{math.Cos(r), math.Sin(r)}
}

// turnDiff is a−b brought into the range (−180, 180].
func turnDiff(a, b float64) float64 {
	d := math.Mod(a-b, 360)
	switch {
	case d > 180:
		d -= 360
	case d <= -180:
		d += 360
	}
	return d
}

// arc is track from a to b that turns by angle degrees on the way:
// positive clockwise on the plan (rightward for a train going from a to
// b), negative counterclockwise and zero for straight track.
type arc struct {
	a, b  Vec
	angle float64
}

func (t arc) straight() bool { return t.angle == 0 }

func (t arc) chord() float64 { return t.a.dist(t.b) }

// radius is the curve's radius, infinite for straight track.
func (t arc) radius() float64 {
	if t.straight() {
		return math.Inf(1)
	}
	return t.chord() / (2 * math.Sin(radians(math.Abs(t.angle))/2))
}

// length is the distance along the track.
func (t arc) length() float64 {
	if t.straight() {
		return t.chord()
	}
	return t.radius() * radians(math.Abs(t.angle))
}

// startHeading is the way a train leaving a for b sets off.
func (t arc) startHeading() float64 { return headingOf(t.b.sub(t.a)) - t.angle/2 }

// endHeading is the way it is going when it reaches b.
func (t arc) endHeading() float64 { return headingOf(t.b.sub(t.a)) + t.angle/2 }

// at is the point a fraction f of the way along.
func (t arc) at(f float64) Vec {
	if t.straight() {
		return t.a.lerp(t.b, f)
	}
	r := t.radius()
	right := 1.0
	if t.angle < 0 {
		right = -1
	}
	// The center lies to the right of the start heading on a clockwise
	// turn, to the left on a counterclockwise one.
	h := t.startHeading()
	center := t.a.add(unit(h + right*90).scale(r))
	from := headingOf(t.a.sub(center))
	return center.add(unit(from + t.angle*f).scale(r))
}

// curveLength measures a smooth curve given by a point function, such as
// a Bézier curve, by summing short chords.
func curveLength(at func(f float64) Vec) float64 {
	const steps = 48
	total := 0.0
	prev := at(0)
	for i := 1; i <= steps; i++ {
		p := at(float64(i) / steps)
		total += prev.dist(p)
		prev = p
	}
	return total
}

// quad is the quadratic Bézier curve from p0 to p2 drawn toward p1.
func quad(p0, p1, p2 Vec) func(float64) Vec {
	return func(f float64) Vec {
		return p0.lerp(p1, f).lerp(p1.lerp(p2, f), f)
	}
}

// cubic is the cubic Bézier curve from p0 to p3 with control points p1
// and p2.
func cubic(p0, p1, p2, p3 Vec) func(float64) Vec {
	return func(f float64) Vec {
		a, b, c := p0.lerp(p1, f), p1.lerp(p2, f), p2.lerp(p3, f)
		return a.lerp(b, f).lerp(b.lerp(c, f), f)
	}
}

// lineCross is where the lines through a1, a2 and through b1, b2 cross,
// or false when they are parallel.
func lineCross(a1, a2, b1, b2 Vec) (Vec, bool) {
	d1, d2 := a2.sub(a1), b2.sub(b1)
	den := d1.X*d2.Y - d1.Y*d2.X
	if math.Abs(den) < 1e-9 {
		return Vec{}, false
	}
	t := ((b1.X-a1.X)*d2.Y - (b1.Y-a1.Y)*d2.X) / den
	return a1.add(d1.scale(t)), true
}
