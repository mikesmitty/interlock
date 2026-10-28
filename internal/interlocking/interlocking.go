package interlocking

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// Entities is the entity manager: the turnouts and blocks, and the
// commands that set turnouts.
type Entities interface {
	List() []entity.Entity
	Command(ctx context.Context, id, command string) (entity.Entity, error)
}

// Plan is the track plan.
type Plan interface {
	Get() trackplan.State
}

// Tracking is train tracking.
type Tracking interface {
	List() []tracking.Train
}

// Store keeps the routes set over a restart.
type Store interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Options configure an Interlocking.
type Options struct {
	Entities Entities
	Plan     Plan
	Tracking Tracking
	// Store may be nil.
	Store  Store
	Logger *slog.Logger
}

// Subscriber is told whenever routes change.
type Subscriber func(Update)

// settingKey is where the routes set are kept in the store.
const settingKey = "interlocking"

// Interlocking sets and releases routes. Its Handle methods never block;
// they queue the change for the goroutine Run runs.
type Interlocking struct {
	opts  Options
	log   *slog.Logger
	inbox chan func(*state)

	mu    sync.RWMutex
	views []Route
	index map[string]int
	// held are the turnouts the routes hold, as the guard sees them.
	held    map[string]holding
	subs    map[int]Subscriber
	nextSub int
}

// New makes an interlocking; Run starts it.
func New(opts Options) *Interlocking {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Interlocking{
		opts:  opts,
		log:   opts.Logger,
		inbox: make(chan func(*state), 4096),
		index: map[string]int{},
		held:  map[string]holding{},
		subs:  map[int]Subscriber{},
	}
}

func (ix *Interlocking) push(fn func(*state)) bool {
	select {
	case ix.inbox <- fn:
		return true
	default:
		ix.log.Warn("the interlocking fell behind; a change was dropped")
		return false
	}
}

