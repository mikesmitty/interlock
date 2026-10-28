package driving

import (
	"cmp"
	"slices"

	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// maxLegs bounds a walk through a block, for a block that is a loop.
const maxLegs = 1000

// endMargin is how far short of the end of the track a train may use a
// train stops when no marker says where, in millimeters: room for the
// estimate to be out without fouling what lies beyond.
const endMargin = 50.0

// course is the track a train runs over on a drive, from where its
// leading end was when the drive began: the legs in order, each taken
// the way the train goes.
type course struct {
	legs []trackplan.Leg
	// at[i] is how far along the course legs[i] starts; the first starts
	// behind it, where its leg does.
	at []float64
	// routes are the routes the course runs over, with how far along it
	// each one's track starts and ends.
	routes []span
	// end is where the track the train may use ends: the far end of the
	// last block the course reaches.
	end float64
	// stop is where the leading end is to stand, and marker the stop
	// marker there, if any.
	stop   float64
	marker string
}

type span struct {
	route      string
	start, end float64
}

// add puts legs on the end of the course.
func (c *course) add(n *trackplan.Network, legs ...trackplan.Leg) {
	for _, leg := range legs {
		if len(c.legs) == 0 {
			c.at = append(c.at, 0)
		} else {
			last := len(c.legs) - 1
			c.at = append(c.at, c.at[last]+n.Length(c.legs[last]))
		}
		c.legs = append(c.legs, leg)
	}
	if last := len(c.legs) - 1; last >= 0 {
		c.end = c.at[last] + n.Length(c.legs[last])
	}
}

// locate finds the leading end, at d along leg, on the course from the
// leg it was last found on: how far along the course it is and the leg
// it is on.
func (c *course) locate(leg trackplan.Leg, d float64, from int) (float64, int, bool) {
	for i := max(from, 0); i < len(c.legs); i++ {
		if c.legs[i] == leg {
			return c.at[i] + d, i, true
		}
	}
	return 0, 0, false
}

// trim drops legs well behind the leading end, on leg i, for a course
// that keeps growing; it returns where that leg is now.
func (c *course) trim(i int) int {
	const keep = 8
	if i <= keep*2 {
		return i
	}
	drop := i - keep
	c.legs, c.at = slices.Clone(c.legs[drop:]), slices.Clone(c.at[drop:])
	return i - drop
}

// planner finds ways over the routes the interlocking sets, with the
// junctions not on a route as they are now.
type planner struct {
	net      *trackplan.Network
	routes   []interlocking.Route
	position func(junction string) trackplan.Position
}

// overlay is position with a route's turnouts as the route sets them.
func (p *planner) overlay(settings []trackplan.Setting) func(string) trackplan.Position {
	return func(j string) trackplan.Position {
		if i := slices.IndexFunc(settings, func(s trackplan.Setting) bool { return s.Junction == j }); i >= 0 {
			return settings[i].Position
		}
		return p.position(j)
	}
}

// walk follows the track from start through its block, with the
// junctions in it as they are, to where the track leaves the block or
// ends.
func (p *planner) walk(start trackplan.Leg) []trackplan.Leg {
	block := p.net.Block(start)
	legs := []trackplan.Leg{start}
	for cur := start; len(legs) < maxLegs; {
		next, err := p.net.Next(cur, p.position)
		if err != nil || p.net.Block(next) != block || next == start {
			break
		}
		legs = append(legs, next)
		cur = next
	}
	return legs
}

// enters reports whether route r leads on from last, the last leg in its
// block before the track leaves it, and if so the first leg it leads
// into in the next block.
func (p *planner) enters(last trackplan.Leg, r interlocking.Route) (trackplan.Leg, bool) {
	position := p.overlay(r.Turnouts)
	next, err := p.net.Next(last, position)
	if err != nil {
		return trackplan.Leg{}, false
	}
	entry := next
	if len(r.Legs) > 0 {
		if next != r.Legs[0] {
			return trackplan.Leg{}, false
		}
		if entry, err = p.net.Next(r.Legs[len(r.Legs)-1], position); err != nil {
			return trackplan.Leg{}, false
		}
	}
	return entry, p.net.Block(entry) == r.To.Block
}

// hop is one route on a way being searched for: the route, the first
// leg it leads into in its block, and the hop before it.
type hop struct {
	route int
	entry trackplan.Leg
	prev  int
}

// way finds the fewest routes from the block the walk walked through to
// the block to, the shortest of those if there are several; nil when
// to is that block.
func (p *planner) way(walked []trackplan.Leg, to string) ([]hop, bool) {
	from := p.net.Block(walked[0])
	if from == to {
		return nil, true
	}
	var hops []hop
	seen := map[int]bool{}
	starts := func(last trackplan.Leg, block, enteredAt string, prev int) []int {
		var out []int
		for i, r := range p.routes {
			if r.From.Block != block || r.From.Point == enteredAt || seen[i] {
				continue
			}
			if entry, ok := p.enters(last, r); ok {
				seen[i] = true
				hops = append(hops, hop{route: i, entry: entry, prev: prev})
				out = append(out, len(hops)-1)
			}
		}
		return out
	}
	length := func(h int) float64 {
		total := 0.0
		for ; h >= 0; h = hops[h].prev {
			total += p.routes[hops[h].route].Length
		}
		return total
	}
	queue := starts(walked[len(walked)-1], from, "", -1)
	for len(queue) > 0 {
		var reached []int
		for _, h := range queue {
			if p.routes[hops[h].route].To.Block == to {
				reached = append(reached, h)
			}
		}
		if len(reached) > 0 {
			best := slices.MinFunc(reached, func(a, b int) int { return cmp.Compare(length(a), length(b)) })
			var out []hop
			for h := best; h >= 0; h = hops[h].prev {
				out = append(out, hops[h])
			}
			slices.Reverse(out)
			return out, true
		}
		var next []int
		for _, h := range queue {
			r := p.routes[hops[h].route]
			through := p.walk(hops[h].entry)
			next = append(next, starts(through[len(through)-1], r.To.Block, r.To.Point, h)...)
		}
		queue = next
	}
	return nil, false
}

// build lays out the course from the leading end, at d along the first
// leg walked, over the routes of a way: the track through the block it
// is in, then each route and the block it leads into.
func (p *planner) build(d float64, walked []trackplan.Leg, way []hop) *course {
	c := &course{}
	c.add(p.net, walked...)
	for i := range c.at {
		c.at[i] -= d
	}
	c.end -= d
	for _, h := range way {
		c.follow(p, p.routes[h.route], h.entry)
	}
	return c
}

// follow puts a route on the end of the course, and the track through
// the block it leads into from entry.
func (c *course) follow(p *planner, r interlocking.Route, entry trackplan.Leg) {
	start := c.end
	c.add(p.net, r.Legs...)
	c.routes = append(c.routes, span{route: r.ID, start: start, end: c.end})
	c.add(p.net, p.walk(entry)...)
}

// stopAt chooses where on the course a train of the length given stands
// in block to: at the marker named, else at a marker there for trains
// running its way, the one for the shortest trains it fits, else the one
// for the longest; with no marker, short of where the track it may use
// ends.
func (c *course) stopAt(n *trackplan.Network, markers []trackplan.Marker, to string, length float64, named string) bool {
	type candidate struct {
		id     string
		at     float64
		length float64
	}
	var found []candidate
	for i, leg := range c.legs {
		if leg.Junction || n.Block(leg) != to {
			continue
		}
		for _, m := range markers {
			if m.Track != leg.Piece || m.Reverse != leg.Reverse {
				continue
			}
			along := m.At
			if leg.Reverse {
				along = n.Length(leg) - m.At
			}
			if at := c.at[i] + along; at > minRun {
				found = append(found, candidate{m.ID, at, m.Length})
			}
		}
	}
	if named != "" {
		i := slices.IndexFunc(found, func(m candidate) bool { return m.id == named })
		if i < 0 {
			return false
		}
		c.stop, c.marker = found[i].at, found[i].id
		return true
	}
	fits := func(m candidate) bool { return m.length == 0 || m.length >= length }
	best := -1
	for i, m := range found {
		switch {
		case best < 0:
			best = i
		case fits(m) != fits(found[best]):
			if fits(m) {
				best = i
			}
		case fits(m):
			// The one for the shortest trains, a marker for any train
			// last; the nearer of two alike.
			if (m.length != 0 && (found[best].length == 0 || m.length < found[best].length)) || (m.length == found[best].length && m.at < found[best].at) {
				best = i
			}
		case m.length > found[best].length:
			best = i
		}
	}
	if best >= 0 {
		c.stop, c.marker = found[best].at, found[best].id
		return true
	}
	c.stop = c.end - endMargin
	return c.stop > minRun
}

// minRun is the least a drive can move a train, in millimeters.
const minRun = 1.0
