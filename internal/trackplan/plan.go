package trackplan

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Limits on a plan, well beyond any layout's needs, so a bad request
// can't make the server work on an endless drawing.
const (
	MaxTiles    = 20000
	MaxCoord    = 1000 // cells lie within -MaxCoord to MaxCoord
	MaxNameLen  = 80
	MaxLengthMM = 1_000_000
)

// Plan is the track diagram as drawn.
type Plan struct {
	Tiles    []Tile    `json:"tiles" yaml:"tiles,omitempty"`
	Blocks   []Block   `json:"blocks" yaml:"blocks,omitempty"`
	Stations []Station `json:"stations" yaml:"stations,omitempty"`
	// Background is an image drawn under the plan; nil for none.
	Background *Background `json:"background,omitempty" yaml:"background,omitempty"`
}

// Tile is one cell of the grid. It holds track, a piece of platform or a
// label, never two of them.
type Tile struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
	// Track lists the directions track leaves the cell by.
	Track []Direction `json:"track,omitempty" yaml:"track,omitempty"`
	// Platform lists the directions a platform continues into the
	// neighboring cells.
	Platform []Direction `json:"platform,omitempty" yaml:"platform,omitempty"`
	// Label is text drawn from the cell rightward.
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
	// Turnout is the turnout entity that sets a turnout or wye.
	Turnout string `json:"turnout,omitempty" yaml:"turnout,omitempty"`
	// Block is the ID of the block the track is part of.
	Block string `json:"block,omitempty" yaml:"block,omitempty"`
}

// MarshalYAML writes a tile on one line, so the file reads as a list of
// cells.
func (t Tile) MarshalYAML() (any, error) {
	type plain Tile
	var n yaml.Node
	if err := n.Encode(plain(t)); err != nil {
		return nil, err
	}
	n.Style = yaml.FlowStyle
	return &n, nil
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
	// LengthMM is the block's length in millimeters, measured on the
	// layout; 0 when not known yet.
	LengthMM int `json:"length_mm,omitempty" yaml:"length_mm,omitempty"`
	// Station is the ID of the station whose platform track this is.
	Station string `json:"station,omitempty" yaml:"station,omitempty"`
}

// Station is a stop with one or more platform tracks (blocks).
type Station struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
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

// Cell is a grid position.
type Cell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func (c Cell) String() string { return fmt.Sprintf("%d,%d", c.X, c.Y) }

// Neighbor is the cell next to c in direction d.
func (c Cell) Neighbor(d Direction) Cell {
	dx, dy := d.Step()
	return Cell{X: c.X + dx, Y: c.Y + dy}
}

func (t Tile) cell() Cell { return Cell{X: t.X, Y: t.Y} }

// Validate checks a plan: every cell holds one thing, track makes a piece,
// and every block and station a tile or block names exists.
func (p Plan) Validate() error {
	if len(p.Tiles) > MaxTiles {
		return invalid("a plan can have at most %d cells", MaxTiles)
	}
	if p.Background != nil {
		if err := p.Background.validate(); err != nil {
			return err
		}
	}
	stations := make(map[string]bool, len(p.Stations))
	for _, s := range p.Stations {
		if err := checkID("station", s.ID, s.Name); err != nil {
			return err
		}
		if stations[s.ID] {
			return invalid("two stations have the ID %q", s.ID)
		}
		stations[s.ID] = true
	}
	blocks := make(map[string]bool, len(p.Blocks))
	for _, b := range p.Blocks {
		if err := checkID("block", b.ID, b.Name); err != nil {
			return err
		}
		if blocks[b.ID] {
			return invalid("two blocks have the ID %q", b.ID)
		}
		blocks[b.ID] = true
		if b.LengthMM < 0 || b.LengthMM > MaxLengthMM {
			return invalid("block %q: the length must be between 0 and %d mm", b.Name, MaxLengthMM)
		}
		if b.Station != "" && !stations[b.Station] {
			return invalid("block %q is at station %q, which isn't in the plan", b.Name, b.Station)
		}
	}
	cells := make(map[Cell]bool, len(p.Tiles))
	for _, t := range p.Tiles {
		c := t.cell()
		if t.X < -MaxCoord || t.X > MaxCoord || t.Y < -MaxCoord || t.Y > MaxCoord {
			return invalid("the cell %s is outside the grid (%d to %d)", c, -MaxCoord, MaxCoord)
		}
		if cells[c] {
			return invalid("the cell %s is listed twice", c)
		}
		cells[c] = true
		if err := t.validate(blocks); err != nil {
			return err
		}
	}
	return nil
}

