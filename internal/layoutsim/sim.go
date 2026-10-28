// Package layoutsim simulates a layout for Interlock to run, so the
// scheduling work can be built and tried without the hardware: trains
// that move along the track plan at the speed their decoders are set to,
// with the decoders' momentum, turnouts set by simulated outposts'
// servos, and detectors that see the trains' decoders as canbus-outpost's
// RailCom detectors do. A detector sees only a train's decoder, not the
// cars around it; it reports a decoder entering after a few DCC packets
// and leaving only after a second without it.
//
// The simulator reads the plan from Interlock, so the blocks and
// turnouts drawn there are the ones the trains run over; a block's
// reporter (or occupancy) entity and a junction's switch name the
// simulated outpost's track or servo by their entity IDs, as in
// reporter.0a0000000110.track_1.
package layoutsim

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// The detectors' timing, from canbus-outpost's src/railcom.c: an entry
// after two matching address readings, a few packets, and an exit after
// ADDR_TIMEOUT_US with none.
const (
	EntryDelay = 30 * time.Millisecond
	ExitDelay  = time.Second
)

// Hardware is the simulated boards the layout acts through: the command
// station whose decoders set the trains' speeds, and the outposts whose
// servos set the junctions and whose detectors see the trains.
type Hardware interface {
	// DecoderState is a decoder's state as the command station last set
	// it.
	DecoderState(address uint16, long bool) sim.Decoder
	// Power reports whether the track is powered.
	Power() bool
	// Turnout is a servo's state: thrown, closed, moving or unknown.
	Turnout(node openlcb.NodeID, servo int) string
	// SetTrack reports a locomotive on a detector's track, or the track
	// clear with nil.
	SetTrack(node openlcb.NodeID, track int, loco *sim.Locomotive)
}

// TrainSpec describes a train on the simulated layout.
type TrainSpec struct {
	Name    string `json:"name" yaml:"name"`
	Address uint16 `json:"address" yaml:"address"`
	Long    bool   `json:"long,omitempty" yaml:"long,omitempty"`
	// Length is the train's length end to end, in millimeters.
	Length float64 `json:"length" yaml:"length"`
	// Decoder is how far the decoder sits from the A end, the end that
	// leads when the decoder goes forward.
	Decoder float64 `json:"decoder" yaml:"decoder"`
	// TopSpeed is how fast the train goes at speed step 126 with the
	// decoder's top voltage (CV 5) at full, in millimeters a second.
	TopSpeed float64 `json:"top_speed" yaml:"top_speed"`
	// Block is where it starts: its B end at the start of the block's
	// first track, from A to B on that track, or from B to A with
	// Reverse.
	Block   string `json:"block" yaml:"block"`
	Reverse bool   `json:"reverse,omitempty" yaml:"reverse,omitempty"`
}

func (s TrainSpec) long() bool { return s.Long || s.Address > traction.ShortAddressMax }

func (s TrainSpec) locomotive() detector.Address {
	if s.long() {
		return detector.Address{Type: detector.Long, Number: s.Address}
	}
	return detector.Address{Type: detector.Short, Number: s.Address}
}

type running struct {
	spec TrainSpec
	path trackplan.Stretch
	// step is the speed step the decoder has reached on the way to the
	// one it is set to, negative in reverse.
	step float64
	// stopped says what stopped the train short, logged once.
	stopped string
	// block is the block its decoder is in, for the log.
	block string
}

// detectorKey is one track of one outpost.
type detectorKey struct {
	node  openlcb.NodeID
	track int
}

type detectorState struct {
	known    bool
	reported *sim.Locomotive
	// seen is the decoder seen now, and since when.
	seen     detector.Address
	since    time.Time
	lastSeen time.Time
	garbled  bool
}

// Sim is the simulated layout. Its methods may be called from any
// goroutine.
type Sim struct {
	hw  Hardware
	log *slog.Logger

	mu        sync.Mutex
	revision  string
	net       *trackplan.Network
	blocks    map[string]detectorKey
	trains    []*running
	detectors map[detectorKey]*detectorState
	started   time.Time
}

// New makes a simulated layout with the trains given, placed once a plan
// arrives through SetPlan.
func New(hw Hardware, trains []TrainSpec, log *slog.Logger) *Sim {
	if log == nil {
		log = slog.Default()
	}
	s := &Sim{hw: hw, log: log, detectors: map[detectorKey]*detectorState{}}
	for _, spec := range trains {
		s.trains = append(s.trains, &running{spec: spec})
	}
	return s
}

// AddDetector has a detector look from the start, so it reports its
// track clear after a second, as the board does, even when no block in
// the plan uses it.
func (s *Sim) AddDetector(node openlcb.NodeID, track int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := detectorKey{node, track}
	if _, ok := s.detectors[key]; !ok {
		s.detectors[key] = &detectorState{}
	}
}

