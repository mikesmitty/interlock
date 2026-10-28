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
// address: the decoder that moves them and the train they run in.
type rosterInfo struct {
	ids     []string
	decoder string
	cvs     decoderCVs
	// unit is the key of the train the address runs in, at how far its
	// decoder is from that train's front, and backward whether its
	// locomotive faces the train's rear.
	unit     string
	at       float64
	backward bool
}

// unit is a train as tracking follows it: a formation from the roster,
// or a locomotive on its own.
type unit struct {
	id        string
	name      string
	formation string
	roster    []string
	length    float64
}

// speed is what a throttle set an address to: the step, negative in
// reverse, the way its locomotive faces.
type speed struct {
	step  float64
	estop bool
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
	// key is the unit key of the train it is, and addr the decoder it is
	// followed by: the one a detector last reported. at is how far that
	// decoder is from the train's front and length the train's length,
	// as the train was laid out, and backward whether the decoder's
	// locomotive faces the train's rear.
	key        string
	addr       detector.Address
	at, length float64
	backward   bool
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
	// trains are the trains followed, by unit key.
	trains map[string]*tracked
	// The roster as last heard, by ID, and what follows from it for each
	// address and each train.
	entries    map[string]train.Entry
	cars       map[string]train.Car
	formations map[string]train.Formation
	roster     map[detector.Address]*rosterInfo
	units      map[string]*unit
	speeds     map[detector.Address]speed
	// cvs reads a decoder's CVs; nil reads none.
	cvs      func(decoder string) decoderCVs
	powerOff bool
	estop    bool
	now      time.Time
	// changed are the trains to publish, by unit key; one no longer
	// followed is published as gone.
	changed map[string]bool
}

func newLayout() *layout {
	return &layout{
		net:        trackplan.Plan{}.Network(),
		reporters:  map[string][]string{},
		detected:   map[string]bool{},
		switches:   map[string]string{},
		turnouts:   map[string]string{},
		seen:       map[string][]detector.Address{},
		trains:     map[string]*tracked{},
		entries:    map[string]train.Entry{},
		cars:       map[string]train.Car{},
		formations: map[string]train.Formation{},
		roster:     map[detector.Address]*rosterInfo{},
		units:      map[string]*unit{},
		speeds:     map[detector.Address]speed{},
		changed:    map[string]bool{},
	}
}

// formationKey and addressKey are the unit keys of a formation's train
// and of a locomotive running on its own.
func formationKey(id string) string        { return "formation/" + id }
func addressKey(a detector.Address) string { return "address/" + idOf(a) }

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
			l.changed[t.key] = true
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

// train follows the roster and the speed each address is set to.
func (l *layout) train(tr train.Train) {
	if tr.Roster {
		l.rosterEntry(tr.Entry, tr.Removed)
	}
	if tr.Removed {
		return
	}
	addr := addressOf(tr.Address, tr.Long)
	s := speed{step: float64(tr.Speed), estop: tr.EStop}
	if tr.Direction == train.Reverse {
		s.step = -s.step
	}
	l.speeds[addr] = s
	if t := l.get(addr); t.addr == addr {
		l.drive(t)
	}
}

// rosterEntry follows a roster entry; only what tracking uses counts as
// a change.
func (l *layout) rosterEntry(e train.Entry, removed bool) {
	old, had := l.entries[e.ID]
	switch {
	case removed && !had:
		return
	case removed:
		delete(l.entries, e.ID)
	case had && old.Name == e.Name && old.Address == e.Address && old.Long == e.Long && old.Length == e.Length && old.Installed == e.Installed:
		return
	default:
		l.entries[e.ID] = e
	}
	l.rebuild()
}

// car follows a car in the roster.
func (l *layout) car(c train.CarState) {
	if c.Removed {
		delete(l.cars, c.ID)
	} else {
		l.cars[c.ID] = c.Car
	}
	l.rebuild()
}

// formation follows a train made up in the roster.
func (l *layout) formation(f train.FormationState) {
	if f.Removed {
		delete(l.formations, f.ID)
	} else {
		l.formations[f.ID] = f.Formation
	}
	l.rebuild()
}

