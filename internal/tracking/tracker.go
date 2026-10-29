// Package tracking follows the trains on the layout: which block each
// locomotive's decoder is in from what the RailCom detectors report,
// where the rest of the train is from the train the roster makes the
// locomotive part of (its locomotives' and cars' lengths, and where the
// locomotive is in it), which way it is going, and between reports how
// far it has run at the speed it was set to. A locomotive in no train
// runs on its own, at its own length.
//
// A detector sees only a train's decoder, so a train is first known by
// its block alone. Once it runs from one block into the next, tracking
// knows where it entered and which way it goes, and lays the train out
// behind or ahead of its decoder along the track it came by. Between
// entries the train runs on at its decoder's speed curve and momentum
// times a top speed measured from the time it took from one entry to
// the next, never past the next detected block before a detector there
// sees it. Each entry puts it back where the decoder actually is.
package tracking

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

// Train is a train as tracking follows it.
type Train struct {
	// ID is the formation's ID for a train made up in the roster, else
	// the address of the locomotive running on its own as the roster
	// writes one: "101", "3l", or "consist-12" for a consist address.
	ID string `json:"id"`
	// Name is the formation's name, else the roster's name for the
	// locomotive at the address, else the address.
	Name string `json:"name"`
	// Formation is the formation's ID; empty for a locomotive on its
	// own.
	Formation string `json:"formation,omitempty"`
	// Address is the decoder the train is followed by: the one a
	// detector last reported.
	Address detector.Address `json:"address"`
	// Roster lists the train's locomotives in the roster.
	Roster []string `json:"roster,omitempty"`
	// Block is the block a detector last saw the decoder in, and Seen
	// whether one sees it now.
	Block string `json:"block,omitempty"`
	Seen  bool   `json:"seen"`
	// Placed is whether tracking knows where the train is in its block
	// and which way it faces, which it learns when the train runs from
	// one block into the next.
	Placed bool `json:"placed"`
	// Blocks are the blocks the train is in, its front first: the
	// decoder's, and those the rest of the train reaches into.
	Blocks []string `json:"blocks"`
	// Direction is the way it goes or would go: forward, with its front
	// leading, or reverse.
	Direction string `json:"direction"`
	// Speed is how fast it goes by the estimate, in millimeters a
	// second, and TopSpeed how fast it was measured to go at full speed
	// on its decoder's speed curve; both 0 until measured.
	Speed    float64 `json:"speed"`
	TopSpeed float64 `json:"top_speed,omitempty"`
	// Speeds are the speeds measured at the steps it held over a whole
	// run from one block into the next.
	Speeds []SpeedPoint `json:"speeds,omitempty"`
	// Step is the speed step its decoder has reached by the estimate,
	// negative in reverse, in the train's terms; Motion is how it moves
	// at each step.
	Step   float64 `json:"-"`
	Motion Motion  `json:"-"`
	// Backward is whether the locomotive of the decoder it is followed
	// by faces the train's rear, so that its decoder's forward runs the
	// train in reverse.
	Backward bool `json:"-"`
	// Addresses are the decoders of the train's locomotives.
	Addresses []detector.Address `json:"-"`
	// Length is the train's length from the roster.
	Length float64 `json:"length,omitempty"`
	// Momentum is whether its decoder's momentum (CVs 3 and 4) is on
	// record. Without it the estimate changes speed at once while the
	// train takes its time.
	Momentum bool `json:"momentum"`
	// Line is where the train is on the plan, rear to front, and Decoder
	// where its decoder is, while it is placed.
	Line    []trackplan.Vec `json:"line,omitempty"`
	Decoder *trackplan.Vec  `json:"decoder,omitempty"`
	// Stretch is the track the train covers, rear to front, while it is
	// placed: what the interlocking releases routes behind.
	Stretch trackplan.Stretch `json:"-"`
	Updated time.Time         `json:"updated,omitzero"`
	// Removed is set on the last update about a train no longer
	// followed, as when its locomotive joins a train in the roster.
	Removed bool `json:"removed,omitempty"`
}

// Entities is the entity manager, for turnouts and reporters.
type Entities interface {
	List() []entity.Entity
}

// Trains is the train manager, for the roster and the speeds trains are
// set to.
type Trains interface {
	List() []train.Train
	Cars() []train.CarState
	Formations() []train.FormationState
	Consists() []train.ConsistState
	Layout() train.Layout
}