// Revision is the revision of the plan the trains run on.
func (s *Sim) Revision() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// SetPlan runs the trains on a plan. Trains whose track is still there
// stay where they are; the others are put back where they start.
func (s *Sim) SetPlan(plan trackplan.Plan, revision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision = revision
	s.net = plan.Network()
	s.blocks = map[string]detectorKey{}
	for _, b := range plan.Blocks {
		id := b.Reporter
		if id == "" {
			id = b.Occupancy
		}
		if node, n, ok := parseEntity(id, "track_"); ok {
			s.blocks[b.ID] = detectorKey{node, n}
		}
	}
	for _, t := range s.trains {
		if t.path.Valid(s.net) {
			continue
		}
		if err := s.place(t); err != nil {
			t.path = nil
			s.log.Warn("sim: train left off the layout", "train", t.spec.Name, "error", err)
		}
	}
}

// place puts a train where its spec says it starts. The caller holds
// s.mu.
func (s *Sim) place(t *running) error {
	legs := s.net.Legs(t.spec.Block)
	if len(legs) == 0 {
		return fmt.Errorf("block %q has no track", t.spec.Block)
	}
	leg := legs[0]
	if t.spec.Reverse {
		leg = leg.Reversed()
	}
	p, moved, _ := trackplan.Stretch{{Leg: leg}}.Grow(s.net, t.spec.Length, s.position)
	if moved < t.spec.Length {
		// Too long for the track ahead: back it up into the track
		// behind.
		back, _, err := p.Reversed(s.net).Grow(s.net, t.spec.Length-moved, s.position)
		if err != nil {
			return fmt.Errorf("no room for %.0f mm: %w", t.spec.Length, err)
		}
		p = back.Reversed(s.net)
	}
	t.path, t.step, t.stopped = p, 0, ""
	s.log.Info("sim: train placed", "train", t.spec.Name, "block", t.spec.Block)
	return nil
}

// position is how a junction is set, as its servo reports; a junction
// without a switch machine stays closed. The caller holds s.mu.
func (s *Sim) position(junction string) trackplan.Position {
	id := s.net.Switch(junction)
	if id == "" {
		return trackplan.Closed
	}
	node, servo, ok := parseEntity(id, "servo_")
	if !ok {
		return trackplan.Closed
	}
	switch s.hw.Turnout(node, servo) {
	case "closed":
		return trackplan.Closed
	case "thrown":
		return trackplan.Thrown
	}
	return ""
}

// Step moves the trains on by dt, as of now, and reports what the
// detectors see.
func (s *Sim) Step(now time.Time, dt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.net == nil {
		return
	}
	if s.started.IsZero() {
		s.started = now
	}
	power := s.hw.Power()
	for _, t := range s.trains {
		if t.path == nil {
			continue
		}
		d := s.hw.DecoderState(t.spec.Address, t.spec.long())
		if !power || d.EStop {
			t.step = 0
		} else {
			target := float64(d.Speed.Value)
			if d.Speed.Reverse {
				target = -target
			}
			t.step = train.Momentum(t.step, target, dt.Seconds(), d.CVs[3], d.CVs[4])
		}
		dist := speed(math.Abs(t.step), d.CVs, t.spec.TopSpeed) * dt.Seconds()
		if dist > 0 {
			s.move(t, dist, t.step < 0)
		}
	}
	s.detect(now)
}

// move carries a train dist millimeters, toward its B end when back.
// The caller holds s.mu.
func (s *Sim) move(t *running, dist float64, back bool) {
	p := t.path
	if back {
		p = p.Reversed(s.net)
	}
	p, _, err := p.Advance(s.net, dist, s.position)
	if back {
		p = p.Reversed(s.net)
	}
	for _, o := range s.trains {
		if o != t && o.path != nil && p.Overlaps(s.net, o.path) {
			s.stop(t, "ran into "+o.spec.Name)
			return
		}
	}
	t.path = p
	if err != nil {
		s.stop(t, err.Error())
		return
	}
	t.stopped = ""
}

func (s *Sim) stop(t *running, why string) {
	if t.stopped != why {
		t.stopped = why
		s.log.Warn("sim: train stopped", "train", t.spec.Name, "why", why)
	}
}

