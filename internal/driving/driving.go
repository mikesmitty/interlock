// Package driving drives trains on their own. Asked to take a train to a
// block, it finds the way there over the routes the interlocking sets,
// sets them, runs the train at the speed asked, and brakes it to a stand
// at the block's stop marker for the train's length and the way it
// runs, by train tracking's estimate of where the train is and the
// speeds tracking has measured for it. It can also run a train on
// around the layout, as the turnouts lie, holding each of a list of
// speed steps for long enough that tracking measures how fast the train
// goes at it.
//
// Each drive runs on a goroutine of its own, looking at where its train
// is every tick and setting its speed. A throttle that changes the
// speed of a train being driven takes it over, and the drive ends.
package driving

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

// Tracking is train tracking.
type Tracking interface {
	Estimate(ctx context.Context, id string) (tracking.Train, error)
}

// Interlocking sets the routes trains run over.
type Interlocking interface {
	List() []interlocking.Route
	Get(id string) (interlocking.Route, bool)
	Set(ctx context.Context, id string) (interlocking.Route, error)
	Cancel(ctx context.Context, id string, force bool) (interlocking.Route, error)
}

// Trains is the train manager, which sets the trains' speeds.
type Trains interface {
	SetSpeed(ctx context.Context, id string, speed float32, direction string) (train.Train, error)
}

// Plan is the track plan.
type Plan interface {
	Get() trackplan.State
}

// Entities is the entity manager, for where the turnouts are.
type Entities interface {
	List() []entity.Entity
}

// Options configure a Driver.
type Options struct {
	Tracking     Tracking
	Interlocking Interlocking
	Trains       Trains
	Plan         Plan
	Entities     Entities
	Logger       *slog.Logger
	// Tick is how often a drive looks where its train is; 50 ms when
	// zero.
	Tick time.Duration
	// SetTimeout is how long a drive waits for its routes' turnouts to
	// move; 20 s when zero.
	SetTimeout time.Duration
	// Now is the time; time.Now when nil.
	Now func() time.Time
}

// The kinds of drive.
const (
	// KindDrive takes a train to a stop.
	KindDrive = "drive"
	// KindMeasure runs a train around to measure its speeds.
	KindMeasure = "measure"
)

// The states of a drive.
const (
	// StateStarting is a drive waiting for its routes' turnouts.
	StateStarting = "starting"
	StateRunning  = "running"
	// StateBraking is a train slowing for where it is to stop.
	StateBraking = "braking"
	// StateStopping is a train set to stop, coming to a stand.
	StateStopping = "stopping"
	// The states a drive ends in.
	StateArrived  = "arrived"
	StateMeasured = "measured"
	StateStopped  = "stopped"
	StateFailed   = "failed"
)

