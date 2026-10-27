package trackplan

import (
	"cmp"
	"fmt"
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
	// ProblemOpenEnd is track that leads into a cell with no track.
	ProblemOpenEnd = "open_end"
	// ProblemMisaligned is track that leads into a cell whose track
	// doesn't lead back.
	ProblemMisaligned = "misaligned"
	// ProblemBlockEmpty is a block with no track drawn in it.
	ProblemBlockEmpty = "block_empty"
	// ProblemBlockSplit is a block drawn as pieces that don't join.
	ProblemBlockSplit = "block_split"
)

// Problem is something in the drawing that needs fixing. The plan can be
// saved with problems; they only mean the topology is incomplete.
type Problem struct {
	Kind    string     `json:"kind"`
	Cell    *Cell      `json:"cell,omitempty"`
	Dir     *Direction `json:"dir,omitempty"`
	Block   string     `json:"block,omitempty"`
	Message string     `json:"message"`
}

// Port is one end of a block: a cell of the block and the direction the
// track leaves the block from it.
type Port struct {
	Block string    `json:"block"`
	X     int       `json:"x"`
	Y     int       `json:"y"`
	Dir   Direction `json:"dir"`
}

func (p Port) compare(o Port) int {
	return cmp.Or(strings.Compare(p.Block, o.Block), cmp.Compare(p.Y, o.Y), cmp.Compare(p.X, o.X), cmp.Compare(p.Dir, o.Dir))
}

// BlockInfo is a block's place in the drawing.
type BlockInfo struct {
	ID    string `json:"id"`
	Cells int    `json:"cells"`
	// Ends are where track continues out of the block. A track that ends
	// at a bumper in the block has no end there.
	Ends []Port `json:"ends"`
}

// Setting is a turnout position a link needs.
type Setting struct {
	X        int      `json:"x"`
	Y        int      `json:"y"`
	Turnout  string   `json:"turnout,omitempty"`
	Position Position `json:"position"`
}

// Link is a way from one block's end to another's (or the same block's)
// over track outside any block, with the turnout positions it needs. Each
// link is listed once; trains can take it either way.
type Link struct {
	From     Port      `json:"from"`
	To       Port      `json:"to"`
	Turnouts []Setting `json:"turnouts,omitempty"`
	// Cells is how many cells of track outside any block lie between.
	Cells int `json:"cells"`
}

// grid indexes a plan's track by cell.
type grid struct {
	tiles  map[Cell]Tile
	shapes map[Cell]Shape
}

func newGrid(p Plan) grid {
	g := grid{tiles: make(map[Cell]Tile, len(p.Tiles)), shapes: make(map[Cell]Shape, len(p.Tiles))}
	for _, t := range p.Tiles {
		if len(t.Track) == 0 {
			continue
		}
		shape, err := Classify(t.Track)
		if err != nil {
			continue // Validate reports it
		}
		g.tiles[t.cell()] = t
		g.shapes[t.cell()] = shape
	}
	return g
}

// joins reports whether the track in c leads out in direction d into
// track that leads back.
func (g grid) joins(c Cell, d Direction) bool {
	n, ok := g.tiles[c.Neighbor(d)]
	return ok && slices.Contains(n.Track, d.Opposite())
}