// setRoster takes the whole roster at once.
func (l *layout) setRoster(trains []train.Train, cars []train.CarState, formations []train.FormationState) {
	for _, tr := range trains {
		if tr.Roster && !tr.Removed {
			l.entries[tr.ID] = tr.Entry
		}
	}
	for _, c := range cars {
		l.cars[c.ID] = c.Car
	}
	for _, f := range formations {
		l.formations[f.ID] = f.Formation
	}
	l.rebuild()
}

// rebuild works out from the roster which train each address runs in
// and where its decoder is in that train, then settles the trains
// followed. A locomotive's decoder is taken to be at its middle.
func (l *layout) rebuild() {
	roster := map[detector.Address]*rosterInfo{}
	for _, id := range slices.Sorted(maps.Keys(l.entries)) {
		e := l.entries[id]
		addr := addressOf(e.Address, e.Long)
		info := roster[addr]
		if info == nil {
			// The first entry by ID speaks for the address.
			info = &rosterInfo{decoder: e.Installed}
			if e.Installed != "" && l.cvs != nil {
				info.cvs = l.cvs(e.Installed)
			}
			roster[addr] = info
		}
		info.ids = append(info.ids, id)
	}
	units := map[string]*unit{}
	for _, id := range slices.Sorted(maps.Keys(l.formations)) {
		f := l.formations[id]
		key := formationKey(f.ID)
		u := &unit{id: f.ID, name: f.Name, formation: f.ID}
		for _, v := range f.Vehicles {
			length := l.cars[v.Car].Length
			if e, ok := l.entries[v.Locomotive]; ok {
				length = e.Length
				u.roster = append(u.roster, e.ID)
				// An address shared by locomotives in two trains runs in
				// the first.
				if info := roster[addressOf(e.Address, e.Long)]; info.unit == "" {
					info.unit, info.at, info.backward = key, u.length+length/2, v.Reverse
				}
			}
			u.length += length
		}
		if len(u.roster) > 0 {
			units[key] = u
		}
	}
	for addr, info := range roster {
		if info.unit != "" {
			continue
		}
		// A locomotive in no train runs on its own.
		names := make([]string, len(info.ids))
		for i, id := range info.ids {
			names[i] = l.entries[id].Name
		}
		length := l.entries[info.ids[0]].Length
		key := addressKey(addr)
		info.unit, info.at = key, length/2
		units[key] = &unit{id: idOf(addr), name: strings.Join(names, " / "), roster: slices.Clone(info.ids), length: length}
	}
	l.roster, l.units = roster, units
	l.settle()
}

// settle follows a roster change into the trains followed: each goes
// with its decoder to the train that decoder runs in now, and is laid
// out again around its decoder for a new length or decoder position.
// Two followed that turn out to be one train leave the one placed, else
// the one seen last.
func (l *layout) settle() {
	for _, key := range slices.Sorted(maps.Keys(l.trains)) {
		t := l.trains[key]
		want := l.unitOf(t.addr)
		if want == key {
			continue
		}
		delete(l.trains, key)
		l.changed[key] = true
		if other := l.trains[want]; other != nil && keeps(other, t) {
			continue
		}
		t.key = want
		l.trains[want] = t
		l.changed[want] = true
	}
	for _, t := range l.trains {
		l.fit(t)
	}
}

// keeps reports whether a, followed already, stays over b when both
// turn out to be one train.
func keeps(a, b *tracked) bool {
	if (a.stretch != nil) != (b.stretch != nil) {
		return a.stretch != nil
	}
	return !b.updated.After(a.updated)
}

// fit lays a train out again around its decoder when its length, or
// where its decoder sits in it, has changed, and takes its decoder's
// speed the way that decoder's locomotive faces now.
func (l *layout) fit(t *tracked) {
	length := l.unitFor(t).length
	var at float64
	var backward bool
	if info := l.roster[t.addr]; info != nil {
		at, backward = info.at, info.backward
	}
	if t.stretch != nil && (length != t.length || at != t.at) {
		leg, d := t.stretch.Back(t.at)
		t.stretch = l.layOut(leg, d, at, length, l.position)
		l.changed[t.key] = true
	}
	t.length, t.at = length, at
	if backward != t.backward {
		// The decoder's forward is the train's other way now.
		t.backward = backward
		t.step, t.heading = -t.step, -t.heading
	}
	l.drive(t)
}