// Drive is a train being driven, or the last drive of a train.
type Drive struct {
	// Train is the tracked train's ID, and Name its name.
	Train string `json:"train"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	// To is the block it goes to, and Marker the stop marker there it
	// stops at, if any.
	To     string `json:"to,omitempty"`
	Marker string `json:"marker,omitempty"`
	// Routes are the routes it runs over, by ID.
	Routes []string `json:"routes"`
	// Direction is the way it runs, in the train's terms: forward with
	// its front leading, or reverse.
	Direction string `json:"direction"`
	// Speed is the step it runs at, and Step the one it is set to now.
	Speed int `json:"speed"`
	Step  int `json:"step"`
	// Braking is how hard it brakes, in millimeters a second per second.
	Braking float64 `json:"braking"`
	State   string  `json:"state"`
	// Remaining is how far it has to go to where it stops, in
	// millimeters, by tracking's estimate.
	Remaining float64 `json:"remaining"`
	// Off is how far short of where it was to stop it came to a stand,
	// by the estimate, negative past it.
	Off *float64 `json:"off,omitempty"`
	// Error says why a drive ended before it got there.
	Error     string     `json:"error,omitempty"`
	Measuring *Measuring `json:"measuring,omitempty"`
	Started   time.Time  `json:"started"`
	Ended     time.Time  `json:"ended,omitzero"`
	Updated   time.Time  `json:"updated"`
}

// Active reports whether the drive is still going.
func (d Drive) Active() bool {
	return d.State == StateStarting || d.State == StateRunning || d.State == StateBraking || d.State == StateStopping
}

// Measuring is the progress of a run to measure a train's speeds.
type Measuring struct {
	// Steps are the steps it measures, At the one it is on, and Done and
	// Skipped those it has measured and those it gave up on.
	Steps   []int `json:"steps"`
	At      int   `json:"at,omitempty"`
	Done    []int `json:"done"`
	Skipped []int `json:"skipped,omitempty"`
}

// Request asks for a train to be driven to a block.
type Request struct {
	// To is the block, and Marker a stop marker there to stop at rather
	// than the one chosen for the train.
	To     string `json:"to"`
	Marker string `json:"marker,omitempty"`
	// Speed is the step to run at; DefaultSpeed when 0.
	Speed int `json:"speed,omitempty"`
	// Direction is the way to go, forward or reverse. When empty the
	// train goes the way it faces, or backs if the block can't be
	// reached that way.
	Direction string `json:"direction,omitempty"`
	// Braking is how hard to brake; DefaultBraking when 0.
	Braking float64 `json:"braking,omitempty"`
}

// MeasureRequest asks for a train's speeds to be measured.
type MeasureRequest struct {
	// Steps are the steps to measure; DefaultSteps when empty.
	Steps []int `json:"steps,omitempty"`
	// Direction is the way to run; the way the train faces when empty.
	Direction string `json:"direction,omitempty"`
}

// DefaultSpeed is the step a train is driven at when none is asked.
const DefaultSpeed = 60

// DefaultSteps are the steps a speed measuring run measures when none
// are asked.
var DefaultSteps = []int{15, 30, 45, 60, 80, 100}

// Errors.
var (
	ErrNotDriving = errors.New("driving: the train isn't being driven")
	ErrNoTrain    = errors.New("driving: no such train")
)

// RefusedError says why a drive can't be begun.
type RefusedError struct {
	Reason string
}

func (e *RefusedError) Error() string { return e.Reason }

func refuse(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// Subscriber is told whenever a drive changes.
type Subscriber func(Drive)

// publishEvery spaces the updates about a drive that is only moving on.
const publishEvery = 250 * time.Millisecond

// Driver drives trains.
type Driver struct {
	opts   Options
	log    *slog.Logger
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	runs    map[string]*run
	subs    map[int]Subscriber
	nextSub int
}

// run is one drive as its goroutine keeps it.
type run struct {
	view    Drive
	address detector.Address
	// command is the roster ID or address the train manager knows the
	// train's locomotive by, and backward whether that locomotive faces
	// the train's rear.
	command  string
	backward bool
	reverse  bool
	// stop is set when the drive is asked to stop the train; abort says
	// why it ends at once, with the train left to whoever has it, or
	// with settle, set to speed 0 first: the layout's emergency stop and
	// track power going off stop it only until they end, and it would
	// start again with no drive to brake it.
	stop   bool
	abort  string
	settle bool
	// sent are the speeds the drive set last, to tell them from a
	// throttle's.
	sent      []sent
	published time.Time
}

type sent struct {
	speed     float32
	direction string
}

// New makes a driver; Run keeps it going.
func New(opts Options) *Driver {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Tick <= 0 {
		opts.Tick = 50 * time.Millisecond
	}
	if opts.SetTimeout <= 0 {
		opts.SetTimeout = 20 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	base, cancel := context.WithCancel(context.Background())
	return &Driver{opts: opts, log: opts.Logger, base: base, cancel: cancel, runs: map[string]*run{}, subs: map[int]Subscriber{}}
}

// Run waits until ctx ends, then stops the trains being driven.
func (d *Driver) Run(ctx context.Context) error {
	<-ctx.Done()
	d.cancel()
	d.wg.Wait()
	return nil
}

// List returns the drives going and the last of each train's that have
// ended, by train.
func (d *Driver) List() []Drive {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Drive, 0, len(d.runs))
	for _, id := range slices.Sorted(maps.Keys(d.runs)) {
		out = append(out, d.runs[id].view)
	}
	return out
}

// Get returns a train's drive, going or last ended.
func (d *Driver) Get(id string) (Drive, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.runs[id]
	if !ok {
		return Drive{}, false
	}
	return r.view, true
}

// Subscribe registers a subscriber and returns a function that removes
// it.
func (d *Driver) Subscribe(sub Subscriber) (unsubscribe func()) {
	d.mu.Lock()
	id := d.nextSub
	d.nextSub++
	d.subs[id] = sub
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.subs, id)
		d.mu.Unlock()
	}
}

// HandleTrain follows the speeds trains are set to: a speed set by
// anything but the drive is a throttle taking the train over.
func (d *Driver) HandleTrain(tr train.Train) {
	if tr.Removed {
		return
	}
	addr := addressOf(tr.Address, tr.Long)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.runs {
		if !r.view.Active() || r.address != addr || r.abort != "" || len(r.sent) == 0 {
			continue
		}
		switch {
		case tr.EStop:
			r.abort = "An emergency stop stopped it."
		case !slices.ContainsFunc(r.sent, func(s sent) bool { return math.Abs(float64(s.speed-tr.Speed)) < 0.5 && s.direction == tr.Direction }):
			r.abort = "A throttle took it over."
		}
	}
}

// HandleLayout follows track power and the layout's emergency stop,
// either of which ends every drive and leaves its train set to speed 0.
func (d *Driver) HandleLayout(l train.Layout) {
	why := ""
	switch {
	case l.PowerOff:
		why = "Track power went off. It stays stopped when power comes back."
	case l.EmergencyStop:
		why = "The layout's emergency stop stopped it. It stays stopped after Resume."
	default:
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.runs {
		if r.view.Active() && r.abort == "" {
			r.abort, r.settle = why, true
		}
	}
}

// Stop brings a train being driven to a stand, braking as it would for
// a stop.
func (d *Driver) Stop(_ context.Context, id string) (Drive, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.runs[id]
	if !ok || !r.view.Active() {
		return Drive{}, ErrNotDriving
	}
	r.stop = true
	return r.view, nil
}

func addressOf(number uint16, long bool) detector.Address {
	if long || number > 127 {
		return detector.Address{Type: detector.Long, Number: number}
	}
	return detector.Address{Type: detector.Short, Number: number}
}

// setup is what begins a drive: the train as tracking has it, the plan's
// network and a planner over the routes.
type setup struct {
	t       tracking.Train
	plan    trackplan.Plan
	net     *trackplan.Network
	p       *planner
	names   map[string]string
	command string
}

// prepare checks that a train can be driven and gathers what driving it
// needs.
func (d *Driver) prepare(ctx context.Context, id string) (*setup, error) {
	d.mu.Lock()
	r, busy := d.runs[id]
	busy = busy && r.view.Active()
	d.mu.Unlock()
	if busy {
		return nil, refuse("%s is being driven already.", r.view.Name)
	}
	t, err := d.opts.Tracking.Estimate(ctx, id)
	if errors.Is(err, tracking.ErrNotTracked) {
		return nil, ErrNoTrain
	}
	if err != nil {
		return nil, err
	}
	switch {
	case !t.Placed:
		return nil, refuse("Tracking doesn't know where %s is in its block yet. Drive it by hand from one block into the next first.", t.Name)
	case !t.Motion.Known():
		return nil, refuse("Tracking hasn't measured how fast %s goes yet. Drive it by hand from one block into the next a few times first.", t.Name)
	case !t.Momentum:
		return nil, refuse("Interlock needs %s's momentum (CVs 3 and 4) to brake it. Read them from its decoder first.", t.Name)
	case len(t.Addresses) > 1:
		return nil, refuse("%s has more than one locomotive. Interlock drives trains with one, or a consist, for now.", t.Name)
	}
	st := d.opts.Plan.Get()
	s := &setup{t: t, plan: st.Plan, net: st.Plan.Network(), names: map[string]string{}}
	for _, b := range st.Plan.Blocks {
		s.names[b.ID] = b.Name
	}
	s.p = &planner{net: s.net, routes: d.opts.Interlocking.List(), position: d.positions(st.Plan)}
	s.command = train.IDFor(t.Address.Number, t.Address.Type == detector.Long)
	if len(t.Roster) > 0 {
		s.command = t.Roster[0]
	}
	return s, nil
}

// positions is how the plan's junctions are set, as their turnouts say:
// "" while one moves or is unknown; a junction with no turnout stays
// closed.
func (d *Driver) positions(plan trackplan.Plan) func(string) trackplan.Position {
	states := map[string]string{}
	if d.opts.Entities != nil {
		for _, e := range d.opts.Entities.List() {
			if e.Kind == entity.KindTurnout {
				states[e.ID] = e.State
			}
		}
	}
	switches := map[string]string{}
	for _, j := range plan.Junctions {
		switches[j.ID] = j.Switch
	}
	return func(j string) trackplan.Position {
		id, ok := switches[j]
		if !ok || id == "" {
			return trackplan.Closed
		}
		switch states[id] {
		case entity.StateClosed:
			return trackplan.Closed
		case entity.StateThrown:
			return trackplan.Thrown
		}
		return ""
	}
}

// leading is the end of a train that leads going the way given: its leg,
// taken that way, and how far along it.
func leading(n *trackplan.Network, s trackplan.Stretch, reverse bool) (trackplan.Leg, float64) {
	if reverse {
		s = s.Reversed(n)
	}
	last := s[len(s)-1]
	return last.Leg, last.To
}

// directions are the ways to try in turn: the one asked, else the way
// the train goes or would go, then the other unless it is moving.
func directions(t tracking.Train, asked string) ([]bool, error) {
	moving := t.Step != 0
	going := t.Direction == train.Reverse
	switch asked {
	case "":
		if moving {
			return []bool{going}, nil
		}
		return []bool{going, !going}, nil
	case train.Forward, train.Reverse:
		want := asked == train.Reverse
		if moving && want != going {
			return nil, refuse("%s is running the other way. Stop it first.", t.Name)
		}
		return []bool{want}, nil
	}
	return nil, refuse("The direction must be forward or reverse.")
}

// Drive begins driving a train to a block: it finds the way there,
// sets the routes and starts the train. The drive goes on after it
// returns; a *RefusedError says why it can't begin.
func (d *Driver) Drive(ctx context.Context, id string, req Request) (Drive, error) {
	if req.Speed == 0 {
		req.Speed = DefaultSpeed
	}
	if req.Braking == 0 {
		req.Braking = DefaultBraking
	}
	if req.Speed < 1 || req.Speed > train.MaxSpeed {
		return Drive{}, refuse("The speed must be a step from 1 to %d.", train.MaxSpeed)
	}
	if req.Braking < 1 || req.Braking > 10_000 || math.IsNaN(req.Braking) {
		return Drive{}, refuse("The braking must be from 1 to 10000 mm/s².")
	}
	s, err := d.prepare(ctx, id)
	if err != nil {
		return Drive{}, err
	}
	to, ok := s.names[req.To]
	if !ok {
		return Drive{}, refuse("The plan has no block %q.", req.To)
	}
	ways, err := directions(s.t, req.Direction)
	if err != nil {
		return Drive{}, err
	}
	var best *course
	var reverse bool
	reason := ""
	for _, rev := range ways {
		lead, at := leading(s.net, s.t.Stretch, rev)
		if s.net.Block(lead) == "" {
			reason = fmt.Sprintf("%s's leading end is between blocks. Drive it into a block first.", s.t.Name)
			continue
		}
		walked := s.p.walk(lead)
		way, found := s.p.way(walked, req.To)
		if !found {
			continue
		}
		c := s.p.build(at, walked, way)
		if !c.stopAt(s.net, s.plan.Markers, req.To, s.t.Length, req.Marker) {
			if req.Marker != "" {
				reason = fmt.Sprintf("The stop marker %s isn't on %s's way through %s.", req.Marker, s.t.Name, to)
			} else {
				reason = fmt.Sprintf("%s is at the end of %s already.", s.t.Name, to)
			}
			continue
		}
		best, reverse = c, rev
		break
	}
	if best == nil {
		if reason == "" {
			reason = fmt.Sprintf("Interlock can't find a way from where %s is to %s over the routes it sets.", s.t.Name, to)
		}
		return Drive{}, refuse("%s", reason)
	}
	r := d.newRun(s, reverse)
	r.view.Kind, r.view.To, r.view.Marker = KindDrive, req.To, best.marker
	r.view.Speed, r.view.Braking = req.Speed, req.Braking
	owned, err := d.setRoutes(ctx, best, s.t.ID, s.names)
	if err != nil {
		return Drive{}, err
	}
	for _, sp := range best.routes {
		r.view.Routes = append(r.view.Routes, sp.route)
	}
	return d.start(r, best, owned, brakes{cruise: req.Speed, braking: req.Braking}, nil, s.net, s.p), nil
}

// newRun is a drive of the train set up, not yet begun.
func (d *Driver) newRun(s *setup, reverse bool) *run {
	now := d.opts.Now()
	dir := train.Forward
	if reverse {
		dir = train.Reverse
	}
	return &run{
		view: Drive{
			Train: s.t.ID, Name: s.t.Name, Routes: []string{}, Direction: dir,
			State: StateStarting, Started: now, Updated: now,
		},
		address:  s.t.Address,
		command:  s.command,
		backward: s.t.Backward,
		reverse:  reverse,
	}
}

// setRoutes sets the routes a course runs over, and returns those it
// set: a route set already, with no train on it but this one, is used as
// it is. If one can't be set, those it set are canceled again.
func (d *Driver) setRoutes(ctx context.Context, c *course, trainID string, names map[string]string) (map[string]bool, error) {
	owned := map[string]bool{}
	undo := func() {
		for id := range owned {
			_, _ = d.opts.Interlocking.Cancel(context.WithoutCancel(ctx), id, false)
		}
	}
	for _, sp := range c.routes {
		rt, ok := d.opts.Interlocking.Get(sp.route)
		if !ok {
			undo()
			return nil, refuse("A route on the way is no longer in the plan.")
		}
		if rt.State != "" {
			if rt.Train != "" && rt.Train != trainID {
				undo()
				return nil, refuse("The route from %s to %s has a train on it.", names[rt.From.Block], names[rt.To.Block])
			}
			continue
		}
		if _, err := d.opts.Interlocking.Set(ctx, sp.route); err != nil {
			undo()
			var refused *interlocking.RefusedError
			if errors.As(err, &refused) {
				return nil, refuse("The route from %s to %s can't be set: %s", names[rt.From.Block], names[rt.To.Block], refused.Reason)
			}
			return nil, err
		}
		owned[sp.route] = true
	}
	return owned, nil
}

// start puts a run in the list and begins its goroutine.
func (d *Driver) start(r *run, c *course, owned map[string]bool, b brakes, m *measurer, net *trackplan.Network, p *planner) Drive {
	r.view.Remaining = math.Round(c.stop)
	d.mu.Lock()
	d.runs[r.view.Train] = r
	view := r.view
	d.mu.Unlock()
	d.log.Info("driving a train", "train", r.view.Name, "kind", r.view.Kind, "to", r.view.To, "marker", r.view.Marker, "routes", r.view.Routes, "direction", r.view.Direction, "speed", r.view.Speed)
	d.send(view)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.drive(r, c, owned, b, m, net, p)
	}()
	return view
}

// send tells the subscribers about a drive.
func (d *Driver) send(v Drive) {
	d.mu.Lock()
	subs := make([]Subscriber, 0, len(d.subs))
	for _, sub := range d.subs {
		subs = append(subs, sub)
	}
	d.mu.Unlock()
	for _, sub := range subs {
		sub(v)
	}
}

// update changes a run's view and publishes it, at once when its state
// changes or now is set, else at most every publishEvery.
func (d *Driver) update(r *run, now bool, change func(*Drive)) {
	d.mu.Lock()
	before := r.view.State
	change(&r.view)
	t := d.opts.Now()
	r.view.Updated = t
	if !now && r.view.State == before && t.Sub(r.published) < publishEvery {
		d.mu.Unlock()
		return
	}
	r.published = t
	v := r.view
	v.Routes = slices.Clone(v.Routes)
	if v.Measuring != nil {
		m := *v.Measuring
		m.Done, m.Skipped = slices.Clone(m.Done), slices.Clone(m.Skipped)
		v.Measuring = &m
	}
	d.mu.Unlock()
	d.send(v)
}

// control is what a drive's goroutine reads of its run each tick.
func (d *Driver) control(r *run) (stop bool, abort string, settle bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return r.stop, r.abort, r.settle
}

// setSpeed sets the train to a step the way the drive runs it.
func (d *Driver) setSpeed(ctx context.Context, r *run, step int) error {
	dir := train.Forward
	if r.reverse != r.backward {
		dir = train.Reverse
	}
	d.mu.Lock()
	r.sent = append(r.sent, sent{speed: float32(step), direction: dir})
	if len(r.sent) > 4 {
		r.sent = r.sent[len(r.sent)-4:]
	}
	d.mu.Unlock()
	_, err := d.opts.Trains.SetSpeed(ctx, r.command, float32(step), dir)
	return err
}

// end finishes a drive in a final state, and cancels the routes it set
// that the train hasn't reached.
func (d *Driver) end(r *run, c *course, owned map[string]bool, pos float64, state, why string) {
	ctx, cancel := d.afterwards()
	defer cancel()
	for _, sp := range c.routes {
		if owned[sp.route] && sp.start > pos+interlocking.Clearance {
			if _, err := d.opts.Interlocking.Cancel(ctx, sp.route, false); err != nil {
				d.log.Debug("driving: canceling a route not reached", "route", sp.route, "error", err)
			}
		}
	}
	d.update(r, true, func(v *Drive) {
		v.State, v.Error, v.Ended = state, why, d.opts.Now()
	})
	if why != "" {
		d.log.Info("a drive ended", "train", r.view.Name, "state", state, "why", why)
	} else {
		d.log.Info("a drive ended", "train", r.view.Name, "state", state, "off", r.view.Off)
	}
}

// afterwards is a context for what a drive does as it ends, which goes
// on as Interlock shuts down but not for long.
func (d *Driver) afterwards() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(d.base), 2*time.Second)
}

// drive is a drive's goroutine: every tick it finds where the train is
// on its course and sets the speed it is to run at.
func (d *Driver) drive(r *run, c *course, owned map[string]bool, b brakes, m *measurer, net *trackplan.Network, p *planner) {
	ctx := d.base
	ticker := time.NewTicker(d.opts.Tick)
	defer ticker.Stop()
	began := d.opts.Now()
	leg, set, commanded := 0, false, -1
	pos := 0.0
	stopping := false
	halt := func(state, why string) {
		ctx, cancel := d.afterwards()
		defer cancel()
		if err := d.setSpeed(ctx, r, 0); err != nil {
			d.log.Warn("driving: stopping a train", "train", r.view.Name, "error", err)
		}
		d.end(r, c, owned, pos, state, why)
	}
	for {
		select {
		case <-ctx.Done():
			halt(StateStopped, "Interlock shut down.")
			return
		case <-ticker.C:
		}
		stop, abort, settle := d.control(r)
		if abort != "" && settle {
			halt(StateStopped, abort)
			return
		}
		if abort != "" {
			d.end(r, c, owned, pos, StateStopped, abort)
			return
		}
		t, err := d.opts.Tracking.Estimate(ctx, r.view.Train)
		if ctx.Err() != nil {
			halt(StateStopped, "Interlock shut down.")
			return
		}
		if err != nil || !t.Placed {
			halt(StateFailed, "Tracking lost the train.")
			return
		}
		lead, at := leading(net, t.Stretch, r.reverse)
		found := false
		if pos, leg, found = c.locate(lead, at, leg); !found {
			halt(StateFailed, "Tracking has the train off the way it was going.")
			return
		}
		// Until its routes are set, the train may run only as far as the
		// first of them.
		limit := math.Inf(1)
		if !set {
			first, why := d.ready(c, pos)
			switch {
			case why != "":
				halt(StateFailed, why)
				return
			case first >= 0 && d.opts.Now().Sub(began) > d.opts.SetTimeout:
				halt(StateFailed, "Its routes' turnouts didn't move.")
				return
			case first >= 0:
				limit = c.routes[first].start - endMargin
			default:
				set = true
			}
		}
		if !set && t.Step == 0 && commanded < 0 {
			// At a stand: it waits where it is.
			if stop {
				d.end(r, c, owned, pos, StateStopped, "")
				return
			}
			continue
		}
		if why := d.authority(c, pos); set && why != "" {
			halt(StateFailed, why)
			return
		}
		if m != nil {
			if why := m.tick(ctx, d, r, c, &b, t, pos, p, net); why != "" {
				halt(StateFailed, why)
				return
			}
		}
		if stop && !stopping {
			stopping = true
			c.stop = min(c.stop, pos+b.stoppingDistance(t.Motion, t.Step))
			if m != nil {
				m.finished = true
			}
		}
		remaining := c.stop - pos
		step := b.step(t.Motion, t.Step, min(c.stop, limit)-pos)
		if step != commanded {
			if err := d.setSpeed(ctx, r, step); err != nil {
				halt(StateFailed, fmt.Sprintf("Its speed couldn't be set: %v", err))
				return
			}
			commanded = step
		}
		if commanded == 0 && t.Step == 0 && !set && stop {
			d.end(r, c, owned, pos, StateStopped, "")
			return
		}
		if commanded == 0 && t.Step == 0 && set && (m == nil || m.finished) {
			off := math.Round(remaining*10) / 10
			state := StateArrived
			switch {
			case stopping:
				state = StateStopped
			case m != nil:
				state = StateMeasured
			}
			d.update(r, true, func(v *Drive) { v.Off, v.Remaining, v.Step = &off, math.Round(remaining), 0 })
			d.end(r, c, owned, pos, state, "")
			return
		}
		d.update(r, false, func(v *Drive) {
			v.Remaining, v.Step = math.Round(max(remaining, 0)), commanded
			switch {
			case !set:
				v.State = StateStarting
			case commanded == 0:
				v.State = StateStopping
			case commanded < b.cruise:
				v.State = StateBraking
			default:
				v.State = StateRunning
			}
		})
		leg = c.trim(leg)
	}
}

// ready finds the first of the routes ahead of the leading end, at pos
// along a course, whose turnouts aren't yet where it needs them, by its
// place in the course; -1 when they all are. It says why the drive
// can't go on if one was canceled.
func (d *Driver) ready(c *course, pos float64) (int, string) {
	first := -1
	for i, sp := range c.routes {
		if pos >= sp.end {
			continue // passed, and released behind the train
		}
		rt, ok := d.opts.Interlocking.Get(sp.route)
		switch {
		case !ok || rt.State == "":
			return -1, "One of its routes was canceled."
		case rt.State == interlocking.StateSetting && first < 0:
			first = i
		}
	}
	return first, ""
}

// authority says why the train may no longer run on: a route ahead of it
// canceled, or one whose turnout has moved; "" while it may.
func (d *Driver) authority(c *course, pos float64) string {
	for _, sp := range c.routes {
		if pos >= sp.end {
			continue // passed, and released behind the train
		}
		rt, ok := d.opts.Interlocking.Get(sp.route)
		switch {
		case !ok || rt.State == "":
			return "A route on its way was canceled."
		case rt.State == interlocking.StateSetting:
			return "A turnout on its way moved."
		}
	}
	return ""
}