// Plan is the track plan.
type Plan interface {
	Get() trackplan.State
}

// CVs keeps decoders' CV values.
type CVs interface {
	Get(decoder string) (train.CVValues, error)
}

// Store keeps where the trains were over a restart.
type Store interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Options configure a Tracker.
type Options struct {
	Entities Entities
	Trains   Trains
	Plan     Plan
	// CVs and Store may be nil.
	CVs    CVs
	Store  Store
	Logger *slog.Logger
	// Tick is how often trains are moved on by their speed; 100 ms
	// when zero.
	Tick time.Duration
}

// Subscriber is told whenever a tracked train changes.
type Subscriber func(Train)

// settingKey is where the trains' places are kept in the store.
const settingKey = "tracking"

// publishEvery spaces the updates about a train that is only moving.
const publishEvery = 250 * time.Millisecond

// Tracker follows the trains. Its Handle methods never block; they queue
// the change for the goroutine Run runs.
type Tracker struct {
	opts  Options
	log   *slog.Logger
	inbox chan func(*layout)

	mu      sync.RWMutex
	views   map[string]Train
	subs    map[int]Subscriber
	nextSub int
}

// New makes a tracker; Run starts it.
func New(opts Options) *Tracker {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Tick <= 0 {
		opts.Tick = 100 * time.Millisecond
	}
	return &Tracker{
		opts:  opts,
		log:   opts.Logger,
		inbox: make(chan func(*layout), 4096),
		views: map[string]Train{},
		subs:  map[int]Subscriber{},
	}
}

func (tk *Tracker) push(fn func(*layout)) {
	select {
	case tk.inbox <- fn:
	default:
		tk.log.Warn("train tracking fell behind; a change was dropped")
	}
}

// HandleEntity follows turnouts and reporters.
func (tk *Tracker) HandleEntity(e entity.Entity) {
	if e.Kind != entity.KindTurnout && e.Kind != entity.KindReporter {
		return
	}
	tk.push(func(l *layout) { l.entity(e) })
}

// HandleTrain follows the roster and the speeds trains are set to.
func (tk *Tracker) HandleTrain(tr train.Train) {
	tk.push(func(l *layout) { l.train(tr) })
}

// HandleCar follows the roster's cars.
func (tk *Tracker) HandleCar(c train.CarState) {
	tk.push(func(l *layout) { l.car(c) })
}

// HandleFormation follows the trains made up in the roster.
func (tk *Tracker) HandleFormation(f train.FormationState) {
	tk.push(func(l *layout) { l.formation(f) })
}

// HandleConsist follows the DCC consists made up, whose decoders
// detectors report by the consist address.
func (tk *Tracker) HandleConsist(c train.ConsistState) {
	tk.push(func(l *layout) { l.consist(c) })
}

// HandleLayout follows track power and the emergency stop.
func (tk *Tracker) HandleLayout(lay train.Layout) {
	tk.push(func(l *layout) { l.powerOff, l.estop = lay.PowerOff, lay.EmergencyStop })
}

// HandlePlan follows the track plan as it is saved.
func (tk *Tracker) HandlePlan(st trackplan.State) {
	tk.push(func(l *layout) { l.setPlan(st.Plan, st.Revision) })
}

// decoderCVs reads the CVs that set how a decoder moves, those read
// from it first, else those imported for it.
func (tk *Tracker) decoderCVs(decoder string) decoderCVs {
	if tk.opts.CVs == nil {
		return decoderCVs{}
	}
	v, err := tk.opts.CVs.Get(decoder)
	if err != nil {
		tk.log.Debug("tracking: reading a decoder's CVs", "decoder", decoder, "error", err)
		return decoderCVs{}
	}
	has := func(cv string) bool {
		_, read := v.CVs[cv]
		_, imported := v.Imported[cv]
		return read || imported
	}
	get := func(cv string) byte {
		n, ok := v.CVs[cv]
		if !ok {
			n = v.Imported[cv]
		}
		return byte(n)
	}
	return decoderCVs{start: get("2"), mid: get("6"), high: get("5"), accel: get("3"), decel: get("4"), known: has("3") && has("4")}
}

// ErrNotTracked is a train tracking doesn't follow.
var ErrNotTracked = errors.New("tracking: no such train")

