package trackplan

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Topology is what the drawing says about the track: where it doesn't
// join up, where each block ends and how the blocks connect.
type Topology struct {
	Problems []Problem   `json:"problems"`
	Blocks   []BlockInfo `json:"blocks"`
	Links    []Link      `json:"links"`
}

// The kinds of problem.
const (
	// ProblemOpenEnd is track that ends without a buffer stop.
	ProblemOpenEnd = "open_end"
	// ProblemKink is two pieces of track that meet at an angle.
	ProblemKink = "kink"
	// ProblemBlockEmpty is a block with no track in it.
	ProblemBlockEmpty = "block_empty"
	// ProblemBlockSplit is a block in pieces that don't join.
	ProblemBlockSplit = "block_split"
)

// KinkTolerance is how far, in degrees, track may change direction where
// two pieces meet before the joint counts as a kink.
const KinkTolerance = 5

// Problem is something in the drawing that needs fixing. The plan can be
// saved with problems; they only mean the topology is incomplete.
type Problem struct {
	Kind string `json:"kind"`
	// Point is where it is, for open ends and kinks.
	Point   string `json:"point,omitempty"`
	Block   string `json:"block,omitempty"`
	Message string `json:"message"`
}

// Port is one end of a block: the point where track leaves it.
type Port struct {
	Block string `json:"block"`
	Point string `json:"point"`
	// Heading is the way the track leaves the block there, in degrees
	// clockwise from rightward on the plan.
	Heading float64 `json:"heading"`
}

func (p Port) compare(o Port) int {
	return cmp.Or(strings.Compare(p.Block, o.Block), strings.Compare(p.Point, o.Point))
}

// BlockInfo is a block's place in the drawing.
type BlockInfo struct {
	ID string `json:"id"`
	// Length is the length of its track in millimeters, taking turnouts
	// in it the closed way.
	Length float64 `json:"length"`
	// Ends are where track continues out of the block. A track that ends
	// at a buffer stop in the block has no end there.
	Ends []Port `json:"ends"`
}

// Setting is a turnout or crossover position a link needs.
type Setting struct {
	Junction string   `json:"junction"`
	Switch   string   `json:"switch,omitempty"`
	Position Position `json:"position"`
}

// Link is a way from one block's end to another's (or the same block's)
// over track outside any block, with the positions it needs. Each link is
// listed once; trains can take it either way.
type Link struct {
	From     Port      `json:"from"`
	To       Port      `json:"to"`
	Turnouts []Setting `json:"turnouts,omitempty"`
	// Length is the track outside any block between them, in
	// millimeters.
	Length float64 `json:"length"`
}

// piece is a track or a junction: something with ends at points and
// routes between them.
type piece struct {
	id     string
	block  string
	ends   []string
	routes []route
	// outward is the heading the track leaves by at each end.
	outward []float64
	// lengths of the routes.
	lengths []float64
	// junction is the index in Plan.Junctions, or -1 for plain track.
	junction int
}

// attach is one end of a piece at a point.
type attach struct {
	piece, end int
}

type graph struct {
	plan   Plan
	pieces []piece
	at     map[string][]attach
	points map[string]Point
}

func newGraph(p Plan) graph {
	g := graph{plan: p, at: map[string][]attach{}, points: make(map[string]Point, len(p.Points))}
	for _, pt := range p.Points {
		g.points[pt.ID] = pt
	}
	pos := func(id string) (Vec, bool) {
		pt, ok := g.points[id]
		return pt.pos(), ok
	}
	for _, t := range p.Tracks {
		a, okA := pos(t.A)
		b, okB := pos(t.B)
		if !okA || !okB {
			continue // Validate reports it
		}
		shape := arc{a, b, t.Angle}
		g.add(piece{
			id: t.ID, block: t.Block, ends: []string{t.A, t.B}, junction: -1,
			routes:  []route{{0, 1, ""}},
			outward: []float64{shape.startHeading() + 180, shape.endHeading()},
			lengths: []float64{shape.length()},
		})
	}
	for i, j := range p.Junctions {
		kind, ok := junctionKinds[j.Kind]
		if !ok || len(j.Ends) != kind.ends {
			continue
		}
		ends := make([]Vec, len(j.Ends))
		missing := false
		for e, id := range j.Ends {
			ends[e], ok = pos(id)
			missing = missing || !ok
		}
		if missing {
			continue
		}
		shape := newJunctionShape(j, ends)
		pc := piece{id: j.ID, block: j.Block, ends: j.Ends, routes: kind.routes, junction: i}
		for e := range ends {
			pc.outward = append(pc.outward, shape.outward(e))
		}
		for _, r := range kind.routes {
			pc.lengths = append(pc.lengths, shape.routeLength(r))
		}
		g.add(pc)
	}
	return g
}

