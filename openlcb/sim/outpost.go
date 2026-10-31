package sim

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// Outpost numbers.
const (
	OutpostServos = 4
	OutpostTracks = 4
)

// Locomotive is one a simulated detector sees.
type Locomotive struct {
	Address detector.Address
	// Facing is "west", "east" or "".
	Facing string
}

type servoState struct {
	closed, known, moving bool
	timer                 *time.Timer
}

type trackState struct {
	known, occupied bool
	loco            Locomotive
	entrySent       uint16
}

// Outpost adds a canbus-outpost's behavior to a simulated node, as the
// firmware in src/app does: its servos act on throw, close and center
// events and report thrown or closed feedback once the configured throw
// time has passed, its tracks produce occupied and clear events and DCC
// detector reports for the locomotives placed on them, its signal heads
// show the aspects events choose, and it answers Identify requests with
// its state. Event IDs come from the configuration
// space, so writing them changes what the outpost listens for, and the
// firmware's default IDs are filled in where the configuration has none.
type Outpost struct {
	*Node
	log  *slog.Logger
	vars map[string]cdi.Variable

	mu     sync.Mutex
	servos [OutpostServos]servoState
	tracks [OutpostTracks]trackState
	heads  [OutpostHeads]headState
	fw     firmwareState
	work   chan func()
	ctx    context.Context
}

// NewOutpost builds an outpost. cfg.Send must be set.
func NewOutpost(cfg node.Config) (*Outpost, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	n, err := New(cfg, OutpostCDI)
	if err != nil {
		return nil, err
	}
	o := &Outpost{Node: n, log: cfg.Logger, vars: make(map[string]cdi.Variable), work: make(chan func(), 256), ctx: context.Background()}
	for _, v := range n.Doc.Variables() {
		o.vars[strings.Join(v.Path, "/")] = v
	}
	o.seedDefaults()
	written := n.Config.OnWrite
	n.Config.OnWrite = func(address uint32, count int) {
		written(address, count)
		o.schedule(o.applyHeads)
	}
	reset := n.Server.OnFactoryReset
	n.Server.OnFactoryReset = func() {
		reset()
		o.seedDefaults()
	}
	// Firmware upgrades, as the board offers them when it has a partition
	// table.
	n.SetLocalProtocols(n.LocalProtocols() | openlcb.ProtocolFirmwareUpgrade)
	n.Server.Firmware = o
	n.Server.OnFrozen = o.announce
	n.Observe(o.observe)
	return o, nil
}

// seedDefaults fills in the firmware's default event IDs where the
// configuration holds zeros: the node ID followed by an index for servo
// events (five per servo), track events (0x20 on) and signal head events
// (0x100 on, sixteen per head), and the next node IDs as reporter ranges.
func (o *Outpost) seedDefaults() {
	id := o.ID()
	set := func(path string, ev openlcb.EventID) {
		v, ok := o.vars[path]
		if !ok {
			return
		}
		data := o.Config.Bytes()
		if openlcb.EventIDFromBytes([8]byte(data[v.Address:v.Address+8])) != 0 {
			return
		}
		_ = o.Config.Write(v.Address, ev.AsSlice())
	}
	names := []string{"Throw Command Event", "Close Command Event", "Thrown Feedback Event", "Closed Feedback Event", "Center Command Event"}
	for i := range OutpostServos {
		for k, name := range names {
			set(servoPath(i, name), id.Event(uint16(i*len(names)+k)))
		}
	}
	for t := range OutpostTracks {
		set(trackPath(t, "Occupied Event"), id.Event(uint16(0x20+2*t)))
		set(trackPath(t, "Clear Event"), id.Event(uint16(0x21+2*t)))
		set(trackPath(t, "Reporter ID"), ((id + 1 + openlcb.NodeID(t)) & openlcb.NodeIDMax).Event(0))
	}
	for h := range OutpostHeads {
		for a, aspect := range headAspects {
			set(headPath(h, aspect.name+" Event"), id.Event(uint16(0x100+16*h+a)))
		}
	}
}

func servoPath(i int, name string) string {
	return fmt.Sprintf("Switch Machines/Servo %d/%s", i+1, name)
}

func trackPath(t int, name string) string {
	return fmt.Sprintf("RailCom Detectors/Track %d/%s", t+1, name)
}

// value decodes a configuration variable by path.
func (o *Outpost) value(path string) string {
	v, ok := o.vars[path]
	if !ok {
		return ""
	}
	s, err := v.Element.Decode(o.Config.Bytes()[v.Address:])
	if err != nil {
		return ""
	}
	return s
}

func (o *Outpost) event(path string) openlcb.EventID {
	ev, err := openlcb.ParseEventID(o.value(path))
	if err != nil {
		return 0
	}
	return ev
}