// Topology works out the plan's problems, block ends and links. It
// expects a valid plan; cells that aren't are left out.
func (p Plan) Topology() Topology {
	g := newGrid(p)
	top := Topology{Problems: []Problem{}, Blocks: []BlockInfo{}, Links: []Link{}}

	cells := make([]Cell, 0, len(g.tiles))
	for c := range g.tiles {
		cells = append(cells, c)
	}
	slices.SortFunc(cells, func(a, b Cell) int { return cmp.Or(cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X)) })

	for _, c := range cells {
		for _, d := range g.tiles[c].Track {
			if g.joins(c, d) {
				continue
			}
			kind, message := ProblemOpenEnd, fmt.Sprintf("The track at %s leads %s into an empty cell.", c, d)
			if _, ok := g.tiles[c.Neighbor(d)]; ok {
				kind, message = ProblemMisaligned, fmt.Sprintf("The track at %s leads %s into track that doesn't lead back.", c, d)
			}
			at, dir := c, d
			top.Problems = append(top.Problems, Problem{Kind: kind, Cell: &at, Dir: &dir, Message: message})
		}
	}

	byBlock := map[string][]Cell{}
	for _, c := range cells {
		if b := g.tiles[c].Block; b != "" {
			byBlock[b] = append(byBlock[b], c)
		}
	}
	for _, b := range p.Blocks {
		members := byBlock[b.ID]
		info := BlockInfo{ID: b.ID, Cells: len(members), Ends: []Port{}}
		if len(members) == 0 {
			top.Problems = append(top.Problems, Problem{Kind: ProblemBlockEmpty, Block: b.ID, Message: fmt.Sprintf("Block %s has no track drawn in it.", b.Name)})
		} else if pieces := g.pieces(members); pieces > 1 {
			top.Problems = append(top.Problems, Problem{Kind: ProblemBlockSplit, Block: b.ID, Message: fmt.Sprintf("Block %s is drawn in %d pieces that don't join.", b.Name, pieces)})
		}
		for _, c := range members {
			for _, d := range g.tiles[c].Track {
				if g.joins(c, d) && g.tiles[c.Neighbor(d)].Block != b.ID {
					info.Ends = append(info.Ends, Port{Block: b.ID, X: c.X, Y: c.Y, Dir: d})
				}
			}
		}
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

// pieces counts the separate pieces a block's cells make, joining cells
// whose track leads into each other.
func (g grid) pieces(members []Cell) int {
	in := make(map[Cell]bool, len(members))
	for _, c := range members {
		in[c] = true
	}
	visited := map[Cell]bool{}
	count := 0
	for _, start := range members {
		if visited[start] {
			continue
		}
		count++
		stack := []Cell{start}
		visited[start] = true
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, d := range g.tiles[c].Track {
				n := c.Neighbor(d)
				if in[n] && !visited[n] && g.joins(c, d) {
					visited[n] = true
					stack = append(stack, n)
				}
			}
		}
	}
	return count
}

// walk follows the track out of a block's end until it reaches a block,
// taking every way a turnout offers. Ways that end at a bumper, run off
// the track or need one turnout in two positions lead nowhere.
func (g grid) walk(from Port) []Link {
	var links []Link
	var settings []Setting
	visited := map[Port]bool{}
	cells := 0

	var step func(c Cell, in Direction)
	step = func(c Cell, in Direction) {
		t := g.tiles[c]
		if t.Block != "" {
			links = append(links, Link{From: from, To: Port{Block: t.Block, X: c.X, Y: c.Y, Dir: in}, Turnouts: slices.Clone(settings), Cells: cells})
			return
		}
		here := Port{X: c.X, Y: c.Y, Dir: in}
		if visited[here] {
			return // around a loop with no block in it
		}
		visited[here] = true
		defer delete(visited, here)
		cells++
		defer func() { cells-- }()

		for _, path := range g.shapes[c].Exit(in) {
			added := false
			if path.Position != "" {
				set, ok := settingOf(settings, c)
				if ok && set != path.Position {
					continue
				}
				if !ok {
					settings = append(settings, Setting{X: c.X, Y: c.Y, Turnout: t.Turnout, Position: path.Position})
					added = true
				}
			}
			if g.joins(c, path.B) {
				step(c.Neighbor(path.B), path.B.Opposite())
			}
			if added {
				settings = settings[:len(settings)-1]
			}
		}
	}
	start := Cell{X: from.X, Y: from.Y}
	step(start.Neighbor(from.Dir), from.Dir.Opposite())
	return links
}

// settingOf returns the position the way being followed already needs
// the turnout in c to be in. A way that comes back through a turnout must
// find it set the same.
func settingOf(settings []Setting, c Cell) (Position, bool) {
	for _, s := range settings {
		if s.X == c.X && s.Y == c.Y {
			return s.Position, true
		}
	}
	return "", false
}

func (l Link) reversed() Link {
	r := Link{From: l.To, To: l.From, Cells: l.Cells}
	if len(l.Turnouts) > 0 {
		r.Turnouts = slices.Clone(l.Turnouts)
		slices.Reverse(r.Turnouts)
	}
	return r
}

// key identifies a link by its ends and the turnout positions it needs,
// in either direction.
func (l Link) key() string {
	parts := make([]string, 0, len(l.Turnouts))
	for _, s := range l.Turnouts {
		parts = append(parts, fmt.Sprintf("%d,%d=%s", s.X, s.Y, s.Position))
	}
	slices.Sort(parts)
	return fmt.Sprintf("%s/%d,%d/%s>%s/%d,%d/%s|%s", l.From.Block, l.From.X, l.From.Y, l.From.Dir, l.To.Block, l.To.X, l.To.Y, l.To.Dir, strings.Join(parts, ";"))
}
