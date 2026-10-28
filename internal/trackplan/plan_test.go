package trackplan

import (
	"math"
	"strings"
	"testing"
)

func TestArc(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	quarter := arc{Vec{0, 0}, Vec{216, 216}, 90}
	if !near(quarter.radius(), 216) || !near(quarter.length(), 216*math.Pi/2) {
		t.Errorf("quarter circle: radius %v, length %v", quarter.radius(), quarter.length())
	}
	if !near(quarter.startHeading(), 0) || !near(quarter.endHeading(), 90) {
		t.Errorf("quarter circle heads %v then %v, want 0 then 90", quarter.startHeading(), quarter.endHeading())
	}
	if mid := quarter.at(0.5); !near(mid.X, 216*math.Sin(math.Pi/4)) || !near(mid.Y, 216-216*math.Cos(math.Pi/4)) {
		t.Errorf("halfway along = %+v", mid)
	}
	left := arc{Vec{0, 0}, Vec{216, -216}, -90}
	if !near(left.startHeading(), 0) || !near(left.endHeading(), -90) || !near(left.at(1).X, 216) {
		t.Errorf("a left turn heads %v then %v", left.startHeading(), left.endHeading())
	}
	straight := arc{Vec{0, 0}, Vec{3, 4}, 0}
	if straight.length() != 5 || !math.IsInf(straight.radius(), 1) || straight.at(0.5) != (Vec{1.5, 2}) {
		t.Errorf("straight: length %v, radius %v", straight.length(), straight.radius())
	}
	if got := turnDiff(10, 350); got != 20 {
		t.Errorf("turnDiff(10, 350) = %v, want 20", got)
	}
	if got := curveLength(quad(Vec{0, 0}, Vec{5, 0}, Vec{10, 0})); !near(got, 10) {
		t.Errorf("a straight Bézier curve is %v long, want 10", got)
	}
	if c, ok := lineCross(Vec{0, 0}, Vec{10, 10}, Vec{0, 10}, Vec{10, 0}); !ok || c != (Vec{5, 5}) {
		t.Errorf("lineCross = %+v, %v", c, ok)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Plan)
		want   string
	}{
		{"fine", func(*Plan) {}, ""},
		{"no grid", func(p *Plan) { p.Grid = 0 }, "grid spacing"},
		{"bad ID", func(p *Plan) { p.Tracks[0].ID = "T 1" }, "lowercase"},
		{"repeated ID", func(p *Plan) { p.Points[1].ID = "u0" }, "two of the plan's points"},
		{"missing point", func(p *Plan) { p.Tracks[0].B = "nowhere" }, "isn't in the plan"},
		{"a loop of one", func(p *Plan) { p.Tracks[0].B = "u0" }, "same point"},
		{"no length", func(p *Plan) { p.Points[1].X, p.Points[1].Y = 0, 0 }, "no length"},
		{"three at a point", func(p *Plan) {
			p.Points = append(p.Points, Point{ID: "z", X: 300, Y: 99})
			p.Tracks = append(p.Tracks, Track{ID: "t9", A: "u1", B: "z"})
		}, "more than two"},
		{"full circle", func(p *Plan) { p.Tracks[0].Angle = 360 }, "full circle"},
		{"unknown kind", func(p *Plan) { p.Junctions[0].Kind = "slip" }, "kinds are"},
		{"too few ends", func(p *Plan) { p.Junctions[0].Ends = p.Junctions[0].Ends[:3] }, "needs 4 ends"},
		{"turnout without its middle", func(p *Plan) {
			p.Junctions[0].Kind = KindTurnout
			p.Junctions[0].Ends = p.Junctions[0].Ends[:3]
		}, "where its routes part"},
		{"crossing with a switch", func(p *Plan) { p.Junctions[0].Kind = KindCrossing }, "nothing to set"},
		{"unknown block", func(p *Plan) { p.Tracks[0].Block = "nope" }, "block \"nope\""},
		{"unknown layer", func(p *Plan) { p.Junctions[0].Layer = "upper" }, "layer \"upper\""},
		{"layer", func(p *Plan) {
			p.Layers = []Layer{{ID: "upper", Name: "Upper deck"}}
			p.Junctions[0].Layer = "upper"
		}, ""},
		{"far away", func(p *Plan) { p.Points[0].X = MaxCoord + 1 }, "within"},
		{"flat platform", func(p *Plan) { p.Platforms = []Platform{{ID: "f1", Length: 100}} }, "length and width"},
		{"empty label", func(p *Plan) { p.Labels = []Label{{ID: "l1", Text: " "}} }, "needs text"},
		{"station", func(p *Plan) {
			p.Stations = []Station{{ID: "times-sq", Name: "Times Sq"}}
			p.Blocks[0].Station = "times-sq"
		}, ""},
		{"unknown station", func(p *Plan) { p.Blocks[0].Station = "nope" }, "station \"nope\""},
		{"marker", func(p *Plan) { p.Markers = []Marker{{ID: "m1", Track: "t1", At: 50, Length: 456}} }, ""},
		{"marker off the plan", func(p *Plan) { p.Markers = []Marker{{ID: "m1", Track: "nope"}} }, "track \"nope\""},
		{"marker for a negative length", func(p *Plan) { p.Markers = []Marker{{ID: "m1", Track: "t1", Length: -1}} }, "length between"},
		{"two markers alike", func(p *Plan) { p.Markers = []Marker{{ID: "m1", Track: "t1"}, {ID: "m1", Track: "t2"}} }, "two of the plan's markers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := station()
			tt.change(&p)
			err := p.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("Validate: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("Validate = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	p := station()
	p.Grid = 0
	p.Points = append(p.Points, Point{ID: "lost", X: 5, Y: 5})
	p.Points[1].X = 300.004
	p.Tracks[0], p.Tracks[3] = p.Tracks[3], p.Tracks[0]
	p.Labels = []Label{{ID: "l1", X: 1, Y: 2, Text: " Times Sq "}}
	p.Markers = []Marker{{ID: "m2", Track: "t1", At: 1e6}, {ID: "m1", Track: "gone"}, {ID: "m3", Track: "t1", At: -5, Length: 456.004}}
	n := p.Normalize()
	if len(n.Markers) != 2 || n.Markers[0].ID != "m2" || n.Markers[1].At != 0 || n.Markers[1].Length != 456 {
		t.Errorf("markers %+v; want m2 and m3, on their track, the one on a track gone dropped", n.Markers)
	} else if length := n.trackLengths()["t1"]; n.Markers[0].At != round2(length) {
		t.Errorf("a marker past its track's end is at %v; want its end, %v", n.Markers[0].At, length)
	}
	if n.Grid != DefaultGrid {
		t.Errorf("grid = %v, want the default %v", n.Grid, DefaultGrid)
	}
	for _, pt := range n.Points {
		if pt.ID == "lost" {
			t.Error("a point no track uses was kept")
		}
	}
	if n.Points[0].ID != "l0" || n.Points[len(n.Points)-1].ID != "u3" {
		t.Errorf("points in order %s…%s", n.Points[0].ID, n.Points[len(n.Points)-1].ID)
	}
	if n.Tracks[0].ID != "t1" || n.Labels[0].Text != "Times Sq" {
		t.Errorf("tracks start at %s, label %q", n.Tracks[0].ID, n.Labels[0].Text)
	}
	for _, pt := range n.Points {
		if pt.ID == "u1" && pt.X != 300 {
			t.Errorf("u1 at %v, want it rounded to 300", pt.X)
		}
	}
	if (Plan{}).Normalize().Tracks == nil {
		t.Error("an empty plan normalizes to nil lists")
	}
}
