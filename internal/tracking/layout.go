package tracking

import (
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

// searchLimit is how far along the track a search looks for where a
// decoder went, in millimeters.
const searchLimit = 10_000

// minUnit is how much running, in seconds at full speed, a run between
// two entries needs before its time says how fast the train goes.
const minUnit = 0.5

// learnRate is how far each new measure moves the top speed toward it.
const learnRate = 0.3

// inside is how far into a block a decoder is put when it is reported
// there, in millimeters.
const inside = 0.5

// lineStep is how far apart the points of a train's line are, in
// millimeters.
const lineStep = 20

// rosterInfo is what the roster says about the locomotives at an
// address.
type rosterInfo struct {
	ids       []string
	name      string
	length    float64
	decoderAt float64
	decoder   string
	cvs       decoderCVs
}

// decoderCVs are the CVs that set how a decoder moves: the speed curve's
// start, middle and top voltages and the momentum.
type decoderCVs struct {
	start, mid, high, accel, decel byte
	// known is whether the momentum CVs are on record.
	known bool
}

// tracked is one train as tracking follows it.
type tracked struct {
	addr detector.Address
	// block is where a detector last saw the decoder; seen is whether one
	// sees it now.
	block string
	seen  bool
	// stretch is where the train is, from its rear to its front; nil
	// while only its block is known.
	stretch trackplan.Stretch
	// target is the speed step it is set to and step the one its decoder
	// has reached, negative in reverse.
	target, step float64
	estop        bool
	// heading is the way it last moved: 1 forward, -1 in reverse, 0 not
	// yet.
	heading float64
	// Since the entry it was last put in place by: whether there was one,
	// how far it moved by the estimate (forward positive), how much
	// running at full speed the speed curve made of the time, the way it
	// ran and whether it went both ways.
	anchored bool
	moved    float64
	unit     float64
	ran      float64
	turned   bool
	// top is the speed measured at full speed, in millimeters a second;
	// 0 until measured.
	top     float64
	samples int
	updated time.Time
}

// layout is what tracking knows. One goroutine owns it.
type layout struct {
	net       *trackplan.Network
	revision  string
	reporters map[string][]string
	detected  map[string]bool
	switches  map[string]string
	turnouts  map[string]string
	seen      map[string][]detector.Address
	trains    map[detector.Address]*tracked
	roster    map[detector.Address]*rosterInfo
	entries   map[string]detector.Address
	names     map[string]string
	powerOff  bool
	estop     bool
	now       time.Time
	changed   map[detector.Address]bool
}

func newLayout() *layout {
	return &layout{
		net:       trackplan.Plan{}.Network(),
		reporters: map[string][]string{},
		detected:  map[string]bool{},
		switches:  map[string]string{},
		turnouts:  map[string]string{},
		seen:      map[string][]detector.Address{},
		trains:    map[detector.Address]*tracked{},
		roster:    map[detector.Address]*rosterInfo{},
		entries:   map[string]detector.Address{},
		names:     map[string]string{},
		changed:   map[detector.Address]bool{},
	}
}

// addressOf is a roster train's address as detectors report it.
func addressOf(number uint16, long bool) detector.Address {
	if long || number > 127 {
		return detector.Address{Type: detector.Long, Number: number}
	}
	return detector.Address{Type: detector.Short, Number: number}
}

// setPlan follows a newly saved plan. Trains whose track is gone are
// known by their block only until they run into the next.
func (l *layout) setPlan(plan trackplan.Plan, revision string) {
	l.net = plan.Network()
	l.revision = revision
	l.reporters = map[string][]string{}
	l.detected = map[string]bool{}
	for _, b := range plan.Blocks {
		if b.Reporter == "" {
			continue
		}
		l.reporters[b.Reporter] = append(l.reporters[b.Reporter], b.ID)
		l.detected[b.ID] = true
	}
	l.switches = map[string]string{}
	for _, j := range plan.Junctions {
		if j.Switch != "" {
			l.switches[j.ID] = j.Switch
		}
	}
	for _, t := range l.trains {
		if t.stretch != nil && !t.stretch.Valid(l.net) {
			t.stretch, t.anchored = nil, false
			l.changed[t.addr] = true
		}
	}
	// What the reporters see now, in the blocks they are linked to now.
	for id, locos := range l.seen {
		for _, b := range l.reporters[id] {
			for _, a := range locos {
				l.enter(a, b)
			}
		}
	}
}

// position is how a junction is set, as its switch entity says: ""
// while it moves or is unknown. A junction without a switch stays
// closed.
func (l *layout) position(junction string) trackplan.Position {
	id, ok := l.switches[junction]
	if !ok {
		return trackplan.Closed
	}
	switch l.turnouts[id] {
	case entity.StateClosed:
		return trackplan.Closed
	case entity.StateThrown:
		return trackplan.Thrown
	}
	return ""
}

// with is position with the junctions in set taken as set there.
func (l *layout) with(set map[string]trackplan.Position) func(string) trackplan.Position {
	return func(j string) trackplan.Position {
		if p, ok := set[j]; ok {
			return p
		}
		return l.position(j)
	}
}

// entity follows a turnout's position and what a reporter sees.
func (l *layout) entity(e entity.Entity) {
	switch e.Kind {
	case entity.KindTurnout:
		if e.Removed {
			delete(l.turnouts, e.ID)
		} else {
			l.turnouts[e.ID] = e.State
		}
	case entity.KindReporter:
		if !e.Available && !e.Removed {
			return // offline: what it saw last still stands
		}
		var now []detector.Address
		if !e.Removed {
			for _, loco := range e.Locomotives {
				now = append(now, loco.Address)
			}
		}
		before := l.seen[e.ID]
		if len(now) == 0 {
			delete(l.seen, e.ID)
		} else {
			l.seen[e.ID] = now
		}
		for _, b := range l.reporters[e.ID] {
			for _, a := range before {
				if !slices.Contains(now, a) {
					l.exit(a, b)
				}
			}
			for _, a := range now {
				if !slices.Contains(before, a) {
					l.enter(a, b)
				}
			}
		}
	}
}

// train follows the roster and the speed each train is set to.
func (l *layout) train(tr train.Train, cvs func(decoder string) decoderCVs) {
	addr := addressOf(tr.Address, tr.Long)
	if tr.Roster {
		l.rosterEntry(tr.Entry, tr.Removed, cvs)
	}
	if tr.Removed {
		return
	}
	t := l.get(addr)
	target := float64(tr.Speed)
	if tr.Direction == train.Reverse {
		target = -target
	}
	if tr.EStop {
		t.step, target = 0, 0
	}
	if t.target != target || t.estop != tr.EStop {
		t.target, t.estop = target, tr.EStop
		l.changed[addr] = true
	}
}

// rosterEntry keeps what a roster entry says, under the address it
// has now.
func (l *layout) rosterEntry(e train.Entry, removed bool, cvs func(decoder string) decoderCVs) {
	if old, ok := l.entries[e.ID]; ok {
		delete(l.entries, e.ID)
		if info := l.roster[old]; info != nil {
			info.ids = slices.DeleteFunc(info.ids, func(id string) bool { return id == e.ID })
			if len(info.ids) == 0 {
				delete(l.roster, old)
			}
			l.changed[old] = true
		}
	}
	if removed {
		delete(l.names, e.ID)
		return
	}
	l.names[e.ID] = e.Name
	addr := addressOf(e.Address, e.Long)
	l.entries[e.ID] = addr
	info := l.roster[addr]
	if info == nil {
		info = &rosterInfo{}
		l.roster[addr] = info
	}
	info.ids = append(info.ids, e.ID)
	slices.Sort(info.ids)
	if info.ids[0] == e.ID || info.length == 0 {
		info.length, info.decoderAt = e.Length, e.DecoderAt
		info.decoder = e.Installed
		if cvs != nil && e.Installed != "" {
			info.cvs = cvs(e.Installed)
		}
	}
	names := make([]string, 0, len(info.ids))
	for _, id := range info.ids {
		names = append(names, l.names[id])
	}
	info.name = strings.Join(names, " / ")
	l.changed[addr] = true
}

// refreshCVs reads the decoders' CVs again, for values read or written
// since.
func (l *layout) refreshCVs(cvs func(decoder string) decoderCVs) {
	for addr, info := range l.roster {
		if info.decoder == "" {
			continue
		}
		if now := cvs(info.decoder); now != info.cvs {
			info.cvs = now
			l.changed[addr] = true
		}
	}
}

// get returns the train at an address, made the first time.
func (l *layout) get(addr detector.Address) *tracked {
	t, ok := l.trains[addr]
	if !ok {
		t = &tracked{addr: addr}
		l.trains[addr] = t
	}
	return t
}

// info is what the roster says about a train; a train the roster lacks
// is a point at its decoder.
func (l *layout) info(t *tracked) rosterInfo {
	if info := l.roster[t.addr]; info != nil {
		return *info
	}
	return rosterInfo{}
}

// sign is -1, 0 or 1 by the sign of v.
func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// going says which way a train goes or would go: 1 forward, -1 in
// reverse, by the way it moves, else the way it is set to, else the way
// it last moved; 0 when none of those is known.
func (t *tracked) going() float64 {
	if s := sign(t.step); s != 0 {
		return s
	}
	if s := sign(t.target); s != 0 {
		return s
	}
	return t.heading
}

// decoder is where the train's decoder is: its leg, taken toward the
// train's front, and how far along it.
func (l *layout) decoder(t *tracked) (trackplan.Leg, float64) {
	return t.stretch.Back(l.info(t).decoderAt)
}

type verdict int

const (
	pass verdict = iota
	found
	barred
)

// route is a way along the track a search found: the leg it reaches,
// how far it is to that leg's start and how it takes the junctions whose
// position isn't known.
type route struct {
	leg  trackplan.Leg
	dist float64
	set  map[string]trackplan.Position
}

// search follows the track on from at along leg, through junctions as
// set and both ways through those moving or unknown, for the nearest leg
// judge finds; a leg judge bars ends that way.
func (l *layout) search(leg trackplan.Leg, at float64, judge func(trackplan.Leg) verdict) (route, bool) {
	var best route
	ok := false
	budget := 4000
	var visit func(cur trackplan.Leg, dist float64, set map[string]trackplan.Position)
	visit = func(cur trackplan.Leg, dist float64, set map[string]trackplan.Position) {
		budget--
		if budget < 0 || dist > searchLimit || (ok && dist >= best.dist) {
			return
		}
		next, err := l.net.Next(cur, l.with(set))
		var against *trackplan.SetAgainstError
		if errors.As(err, &against) && against.Position == "" {
			if _, chosen := set[against.Junction]; !chosen {
				for _, p := range []trackplan.Position{trackplan.Closed, trackplan.Thrown} {
					s := maps.Clone(set)
					if s == nil {
						s = map[string]trackplan.Position{}
					}
					s[against.Junction] = p
					visit(cur, dist, s)
				}
			}
			return
		}
		if err != nil {
			return
		}
		switch judge(next) {
		case found:
			best, ok = route{leg: next, dist: dist, set: set}, true
			return
		case barred:
			return
		}
		visit(next, dist+l.net.Length(next), set)
	}
	visit(leg, l.net.Length(leg)-at, nil)
	return best, ok
}

// into finds legs in block, barring detected blocks other than it and
// from: a decoder can't pass through one unseen.
func (l *layout) into(block, from string) func(trackplan.Leg) verdict {
	return func(leg trackplan.Leg) verdict {
		b := l.net.Block(leg)
		switch {
		case b == block:
			return found
		case b != "" && b != from && l.detected[b]:
			return barred
		}
		return pass
	}
}

// enter is a decoder reported in a block.
func (l *layout) enter(addr detector.Address, block string) {
	t := l.get(addr)
	l.changed[addr] = true
	t.updated = l.now
	if t.block == block {
		t.seen = true
		return
	}
	prev := t.block
	t.block, t.seen = block, true
	if t.stretch != nil && l.carry(t, prev, block) {
		return
	}
	if prev != "" && l.placeFrom(t, prev, block) {
		return
	}
	t.stretch, t.anchored = nil, false
}

// exit is a decoder no longer reported in a block. Where it was last
// seen is kept; it is between blocks, or the detector lost it a moment.
func (l *layout) exit(addr detector.Address, block string) {
	if t, ok := l.trains[addr]; ok && t.block == block {
		t.seen = false
		t.updated = l.now
		l.changed[addr] = true
	}
}

// carry moves a placed train to where its decoder entered block, the way
// it is going if the block is that way, and learns from the run how fast
// it goes.
func (l *layout) carry(t *tracked, prev, block string) bool {
	leg, at := l.decoder(t)
	// The decoder goes just inside the block, so it isn't left on the
	// joint with the track outside.
	var delta float64
	var set map[string]trackplan.Position
	if l.net.Block(leg) == block {
		// Held where the block begins, waiting to be seen there.
		delta = max(inside-at, 0)
	} else {
		ahead, okAhead := l.search(leg, at, l.into(block, prev))
		behind, okBehind := l.search(leg.Reversed(), l.net.Length(leg)-at, l.into(block, prev))
		switch way := t.going(); {
		case okAhead && (!okBehind || way > 0 || (way == 0 && ahead.dist <= behind.dist)):
			delta, set = ahead.dist+inside, ahead.set
		case okBehind:
			delta, set = -behind.dist-inside, behind.set
		default:
			return false
		}
	}
	moved := l.move(t, delta, l.with(set))
	if leg, _ := l.decoder(t); math.Abs(moved-delta) > 1 || l.net.Block(leg) != block {
		return false
	}
	if t.anchored && !t.turned && t.unit >= minUnit {
		run := math.Abs(t.moved + moved)
		if sign(t.moved+moved) == t.ran || t.ran == 0 {
			l.learn(t, run/t.unit)
		}
	}
	l.anchor(t)
	return true
}

// learn takes a measure of the train's top speed.
func (l *layout) learn(t *tracked, top float64) {
	if top <= 0 || math.IsInf(top, 0) || math.IsNaN(top) {
		return
	}
	if t.samples == 0 {
		t.top = top
	} else {
		t.top += (top - t.top) * learnRate
	}
	t.samples++
}

func (l *layout) anchor(t *tracked) {
	t.anchored, t.moved, t.unit, t.ran, t.turned = true, 0, 0, 0, false
}

// placeFrom puts a train whose place isn't known at where the track from
// block prev enters block, its decoder just inside, the rest of it
// behind or ahead as it faces.
func (l *layout) placeFrom(t *tracked, prev, block string) bool {
	var best route
	ok := false
	for _, leg := range l.net.Legs(prev) {
		for _, start := range []trackplan.Leg{leg, leg.Reversed()} {
			r, found := l.search(start, 0, l.into(block, prev))
			if found && (!ok || r.dist < best.dist) {
				best, ok = r, true
			}
		}
	}
	if !ok {
		return false
	}
	info := l.info(t)
	position := l.with(best.set)
	leg, at := best.leg, min(inside, l.net.Length(best.leg))
	if t.going() < 0 {
		// Running in reverse: the front is behind the decoder.
		leg, at = leg.Reversed(), l.net.Length(leg)-at
	}
	s := trackplan.Stretch{{Leg: leg, From: at, To: at}}
	s, _, _ = s.Grow(l.net, info.decoderAt, position)
	r, _, _ := s.Reversed(l.net).Grow(l.net, info.length-info.decoderAt, position)
	t.stretch = r.Reversed(l.net)
	l.anchor(t)
	return true
}

// move carries a train d millimeters toward its front, or back toward
// its rear when d is negative, and returns how far it went.
func (l *layout) move(t *tracked, d float64, position func(string) trackplan.Position) float64 {
	if d == 0 || t.stretch == nil {
		return 0
	}
	if d > 0 {
		s, moved, _ := t.stretch.Advance(l.net, d, position)
		t.stretch = s
		return moved
	}
	s, moved, _ := t.stretch.Reversed(l.net).Advance(l.net, -d, position)
	t.stretch = s.Reversed(l.net)
	return -moved
}

// room is how far a train's decoder can go the way given before it would
// reach a detected block other than the one it was seen in.
func (l *layout) room(t *tracked, way float64) float64 {
	leg, at := l.decoder(t)
	if way < 0 {
		leg, at = leg.Reversed(), l.net.Length(leg)-at
	}
	judge := func(next trackplan.Leg) verdict {
		if b := l.net.Block(next); b != "" && b != t.block && l.detected[b] {
			return found
		}
		return pass
	}
	if at < l.net.Length(leg) && judge(leg) == found {
		return 0 // where the block begins
	}
	if r, ok := l.search(leg, at, judge); ok {
		return r.dist
	}
	return math.Inf(1)
}

// tick moves the trains on to now by what their decoders were set to.
func (l *layout) tick(now time.Time) {
	dt := 0.0
	if !l.now.IsZero() {
		dt = min(now.Sub(l.now).Seconds(), 1)
	}
	l.now = now
	if dt <= 0 {
		return
	}
	for _, t := range l.trains {
		info := l.info(t)
		if l.powerOff || l.estop || t.estop {
			t.step = 0
		} else {
			t.step = train.Momentum(t.step, t.target, dt, info.cvs.accel, info.cvs.decel)
		}
		way := sign(t.step)
		if way == 0 {
			continue
		}
		t.heading = way
		curve := train.SpeedCurve(math.Abs(t.step), info.cvs.start, info.cvs.mid, info.cvs.high)
		if t.anchored {
			t.unit += curve * dt
			if t.ran != 0 && t.ran != way {
				t.turned = true
			}
			t.ran = way
		}
		if t.stretch == nil || t.top == 0 || curve == 0 {
			continue
		}
		d := min(t.top*curve*dt, l.room(t, way))
		if d <= 0 {
			continue
		}
		moved := l.move(t, way*d, l.position)
		t.moved += moved
		if moved != 0 {
			t.updated = now
			l.changed[t.addr] = true
		}
	}
}

// view is how a train is shown.
func (l *layout) view(t *tracked) Train {
	info := l.info(t)
	v := Train{
		ID:        idOf(t.addr),
		Name:      info.name,
		Address:   t.addr,
		Roster:    slices.Clone(info.ids),
		Block:     t.block,
		Seen:      t.seen,
		Placed:    t.stretch != nil,
		Blocks:    []string{},
		Direction: train.Forward,
		TopSpeed:  math.Round(t.top*10) / 10,
		Length:    info.length,
		Momentum:  info.cvs.known,
		Updated:   t.updated,
	}
	if v.Name == "" {
		v.Name = "locomotive " + t.addr.String()
	}
	if t.going() < 0 {
		v.Direction = train.Reverse
	}
	if t.top > 0 {
		v.Speed = math.Round(t.top*train.SpeedCurve(math.Abs(t.step), info.cvs.start, info.cvs.mid, info.cvs.high)*10) / 10
	}
	if t.stretch == nil {
		if t.block != "" {
			v.Blocks = []string{t.block}
		}
		return v
	}
	// By the estimate, not the reports: a detector reports a decoder
	// gone only a second after it left.
	v.Blocks = t.stretch.Blocks(l.net)
	for _, s := range t.stretch {
		for d := s.From; ; d += lineStep {
			d = min(d, s.To)
			v.Line = append(v.Line, round(l.net.At(s.Leg, d)))
			if d >= s.To {
				break
			}
		}
	}
	leg, at := l.decoder(t)
	p := round(l.net.At(leg, at))
	v.Decoder = &p
	return v
}

func round(p trackplan.Vec) trackplan.Vec {
	return trackplan.Vec{X: math.Round(p.X*10) / 10, Y: math.Round(p.Y*10) / 10}
}

// idOf is how a tracked train is named in the API: its address as the
// roster writes one, or "consist-N".
func idOf(a detector.Address) string {
	switch a.Type {
	case detector.Consist:
		return "consist-" + strconv.Itoa(int(a.Number))
	case detector.Long:
		return train.IDFor(a.Number, true)
	}
	return train.IDFor(a.Number, false)
}
