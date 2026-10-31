// Package signals works out what the track plan's signals show and sets
// their heads, in the manner of the New York City subway.
//
// A signal stands in a block for trains running one way and governs the
// way out of the block ahead of it. The track there says what kind it
// is. With a junction ahead it is a home signal: it stays at stop until
// the interlocking has set a route past it, with the turnouts in place
// and nothing on the track, and goes back to stop when a train takes the
// route. With plain track ahead it is an automatic signal, which needs no
// route and stops only for what is ahead. Past a signal that doesn't say
// stop the train is told about the signal after it: clear if that one
// doesn't say stop either, else approach, as it is where no signal
// follows.
//
// Signals show what the interlocking and tracking have decided; they
// don't stop trains. A head on a signal belongs to it: if anything else
// sets the head, it is put back.
//
// One goroutine owns the state; the Handle methods only queue what
// changed for it.
package signals

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
)

// The kinds of signal.
const (
	// KindHome is a signal with a junction ahead, which needs a route.
	KindHome = "home"
	// KindAutomatic is a signal with plain track ahead.
	KindAutomatic = "automatic"
)

// What a signal tells a train.
const (
	// Stop is stop and stay.
	Stop = "stop"
	// Approach is proceed, ready to stop at the next signal.
	Approach = "approach"
	// Clear is proceed; the next signal doesn't say stop.
	Clear = "clear"
)

// Signal is what a signal on the plan shows.
type Signal struct {
	// ID is the signal's ID in the plan.
	ID string `json:"id"`
	// Kind is home or automatic; empty for a signal with no block end
	// ahead of it to govern.
	Kind string `json:"kind,omitempty"`
	// Indication is stop, approach or clear.
	Indication string `json:"indication"`
	// Diverging is a home signal clearing a train off the straight way.
	Diverging bool `json:"diverging,omitempty"`
	// Route is the way on, by route ID, when it doesn't say stop.
	Route string `json:"route,omitempty"`
	// Upper and Lower are the aspects its heads show, Lower only on a
	// signal with a lower head to show it or a home signal.
	Upper string `json:"upper"`
	Lower string `json:"lower,omitempty"`
	// Reason says why it is at stop.
	Reason string `json:"reason,omitempty"`
}

// Update is a change to the signals: one signal, or when the plan has
// changed, all of them.
type Update struct {
	Signal  *Signal
	Signals []Signal
}

// Subscriber is told whenever a signal changes.
type Subscriber func(Update)

// Entities is the entity manager: the turnouts, blocks and signal heads,
// and the commands that set the heads.
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

// Interlocking is the routes.
type Interlocking interface {
	List() []interlocking.Route
}

// Options configure an Engine.
type Options struct {
	Entities     Entities
	Plan         Plan
	Tracking     Tracking
	Interlocking Interlocking
	Logger       *slog.Logger
}

// Engine keeps the signals up to date. Its Handle methods never block;
// they queue the change for the goroutine Run runs.
type Engine struct {
	opts  Options
	log   *slog.Logger
	inbox chan func(*state)

	mu      sync.RWMutex
	views   []Signal
	subs    map[int]Subscriber
	nextSub int
}

// state is what the engine knows. One goroutine owns it.
type state struct {
	world
	shown map[string]Signal
	// all is set when the plan changed, so every signal is published.
	all bool
	// failed are the aspects heads couldn't be set to, so that a head
	// that keeps refusing is reported once.
	failed map[string]string
}

// New makes an engine; Run starts it.
func New(opts Options) *Engine {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Engine{opts: opts, log: opts.Logger, inbox: make(chan func(*state), 4096), subs: map[int]Subscriber{}}
}

func (e *Engine) push(fn func(*state)) {
	select {
	case e.inbox <- fn:
	default:
		e.log.Warn("the signals fell behind; a change was dropped")
	}
}

// HandleEntity follows the turnouts, blocks and heads.
func (e *Engine) HandleEntity(ent entity.Entity) {
	switch ent.Kind {
	case entity.KindTurnout, entity.KindBlock, entity.KindSignalHead:
		e.push(func(s *state) { s.entity(ent) })
	}
}

// HandleTrain follows the trains tracking places.
func (e *Engine) HandleTrain(t tracking.Train) {
	e.push(func(s *state) { s.train(t) })
}

// HandlePlan follows the track plan as it is saved.
func (e *Engine) HandlePlan(st trackplan.State) {
	e.push(func(s *state) { s.setPlan(st) })
}

