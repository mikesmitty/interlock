package trackplan

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// Network is the plan's track as pieces joined end to end, for following
// a train along it: from one piece to the next, through junctions the
// way they are set.
type Network struct {
	g      graph
	index  map[pieceKey]int
	curves [][]func(float64) Vec
}

type pieceKey struct {
	id       string
	junction bool
}

// Leg is one way through one piece: a track from end to end or one route
// through a junction, taken one way.
type Leg struct {
	// Piece is the track's ID, or the junction's when Junction is set.
	Piece    string `json:"piece"`
	Junction bool   `json:"junction,omitempty"`
	// Route is the route through a junction, in its kind's order; 0 for
	// track.
	Route int `json:"route,omitempty"`
	// Reverse is going from the route's last end to its first: from B to
	// A along a track.
	Reverse bool `json:"reverse,omitempty"`
}

// Reversed is the same leg taken the other way.
func (l Leg) Reversed() Leg {
	l.Reverse = !l.Reverse
	return l
}

// ErrTrackEnds is the track coming to an end: a buffer stop or an end
// that leads nowhere.
var ErrTrackEnds = errors.New("the track ends")

// SetAgainstError is a junction that offers no way on from the end a
// train comes to, as set (or while it is unknown or moving).
type SetAgainstError struct {
	Junction string
	Position Position
}

func (e *SetAgainstError) Error() string {
	if e.Position == "" {
		return fmt.Sprintf("junction %s is not set", e.Junction)
	}
	return fmt.Sprintf("junction %s is %s against the way in", e.Junction, e.Position)
}

// Network builds the plan's network. Pieces that aren't valid are left
// out, as in Topology.
func (p Plan) Network() *Network {
	n := &Network{g: newGraph(p), index: map[pieceKey]int{}}
	points := n.g.points
	tracks := make(map[string]Track, len(p.Tracks))
	for _, t := range p.Tracks {
		tracks[t.ID] = t
	}
	for i, pc := range n.g.pieces {
		n.index[pieceKey{pc.id, pc.junction >= 0}] = i
		var curves []func(float64) Vec
		if pc.junction < 0 {
			t := tracks[pc.id]
			shape := arc{points[t.A].pos(), points[t.B].pos(), t.Angle}
			curves = append(curves, shape.at)
		} else {
			j := p.Junctions[pc.junction]
			ends := make([]Vec, len(j.Ends))
			for e, id := range j.Ends {
				ends[e] = points[id].pos()
			}
			shape := newJunctionShape(j, ends)
			for _, r := range pc.routes {
				curves = append(curves, shape.curve(r))
			}
		}
		n.curves = append(n.curves, curves)
	}
	return n
}

func (n *Network) piece(l Leg) (int, bool) {
	i, ok := n.index[pieceKey{l.Piece, l.Junction}]
	if !ok || l.Route < 0 || l.Route >= len(n.g.pieces[i].routes) {
		return 0, false
	}
	return i, true
}

// Valid reports whether the leg is in the network.
func (n *Network) Valid(l Leg) bool {
	_, ok := n.piece(l)
	return ok
}

// Length is how far the leg runs, in millimeters; 0 for a leg that isn't
// in the network.
func (n *Network) Length(l Leg) float64 {
	i, ok := n.piece(l)
	if !ok {
		return 0
	}
	return n.g.pieces[i].lengths[l.Route]
}

// Block is the block the leg's piece is in, if any.
func (n *Network) Block(l Leg) string {
	i, ok := n.piece(l)
	if !ok {
		return ""
	}
	return n.g.pieces[i].block
}

// ends are the leg's first and last ends, in its piece's order.
func (n *Network) ends(i int, l Leg) (from, to int) {
	r := n.g.pieces[i].routes[l.Route]
	if l.Reverse {
		return r.to, r.from
	}
	return r.from, r.to
}

// Next is the leg a train takes after l, with the junctions set as
// position says ("" for unknown or moving). It returns ErrTrackEnds at a
// buffer stop or an end that leads nowhere, and a SetAgainstError at a
// junction that has no way on as set.
func (n *Network) Next(l Leg, position func(junction string) Position) (Leg, error) {
	i, ok := n.piece(l)
	if !ok {
		return Leg{}, fmt.Errorf("leg %s isn't in the plan", l.Piece)
	}
	_, out := n.ends(i, l)
	in, ok := n.g.across(attach{i, out})
	if !ok {
		return Leg{}, ErrTrackEnds
	}
	return n.enter(in, position)
}

