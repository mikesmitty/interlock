package trackplan

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// DefaultJMRIScale is how many millimeters one unit of a Layout Editor
// panel is when RailModeller exports it: a centimeter.
const DefaultJMRIScale = 10

// MaxImportSize bounds an imported panel file.
const MaxImportSize = 32 << 20

// Import is track read from another program, to add to a plan.
type Import struct {
	Plan Plan `json:"plan"`
	// Notes say what was left out or may need checking.
	Notes []string `json:"notes,omitempty"`
}

// The parts of a JMRI Layout Editor panel file that hold track.
type jmriPanel struct {
	Editors []struct {
		Points []struct {
			Ident    string  `xml:"ident,attr"`
			Type     string  `xml:"type,attr"`
			X        float64 `xml:"x,attr"`
			Y        float64 `xml:"y,attr"`
			Connect1 string  `xml:"connect1name,attr"`
			Connect2 string  `xml:"connect2name,attr"`
		} `xml:"positionablepoint"`
		Segments []struct {
			Ident    string `xml:"ident,attr"`
			Connect1 string `xml:"connect1name,attr"`
			Type1    string `xml:"type1,attr"`
			Connect2 string `xml:"connect2name,attr"`
			Type2    string `xml:"type2,attr"`
			Arc      string `xml:"arc,attr"`
			Circle   string `xml:"circle,attr"`
			Flip     string `xml:"flip,attr"`
			Angle    string `xml:"angle,attr"`
			Bezier   string `xml:"bezier,attr"`
		} `xml:"tracksegment"`
		Turnouts []jmriJunction `xml:"layoutturnout"`
		Xings    []jmriJunction `xml:"levelxing"`
		Slips    []struct{}     `xml:"layoutSlip"`
		Tables   []struct{}     `xml:"layoutturntable"`
	} `xml:"LayoutEditor"`
}

type jmriJunction struct {
	Ident      string `xml:"ident,attr"`
	Type       string `xml:"type,attr"`
	Continuing string `xml:"continuing,attr"`
	XCen       string `xml:"xcen,attr"`
	YCen       string `xml:"ycen,attr"`
	XA         string `xml:"xa,attr"`
	YA         string `xml:"ya,attr"`
	XB         string `xml:"xb,attr"`
	YB         string `xml:"yb,attr"`
	XC         string `xml:"xc,attr"`
	YC         string `xml:"yc,attr"`
	XD         string `xml:"xd,attr"`
	YD         string `xml:"yd,attr"`
}

// legs returns the junction's center and the ends A to D that the file
// gives, in millimeters; the callers mirror those it leaves out through
// the center as Layout Editor places them.
func (j jmriJunction) legs(scale float64) (cen Vec, ends [4]*Vec, err error) {
	num := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v, err == nil && finite(v)
	}
	x, okX := num(j.XCen)
	y, okY := num(j.YCen)
	if !okX || !okY {
		return Vec{}, ends, fmt.Errorf("%s has no center", j.Ident)
	}
	cen = Vec{x * scale, y * scale}
	for i, xy := range [4][2]string{{j.XA, j.YA}, {j.XB, j.YB}, {j.XC, j.YC}, {j.XD, j.YD}} {
		x, okX := num(xy[0])
		y, okY := num(xy[1])
		if okX && okY {
			ends[i] = &Vec{x * scale, y * scale}
		}
	}
	return cen, ends, nil
}

func mirror(cen Vec, p *Vec) *Vec {
	if p == nil {
		return nil
	}
	m := cen.scale(2).sub(*p)
	return &m
}

