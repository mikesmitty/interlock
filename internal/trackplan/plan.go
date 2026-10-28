// Package trackplan is the track plan: the layout's track drawn to scale
// in millimeters, the blocks and stations it is divided into, and the
// topology worked out from the drawing (where each block ends and which
// blocks connect through which turnouts), with lengths measured along
// the track.
//
// Track runs between points: each piece of plain track joins two points,
// straight or turning by an angle, and a point joins at most two pieces
// end to end. Where track branches or crosses there is a junction (a
// turnout, wye, crossing or crossover) whose ends are points too. The
// browser draws and places track with the same geometry
// (web/src/trackgeom.ts); keep the two in step.
//
// Plans drawn before the plan was to scale were grids of cells; OpenFile
// converts them (legacy.go).
package trackplan

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Limits on a plan, well beyond any layout's needs, so a bad request
// can't make the server work on an endless drawing.
const (
	MaxElements = 50000
	// MaxCoord bounds positions, in millimeters either side of 0.
	MaxCoord    = 1_000_000
	MaxNameLen  = 80
	MaxLengthMM = 1_000_000
	// MaxGrid bounds the snap spacing.
	MaxGrid = 1000
)

// DefaultGrid is the snap spacing of a new plan: the distance between
// the tracks of Kato Unitrack N double track.
const DefaultGrid = 33

// Plan is the track plan as drawn.
type Plan struct {
	Points    []Point    `json:"points" yaml:"points,omitempty"`
	Tracks    []Track    `json:"tracks" yaml:"tracks,omitempty"`
	Junctions []Junction `json:"junctions" yaml:"junctions,omitempty"`
	Platforms []Platform `json:"platforms" yaml:"platforms,omitempty"`
	Labels    []Label    `json:"labels" yaml:"labels,omitempty"`
	Layers    []Layer    `json:"layers" yaml:"layers,omitempty"`
	Blocks    []Block    `json:"blocks" yaml:"blocks,omitempty"`
	Stations  []Station  `json:"stations" yaml:"stations,omitempty"`
	// Grid is the editor's snap spacing in millimeters.
	Grid float64 `json:"grid" yaml:"grid"`
	// Background is an image drawn under the plan; nil for none.
	Background *Background `json:"background,omitempty" yaml:"background,omitempty"`
}

// Point is where track ends or where two pieces of track meet.
type Point struct {
	ID string  `json:"id" yaml:"id"`
	X  float64 `json:"x" yaml:"x"`
	Y  float64 `json:"y" yaml:"y"`
	// Bumper marks the end of a track that ends in a buffer stop.
	Bumper bool `json:"bumper,omitempty" yaml:"bumper,omitempty"`
}

func (p Point) pos() Vec { return Vec{p.X, p.Y} }

// Track is plain track from point A to point B.
type Track struct {
	ID string `json:"id" yaml:"id"`
	A  string `json:"a" yaml:"a"`
	B  string `json:"b" yaml:"b"`
	// Angle is how far the track turns from A to B, in degrees: positive
	// clockwise on the plan, negative counterclockwise, 0 for straight.
	Angle float64 `json:"angle,omitempty" yaml:"angle,omitempty"`
	// Block is the ID of the block the track is part of.
	Block string `json:"block,omitempty" yaml:"block,omitempty"`
	Layer string `json:"layer,omitempty" yaml:"layer,omitempty"`
}

// Junction is a turnout, wye, crossing or crossover.
type Junction struct {
	ID   string `json:"id" yaml:"id"`
	Kind string `json:"kind" yaml:"kind"`
	// Ends are the points where track leaves it, in the order its kind
	// gives (see the Kind constants).
	Ends []string `json:"ends" yaml:"ends"`
	// At is where a turnout's or wye's routes part, where the lines
	// along its ends meet. Crossings and crossovers work theirs out.
	At *Vec `json:"at,omitempty" yaml:"at,omitempty"`
	// Switch is the turnout entity that sets it.
	Switch string `json:"switch,omitempty" yaml:"switch,omitempty"`
	Block  string `json:"block,omitempty" yaml:"block,omitempty"`
	Layer  string `json:"layer,omitempty" yaml:"layer,omitempty"`
}

// Platform is a rectangle beside or between tracks.
type Platform struct {
	ID string `json:"id" yaml:"id"`
	// X and Y are its middle.
	X      float64 `json:"x" yaml:"x"`
	Y      float64 `json:"y" yaml:"y"`
	Length float64 `json:"length" yaml:"length"`
	Width  float64 `json:"width" yaml:"width"`
	// Angle is the heading of its length, clockwise from rightward.
	Angle float64 `json:"angle,omitempty" yaml:"angle,omitempty"`
	Layer string  `json:"layer,omitempty" yaml:"layer,omitempty"`
}