// enter is the leg a train takes into a piece by one of its ends.
func (n *Network) enter(in attach, position func(junction string) Position) (Leg, error) {
	pc := n.g.pieces[in.piece]
	var set Position
	if pc.junction >= 0 && junctionKinds[n.g.plan.Junctions[pc.junction].Kind].switches {
		set = position(pc.id)
	}
	for r, rt := range pc.routes {
		if rt.position != "" && rt.position != set {
			continue
		}
		switch in.end {
		case rt.from:
			return Leg{Piece: pc.id, Junction: pc.junction >= 0, Route: r}, nil
		case rt.to:
			return Leg{Piece: pc.id, Junction: pc.junction >= 0, Route: r, Reverse: true}, nil
		}
	}
	return Leg{}, &SetAgainstError{Junction: pc.id, Position: set}
}

// At is the place on the plan dist millimeters along the leg from where
// it starts.
func (n *Network) At(l Leg, dist float64) Vec {
	i, ok := n.piece(l)
	if !ok {
		return Vec{}
	}
	length := n.g.pieces[i].lengths[l.Route]
	f := 0.0
	if length > 0 {
		f = math.Min(math.Max(dist/length, 0), 1)
	}
	if l.Reverse {
		f = 1 - f
	}
	return n.curves[i][l.Route](f)
}

// Legs lists the plan's tracks in a block (all of them for ""), in plan
// order and each taken from A to B, as places to put a train.
func (n *Network) Legs(block string) []Leg {
	var legs []Leg
	for _, pc := range n.g.pieces {
		if pc.junction >= 0 || (block != "" && pc.block != block) {
			continue
		}
		legs = append(legs, Leg{Piece: pc.id})
	}
	return legs
}

// Switch is the turnout entity that sets a junction, if any.
func (n *Network) Switch(junction string) string {
	i, ok := n.index[pieceKey{junction, true}]
	if !ok {
		return ""
	}
	return n.g.plan.Junctions[n.g.pieces[i].junction].Switch
}

// Way is the track a link covers, taken from one of its ends to the
// other: the legs from where track leaves from's block to where it
// enters to's, with the junctions set as settings say. A link between
// blocks that meet end to end covers none.
func (n *Network) Way(from, to Port, settings []Setting) ([]Leg, error) {
	position := func(junction string) Position {
		if i := slices.IndexFunc(settings, func(s Setting) bool { return s.Junction == junction }); i >= 0 {
			return settings[i].Position
		}
		return ""
	}
	var leg Leg
	found := false
	for _, a := range n.g.at[from.Point] {
		if n.g.pieces[a.piece].block == from.Block {
			continue
		}
		var err error
		if leg, err = n.enter(a, position); err != nil {
			return nil, err
		}
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("no track leaves block %s at %s", from.Block, from.Point)
	}
	var legs []Leg
	for range len(n.g.pieces) + 1 {
		if block := n.Block(leg); block != "" {
			i, _ := n.piece(leg)
			first, _ := n.ends(i, leg)
			if block != to.Block || n.g.pieces[i].ends[first] != to.Point {
				return nil, fmt.Errorf("the way from block %s leads into block %s at %s, not block %s at %s", from.Block, block, n.g.pieces[i].ends[first], to.Block, to.Point)
			}
			return legs, nil
		}
		legs = append(legs, leg)
		var err error
		if leg, err = n.Next(leg, position); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("the way from block %s goes round without reaching a block", from.Block)
}

// Fouls reports whether two legs share track, so that two trains can't
// be on them at once: the same track either way, or one junction on two
// routes, other than a crossover's two straight tracks.
func (n *Network) Fouls(a, b Leg) bool {
	if a.Piece != b.Piece || a.Junction != b.Junction {
		return false
	}
	if !a.Junction || a.Route == b.Route {
		return true
	}
	i, okA := n.piece(a)
	_, okB := n.piece(b)
	if !okA || !okB {
		return false
	}
	pc := n.g.pieces[i]
	routes := pc.routes
	parallel := junctionKinds[n.g.plan.Junctions[pc.junction].Kind].parallel
	return !parallel || routes[a.Route].position != Closed || routes[b.Route].position != Closed
}
