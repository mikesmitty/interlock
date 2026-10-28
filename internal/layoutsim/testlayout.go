package layoutsim

import (
	"fmt"
	"math"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/openlcb"
)

// StationID is the simulated command station's node ID.
const StationID openlcb.NodeID = 0x0A_00_00_00_01_00

// OutpostID is the node ID of the nth simulated outpost, from 1:
// 0A.00.00.00.01.10, .20 and so on. Like the board, an outpost takes the
// four node IDs after its own for its detectors' report ranges, so the
// outposts are spaced well apart.
func OutpostID(n int) openlcb.NodeID { return StationID + 0x10*openlcb.NodeID(n) }

// TestOutposts is how many outposts the test layout uses.
const TestOutposts = 3

// The test layout's measures: Kato Unitrack N double track, 33 mm apart
// on the straights and on concentric R315 and R282 curves.
const (
	straight    = 6 * 248.0
	outerRadius = 315.0
	innerRadius = 282.0
)

// A Kato #4 turnout set at its points heading east: the closed end, the
// thrown end and where the routes part, then the end of the curve that
// brings the branch alongside, 32.8 mm over.
var (
	no4Closed = trackplan.Vec{X: 126, Y: 0}
	no4Thrown = trackplan.Vec{X: 124.49, Y: 16.39}
	no4At     = trackplan.Vec{X: 63.32, Y: 0}
	no4Beside = trackplan.Vec{X: 248.98, Y: 32.8}
)

// TestLayout is a small layout to run trains on before a real plan has
// its blocks: a double-track loop, the outer track clockwise and the
// inner counterclockwise, with Park Av on the top straight and Main St on
// the bottom, where a siding outside the outer track lets one train pass
// another. A crossover east of Park Av turns trains back. Its nine blocks
// and three turnouts are linked to the first three simulated outposts.
func TestLayout() trackplan.Plan {
	b := newBuilder()
	ent := func(kind entity.Kind, outpost int, slug string) string {
		return entity.ProfileID(kind, OutpostID(outpost), slug)
	}
	detected := func(id, name string, outpost, track int, station string) {
		slug := fmt.Sprintf("track_%d", track)
		b.plan.Blocks = append(b.plan.Blocks, trackplan.Block{
			ID: id, Name: name, Station: station,
			Occupancy: ent(entity.KindBlock, outpost, slug),
			Reporter:  ent(entity.KindReporter, outpost, slug),
		})
	}
	b.plan.Stations = []trackplan.Station{{ID: "park", Name: "Park Av"}, {ID: "main", Name: "Main St"}}
	detected("main-outer", "Main St outer", 1, 1, "main")
	detected("main-siding", "Main St siding", 1, 2, "main")
	detected("main-inner", "Main St inner", 1, 3, "main")
	detected("park-outer", "Park Av outer", 2, 1, "park")
	detected("park-inner", "Park Av inner", 2, 2, "park")
	detected("east-outer", "East outer", 2, 3, "")
	detected("east-inner", "East inner", 2, 4, "")
	detected("west-outer", "West outer", 3, 1, "")
	detected("west-inner", "West inner", 3, 2, "")

	for _, loop := range []struct {
		name, east, west, park string
		y, r                   float64
	}{
		{"outer", "east-outer", "west-outer", "park-outer", outerRadius, outerRadius},
		{"inner", "east-inner", "west-inner", "park-inner", innerRadius, innerRadius},
	} {
		y, r := loop.y, loop.r
		n := func(s string) string { return loop.name + "-" + s }
		// Along the top, eastward, to the crossover.
		b.track(n("top-west"), v(0, -y), v(496, -y), 0, loop.west)
		b.track(n("top-park"), v(496, -y), v(1240, -y), 0, loop.park)
		// Around the east end, clockwise.
		b.track(n("east-1"), v(straight, -y), v(straight+r, 0), 90, loop.east)
		b.track(n("east-2"), v(straight+r, 0), v(straight, y), 90, loop.east)
		b.track(n("bottom-east"), v(straight, y), v(1240, y), 0, loop.east)
		// Around the west end, back to the top.
		b.track(n("bottom-west"), v(248, y), v(0, y), 0, loop.west)
		b.track(n("west-1"), v(0, y), v(-r, 0), 90, loop.west)
		b.track(n("west-2"), v(-r, 0), v(0, -y), 90, loop.west)
	}
	// Main St: the inner track straight through, the outer through two
	// turnouts with the siding outside it.
	b.track("inner-main", v(1240, innerRadius), v(248, innerRadius), 0, "main-inner")
	east := b.turnout("main-east", v(1240, outerRadius), 180, -1, ent(entity.KindTurnout, 1, "servo_1"))
	west := b.turnout("main-west", v(248, outerRadius), 0, 1, ent(entity.KindTurnout, 1, "servo_2"))
	b.track("outer-main", east.closed, west.closed, 0, "main-outer")
	b.track("siding-east", east.thrown, east.beside, 15, "main-siding")
	b.track("siding", east.beside, west.beside, 0, "main-siding")
	b.track("siding-west", west.beside, west.thrown, 15, "main-siding")
	// The crossover from the outer track's west end to the inner's east.
	b.plan.Junctions = append(b.plan.Junctions, trackplan.Junction{
		ID: "park-crossover", Kind: trackplan.KindCrossover,
		Ends:   []string{b.pt(v(1240, -outerRadius)), b.pt(v(straight, -outerRadius)), b.pt(v(1240, -innerRadius)), b.pt(v(straight, -innerRadius))},
		Switch: ent(entity.KindTurnout, 2, "servo_1"),
	})

	b.plan.Platforms = []trackplan.Platform{
		{ID: "park-north", X: 868, Y: -outerRadius - 30, Length: 600, Width: 30},
		{ID: "park-south", X: 868, Y: -innerRadius + 30, Length: 600, Width: 30},
		{ID: "main-north", X: 744, Y: innerRadius - 30, Length: 600, Width: 30},
		{ID: "main-south", X: 744, Y: outerRadius + no4Beside.Y + 30, Length: 480, Width: 30},
	}
	// A stop marker at the far end of each platform, for trains of any
	// length running the way the loop goes.
	b.marker("park-outer-stop", "outer-top-park", 1160, false)
	b.marker("park-inner-stop", "inner-top-park", 576, true)
	b.marker("main-inner-stop", "inner-main", 1036, true)
	b.marker("main-siding-stop", "siding", 512, false)
	b.plan.Labels = []trackplan.Label{
		{ID: "park-name", X: 700, Y: -outerRadius - 70, Text: "Park Av"},
		{ID: "main-name", X: 700, Y: innerRadius - 70, Text: "Main St"},
	}
	return b.plan
}