// Label is text on the plan, such as a station's name.
type Label struct {
	ID string `json:"id" yaml:"id"`
	// X and Y are where the text starts, on its middle line.
	X     float64 `json:"x" yaml:"x"`
	Y     float64 `json:"y" yaml:"y"`
	Text  string  `json:"text" yaml:"text"`
	Layer string  `json:"layer,omitempty" yaml:"layer,omitempty"`
}

// Layer is a part of the plan that can be shown or hidden on its own,
// such as one deck of a layout with two.
type Layer struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// Block is a stretch of track that trains are detected in.
type Block struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
	// Occupancy is the block entity that reports the track occupied or
	// clear.
	Occupancy string `json:"occupancy,omitempty" yaml:"occupancy,omitempty"`
	// Reporter is the reporter entity that names the locomotives in it.
	Reporter string `json:"reporter,omitempty" yaml:"reporter,omitempty"`
	// LengthMM is the block's length in millimeters as measured on the
	// layout, when it differs from the drawing's; 0 to go by the
	// drawing.
	LengthMM int `json:"length_mm,omitempty" yaml:"length_mm,omitempty"`
	// Station is the ID of the station whose platform track this is.
	Station string `json:"station,omitempty" yaml:"station,omitempty"`
}

// Station is a stop with one or more platform tracks (blocks).
type Station struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// The plan's elements are written one to a line, so the file reads as
// lists.

func flow(v any) (any, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	n.Style = yaml.FlowStyle
	return &n, nil
}

// MarshalYAML writes the point on one line.
func (p Point) MarshalYAML() (any, error) {
	type plain Point
	return flow(plain(p))
}

// MarshalYAML writes the track on one line.
func (t Track) MarshalYAML() (any, error) {
	type plain Track
	return flow(plain(t))
}

// MarshalYAML writes the junction on one line.
func (j Junction) MarshalYAML() (any, error) {
	type plain Junction
	return flow(plain(j))
}

// MarshalYAML writes the platform on one line.
func (p Platform) MarshalYAML() (any, error) {
	type plain Platform
	return flow(plain(p))
}

// MarshalYAML writes the label on one line.
func (l Label) MarshalYAML() (any, error) {
	type plain Label
	return flow(plain(l))
}

// InvalidError says what is wrong with a plan.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return "track plan: " + e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func inRange(vs ...float64) bool {
	for _, v := range vs {
		if !finite(v) || math.Abs(v) > MaxCoord {
			return false
		}
	}
	return true
}

// idSet checks one kind of element's IDs: well formed and not repeated.
type idSet struct {
	what string
	seen map[string]bool
}

func newIDSet(what string, n int) idSet { return idSet{what: what, seen: make(map[string]bool, n)} }

func (s idSet) add(id string) error {
	if !idPattern.MatchString(id) {
		return invalid("%s ID %q must be lowercase letters, digits, - and _, starting with a letter or digit", s.what, id)
	}
	if s.seen[id] {
		return invalid("two of the plan's %ss have the ID %q", s.what, id)
	}
	s.seen[id] = true
	return nil
}

