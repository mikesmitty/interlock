package trackplan

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// panelFile is a Layout Editor panel as RailModeller Pro exports one, a
// unit to the centimeter: a line from a buffer stop curving into a right
// hand turnout, its branch and straight leg ending in track, a double
// crossover, a level crossing, and two pieces laid end to end that
// aren't connected.
const panelFile = `<?xml version="1.0" encoding="UTF-8"?>
<layout-config>
  <LayoutEditor class="jmri.jmrit.display.layoutEditor.configurexml.LayoutEditorXml">
    <positionablepoint ident="EB1" type="END_BUMPER" x="0" y="0" connect1name="T1"/>
    <positionablepoint ident="A2" type="ANCHOR" x="20" y="0" connect1name="T1"/>
    <positionablepoint ident="A3" type="ANCHOR" x="40" y="0" connect1name="T3"/>
    <positionablepoint ident="A4" type="ANCHOR" x="44.07" y="6.57" connect1name="T4"/>
    <positionablepoint ident="A5" type="ANCHOR" x="61.34" y="16.66" connect1name="T6"/>
    <positionablepoint ident="A6" type="ANCHOR" x="61.34" y="16.66" connect1name="T7"/>
    <positionablepoint ident="A7" type="ANCHOR" x="80" y="20" connect1name="T7"/>
    <tracksegment ident="T1" connect1name="EB1" type1="POS_POINT" connect2name="A2" type2="POS_POINT"/>
    <tracksegment ident="T2" connect1name="A2" type1="POS_POINT" connect2name="TO0" type2="TURNOUT_A"/>
    <tracksegment ident="T3" connect1name="TO0" type1="TURNOUT_B" connect2name="A3" type2="POS_POINT"/>
    <tracksegment ident="T4" connect1name="TO0" type1="TURNOUT_C" connect2name="A4" type2="POS_POINT" arc="yes" circle="yes" angle="15.0" flip="no"/>
    <tracksegment ident="T5" connect1name="A4" type1="POS_POINT" connect2name="A4" type2="POS_POINT"/>
    <tracksegment ident="T6" connect1name="A4" type1="POS_POINT" connect2name="A5" type2="POS_POINT"/>
    <tracksegment ident="T7" connect1name="A6" type1="POS_POINT" connect2name="A7" type2="POS_POINT" arc="yes" circle="yes" angle="45.0" flip="yes"/>
    <tracksegment ident="T8" connect1name="X1" type1="TURNOUT_A" connect2name="L1" type2="LEVEL_XING_A"/>
    <layoutturnout ident="TO0" type="1" continuing="2" xcen="26.3" ycen="0" xa="20" ya="0" xb="32.6" yb="0" xc="32.5" yc="1.7"/>
    <layoutturnout ident="X1" type="4" continuing="2" xcen="215.5" ycen="11.65" xa="200" ya="10" xb="231" yb="10" xc="231" yc="13.3" xd="200" yd="13.3"/>
    <levelxing ident="L1" xcen="180" ycen="0" xa="175" ya="0" xb="180" yb="-5"/>
    <layoutSlip ident="SL1"/>
  </LayoutEditor>
</layout-config>`

func TestImportJMRI(t *testing.T) {
	imp, err := ImportJMRI(strings.NewReader(panelFile), DefaultJMRIScale)
	if err != nil {
		t.Fatal(err)
	}
	p := imp.Plan
	if len(p.Tracks) != 6 || len(p.Junctions) != 3 {
		t.Fatalf("imported %d tracks and %d junctions, want 6 and 3", len(p.Tracks), len(p.Junctions))
	}
	pos := map[string]Point{}
	for _, pt := range p.Points {
		pos[pt.ID] = pt
	}
	if !pos[p.Tracks[0].A].Bumper || pos[p.Tracks[0].B].X != 200 {
		t.Errorf("first track runs from %+v to %+v, want from a buffer stop to 200 mm", pos[p.Tracks[0].A], pos[p.Tracks[0].B])
	}

	var turnout, cross, xing Junction
	for _, j := range p.Junctions {
		switch j.Kind {
		case KindTurnout:
			turnout = j
		case KindDoubleCrossover:
			cross = j
		case KindCrossing:
			xing = j
		}
	}
	if turnout.At == nil || *turnout.At != (Vec{263, 0}) || pos[turnout.Ends[1]].X != 326 || pos[turnout.Ends[2]].Y != 17 {
		t.Errorf("turnout = %+v", turnout)
	}
	// A beside D: a1 at A, b1 at D.
	if pos[cross.Ends[0]].X != 2000 || pos[cross.Ends[2]] != (Point{ID: cross.Ends[2], X: 2000, Y: 133}) {
		t.Errorf("crossover ends %v", cross.Ends)
	}
	if pos[xing.Ends[1]].X != 1850 || pos[xing.Ends[3]].Y != 50 {
		t.Errorf("crossing ends %+v %+v, want A's opposite at 1850 and B's at 50 mm", pos[xing.Ends[1]], pos[xing.Ends[3]])
	}

	angles := map[float64]bool{}
	for _, tr := range p.Tracks {
		angles[tr.Angle] = true
	}
	if !angles[15] || !angles[-45] {
		t.Errorf("track angles %v, want the branch's 15 and the flipped curve's -45", angles)
	}

	// The curve laid end to end with the line before it joins it; the
	// zero length segment is no track.
	top := p.Topology()
	var open []string
	for _, pr := range top.Problems {
		if pr.Kind == ProblemOpenEnd {
			open = append(open, pr.Point)
		}
	}
	if len(open) != 8 {
		t.Errorf("open ends %v, want the straight leg's, the curve's and six of the junctions'", open)
	}
	if strings.Join(imp.Notes, " ") != "Left out 1 slip; draw them as crossings with turnouts." {
		t.Errorf("notes = %q", imp.Notes)
	}
	for _, tr := range p.Tracks {
		if tr.Angle == 15 {
			a, b := pos[tr.A].pos(), pos[tr.B].pos()
			if h := (arc{a, b, tr.Angle}).startHeading(); math.Abs(h-15) > 1 {
				t.Errorf("the branch's curve sets off heading %.1f, want along the turnout's 15", h)
			}
		}
	}
}

func TestImportJMRIRejects(t *testing.T) {
	tests := []struct {
		name, body string
		scale      float64
		want       string
	}{
		{"not XML", "tiles: []", DefaultJMRIScale, "isn't a JMRI panel"},
		{"no panel", "<layout-config/>", DefaultJMRIScale, "no Layout Editor"},
		{"no track", "<layout-config><LayoutEditor/></layout-config>", DefaultJMRIScale, "no track"},
		{"bad scale", panelFile, 0, "scale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ImportJMRI(strings.NewReader(tt.body), tt.scale)
			var invalidErr *InvalidError
			if !errors.As(err, &invalidErr) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ImportJMRI = %v, want an *InvalidError containing %q", err, tt.want)
			}
		})
	}
}