// ImportJMRI reads the track of a JMRI Layout Editor panel file, as
// RailModeller Pro exports a layout for JMRI, scale millimeters to the
// panel's unit. Track segments become track, end bumpers buffer stops,
// turnouts, wyes and crossovers junctions and level crossings crossings.
// Blocks, signals and the rest are left out.
func ImportJMRI(r io.Reader, scale float64) (Import, error) {
	if !finite(scale) || scale <= 0 || scale > 1000 {
		return Import{}, invalid("the scale must be above 0 and at most 1000 mm to a unit")
	}
	var panel jmriPanel
	if err := xml.NewDecoder(io.LimitReader(r, MaxImportSize)).Decode(&panel); err != nil {
		return Import{}, invalid("the file isn't a JMRI panel file: %v", err)
	}
	if len(panel.Editors) == 0 {
		return Import{}, invalid("the file has no Layout Editor panel; export the layout for JMRI's PanelPro")
	}

	in := importer{scale: scale, joins: map[string]int{}, at: map[string]Vec{}}
	for _, ed := range panel.Editors {
		anchors := map[string]string{}
		ends := map[string]string{}
		for _, pt := range ed.Points {
			anchors[pt.Ident] = in.point(Vec{pt.X, pt.Y}.scale(scale), pt.Type == "END_BUMPER" || pt.Type == "2")
		}

		for _, t := range ed.Turnouts {
			if err := in.turnout(t, ends); err != nil {
				in.note("Left out turnout %s: %v.", t.Ident, err)
			}
		}
		for _, x := range ed.Xings {
			if err := in.crossing(x, ends); err != nil {
				in.note("Left out level crossing %s: %v.", x.Ident, err)
			}
		}

		resolve := func(name, typ string) (string, bool) {
			switch typ {
			case "POS_POINT", "1", "":
				id, ok := anchors[name]
				return id, ok
			}
			leg := jmriLeg(typ)
			if leg == "" {
				return "", false
			}
			id, ok := ends[name+"/"+leg]
			return id, ok
		}
		for _, s := range ed.Segments {
			a, okA := resolve(s.Connect1, s.Type1)
			b, okB := resolve(s.Connect2, s.Type2)
			if !okA || !okB {
				in.note("Left out track segment %s: it doesn't lead to track that was imported.", s.Ident)
				continue
			}
			angle := 0.0
			if s.Arc == "yes" {
				v, err := strconv.ParseFloat(s.Angle, 64)
				if err != nil || !finite(v) || v <= 0 || v >= 360 {
					v = 90 // Layout Editor's quarter circle
				}
				if s.Circle != "yes" {
					in.ellipses++
				}
				angle = v
				if s.Flip == "yes" {
					angle = -v
				}
			}
			if s.Bezier == "yes" {
				in.beziers++
			}
			in.track(a, b, angle)
		}
		if n := len(ed.Slips); n > 0 {
			in.note("Left out %d slip%s; draw them as crossings with turnouts.", n, plural(n))
		}
		if n := len(ed.Tables); n > 0 {
			in.note("Left out %d turntable%s.", n, plural(n))
		}
	}
	if in.ellipses > 0 {
		in.note("%d curve%s drawn as part of an ellipse in the panel came in as circular curves.", in.ellipses, plural(in.ellipses))
	}
	if in.beziers > 0 {
		in.note("%d curve%s drawn as a Bézier curve in the panel came in straight.", in.beziers, plural(in.beziers))
	}
	if in.crossovers > 0 {
		in.note("Check the direction of the single crossovers (%d): the file's right and left hand were taken as Layout Editor's.", in.crossovers)
	}
	if len(in.plan.Tracks)+len(in.plan.Junctions) == 0 {
		return Import{}, invalid("the panel has no track in it")
	}
	in.joinLooseEnds()
	in.plan.Grid = DefaultGrid
	plan := in.plan.Normalize()
	if err := plan.Validate(); err != nil {
		return Import{}, fmt.Errorf("the imported track doesn't hold together: %w", err)
	}
	return Import{Plan: plan, Notes: in.notes}, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// jmriLeg names the end of a turnout or crossing a connection type
// names: TURNOUT_B, LEVEL_XING_C, or the numbers older files use.
func jmriLeg(typ string) string {
	switch typ {
	case "TURNOUT_A", "2":
		return "A"
	case "TURNOUT_B", "3":
		return "B"
	case "TURNOUT_C", "4":
		return "C"
	case "TURNOUT_D", "5":
		return "D"
	case "LEVEL_XING_A", "6":
		return "A"
	case "LEVEL_XING_B", "7":
		return "B"
	case "LEVEL_XING_C", "8":
		return "C"
	case "LEVEL_XING_D", "9":
		return "D"
	}
	return ""
}

type importer struct {
	scale float64
	plan  Plan
	notes []string
	// joins counts the track ends at each point.
	joins                         map[string]int
	at                            map[string]Vec
	ellipses, beziers, crossovers int
}

func (in *importer) note(format string, args ...any) {
	in.notes = append(in.notes, fmt.Sprintf(format, args...))
}

// point adds a point at v, in millimeters.
func (in *importer) point(v Vec, bumper bool) string {
	id := fmt.Sprintf("p%d", len(in.plan.Points)+1)
	in.plan.Points = append(in.plan.Points, Point{ID: id, X: v.X, Y: v.Y, Bumper: bumper})
	in.at[id] = v
	return id
}

// use takes a point for one more track end, or a copy of it when two
// ends meet there already, which the plan doesn't allow without a
// junction.
func (in *importer) use(id string) string {
	if in.joins[id] < 2 {
		in.joins[id]++
		return id
	}
	copied := in.point(in.at[id], false)
	in.joins[copied]++
	return copied
}

func (in *importer) track(a, b string, angle float64) {
	if in.at[a].dist(in.at[b]) < 0.01 {
		// A link between two ends on the same spot, which
		// joinLooseEnds joins.
		return
	}
	a, b = in.use(a), in.use(b)
	in.plan.Tracks = append(in.plan.Tracks, Track{ID: fmt.Sprintf("t%d", len(in.plan.Tracks)+1), A: a, B: b, Angle: angle})
}

func (in *importer) junction(kind string, ends []string, at *Vec) {
	for i, e := range ends {
		ends[i] = in.use(e)
	}
	in.plan.Junctions = append(in.plan.Junctions, Junction{ID: fmt.Sprintf("j%d", len(in.plan.Junctions)+1), Kind: kind, Ends: ends, At: at})
}

// turnout imports a Layout Editor turnout of any type but a slip. Its
// ends A to D become points other track joins as A to D.
func (in *importer) turnout(t jmriJunction, ends map[string]string) error {
	cen, legs, err := t.legs(in.scale)
	if err != nil {
		return err
	}
	typ := t.Type
	switch typ {
	case "1", "2", "3", "RH_TURNOUT", "LH_TURNOUT", "WYE_TURNOUT":
		if legs[0] == nil {
			legs[0] = mirror(cen, legs[1])
		}
		if legs[0] == nil || legs[1] == nil || legs[2] == nil {
			return fmt.Errorf("it lacks the place of an end")
		}
		ids := map[string]string{}
		for i, leg := range []string{"A", "B", "C"} {
			ids[leg] = in.point(*legs[i], false)
			ends[t.Ident+"/"+leg] = ids[leg]
		}
		kind := KindTurnout
		if typ == "3" || typ == "WYE_TURNOUT" {
			kind = KindWye
		}
		closed, thrown := ids["B"], ids["C"]
		if t.Continuing == "4" { // the B leg is the thrown one
			closed, thrown = thrown, closed
		}
		in.junction(kind, []string{ids["A"], closed, thrown}, &cen)
		return nil
	case "4", "5", "6", "DOUBLE_XOVER", "RH_XOVER", "LH_XOVER":
		if legs[0] == nil {
			legs[0] = mirror(cen, legs[2])
		}
		if legs[3] == nil {
			legs[3] = mirror(cen, legs[1])
		}
		for _, leg := range legs {
			if leg == nil {
				return fmt.Errorf("it lacks the place of an end")
			}
		}
		ids := [4]string{}
		for i, leg := range []string{"A", "B", "C", "D"} {
			ids[i] = in.point(*legs[i], false)
			ends[t.Ident+"/"+leg] = ids[i]
		}
		// A and B are one track, D and C the other beside it; a right
		// hand crossover crosses from A to C, a left hand one from D to
		// B.
		a1, a2, b1, b2 := 0, 1, 3, 2
		kind := KindDoubleCrossover
		switch typ {
		case "5", "RH_XOVER":
			kind = KindCrossover
			in.crossovers++
		case "6", "LH_XOVER":
			kind = KindCrossover
			a1, a2, b1, b2 = 3, 2, 0, 1
			in.crossovers++
		}
		// Whatever the letters, a1 must lie beside b1.
		if legs[a1].dist(*legs[b1]) > legs[a1].dist(*legs[b2]) {
			b1, b2 = b2, b1
		}
		in.junction(kind, []string{ids[a1], ids[a2], ids[b1], ids[b2]}, nil)
		return nil
	}
	return fmt.Errorf("its type %s isn't one Interlock knows", typ)
}

// crossing imports a level crossing: A to C crosses B to D.
func (in *importer) crossing(x jmriJunction, ends map[string]string) error {
	cen, legs, err := x.legs(in.scale)
	if err != nil {
		return err
	}
	if legs[2] == nil {
		legs[2] = mirror(cen, legs[0])
	}
	if legs[3] == nil {
		legs[3] = mirror(cen, legs[1])
	}
	ids := [4]string{}
	for i, leg := range []string{"A", "B", "C", "D"} {
		if legs[i] == nil {
			return fmt.Errorf("it lacks the place of an end")
		}
		ids[i] = in.point(*legs[i], false)
		ends[x.Ident+"/"+leg] = ids[i]
	}
	in.junction(KindCrossing, []string{ids[0], ids[2], ids[1], ids[3]}, nil)
	return nil
}

// joinLooseEnds joins track ends that lie on the same spot but at
// different points, as happens where an exported layout's pieces were
// laid end to end without being connected.
func (in *importer) joinLooseEnds() {
	var loose []int
	for i, p := range in.plan.Points {
		if in.joins[p.ID] == 1 {
			loose = append(loose, i)
		}
	}
	merged := map[string]string{}
	for i, a := range loose {
		pa := in.plan.Points[a]
		if merged[pa.ID] != "" {
			continue
		}
		for _, b := range loose[i+1:] {
			pb := in.plan.Points[b]
			if merged[pb.ID] == "" && math.Abs(pa.X-pb.X) < 0.5 && math.Abs(pa.Y-pb.Y) < 0.5 {
				merged[pb.ID] = pa.ID
				break
			}
		}
	}
	if len(merged) == 0 {
		return
	}
	swap := func(id string) string {
		if to, ok := merged[id]; ok {
			return to
		}
		return id
	}
	for i := range in.plan.Tracks {
		t := &in.plan.Tracks[i]
		t.A, t.B = swap(t.A), swap(t.B)
	}
	for i := range in.plan.Junctions {
		for e, id := range in.plan.Junctions[i].Ends {
			in.plan.Junctions[i].Ends[e] = swap(id)
		}
	}
	// Normalize drops the points nothing uses any more.
}