func (o *Outpost) intValue(path string) int {
	n, _ := strconv.Atoi(o.value(path))
	return n
}

// Run operates the node and the outpost's own goroutine until ctx ends.
func (o *Outpost) Run(ctx context.Context) error {
	o.mu.Lock()
	o.ctx = ctx
	o.mu.Unlock()
	go o.worker(ctx)
	return o.Node.Run(ctx)
}

func (o *Outpost) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			o.mu.Lock()
			for i := range o.servos {
				if o.servos[i].timer != nil {
					o.servos[i].timer.Stop()
				}
			}
			o.mu.Unlock()
			return
		case fn := <-o.work:
			fn()
		}
	}
}

// schedule runs fn on the worker, off the node goroutine.
func (o *Outpost) schedule(fn func()) {
	select {
	case o.work <- fn:
	default:
		o.log.Warn("sim: work queue full")
	}
}

func (o *Outpost) observe(ev node.Event) {
	switch ev.Kind {
	case node.EventStateChanged:
		if ev.State == node.StateInitialized {
			o.schedule(o.initialPositions)
			o.schedule(o.powerOnHeads)
		}
	case node.EventMessage:
		msg := ev.Message
		if msg.MTI.IsAddressed() && msg.Dest.Alias != o.Alias() {
			return
		}
		switch msg.MTI {
		case openlcb.MTIEventReport:
			if id, ok := msg.EventID(); ok {
				o.schedule(func() { o.onEvent(id) })
			}
		case openlcb.MTIIdentifyEventsGlobal, openlcb.MTIIdentifyEventsAddressed:
			o.schedule(o.identifyAll)
		case openlcb.MTIIdentifyProducer:
			if id, ok := msg.EventID(); ok {
				o.schedule(func() { o.identifyProducer(id) })
			}
		case openlcb.MTIIdentifyConsumer:
			if id, ok := msg.EventID(); ok {
				o.schedule(func() { o.identifyConsumer(id) })
			}
		}
	}
}

func (o *Outpost) send(msg openlcb.Message) {
	o.mu.Lock()
	ctx := o.ctx
	o.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := o.Send(ctx, msg); err != nil {
		o.log.Debug("sim: send failed", "error", err)
	}
}

func (o *Outpost) sendEvent(ev openlcb.EventID) {
	if ev == 0 {
		return
	}
	o.send(openlcb.Message{MTI: openlcb.MTIEventReport, Payload: ev.AsSlice()})
}

// --- servos ----------------------------------------------------------------

// initialPositions moves every enabled servo to its default state, as the
// firmware does at power on.
func (o *Outpost) initialPositions() {
	for i := range OutpostServos {
		if o.intValue(servoPath(i, "Enabled")) == 0 {
			continue
		}
		switch o.intValue(servoPath(i, "Default State")) {
		case 0:
			o.move(i, false, 0)
		case 1:
			o.move(i, true, 0)
		}
	}
}

func (o *Outpost) onEvent(ev openlcb.EventID) {
	o.headEvent(ev)
	for i := range OutpostServos {
		if o.intValue(servoPath(i, "Enabled")) == 0 {
			continue
		}
		throwTime := time.Duration(o.intValue(servoPath(i, "Throw Time"))) * 100 * time.Millisecond
		switch ev {
		case o.event(servoPath(i, "Throw Command Event")):
			o.log.Info("sim: throw", "servo", i+1)
			o.move(i, false, throwTime)
		case o.event(servoPath(i, "Close Command Event")):
			o.log.Info("sim: close", "servo", i+1)
			o.move(i, true, throwTime)
		case o.event(servoPath(i, "Center Command Event")):
			o.log.Info("sim: center", "servo", i+1)
			o.mu.Lock()
			if o.servos[i].timer != nil {
				o.servos[i].timer.Stop()
			}
			o.servos[i] = servoState{}
			o.mu.Unlock()
		}
	}
}

// move starts a servo toward a position; feedback follows after d.
func (o *Outpost) move(i int, closed bool, d time.Duration) {
	o.mu.Lock()
	s := &o.servos[i]
	if s.timer != nil {
		s.timer.Stop()
	}
	s.closed, s.known = closed, true
	if d <= 0 {
		s.moving = false
		o.mu.Unlock()
		o.sendFeedback(i, closed)
		return
	}
	s.moving = true
	s.timer = time.AfterFunc(d, func() {
		o.schedule(func() {
			o.mu.Lock()
			arrived := o.servos[i].moving && o.servos[i].closed == closed
			if arrived {
				o.servos[i].moving = false
			}
			o.mu.Unlock()
			if arrived {
				o.sendFeedback(i, closed)
			}
		})
	})
	o.mu.Unlock()
}