func (g *graph) add(pc piece) {
	i := len(g.pieces)
	g.pieces = append(g.pieces, pc)
	for e, pt := range pc.ends {
		g.at[pt] = append(g.at[pt], attach{i, e})
	}
}

// across is the piece end joined to a at its point, if any.
func (g graph) across(a attach) (attach, bool) {
	for _, o := range g.at[g.pieces[a.piece].ends[a.end]] {
		if o != a {
			return o, true
		}
	}
	return attach{}, false
}

// Topology works out the plan's problems, block ends and links. It
// expects a valid plan; pieces that aren't are left out.
func (p Plan) Topology() Topology {
	g := newGraph(p)
	top := Topology{Problems: []Problem{}, Blocks: []BlockInfo{}, Links: []Link{}}

	ids := make([]string, 0, len(g.points))
	for id := range g.points {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b)) })
	for _, id := range ids {
		pt := g.points[id]
		ats := g.at[id]
		switch len(ats) {
		case 1:
			if !pt.Bumper {
				top.Problems = append(top.Problems, Problem{Kind: ProblemOpenEnd, Point: id, Message: fmt.Sprintf("Track ends at %s without a buffer stop.", where(pt))})
			}
		case 2:
			a, b := ats[0], ats[1]
			turn := turnDiff(g.pieces[a.piece].outward[a.end], g.pieces[b.piece].outward[b.end]+180)
			if math.Abs(turn) > KinkTolerance {
				top.Problems = append(top.Problems, Problem{Kind: ProblemKink, Point: id, Message: fmt.Sprintf("Track bends by %.0f° where it joins at %s.", math.Abs(turn), where(pt))})
			}
		}
	}

	byBlock := map[string][]int{}
	for i, pc := range g.pieces {
		if pc.block != "" {
			byBlock[pc.block] = append(byBlock[pc.block], i)
		}
	}
	for _, b := range p.Blocks {
		members := byBlock[b.ID]
		info := BlockInfo{ID: b.ID, Ends: []Port{}}
		if len(members) == 0 {
			top.Problems = append(top.Problems, Problem{Kind: ProblemBlockEmpty, Block: b.ID, Message: fmt.Sprintf("Block %s has no track in it.", b.Name)})
		} else if n := g.parts(members); n > 1 {
			top.Problems = append(top.Problems, Problem{Kind: ProblemBlockSplit, Block: b.ID, Message: fmt.Sprintf("Block %s is in %d pieces that don't join.", b.Name, n)})
		}
		for _, i := range members {
			pc := g.pieces[i]
			info.Length += pc.lengths[0]
			for e := range pc.ends {
				o, ok := g.across(attach{i, e})
				if ok && g.pieces[o.piece].block != b.ID {
					info.Ends = append(info.Ends, Port{Block: b.ID, Point: pc.ends[e], Heading: roundHeading(pc.outward[e])})
				}
			}
		}
		info.Length = math.Round(info.Length)
		slices.SortFunc(info.Ends, Port.compare)
		top.Blocks = append(top.Blocks, info)
	}

	seen := map[string]bool{}
	for _, info := range top.Blocks {
		for _, end := range info.Ends {
			for _, l := range g.walk(end) {
				if l.To.compare(l.From) < 0 {
					l = l.reversed()
				}
				if key := l.key(); !seen[key] {
					seen[key] = true
					top.Links = append(top.Links, l)
				}
			}
		}
	}
	slices.SortFunc(top.Links, func(a, b Link) int {
		return cmp.Or(a.From.compare(b.From), a.To.compare(b.To), strings.Compare(a.key(), b.key()))
	})
	return top
}

