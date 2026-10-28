// Package tracking follows the trains on the layout: which block each
// locomotive's decoder is in from what the RailCom detectors report,
// where the rest of the train is from its length and where its decoder
// sits in it, which way it is going, and between reports how far it has
// run at the speed it was set to.
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
	// ID is its address as the roster writes one: "101", "3l", or
	// "consist-12" for a consist address.
	ID string `json:"id"`
	// Name is the roster's name for the locomotive at the address, else
	// the address.
	Name    string           `json:"name"`
	Address detector.Address `json:"address"`
	// Roster lists the roster entries at the address.
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
	// second, and TopSpeed how fast it was measured to go at full speed;
	// both 0 until measured.
	Speed    float64 `json:"speed"`
	TopSpeed float64 `json:"top_speed,omitempty"`
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
	Updated time.Time       `json:"updated,omitzero"`
}

// Entities is the entity manager, for turnouts and reporters.
type Entities interface {
	List() []entity.Entity
}

// Trains is the train manager, for the roster and the speeds trains are
// set to.
type Trains interface {
	List() []train.Train
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
	views   map[detector.Address]Train
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
		views: map[detector.Address]Train{},
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
	tk.push(func(l *layout) { l.train(tr, tk.decoderCVs) })
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
	if tk.opts.Plan != nil {
		st := tk.opts.Plan.Get()
		l.setPlan(st.Plan, st.Revision)
	}
	if tk.opts.Trains != nil {
		for _, tr := range tk.opts.Trains.List() {
			l.train(tr, tk.decoderCVs)
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
	published := map[detector.Address]time.Time{}
	dirty := true
	publish := func(all bool) {
		now := time.Now()
		for addr := range l.changed {
			if !all && now.Sub(published[addr]) < publishEvery {
				continue
			}
			delete(l.changed, addr)
			t, ok := l.trains[addr]
			if !ok || !t.known() {
				continue
			}
			published[addr] = now
			tk.send(addr, l.view(t))
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
			fn(l)
			publish(true)
		case now := <-ticker.C:
			l.tick(now)
			publish(false)
		case <-reading.C:
			l.refreshCVs(tk.decoderCVs)
			publish(true)
		case <-saving.C:
			if dirty {
				tk.save(ctx, l)
				dirty = false
			}
		}
	}
}

func (tk *Tracker) send(addr detector.Address, v Train) {
	tk.mu.Lock()
	tk.views[addr] = v
	subs := make([]Subscriber, 0, len(tk.subs))
	for _, sub := range tk.subs {
		subs = append(subs, sub)
	}
	tk.mu.Unlock()
	for _, sub := range subs {
		sub(v)
	}
}

// saved is where the trains were, as kept in the store.
type saved struct {
	Trains []savedTrain `json:"trains"`
}

type savedTrain struct {
	Address detector.Address  `json:"address"`
	Block   string            `json:"block,omitempty"`
	Stretch trackplan.Stretch `json:"stretch,omitempty"`
	Heading float64           `json:"heading,omitempty"`
	Top     float64           `json:"top,omitempty"`
	Samples int               `json:"samples,omitempty"`
}

func (l *layout) state() saved {
	st := saved{Trains: []savedTrain{}}
	for _, t := range l.trains {
		if t.block == "" && t.top == 0 {
			continue
		}
		st.Trains = append(st.Trains, savedTrain{Address: t.addr, Block: t.block, Stretch: t.stretch, Heading: t.heading, Top: t.top, Samples: t.samples})
	}
	slices.SortFunc(st.Trains, func(a, b savedTrain) int {
		return cmp.Or(cmp.Compare(a.Address.Type, b.Address.Type), cmp.Compare(a.Address.Number, b.Address.Number))
	})
	return st
}

// restore puts the trains back where they were. A train is known by its
// block alone if the plan no longer has its track.
func (l *layout) restore(st saved) {
	for _, s := range st.Trains {
		t := l.get(s.Address)
		t.block, t.heading, t.top, t.samples = s.Block, s.Heading, s.Top, s.Samples
		if s.Stretch.Valid(l.net) {
			t.stretch = s.Stretch
		}
		l.changed[s.Address] = true
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