// TestTrains are the test layout's trains: subway trains of four and five
// cars, one of them a motor car with the decoder, one at each station.
func TestTrains() []TrainSpec {
	return []TrainSpec{
		{Name: "Local 1", Address: 101, Cars: []float64{114, 114, 114, 114}, Motor: 0, TopSpeed: 160, Block: "main-outer"},
		{Name: "Local 2", Address: 102, Cars: []float64{97, 97, 97, 97, 97}, Motor: 2, TopSpeed: 140, Block: "park-inner", Reverse: true},
	}
}

func v(x, y float64) trackplan.Vec { return trackplan.Vec{X: x, Y: y} }

type builder struct {
	plan   trackplan.Plan
	points map[[2]float64]string
}

func newBuilder() *builder {
	return &builder{plan: trackplan.Plan{Grid: trackplan.DefaultGrid}, points: map[[2]float64]string{}}
}

// pt is the point at a place, made the first time it is asked for.
func (b *builder) pt(at trackplan.Vec) string {
	key := [2]float64{math.Round(at.X * 100), math.Round(at.Y * 100)}
	if id, ok := b.points[key]; ok {
		return id
	}
	id := fmt.Sprintf("p%d", len(b.plan.Points)+1)
	b.points[key] = id
	b.plan.Points = append(b.plan.Points, trackplan.Point{ID: id, X: key[0] / 100, Y: key[1] / 100})
	return id
}

func (b *builder) track(id string, from, to trackplan.Vec, angle float64, block string) {
	b.plan.Tracks = append(b.plan.Tracks, trackplan.Track{ID: id, A: b.pt(from), B: b.pt(to), Angle: angle, Block: block})
}

// marker puts a stop marker on a straight track running east or west,
// where it reaches x.
func (b *builder) marker(id, track string, x float64, reverse bool) {
	for _, t := range b.plan.Tracks {
		if t.ID != track {
			continue
		}
		for _, p := range b.plan.Points {
			if p.ID == t.A {
				b.plan.Markers = append(b.plan.Markers, trackplan.Marker{ID: id, Track: track, At: math.Abs(x - p.X), Reverse: reverse})
				return
			}
		}
	}
	panic("no track " + track)
}

type placed struct{ closed, thrown, beside trackplan.Vec }

// turnout puts a #4 turnout with its points at points, heading as given,
// branching to the right of that heading with side 1 or to the left with
// -1, and returns where its ends are and where its branch comes
// alongside.
func (b *builder) turnout(id string, points trackplan.Vec, heading, side float64, switchID string) placed {
	r := heading * math.Pi / 180
	cos, sin := math.Cos(r), math.Sin(r)
	at := func(local trackplan.Vec) trackplan.Vec {
		y := local.Y * side
		return v(points.X+local.X*cos-y*sin, points.Y+local.X*sin+y*cos)
	}
	p := placed{closed: at(no4Closed), thrown: at(no4Thrown), beside: at(no4Beside)}
	center := at(no4At)
	b.plan.Junctions = append(b.plan.Junctions, trackplan.Junction{
		ID: id, Kind: trackplan.KindTurnout, Switch: switchID,
		Ends: []string{b.pt(points), b.pt(p.closed), b.pt(p.thrown)},
		At:   &center,
	})
	return p
}