func (o *Outpost) sendFeedback(i int, closed bool) {
	name := "Thrown Feedback Event"
	if closed {
		name = "Closed Feedback Event"
	}
	o.log.Info("sim: arrived", "servo", i+1, "closed", closed)
	o.sendEvent(o.event(servoPath(i, name)))
}

// Turnout reports a servo's state: thrown, closed, moving or unknown.
func (o *Outpost) Turnout(i int) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.servos[i]
	switch {
	case !s.known:
		return "unknown"
	case s.moving:
		return "moving"
	case s.closed:
		return "closed"
	default:
		return "thrown"
	}
}

// --- tracks ----------------------------------------------------------------

// SetTrack places a locomotive on a track, or clears it with nil, and
// sends what the detector would: entry and exit reports, occupied and
// clear events.
func (o *Outpost) SetTrack(t int, loco *Locomotive) {
	if t < 0 || t >= OutpostTracks {
		return
	}
	o.schedule(func() { o.setTrack(t, loco) })
}

func (o *Outpost) setTrack(t int, loco *Locomotive) {
	o.mu.Lock()
	was := o.tracks[t]
	tr := &o.tracks[t]
	tr.known = true
	tr.occupied = loco != nil
	if loco != nil {
		tr.loco = *loco
	}
	o.mu.Unlock()

	if loco != nil {
		same := was.occupied && was.loco.Address == loco.Address
		if was.occupied && !same {
			o.sendReport(t, detector.Report{Kind: detector.Exit, Address: was.loco.Address})
		}
		if !same {
			o.mu.Lock()
			tr.entrySent = 0
			o.mu.Unlock()
		}
		o.sendEntry(t)
		if !was.known || !was.occupied {
			o.sendEvent(o.event(trackPath(t, "Occupied Event")))
		}
		return
	}
	if was.occupied {
		o.sendReport(t, detector.Report{Kind: detector.Exit, Address: was.loco.Address})
		o.mu.Lock()
		tr.entrySent = 0
		o.mu.Unlock()
	}
	if !was.known || was.occupied {
		o.sendEvent(o.event(trackPath(t, "Clear Event")))
	}
}

func (o *Outpost) reporterRange(t int) (openlcb.EventRange, bool) {
	base := o.event(trackPath(t, "Reporter ID")) &^ 0xFFFF
	if base == 0 {
		return openlcb.EventRange{}, false
	}
	return detector.Range(base), true
}

// entryKind is how a locomotive on the track is reported: with its facing
// when the track's orientation is configured, else as a plain entry.
func (o *Outpost) entryKind(t int, loco Locomotive) detector.Kind {
	if o.intValue(trackPath(t, "Orientation")) == 0 {
		return detector.Entry
	}
	switch loco.Facing {
	case "west":
		return detector.EntryWest
	case "east":
		return detector.EntryEast
	default:
		return detector.Entry
	}
}

func (o *Outpost) sendReport(t int, rep detector.Report) {
	rng, ok := o.reporterRange(t)
	if !ok {
		return
	}
	o.sendEvent(rep.Event(rng))
}

// sendEntry reports the locomotive on the track as entering, unless that
// report already went out.
func (o *Outpost) sendEntry(t int) {
	o.mu.Lock()
	tr := &o.tracks[t]
	rep := detector.Report{Kind: o.entryKind(t, tr.loco), Address: tr.loco.Address}
	suffix := rep.Suffix()
	if tr.entrySent == suffix {
		o.mu.Unlock()
		return
	}
	tr.entrySent = suffix
	o.mu.Unlock()
	o.sendReport(t, rep)
}

// --- identification ------------------------------------------------------

type eventState uint8

const (
	stateUnknown eventState = iota
	stateValid
	stateInvalid
)

func identifiedMTI(producer bool, st eventState) openlcb.MTI {
	switch {
	case producer && st == stateValid:
		return openlcb.MTIProducerIdentifiedValid
	case producer && st == stateInvalid:
		return openlcb.MTIProducerIdentifiedInvalid
	case producer:
		return openlcb.MTIProducerIdentifiedUnknown
	case st == stateValid:
		return openlcb.MTIConsumerIdentifiedValid
	case st == stateInvalid:
		return openlcb.MTIConsumerIdentifiedInvalid
	default:
		return openlcb.MTIConsumerIdentifiedUnknown
	}
}

func (o *Outpost) sendIdentified(ev openlcb.EventID, producer bool, st eventState) {
	if ev == 0 {
		return
	}
	o.send(openlcb.Message{MTI: identifiedMTI(producer, st), Payload: ev.AsSlice()})
}