// Validate checks a plan: IDs are unique, track joins points that exist,
// no point joins more than two pieces of track, junctions have the ends
// their kind needs, and every block, station and layer named exists.
func (p Plan) Validate() error {
	if n := len(p.Points) + len(p.Tracks) + len(p.Junctions) + len(p.Platforms) + len(p.Labels); n > MaxElements {
		return invalid("a plan can have at most %d points, tracks, junctions, platforms and labels", MaxElements)
	}
	if !finite(p.Grid) || p.Grid <= 0 || p.Grid > MaxGrid {
		return invalid("the grid spacing must be above 0 and at most %d mm", MaxGrid)
	}
	if p.Background != nil {
		if err := p.Background.validate(); err != nil {
			return err
		}
	}
	layers := newIDSet("layer", len(p.Layers))
	for _, l := range p.Layers {
		if err := layers.add(l.ID); err != nil {
			return err
		}
		if err := checkName("layer", l.ID, l.Name); err != nil {
			return err
		}
	}
	layer := func(what, id, l string) error {
		if l != "" && !layers.seen[l] {
			return invalid("%s %s is on layer %q, which isn't in the plan", what, id, l)
		}
		return nil
	}
	stations := newIDSet("station", len(p.Stations))
	for _, s := range p.Stations {
		if err := stations.add(s.ID); err != nil {
			return err
		}
		if err := checkName("station", s.ID, s.Name); err != nil {
			return err
		}
	}
	blocks := newIDSet("block", len(p.Blocks))
	for _, b := range p.Blocks {
		if err := blocks.add(b.ID); err != nil {
			return err
		}
		if err := checkName("block", b.ID, b.Name); err != nil {
			return err
		}
		if b.LengthMM < 0 || b.LengthMM > MaxLengthMM {
			return invalid("block %q: the length must be between 0 and %d mm", b.Name, MaxLengthMM)
		}
		if b.Station != "" && !stations.seen[b.Station] {
			return invalid("block %q is at station %q, which isn't in the plan", b.Name, b.Station)
		}
	}
	block := func(what, id, b string) error {
		if b != "" && !blocks.seen[b] {
			return invalid("%s %s is in block %q, which isn't in the plan", what, id, b)
		}
		return nil
	}

	points := newIDSet("point", len(p.Points))
	at := make(map[string]Vec, len(p.Points))
	for _, pt := range p.Points {
		if err := points.add(pt.ID); err != nil {
			return err
		}
		if !inRange(pt.X, pt.Y) {
			return invalid("point %s must be within %d mm of the origin", pt.ID, MaxCoord)
		}
		at[pt.ID] = pt.pos()
	}
	joins := make(map[string]int, len(p.Points))
	join := func(what, id, pt string) error {
		if !points.seen[pt] {
			return invalid("%s %s ends at point %q, which isn't in the plan", what, id, pt)
		}
		joins[pt]++
		if joins[pt] > 2 {
			return invalid("more than two pieces of track meet at point %s; track branches only at a junction", pt)
		}
		return nil
	}

	tracks := newIDSet("track", len(p.Tracks))
	for _, t := range p.Tracks {
		if err := tracks.add(t.ID); err != nil {
			return err
		}
		if t.A == t.B {
			return invalid("track %s starts and ends at the same point", t.ID)
		}
		for _, pt := range []string{t.A, t.B} {
			if err := join("track", t.ID, pt); err != nil {
				return err
			}
		}
		if at[t.A] == at[t.B] {
			return invalid("track %s has no length: its points are in the same place", t.ID)
		}
		if !finite(t.Angle) || math.Abs(t.Angle) >= 360 {
			return invalid("track %s must turn by less than a full circle", t.ID)
		}
		if err := block("track", t.ID, t.Block); err != nil {
			return err
		}
		if err := layer("track", t.ID, t.Layer); err != nil {
			return err
		}
	}

	junctions := newIDSet("junction", len(p.Junctions))
	for _, j := range p.Junctions {
		if err := junctions.add(j.ID); err != nil {
			return err
		}
		kind, ok := junctionKinds[j.Kind]
		if !ok {
			return invalid("junction %s is a %q; the kinds are turnout, wye, crossing, crossover and double_crossover", j.ID, j.Kind)
		}
		if len(j.Ends) != kind.ends {
			return invalid("%s %s needs %d ends, not %d", kind.name, j.ID, kind.ends, len(j.Ends))
		}
		for i, pt := range j.Ends {
			if slices.Contains(j.Ends[:i], pt) {
				return invalid("%s %s names point %s twice", kind.name, j.ID, pt)
			}
			if err := join(kind.name, j.ID, pt); err != nil {
				return err
			}
		}
		if kind.center && j.At == nil {
			return invalid("%s %s needs the point where its routes part", kind.name, j.ID)
		}
		if j.At != nil && !inRange(j.At.X, j.At.Y) {
			return invalid("%s %s must be within %d mm of the origin", kind.name, j.ID, MaxCoord)
		}
		if j.Switch != "" && !kind.switches {
			return invalid("a %s has nothing to set, so %s can't have a switch machine", kind.name, j.ID)
		}
		if err := block(kind.name, j.ID, j.Block); err != nil {
			return err
		}
		if err := layer(kind.name, j.ID, j.Layer); err != nil {
			return err
		}
	}

	platforms := newIDSet("platform", len(p.Platforms))
	for _, pl := range p.Platforms {
		if err := platforms.add(pl.ID); err != nil {
			return err
		}
		if !inRange(pl.X, pl.Y, pl.Angle) || !finite(pl.Length, pl.Width) || pl.Length <= 0 || pl.Width <= 0 || pl.Length > MaxCoord || pl.Width > MaxCoord {
			return invalid("platform %s needs a place within %d mm of the origin and a length and width above 0", pl.ID, MaxCoord)
		}
		if err := layer("platform", pl.ID, pl.Layer); err != nil {
			return err
		}
	}
	labels := newIDSet("label", len(p.Labels))
	for _, l := range p.Labels {
		if err := labels.add(l.ID); err != nil {
			return err
		}
		if !inRange(l.X, l.Y) {
			return invalid("label %s must be within %d mm of the origin", l.ID, MaxCoord)
		}
		if strings.TrimSpace(l.Text) == "" || utf8.RuneCountInString(l.Text) > MaxNameLen {
			return invalid("label %s needs text of at most %d characters", l.ID, MaxNameLen)
		}
		if err := layer("label", l.ID, l.Layer); err != nil {
			return err
		}
	}
	return nil
}