func where(pt Point) string { return fmt.Sprintf("%.0f, %.0f mm", pt.X, pt.Y) }

func roundHeading(h float64) float64 {
	h = math.Round(math.Mod(h+360, 360)*10) / 10
	if h == 360 {
		h = 0
	}
	return h
}

// parts counts the separate parts a block's pieces make.
func (g graph) parts(members []int) int {
	in := make(map[int]bool, len(members))
	for _, i := range members {
		in[i] = true
	}
	visited := map[int]bool{}
	count := 0
	for _, start := range members {
		if visited[start] {
			continue
		}
		count++
		stack := []int{start}
		visited[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for e := range g.pieces[i].ends {
				o, ok := g.across(attach{i, e})
				if ok && in[o.piece] && !visited[o.piece] {
					visited[o.piece] = true
					stack = append(stack, o.piece)
				}
			}
		}
	}
	return count
}

// walk follows the track out of a block's end until it reaches a block,
// taking every way a junction offers. Ways that end at a buffer stop or
// an open end, or need one junction in two positions, lead nowhere.
func (g graph) walk(from Port) []Link {
	var links []Link
	var settings []Setting
	visited := map[attach]bool{}
	length := 0.0

	var step func(in attach)
	step = func(in attach) {
		pc := g.pieces[in.piece]
		if pc.block != "" {
			links = append(links, Link{From: from, To: Port{Block: pc.block, Point: pc.ends[in.end], Heading: roundHeading(pc.outward[in.end])}, Turnouts: slices.Clone(settings), Length: math.Round(length)})
			return
		}
		if visited[in] {
			return // around a loop with no block in it
		}
		visited[in] = true
		defer delete(visited, in)

		for r, rt := range pc.routes {
			var out int
			switch in.end {
			case rt.from:
				out = rt.to
			case rt.to:
				out = rt.from
			default:
				continue
			}
			added := false
			if rt.position != "" {
				set, ok := settingOf(settings, pc.id)
				if ok && set != rt.position {
					continue
				}
				if !ok {
					settings = append(settings, Setting{Junction: pc.id, Switch: g.plan.Junctions[pc.junction].Switch, Position: rt.position})
					added = true
				}
			}
			length += pc.lengths[r]
			if next, ok := g.across(attach{in.piece, out}); ok {
				step(next)
			}
			length -= pc.lengths[r]
			if added {
				settings = settings[:len(settings)-1]
			}
		}
	}

	for _, a := range g.at[from.Point] {
		if g.pieces[a.piece].block != from.Block {
			step(a)
		}
	}
	return links
}

// settingOf returns the position the way being followed already needs a
// junction to be in. A way that comes back through a junction must find
// it set the same.
func settingOf(settings []Setting, junction string) (Position, bool) {
	for _, s := range settings {
		if s.Junction == junction {
			return s.Position, true
		}
	}
	return "", false
}

func (l Link) reversed() Link {
	r := Link{From: l.To, To: l.From, Length: l.Length}
	if len(l.Turnouts) > 0 {
		r.Turnouts = slices.Clone(l.Turnouts)
		slices.Reverse(r.Turnouts)
	}
	return r
}

// key identifies a link by its ends and the positions it needs, in
// either direction.
func (l Link) key() string {
	parts := make([]string, 0, len(l.Turnouts))
	for _, s := range l.Turnouts {
		parts = append(parts, s.Junction+"="+string(s.Position))
	}
	slices.Sort(parts)
	return fmt.Sprintf("%s/%s>%s/%s|%s", l.From.Block, l.From.Point, l.To.Block, l.To.Point, strings.Join(parts, ";"))
}