// Forget stops following where a train is, by its ID, as when it has
// been taken off the layout: it is published as removed, and found again
// when a detector next reports its decoder entering a block.
func (tk *Tracker) Forget(ctx context.Context, id string) error {
	done := make(chan bool, 1)
	tk.push(func(l *layout) { done <- l.forget(id) })
	select {
	case found := <-done:
		if !found {
			return ErrNotTracked
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Estimate returns a train as tracking estimates it this moment, by its
// ID, moved on to now rather than as last published.
func (tk *Tracker) Estimate(ctx context.Context, id string) (Train, error) {
	done := make(chan *Train, 1)
	tk.push(func(l *layout) {
		l.tick(time.Now())
		for _, t := range l.trains {
			if t.known() && l.unitFor(t).id == id {
				v := l.view(t)
				done <- &v
				return
			}
		}
		done <- nil
	})
	select {
	case v := <-done:
		if v == nil {
			return Train{}, ErrNotTracked
		}
		return *v, nil
	case <-ctx.Done():
		return Train{}, ctx.Err()
	}
}

// List returns the tracked trains by name.
func (tk *Tracker) List() []Train {
	tk.mu.RLock()
	out := make([]Train, 0, len(tk.views))
	for _, v := range tk.views {
		out = append(out, v)
	}
	tk.mu.RUnlock()
	slices.SortFunc(out, func(a, b Train) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Subscribe registers a subscriber and returns a function that removes
// it.
func (tk *Tracker) Subscribe(sub Subscriber) (unsubscribe func()) {
	tk.mu.Lock()
	id := tk.nextSub
	tk.nextSub++
	tk.subs[id] = sub
	tk.mu.Unlock()
	return func() {
		tk.mu.Lock()
		delete(tk.subs, id)
		tk.mu.Unlock()
	}
}

// Run tracks the trains until ctx ends, then saves where they are.
func (tk *Tracker) Run(ctx context.Context) error {
	l := newLayout()
	l.now = time.Now()
	l.cvs = tk.decoderCVs
	if tk.opts.Plan != nil {
		st := tk.opts.Plan.Get()
		l.setPlan(st.Plan, st.Revision)
	}
	if tk.opts.Trains != nil {
		trains := tk.opts.Trains.List()
		l.setRoster(trains, tk.opts.Trains.Cars(), tk.opts.Trains.Formations())
		for _, c := range tk.opts.Trains.Consists() {
			l.consist(c)
		}
		for _, tr := range trains {
			l.train(tr)
		}
		lay := tk.opts.Trains.Layout()
		l.powerOff, l.estop = lay.PowerOff, lay.EmergencyStop
	}
	tk.restore(ctx, l)
	if tk.opts.Entities != nil {
		for _, e := range tk.opts.Entities.List() {
			l.entity(e)
		}
	}
	published := map[string]time.Time{}
	dirty := true
	publish := func(all bool) {
		now := time.Now()
		for key := range l.changed {
			t, ok := l.trains[key]
			if ok && !all && now.Sub(published[key]) < publishEvery {
				continue
			}
			delete(l.changed, key)
			if !ok {
				delete(published, key)
				dirty = tk.drop(key) || dirty
				continue
			}
			if !t.known() {
				// Not seen yet, or forgotten.
				dirty = tk.drop(key) || dirty
				continue
			}
			published[key] = now
			tk.send(key, l.view(t))
			dirty = true
		}
	}
	publish(true)

	ticker := time.NewTicker(tk.opts.Tick)
	defer ticker.Stop()
	saving := time.NewTicker(5 * time.Second)
	defer saving.Stop()
	reading := time.NewTicker(10 * time.Second)
	defer reading.Stop()
	for {
		select {
		case <-ctx.Done():
			tk.save(context.WithoutCancel(ctx), l)
			return nil
		case fn := <-tk.inbox:
			// The trains move on to now first, so what came is taken
			// where they are as it comes.
			l.tick(time.Now())
			fn(l)
			publish(true)
		case now := <-ticker.C:
			l.tick(now)
			publish(false)
		case <-reading.C:
			l.refreshCVs()
			publish(true)
		case <-saving.C:
			if dirty {
				tk.save(ctx, l)
				dirty = false
			}
		}
	}
}

func (tk *Tracker) send(key string, v Train) {
	tk.mu.Lock()
	tk.views[key] = v
	subs := make([]Subscriber, 0, len(tk.subs))
	for _, sub := range tk.subs {
		subs = append(subs, sub)
	}
	tk.mu.Unlock()
	for _, sub := range subs {
		sub(v)
	}
}

// drop tells subscribers a train is no longer followed, if it was shown;
// it reports whether it was.
func (tk *Tracker) drop(key string) bool {
	tk.mu.Lock()
	v, ok := tk.views[key]
	delete(tk.views, key)
	subs := make([]Subscriber, 0, len(tk.subs))
	for _, sub := range tk.subs {
		subs = append(subs, sub)
	}
	tk.mu.Unlock()
	if !ok {
		return false
	}
	v.Removed = true
	v.Updated = time.Now()
	for _, sub := range subs {
		sub(v)
	}
	return true
}

// saved is where the trains were, as kept in the store.
type saved struct {
	Trains []savedTrain `json:"trains"`
}

// savedTrain is one train, by the decoder it was followed by. At and
// Length are where that decoder was in the train and how long the train
// was as its stretch was laid out.
type savedTrain struct {
	Address detector.Address  `json:"address"`
	Block   string            `json:"block,omitempty"`
	Stretch trackplan.Stretch `json:"stretch,omitempty"`
	At      float64           `json:"at,omitempty"`
	Length  float64           `json:"length,omitempty"`
	Heading float64           `json:"heading,omitempty"`
	Top     float64           `json:"top,omitempty"`
	Samples int               `json:"samples,omitempty"`
	Speeds  []SpeedPoint      `json:"speeds,omitempty"`
}

func (l *layout) state() saved {
	st := saved{Trains: []savedTrain{}}
	for _, t := range l.trains {
		if t.block == "" && t.top == 0 && len(t.speeds) == 0 {
			continue
		}
		st.Trains = append(st.Trains, savedTrain{Address: t.addr, Block: t.block, Stretch: t.stretch, At: t.at, Length: t.length, Heading: t.heading, Top: t.top, Samples: t.samples, Speeds: t.speeds})
	}
	slices.SortFunc(st.Trains, func(a, b savedTrain) int {
		return cmp.Or(cmp.Compare(a.Address.Type, b.Address.Type), cmp.Compare(a.Address.Number, b.Address.Number))
	})
	return st
}

// restore puts the trains back where they were, each with the train its
// decoder runs in now, laid out again around the decoder if the roster
// has changed the train since. A train is known by its block alone if
// the plan no longer has its track.
func (l *layout) restore(st saved) {
	for _, s := range st.Trains {
		t := l.get(s.Address)
		l.follow(t, s.Address)
		t.block, t.heading, t.top, t.samples = s.Block, s.Heading, s.Top, s.Samples
		t.speeds = slices.DeleteFunc(slices.Clone(s.Speeds), func(p SpeedPoint) bool { return p.Step < 1 || p.Step > train.MaxSpeed || p.Speed <= 0 })
		if s.Stretch.Valid(l.net) {
			t.stretch, t.at, t.length = s.Stretch, s.At, s.Length
			if s.Length == 0 {
				// Saved before the stretch's layout was: take it as it is.
				t.length = s.Stretch.Length()
				t.at = min(l.info(t).at, t.length)
			}
			l.fit(t)
		}
		l.changed[t.key] = true
	}
}

func (tk *Tracker) save(ctx context.Context, l *layout) {
	if tk.opts.Store == nil {
		return
	}
	data, err := json.Marshal(l.state())
	if err == nil {
		err = tk.opts.Store.SetSetting(ctx, settingKey, string(data))
	}
	if err != nil {
		tk.log.Warn("saving where the trains are", "error", err)
	}
}

func (tk *Tracker) restore(ctx context.Context, l *layout) {
	if tk.opts.Store == nil {
		return
	}
	value, ok, err := tk.opts.Store.Setting(ctx, settingKey)
	if err != nil || !ok {
		if err != nil {
			tk.log.Warn("reading where the trains were", "error", err)
		}
		return
	}
	var st saved
	if err := json.Unmarshal([]byte(value), &st); err != nil {
		tk.log.Warn("reading where the trains were", "error", err)
		return
	}
	l.restore(st)
}