func checkName(what, id, name string) error {
	if strings.TrimSpace(name) == "" {
		return invalid("%s %q needs a name", what, id)
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return invalid("%s %q: the name is longer than %d characters", what, id, MaxNameLen)
	}
	return nil
}

// Normalize puts a plan in its saved form: points no track uses dropped,
// elements in ID order, positions to a hundredth of a millimeter, names
// trimmed and no nil lists.
func (p Plan) Normalize() Plan {
	used := map[string]bool{}
	for _, t := range p.Tracks {
		used[t.A], used[t.B] = true, true
	}
	for _, j := range p.Junctions {
		for _, e := range j.Ends {
			used[e] = true
		}
	}
	out := Plan{
		Points:     []Point{},
		Tracks:     slices.Clone(p.Tracks),
		Junctions:  make([]Junction, len(p.Junctions)),
		Platforms:  make([]Platform, len(p.Platforms)),
		Labels:     make([]Label, len(p.Labels)),
		Layers:     make([]Layer, len(p.Layers)),
		Blocks:     make([]Block, len(p.Blocks)),
		Stations:   make([]Station, len(p.Stations)),
		Grid:       p.Grid,
		Background: p.Background,
	}
	if out.Grid == 0 {
		out.Grid = DefaultGrid
	}
	for _, pt := range p.Points {
		if used[pt.ID] {
			pt.X, pt.Y = round2(pt.X), round2(pt.Y)
			out.Points = append(out.Points, pt)
		}
	}
	if out.Tracks == nil {
		out.Tracks = []Track{}
	}
	for i := range out.Tracks {
		out.Tracks[i].Angle = round2(out.Tracks[i].Angle)
	}
	for i, j := range p.Junctions {
		j.Ends = slices.Clone(j.Ends)
		if j.At != nil {
			at := Vec{round2(j.At.X), round2(j.At.Y)}
			j.At = &at
		}
		out.Junctions[i] = j
	}
	for i, pl := range p.Platforms {
		pl.X, pl.Y, pl.Length, pl.Width, pl.Angle = round2(pl.X), round2(pl.Y), round2(pl.Length), round2(pl.Width), round2(pl.Angle)
		out.Platforms[i] = pl
	}
	for i, l := range p.Labels {
		l.X, l.Y, l.Text = round2(l.X), round2(l.Y), strings.TrimSpace(l.Text)
		out.Labels[i] = l
	}
	for i, l := range p.Layers {
		l.Name = strings.TrimSpace(l.Name)
		out.Layers[i] = l
	}
	for i, b := range p.Blocks {
		b.Name = strings.TrimSpace(b.Name)
		out.Blocks[i] = b
	}
	for i, s := range p.Stations {
		s.Name = strings.TrimSpace(s.Name)
		out.Stations[i] = s
	}
	byID := func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b)) }
	slices.SortFunc(out.Points, func(a, b Point) int { return byID(a.ID, b.ID) })
	slices.SortFunc(out.Tracks, func(a, b Track) int { return byID(a.ID, b.ID) })
	slices.SortFunc(out.Junctions, func(a, b Junction) int { return byID(a.ID, b.ID) })
	slices.SortFunc(out.Platforms, func(a, b Platform) int { return byID(a.ID, b.ID) })
	slices.SortFunc(out.Labels, func(a, b Label) int { return byID(a.ID, b.ID) })
	return out
}

func round2(v float64) float64 {
	r := math.Round(v*100) / 100
	if r == 0 {
		return 0 // not -0
	}
	return r
}

// Empty reports whether the plan has no track, platforms or labels.
func (p Plan) Empty() bool {
	return len(p.Tracks) == 0 && len(p.Junctions) == 0 && len(p.Platforms) == 0 && len(p.Labels) == 0
}