// follow has a train followed by another of its decoders from now on.
// The train stays where it is; only the point it is followed by moves.
func (l *layout) follow(t *tracked, addr detector.Address) {
	if t.addr == addr {
		return
	}
	t.addr = addr
	if info := l.roster[addr]; info != nil {
		t.at, t.backward = info.at, info.backward
	}
	l.drive(t)
}

// drive takes the speed the train's decoder is set to, in the train's
// terms: a locomotive facing the train's rear runs it backward going
// forward.
func (l *layout) drive(t *tracked) {
	s := l.speeds[t.addr]
	target := s.step
	if t.backward {
		target = -target
	}
	if s.estop {
		t.step, target = 0, 0
	}
	if t.target != target || t.estop != s.estop {
		t.target, t.estop = target, s.estop
		l.changed[t.key] = true
	}
}

// refreshCVs reads the decoders' CVs again, for values read or written
// since.
func (l *layout) refreshCVs() {
	if l.cvs == nil {
		return
	}
	for _, info := range l.roster {
		if info.decoder == "" {
			continue
		}
		if now := l.cvs(info.decoder); now != info.cvs {
			info.cvs = now
			l.changed[info.unit] = true
		}
	}
}

// known reports whether a detector has seen the train: until one has,
// tracking only follows the speed it is set to.
func (t *tracked) known() bool { return t.block != "" || t.stretch != nil }

// get returns the train an address runs in, made the first time and
// followed by that address's decoder until a detector reports another.
func (l *layout) get(addr detector.Address) *tracked {
	key := l.unitOf(addr)
	t, ok := l.trains[key]
	if !ok {
		t = &tracked{key: key, addr: addr}
		l.trains[key] = t
		l.fit(t)
	}
	return t
}

// unitOf is the key of the train an address runs in.
func (l *layout) unitOf(addr detector.Address) string {
	if info := l.roster[addr]; info != nil {
		return info.unit
	}
	return addressKey(addr)
}

// unitFor is what the roster says about a train; a train the roster
// lacks is a point at its decoder.
func (l *layout) unitFor(t *tracked) unit {
	if u := l.units[t.key]; u != nil {
		return *u
	}
	return unit{id: idOf(t.addr)}
}

// info is what the roster says about the decoder a train is followed
// by.
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
	return t.stretch.Back(t.at)
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
	l.follow(t, addr)
	l.changed[t.key] = true
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
	if t, ok := l.trains[l.unitOf(addr)]; ok && t.addr == addr && t.block == block {
		t.seen = false
		t.updated = l.now
		l.changed[t.key] = true
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
	leg, at := best.leg, min(inside, l.net.Length(best.leg))
	if t.going() < 0 {
		// Running in reverse: the front is behind the decoder.
		leg, at = leg.Reversed(), l.net.Length(leg)-at
	}
	t.stretch = l.layOut(leg, at, t.at, t.length, l.with(best.set))
	l.anchor(t)
	return true
}

// layOut is a train laid out around its decoder at d along leg, taken
// toward the front: ahead of it by at, behind it by the rest of length.
func (l *layout) layOut(leg trackplan.Leg, d, at, length float64, position func(string) trackplan.Position) trackplan.Stretch {
	s := trackplan.Stretch{{Leg: leg, From: d, To: d}}
	s, _, _ = s.Grow(l.net, at, position)
	r, _, _ := s.Reversed(l.net).Grow(l.net, max(length-at, 0), position)
	return r.Reversed(l.net)
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
			l.changed[t.key] = true
		}
	}
}

// view is how a train is shown.
func (l *layout) view(t *tracked) Train {
	info, u := l.info(t), l.unitFor(t)
	v := Train{
		ID:        u.id,
		Name:      u.name,
		Formation: u.formation,
		Address:   t.addr,
		Roster:    slices.Clone(u.roster),
		Block:     t.block,
		Seen:      t.seen,
		Placed:    t.stretch != nil,
		Blocks:    []string{},
		Direction: train.Forward,
		TopSpeed:  math.Round(t.top*10) / 10,
		Length:    u.length,
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