// detect reports what each detector sees. The caller holds s.mu.
func (s *Sim) detect(now time.Time) {
	present := map[detectorKey][]*running{}
	for _, t := range s.trains {
		if t.path == nil {
			continue
		}
		leg, _ := t.path.Back(t.spec.Decoder)
		block := s.net.Block(leg)
		if block != t.block {
			t.block = block
			s.log.Info("sim: decoder in block", "train", t.spec.Name, "block", block)
		}
		if key, ok := s.blocks[block]; ok {
			present[key] = append(present[key], t)
		}
	}
	for _, key := range s.blockKeys() {
		if _, ok := s.detectors[key]; !ok {
			s.detectors[key] = &detectorState{}
		}
	}
	for key, st := range s.detectors {
		here := present[key]
		if len(here) == 0 {
			// Reported clear a second after the decoder was last seen, or,
			// at the start, a second after the detector began to look.
			last := st.lastSeen
			if !st.known {
				last = s.started
			}
			if (st.reported != nil || !st.known) && now.Sub(last) >= ExitDelay {
				s.hw.SetTrack(key.node, key.track, nil)
				st.known, st.reported, st.seen, st.garbled = true, nil, detector.Address{}, false
			}
			continue
		}
		st.lastSeen = now
		pick := here[0]
		for _, t := range here {
			if st.reported != nil && t.spec.locomotive() == st.reported.Address {
				pick = t
			}
		}
		if len(here) > 1 && !st.garbled {
			st.garbled = true
			s.log.Warn("sim: two decoders on one detector; a real one would garble them", "node", key.node.String(), "track", key.track+1)
		}
		addr := pick.spec.locomotive()
		if st.seen != addr {
			st.seen, st.since = addr, now
		}
		loco := sim.Locomotive{Address: addr, Facing: facing(pick)}
		if (st.reported == nil || *st.reported != loco) && now.Sub(st.since) >= EntryDelay {
			s.hw.SetTrack(key.node, key.track, &loco)
			st.known, st.reported = true, &loco
		}
	}
}

// blockKeys lists the detectors the plan's blocks use.
func (s *Sim) blockKeys() []detectorKey {
	keys := make([]detectorKey, 0, len(s.blocks))
	for _, k := range s.blocks {
		keys = append(keys, k)
	}
	return keys
}

// facing is the way a train's A end faces where its decoder is: east
// when it faces the way the track there runs from its A point to its B
// point (a junction route from its first end to its last).
func facing(t *running) string {
	leg, _ := t.path.Back(t.spec.Decoder)
	if leg.Reverse {
		return "west"
	}
	return "east"
}

// speed is how fast a train goes at a speed step, in millimeters a
// second: its decoder's speed curve scaled to the train's top speed.
func speed(step float64, cvs map[uint16]byte, top float64) float64 {
	return train.SpeedCurve(step, cvs[2], cvs[6], cvs[5]) * top
}

// parseEntity reads a profile entity ID such as
// reporter.0a0000000110.track_1 into its node and the zero-based number
// after prefix.
func parseEntity(id, prefix string) (openlcb.NodeID, int, bool) {
	parts := strings.Split(id, ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[2], prefix) {
		return 0, 0, false
	}
	node, err := strconv.ParseUint(parts[1], 16, 48)
	if err != nil {
		return 0, 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(parts[2], prefix))
	if err != nil || n < 1 {
		return 0, 0, false
	}
	return openlcb.NodeID(node), n - 1, true
}

// Position is where a simulated train really is.
type Position struct {
	// Stretch is the track it covers from its B end to its A end, the end
	// that leads going forward.
	Stretch trackplan.Stretch
	// Block is the block its decoder is in.
	Block string
}

// Position returns where the train at an address really is, to check
// what Interlock makes of the detectors' reports against.
func (s *Sim) Position(address uint16) (Position, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.trains {
		if t.spec.Address == address && t.path != nil {
			leg, _ := t.path.Back(t.spec.Decoder)
			return Position{Stretch: append(trackplan.Stretch(nil), t.path...), Block: s.net.Block(leg)}, true
		}
	}
	return Position{}, false
}

// State is where the trains are, to carry them over a restart.
type State struct {
	Trains []TrainState `json:"trains"`
}

// TrainState is one train's place and speed step.
type TrainState struct {
	Address uint16            `json:"address"`
	Path    trackplan.Stretch `json:"path"`
	Step    float64           `json:"step"`
}

// State returns where the trains are now.
func (s *Sim) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Trains: []TrainState{}}
	for _, t := range s.trains {
		if t.path != nil {
			st.Trains = append(st.Trains, TrainState{Address: t.spec.Address, Path: append(trackplan.Stretch(nil), t.path...), Step: t.step})
		}
	}
	return st
}

// Restore puts the trains back where a saved state has them. Call it
// before the first SetPlan, which keeps them there if their track is
// still in the plan.
func (s *Sim) Restore(st State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, saved := range st.Trains {
		for _, t := range s.trains {
			if t.spec.Address == saved.Address {
				t.path, t.step = saved.Path, saved.Step
			}
		}
	}
}