// HandleRoutes follows the interlocking's routes.
func (e *Engine) HandleRoutes(interlocking.Update) {
	e.push(func(*state) {})
}

func (s *state) entity(e entity.Entity) {
	if e.Removed {
		delete(s.entities, e.ID)
	} else {
		s.entities[e.ID] = e
	}
}

func (s *state) train(t tracking.Train) {
	if t.Removed {
		delete(s.trains, t.ID)
	} else {
		s.trains[t.ID] = t
	}
}

func (s *state) setPlan(st trackplan.State) {
	s.plan, s.net = st.Plan, st.Plan.Network()
	s.blocks = make(map[string]trackplan.Block, len(st.Plan.Blocks))
	for _, b := range st.Plan.Blocks {
		s.blocks[b.ID] = b
	}
	s.all = true
}

// List returns what every signal on the plan shows.
func (e *Engine) List() []Signal {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return slices.Clone(e.views)
}

// Subscribe registers a subscriber and returns a function that removes
// it.
func (e *Engine) Subscribe(sub Subscriber) (unsubscribe func()) {
	e.mu.Lock()
	id := e.nextSub
	e.nextSub++
	e.subs[id] = sub
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
	}
}

// Run keeps the signals up to date until ctx ends.
func (e *Engine) Run(ctx context.Context) error {
	s := &state{shown: map[string]Signal{}, failed: map[string]string{}}
	s.net = trackplan.Plan{}.Network()
	s.trains, s.entities = map[string]tracking.Train{}, map[string]entity.Entity{}
	if e.opts.Plan != nil {
		s.setPlan(e.opts.Plan.Get())
	}
	if e.opts.Entities != nil {
		for _, ent := range e.opts.Entities.List() {
			s.entity(ent)
		}
	}
	if e.opts.Tracking != nil {
		for _, t := range e.opts.Tracking.List() {
			s.train(t)
		}
	}
	e.update(ctx, s)
	for {
		select {
		case <-ctx.Done():
			return nil
		case fn := <-e.inbox:
			fn(s)
			// A burst of changes looks at the signals once.
			for more := true; more; {
				select {
				case fn := <-e.inbox:
					fn(s)
				default:
					more = false
				}
			}
			e.update(ctx, s)
		}
	}
}

// update looks at every signal, shares what changed and sets the heads.
func (e *Engine) update(ctx context.Context, s *state) {
	s.routes = nil
	if e.opts.Interlocking != nil {
		s.routes = e.opts.Interlocking.List()
	}
	views := s.evaluate()
	e.publish(s, views)
	for i, sg := range s.plan.Signals {
		e.setHead(ctx, s, sg.Upper, views[i].Upper)
		e.setHead(ctx, s, sg.Lower, views[i].Lower)
	}
}

func (e *Engine) publish(s *state, views []Signal) {
	var updates []Update
	shown := make(map[string]Signal, len(views))
	for i, v := range views {
		shown[v.ID] = v
		if was, ok := s.shown[v.ID]; !s.all && (!ok || was != v) {
			updates = append(updates, Update{Signal: &views[i]})
		}
	}
	if s.all {
		updates = []Update{{Signals: slices.Clone(views)}}
	}
	if len(updates) == 0 {
		return
	}
	s.all = false
	s.shown = shown
	e.mu.Lock()
	e.views = views
	subs := slices.Collect(maps.Values(e.subs))
	e.mu.Unlock()
	for _, u := range updates {
		for _, sub := range subs {
			sub(u)
		}
	}
}

// setHead sets a head to an aspect, or the nearest it has a lamp for. A
// head that is offline is set when it returns.
func (e *Engine) setHead(ctx context.Context, s *state, id, aspect string) {
	if id == "" || aspect == "" {
		return
	}
	head, ok := s.entities[id]
	if !ok || !head.Available {
		return
	}
	want := shownBy(head, aspect)
	if want == "" || head.State == want {
		delete(s.failed, id)
		return
	}
	updated, err := e.opts.Entities.Command(ctx, id, want)
	if err != nil {
		if s.failed[id] != want && ctx.Err() == nil {
			e.log.Warn("setting a signal head", "head", id, "aspect", want, "error", err)
			s.failed[id] = want
		}
		return
	}
	delete(s.failed, id)
	s.entities[id] = updated
}