func (t Tile) validate(blocks map[string]bool) error {
	c := t.cell()
	kinds := 0
	if len(t.Track) > 0 {
		kinds++
	}
	if len(t.Platform) > 0 {
		kinds++
	}
	if t.Label != "" {
		kinds++
	}
	switch {
	case kinds == 0:
		return invalid("the cell %s holds nothing", c)
	case kinds > 1:
		return invalid("the cell %s holds more than one of track, platform and label", c)
	}
	if len(t.Track) > 0 {
		shape, err := Classify(t.Track)
		if err != nil {
			return invalid("the track at %s: %v", c, err)
		}
		if t.Turnout != "" && !shape.Piece.Switches() {
			return invalid("the track at %s has a turnout linked but is a %s", c, strings.ReplaceAll(string(shape.Piece), "_", " "))
		}
		if t.Block != "" && !blocks[t.Block] {
			return invalid("the track at %s is in block %q, which isn't in the plan", c, t.Block)
		}
		return nil
	}
	if t.Turnout != "" || t.Block != "" {
		return invalid("the cell %s has a turnout or block but no track", c)
	}
	if len(t.Platform) > 0 {
		seen := map[Direction]bool{}
		for _, d := range t.Platform {
			if d > NorthWest {
				return invalid("the platform at %s: no direction %d", c, d)
			}
			if seen[d] {
				return invalid("the platform at %s lists %s twice", c, d)
			}
			seen[d] = true
		}
		return nil
	}
	if utf8.RuneCountInString(t.Label) > MaxNameLen {
		return invalid("the label at %s is longer than %d characters", c, MaxNameLen)
	}
	return nil
}

func checkID(what, id, name string) error {
	if !idPattern.MatchString(id) {
		return invalid("%s ID %q must be lowercase letters, digits, - and _, starting with a letter or digit", what, id)
	}
	if strings.TrimSpace(name) == "" {
		return invalid("%s %q needs a name", what, id)
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return invalid("%s %q: the name is longer than %d characters", what, id, MaxNameLen)
	}
	return nil
}

// Normalize puts a plan in its saved form: cells in reading order,
// directions clockwise from north, names trimmed and no nil lists.
func (p Plan) Normalize() Plan {
	out := Plan{
		Tiles:      make([]Tile, len(p.Tiles)),
		Blocks:     make([]Block, len(p.Blocks)),
		Stations:   make([]Station, len(p.Stations)),
		Background: p.Background,
	}
	for i, t := range p.Tiles {
		t.Track = sortedDirections(t.Track)
		t.Platform = sortedDirections(t.Platform)
		t.Label = strings.TrimSpace(t.Label)
		out.Tiles[i] = t
	}
	slices.SortFunc(out.Tiles, func(a, b Tile) int {
		return cmp.Or(cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	for i, b := range p.Blocks {
		b.Name = strings.TrimSpace(b.Name)
		out.Blocks[i] = b
	}
	for i, s := range p.Stations {
		s.Name = strings.TrimSpace(s.Name)
		out.Stations[i] = s
	}
	return out
}

func sortedDirections(ds []Direction) []Direction {
	if len(ds) == 0 {
		return nil
	}
	out := slices.Clone(ds)
	slices.Sort(out)
	return out
}