// do runs fn on the interlocking's goroutine and waits for it.
func (ix *Interlocking) do(ctx context.Context, fn func(*state)) error {
	done := make(chan struct{})
	if !ix.push(func(s *state) { fn(s); close(done) }) {
		return fmt.Errorf("the interlocking is busy")
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HandleEntity follows turnouts and blocks.
func (ix *Interlocking) HandleEntity(e entity.Entity) {
	if e.Kind != entity.KindTurnout && e.Kind != entity.KindBlock {
		return
	}
	ix.push(func(s *state) { s.entity(e) })
}

// HandleTrain follows the trains tracking places.
func (ix *Interlocking) HandleTrain(t tracking.Train) {
	ix.push(func(s *state) { s.train(t) })
}

// HandlePlan follows the track plan as it is saved.
func (ix *Interlocking) HandlePlan(st trackplan.State) {
	ix.push(func(s *state) { s.setPlan(st) })
}

// Guard refuses commands that would move a turnout a route holds; the
// entity manager asks it before every command.
func (ix *Interlocking) Guard(e entity.Entity, command string) error {
	if e.Kind != entity.KindTurnout {
		return nil
	}
	ix.mu.RLock()
	h, ok := ix.held[e.ID]
	ix.mu.RUnlock()
	if !ok {
		return nil
	}
	want := map[string]string{"throw": entity.StateThrown, "close": entity.StateClosed}[command]
	if want == h.state {
		return nil
	}
	return &entity.LockedError{Reason: fmt.Sprintf("%s is locked %s by the route %s.", e.Name, h.state, h.by)}
}

// holding is a turnout a route holds: the state it is held in and the
// route, named.
type holding struct {
	state, by string
}

// List returns every route of the plan, set or not.
func (ix *Interlocking) List() []Route {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return slices.Clone(ix.views)
}

// Get returns one route.
func (ix *Interlocking) Get(id string) (Route, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	i, ok := ix.index[id]
	if !ok {
		return Route{}, false
	}
	return ix.views[i], true
}

// Set sets a route for a train: it locks the route's turnouts and moves
// those not yet where it needs them. A route set already moves back any
// that have moved away. It returns a *RefusedError saying why if
// something stands in the way, and ErrNoRoute for a route the plan
// doesn't have.
func (ix *Interlocking) Set(ctx context.Context, id string) (Route, error) {
	var view Route
	var cmds []command
	var err error
	if derr := ix.do(ctx, func(s *state) {
		s.now = time.Now()
		view, cmds, err = s.set(id)
		ix.publish(s)
	}); derr != nil {
		return Route{}, derr
	}
	if err != nil {
		return view, err
	}
	for _, c := range cmds {
		if _, cerr := ix.opts.Entities.Command(ctx, c.entity, c.command); cerr != nil {
			_ = ix.do(context.WithoutCancel(ctx), func(s *state) {
				_, _ = s.cancel(id, true)
				ix.publish(s)
			})
			return view, fmt.Errorf("setting the route: %w", cerr)
		}
	}
	if v, ok := ix.Get(id); ok {
		return v, nil
	}
	return view, nil
}

// Cancel releases a route. It refuses one a train is on, which is
// released behind the train as it goes, unless force is given.
func (ix *Interlocking) Cancel(ctx context.Context, id string, force bool) (Route, error) {
	var view Route
	var err error
	if derr := ix.do(ctx, func(s *state) {
		view, err = s.cancel(id, force)
		ix.publish(s)
	}); derr != nil {
		return Route{}, derr
	}
	return view, err
}

// Subscribe registers a subscriber and returns a function that removes
// it.
func (ix *Interlocking) Subscribe(sub Subscriber) (unsubscribe func()) {
	ix.mu.Lock()
	id := ix.nextSub
	ix.nextSub++
	ix.subs[id] = sub
	ix.mu.Unlock()
	return func() {
		ix.mu.Lock()
		delete(ix.subs, id)
		ix.mu.Unlock()
	}
}

// Run keeps the routes until ctx ends, then saves those set.
func (ix *Interlocking) Run(ctx context.Context) error {
	s := newState()
	s.now = time.Now()
	if ix.opts.Plan != nil {
		s.setPlan(ix.opts.Plan.Get())
	}
	if ix.opts.Entities != nil {
		for _, e := range ix.opts.Entities.List() {
			s.entity(e)
		}
	}
	if ix.opts.Tracking != nil {
		for _, t := range ix.opts.Tracking.List() {
			s.train(t)
		}
	}
	ix.restore(ctx, s)
	ix.publish(s)
	saved := ix.encode(s)

	saving := time.NewTicker(2 * time.Second)
	defer saving.Stop()
	for {
		select {
		case <-ctx.Done():
			ix.save(context.WithoutCancel(ctx), s, &saved)
			return nil
		case fn := <-ix.inbox:
			s.now = time.Now()
			fn(s)
			ix.publish(s)
		case <-saving.C:
			ix.save(ctx, s, &saved)
		}
	}
}

// publish shares what changed: the views, the locks the guard sees, and
// the updates to subscribers.
func (ix *Interlocking) publish(s *state) {
	if !s.all && len(s.changed) == 0 {
		return
	}
	views := make([]Route, len(s.routes))
	index := make(map[string]int, len(s.routes))
	for i, r := range s.routes {
		views[i] = r.view()
		index[r.ID] = i
	}
	var updates []Update
	if s.all {
		updates = append(updates, Update{Routes: slices.Clone(views)})
	} else {
		for _, id := range slices.Sorted(maps.Keys(s.changed)) {
			if i, ok := index[id]; ok {
				v := views[i]
				updates = append(updates, Update{Route: &v})
			}
		}
	}
	s.all = false
	clear(s.changed)

	held := make(map[string]holding, len(s.locks))
	for id, l := range s.locks {
		held[id] = holding{state: l.state, by: s.describe(l.route)}
	}

	ix.mu.Lock()
	ix.views, ix.index, ix.held = views, index, held
	subs := make([]Subscriber, 0, len(ix.subs))
	for _, sub := range ix.subs {
		subs = append(subs, sub)
	}
	ix.mu.Unlock()
	for _, u := range updates {
		for _, sub := range subs {
			sub(u)
		}
	}
}

// saved is the routes set, as kept in the store.
type saved struct {
	Routes []savedRoute `json:"routes"`
}

// savedRoute is one route set, by its ends and the positions it needs.
type savedRoute struct {
	Key      string    `json:"key"`
	Since    time.Time `json:"since"`
	Train    string    `json:"train,omitempty"`
	Released int       `json:"released,omitempty"`
	Visited  []bool    `json:"visited,omitempty"`
}

func (ix *Interlocking) encode(s *state) string {
	st := saved{Routes: []savedRoute{}}
	for _, r := range s.routes {
		if r.active {
			st.Routes = append(st.Routes, savedRoute{Key: r.key, Since: r.Since, Train: r.Train, Released: r.Released, Visited: r.visited})
		}
	}
	data, _ := json.Marshal(st)
	return string(data)
}

func (ix *Interlocking) save(ctx context.Context, s *state, last *string) {
	if ix.opts.Store == nil {
		return
	}
	data := ix.encode(s)
	if data == *last {
		return
	}
	if err := ix.opts.Store.SetSetting(ctx, settingKey, data); err != nil {
		ix.log.Warn("saving the routes set", "error", err)
		return
	}
	*last = data
}

// restore sets again the routes that were set, those the plan still
// has.
func (ix *Interlocking) restore(ctx context.Context, s *state) {
	if ix.opts.Store == nil {
		return
	}
	value, ok, err := ix.opts.Store.Setting(ctx, settingKey)
	if err != nil || !ok {
		if err != nil {
			ix.log.Warn("reading the routes that were set", "error", err)
		}
		return
	}
	var st saved
	if err := json.Unmarshal([]byte(value), &st); err != nil {
		ix.log.Warn("reading the routes that were set", "error", err)
		return
	}
	s.restore(st)
}

// restore puts back routes as they were set.
func (s *state) restore(st saved) {
	for _, sr := range st.Routes {
		r, ok := s.byKey[sr.Key]
		if !ok {
			continue
		}
		r.active, r.Since, r.Train = true, sr.Since, sr.Train
		r.visited = make([]bool, len(r.Legs))
		if len(sr.Visited) == len(r.Legs) && sr.Released <= len(r.Legs) {
			copy(r.visited, sr.Visited)
			r.Released = sr.Released
		}
		s.changed[r.ID] = true
	}
	s.update()
}