func (o *Outpost) commandState(i int, closed bool) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.servos[i]
	switch {
	case !s.known:
		return stateUnknown
	case s.closed == closed:
		return stateValid
	default:
		return stateInvalid
	}
}

func (o *Outpost) feedbackState(i int, closed bool) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.servos[i]
	switch {
	case !s.known:
		return stateUnknown
	case s.closed == closed && !s.moving:
		return stateValid
	default:
		return stateInvalid
	}
}

func (o *Outpost) occupancyState(t int, occupied bool) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	tr := o.tracks[t]
	switch {
	case !tr.known:
		return stateUnknown
	case tr.occupied == occupied:
		return stateValid
	default:
		return stateInvalid
	}
}

// servoEvents lists a servo's events with whether each is produced and
// its state.
func (o *Outpost) servoEvents(i int) []struct {
	ev       openlcb.EventID
	producer bool
	state    eventState
} {
	return []struct {
		ev       openlcb.EventID
		producer bool
		state    eventState
	}{
		{o.event(servoPath(i, "Throw Command Event")), false, o.commandState(i, false)},
		{o.event(servoPath(i, "Close Command Event")), false, o.commandState(i, true)},
		{o.event(servoPath(i, "Center Command Event")), false, stateUnknown},
		{o.event(servoPath(i, "Thrown Feedback Event")), true, o.feedbackState(i, false)},
		{o.event(servoPath(i, "Closed Feedback Event")), true, o.feedbackState(i, true)},
	}
}

func (o *Outpost) identifyAll() {
	for i := range OutpostServos {
		for _, e := range o.servoEvents(i) {
			o.sendIdentified(e.ev, e.producer, e.state)
		}
	}
	for t := range OutpostTracks {
		o.sendIdentified(o.event(trackPath(t, "Occupied Event")), true, o.occupancyState(t, true))
		o.sendIdentified(o.event(trackPath(t, "Clear Event")), true, o.occupancyState(t, false))
		if rng, ok := o.reporterRange(t); ok {
			o.send(openlcb.Message{MTI: openlcb.MTIProducerRangeIdentified, Payload: rng.Encode().AsSlice()})
		}
	}
	o.identifyHeads()
}

func (o *Outpost) identifyProducer(ev openlcb.EventID) {
	for i := range OutpostServos {
		for _, e := range o.servoEvents(i) {
			if e.producer && e.ev == ev {
				o.sendIdentified(ev, true, e.state)
			}
		}
	}
	for t := range OutpostTracks {
		if o.event(trackPath(t, "Occupied Event")) == ev {
			o.sendIdentified(ev, true, o.occupancyState(t, true))
		}
		if o.event(trackPath(t, "Clear Event")) == ev {
			o.sendIdentified(ev, true, o.occupancyState(t, false))
		}
		if rng, ok := o.reporterRange(t); ok && rng.Contains(ev) {
			o.reporterIdentify(t, ev, rng)
			o.send(openlcb.Message{MTI: openlcb.MTIProducerRangeIdentified, Payload: rng.Encode().AsSlice()})
		}
	}
}

func (o *Outpost) identifyConsumer(ev openlcb.EventID) {
	for i := range OutpostServos {
		for _, e := range o.servoEvents(i) {
			if !e.producer && e.ev == ev {
				o.sendIdentified(ev, false, e.state)
			}
		}
	}
	o.identifyHead(ev)
}

// reporterIdentify answers Identify Producer for an event in a track's
// range the way the firmware does: asked about "long address 0 left", a
// detector lists the locomotive present first.
func (o *Outpost) reporterIdentify(t int, ev openlcb.EventID, rng openlcb.EventRange) {
	o.mu.Lock()
	tr := o.tracks[t]
	o.mu.Unlock()
	rep, _ := detector.Decode(ev, rng)
	present := tr.known && tr.occupied
	here := present && rep.Address == tr.loco.Address
	facing := o.entryKind(t, tr.loco)

	if uint16(ev) == 0 && present {
		o.sendIdentified(detector.Report{Kind: facing, Address: tr.loco.Address}.Event(rng), true, stateValid)
	}
	var st eventState
	switch {
	case !tr.known:
		st = stateUnknown
	case rep.Kind == detector.Exit:
		st = stateValid
		if here {
			st = stateInvalid
		}
	case rep.Kind == detector.Entry:
		st = stateInvalid
		if here {
			st = stateValid
		}
	case o.intValue(trackPath(t, "Orientation")) == 0:
		st = stateUnknown // facing is not reported
	case !here:
		st = stateInvalid
	case facing == detector.Entry:
		st = stateUnknown
	case facing == rep.Kind:
		st = stateValid
	default:
		st = stateInvalid
	}
	o.sendIdentified(ev, true, st)
}
