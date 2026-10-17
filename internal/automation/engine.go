package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/openlcb"
)

// Entities is the entity manager.
type Entities interface {
	Get(id string) (entity.Entity, bool)
	List() []entity.Entity
	Command(ctx context.Context, id, command string) (entity.Entity, error)
}

// Bus sends events from Interlock's node.
type Bus interface {
	SendEvent(ctx context.Context, id openlcb.EventID) error
}

// Nodes is the registry; it may be nil.
type Nodes interface {
	Get(id openlcb.NodeID) (registry.Info, bool)
	List() []registry.Info
}

// Store keeps traces; it may be nil.
type Store interface {
	SaveTrace(ctx context.Context, rec store.TraceRecord) error
	Trace(ctx context.Context, id string) (*store.TraceRecord, error)
	Traces(ctx context.Context, automationID string, limit int) ([]store.TraceRecord, error)
	LatestTraces(ctx context.Context) ([]store.TraceRecord, error)
	PruneTraces(ctx context.Context, automationID string, keep int) error
	DeleteTraces(ctx context.Context, automationID string) error
}

// Options configure the engine.
type Options struct {
	Entities Entities
	Bus      Bus
	Nodes    Nodes
	Store    Store
	// File is automations.yaml; nil keeps automations in memory.
	File   *File
	Logger *slog.Logger
	// KeepTraces is how many traces to keep per automation; DefaultKeepTraces when zero.
	KeepTraces int
}

// DefaultKeepTraces is how many traces are kept per automation.
const DefaultKeepTraces = 20

// Status is an automation with what the engine knows about it.
type Status struct {
	Automation
	Running       int       `json:"running"`
	Queued        int       `json:"queued,omitempty"`
	LastTriggered time.Time `json:"last_triggered,omitzero"`
	LastResult    string    `json:"last_result,omitempty"`
	LastTrace     string    `json:"last_trace,omitempty"`
	// Problems are things that keep the automation from working as
	// written, such as an entity that does not exist.
	Problems []string `json:"problems,omitempty"`
	// Removed is set on the last update about a deleted automation.
	Removed bool `json:"removed,omitempty"`
}

// UnmarshalJSON reads both the automation and the status fields, which
// the embedded Automation's UnmarshalJSON would otherwise hide.
func (s *Status) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &s.Automation); err != nil {
		return err
	}
	var rest struct {
		Running       int       `json:"running"`
		Queued        int       `json:"queued"`
		LastTriggered time.Time `json:"last_triggered"`
		LastResult    string    `json:"last_result"`
		LastTrace     string    `json:"last_trace"`
		Problems      []string  `json:"problems"`
		Removed       bool      `json:"removed"`
	}
	if err := json.Unmarshal(data, &rest); err != nil {
		return err
	}
	s.Running, s.Queued, s.LastTriggered = rest.Running, rest.Queued, rest.LastTriggered
	s.LastResult, s.LastTrace, s.Problems, s.Removed = rest.LastResult, rest.LastTrace, rest.Problems, rest.Removed
	return nil
}

// auto is an automation with its runtime bookkeeping; e.mu guards it.
type auto struct {
	doc        Automation
	running    map[string]*run
	queue      []TriggerInfo
	last       time.Time
	lastResult string
	lastTrace  string
	problems   []string
}

// signal is something that may trigger an automation.
type signal struct {
	kind   signalKind
	entity entity.Entity
	event  openlcb.EventID
	node   registry.Info
}

type signalKind int

const (
	sigEntity signalKind = iota
	sigEvent
	sigNode
	sigStart
	sigReload
)

// Engine runs the automations.
type Engine struct {
	opts Options
	log  *slog.Logger
	keep int

	mu        sync.RWMutex
	autos     map[string]*auto
	waiters   map[int]*waiter
	nextWait  int
	subs      map[int]func(Status)
	traceSubs map[int]func(Trace)
	nextSub   int

	inbox chan signal
	wg    sync.WaitGroup

	// Dispatcher state, touched only by Run's goroutine.
	states map[string]entity.Entity
	online map[openlcb.NodeID]bool
	timed  []*timed
	holds  []*hold
}

// New builds an engine over opts and loads the automations from the
// file. Call Run to start it.
func New(opts Options) *Engine {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	e := &Engine{
		opts:      opts,
		log:       opts.Logger,
		keep:      opts.KeepTraces,
		autos:     make(map[string]*auto),
		waiters:   make(map[int]*waiter),
		subs:      make(map[int]func(Status)),
		traceSubs: make(map[int]func(Trace)),
		inbox:     make(chan signal, 1024),
		states:    make(map[string]entity.Entity),
		online:    make(map[openlcb.NodeID]bool),
	}
	if e.keep <= 0 {
		e.keep = DefaultKeepTraces
	}
	for _, doc := range opts.File.List() {
		e.autos[doc.ID] = &auto{doc: doc, running: make(map[string]*run)}
	}
	return e
}

// --- feeding the engine -------------------------------------------------

// HandleEntity follows the entity manager. It never blocks, since the
// manager may call it from the node's goroutine.
func (e *Engine) HandleEntity(ent entity.Entity) {
	e.push(signal{kind: sigEntity, entity: ent})
}

// HandleMessage watches the bus for event reports.
func (e *Engine) HandleMessage(msg *openlcb.Message) {
	if msg.MTI != openlcb.MTIEventReport {
		return
	}
	ev, ok := msg.EventID()
	if !ok {
		return
	}
	e.push(signal{kind: sigEvent, event: ev})
}

// HandleNode follows the registry for nodes coming and going.
func (e *Engine) HandleNode(info registry.Info) {
	if info.Local {
		return
	}
	e.push(signal{kind: sigNode, node: info})
}

func (e *Engine) push(sig signal) {
	select {
	case e.inbox <- sig:
	default:
		e.log.Warn("automation engine fell behind; a signal was dropped", "kind", sig.kind)
	}
}

// Run dispatches triggers until ctx ends, then cancels the runs still
// going and waits for them.
func (e *Engine) Run(ctx context.Context) error {
	e.prime()
	e.reschedule(time.Now())
	e.dispatch(signal{kind: sigStart})
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		now := time.Now()
		next := e.nextDue()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if !next.IsZero() {
			timer.Reset(max(next.Sub(now), 0))
		}
		select {
		case <-ctx.Done():
			e.cancelAll("Interlock is shutting down")
			e.wg.Wait()
			return nil
		case sig := <-e.inbox:
			e.dispatch(sig)
		case <-timer.C:
			e.fireDue(time.Now())
		}
	}
}

// prime takes the first look at entities and nodes so later changes are
// transitions, and restores last results from the stored traces.
func (e *Engine) prime() {
	for _, ent := range e.opts.Entities.List() {
		e.states[ent.ID] = ent
	}
	if e.opts.Nodes != nil {
		for _, info := range e.opts.Nodes.List() {
			if !info.Local {
				e.online[info.ID] = info.Online
			}
		}
	}
	e.mu.Lock()
	for _, a := range e.autos {
		a.problems = e.problemsLocked(a.doc)
	}
	e.mu.Unlock()
	if e.opts.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	recs, err := e.opts.Store.LatestTraces(ctx)
	if err != nil {
		e.log.Warn("could not read the last automation traces", "error", err)
		return
	}
	e.mu.Lock()
	for _, rec := range recs {
		if a, ok := e.autos[rec.AutomationID]; ok {
			a.last, a.lastResult, a.lastTrace = rec.StartedAt, rec.Result, rec.ID
			if rec.Result == ResultRunning { // cut short by a restart
				a.lastResult = ResultCanceled
			}
		}
	}
	e.mu.Unlock()
}

// --- the dispatcher -----------------------------------------------------

// fire is a trigger that matched, to be acted on once the lock is free.
type fire struct {
	owner ownerRef
	idx   int
	info  TriggerInfo
}

// ownerRef names who owns a trigger: an automation or a wait action.
type ownerRef struct {
	auto   string
	waiter int
}

// timed is a time or interval trigger and when it next fires.
type timed struct {
	auto string
	idx  int
	at   time.Time
}

// hold is a state trigger with "for" waiting out its time.
type hold struct {
	owner  ownerRef
	idx    int
	entity string
	state  string
	at     time.Time
	info   TriggerInfo
}

func (e *Engine) dispatch(sig signal) {
	now := time.Now()
	var fires []fire
	switch sig.kind {
	case sigReload:
		e.reschedule(now)
		return
	case sigStart:
		e.mu.RLock()
		for id, a := range e.autos {
			if !a.doc.Enabled {
				continue
			}
			for i, t := range a.doc.Triggers {
				if t.Type == TriggerStart {
					fires = append(fires, fire{ownerRef{auto: id}, i, TriggerInfo{Index: i, ID: t.ID, Type: t.Type, Description: "Interlock started", At: now}})
				}
			}
		}
		e.mu.RUnlock()
	case sigEntity:
		fires = e.dispatchEntity(sig.entity, now)
	case sigEvent:
		fires = e.matchAll(now, func(t Trigger) (TriggerInfo, bool) {
			if t.Type != TriggerEvent || t.Event != sig.event {
				return TriggerInfo{}, false
			}
			return TriggerInfo{Type: t.Type, Description: "Event " + sig.event.String() + " was received", Event: sig.event}, true
		})
	case sigNode:
		fires = e.dispatchNode(sig.node, now)
	}
	e.act(fires, now)
}

// matchAll runs match over every trigger of every enabled automation and
// every waiter, under the read lock, and returns the matches.
func (e *Engine) matchAll(now time.Time, match func(Trigger) (TriggerInfo, bool)) []fire {
	var fires []fire
	e.mu.RLock()
	defer e.mu.RUnlock()
	for id, a := range e.autos {
		if !a.doc.Enabled {
			continue
		}
		for i, t := range a.doc.Triggers {
			if info, ok := match(t); ok {
				info.Index, info.ID, info.At = i, t.ID, now
				fires = append(fires, fire{ownerRef{auto: id}, i, info})
			}
		}
	}
	for id, w := range e.waiters {
		for i, t := range w.triggers {
			if info, ok := match(t); ok {
				info.Index, info.ID, info.At = i, t.ID, now
				fires = append(fires, fire{ownerRef{waiter: id}, i, info})
			}
		}
	}
	return fires
}

func (e *Engine) dispatchEntity(ent entity.Entity, now time.Time) []fire {
	prev, known := e.states[ent.ID]
	if ent.Removed {
		delete(e.states, ent.ID)
	} else {
		e.states[ent.ID] = ent
	}
	e.refreshProblems(ent.ID)
	if !known || ent.Removed {
		return nil
	}
	stateChanged := prev.State != ent.State
	if stateChanged {
		// A state that did not hold long enough cancels its holds.
		e.holds = slices.DeleteFunc(e.holds, func(h *hold) bool { return h.entity == ent.ID && h.state != ent.State })
	}
	entered, left := locomotiveDiff(prev.Locomotives, ent.Locomotives)
	if !stateChanged && len(entered) == 0 && len(left) == 0 {
		return nil
	}
	return e.matchAll(now, func(t Trigger) (TriggerInfo, bool) {
		if t.Entity != ent.ID {
			return TriggerInfo{}, false
		}
		switch t.Type {
		case TriggerState:
			if !stateChanged || (t.From != "" && t.From != prev.State) || (t.To != "" && t.To != ent.State) {
				return TriggerInfo{}, false
			}
			desc := ent.Name + " became " + ent.State
			if t.For > 0 {
				desc += " and stayed so for " + t.For.String()
			}
			return TriggerInfo{Type: t.Type, Description: desc, Entity: ent.ID, EntityName: ent.Name, From: prev.State, To: ent.State}, true
		case TriggerLocomotive:
			var moved []entity.Locomotive
			var verb string
			if t.Direction != "left" {
				moved, verb = entered, " entered "
			}
			if t.Direction != "entered" && len(moved) == 0 {
				moved, verb = left, " left "
			}
			for _, loco := range moved {
				if t.Address != 0 && (loco.Address.Number != t.Address || loco.Address.Type.String() != addressTypeOr(t.AddressType)) {
					continue
				}
				return TriggerInfo{Type: t.Type, Description: "Locomotive " + loco.Name + verb + ent.Name, Entity: ent.ID, EntityName: ent.Name, Locomotive: loco.Name}, true
			}
		}
		return TriggerInfo{}, false
	})
}

func addressTypeOr(s string) string {
	if s == "" {
		return "long"
	}
	return s
}

// locomotiveDiff says which locomotives appeared and which went away.
func locomotiveDiff(before, after []entity.Locomotive) (entered, left []entity.Locomotive) {
	for _, l := range after {
		if !slices.ContainsFunc(before, func(b entity.Locomotive) bool { return b.Address == l.Address }) {
			entered = append(entered, l)
		}
	}
	for _, l := range before {
		if !slices.ContainsFunc(after, func(a entity.Locomotive) bool { return a.Address == l.Address }) {
			left = append(left, l)
		}
	}
	return entered, left
}

func (e *Engine) dispatchNode(info registry.Info, now time.Time) []fire {
	if info.Forgotten {
		delete(e.online, info.ID)
		return nil
	}
	was, known := e.online[info.ID]
	e.online[info.ID] = info.Online
	if !known || was == info.Online {
		return nil
	}
	to := "offline"
	verb := " went offline"
	if info.Online {
		to, verb = "online", " came online"
	}
	return e.matchAll(now, func(t Trigger) (TriggerInfo, bool) {
		if t.Type != TriggerNode || (t.Node != 0 && t.Node != info.ID) || (t.To != "" && t.To != to) {
			return TriggerInfo{}, false
		}
		return TriggerInfo{Type: t.Type, Description: info.Name() + verb, Node: info.ID, NodeName: info.Name(), To: to}, true
	})
}

// act starts runs and wakes waiters for the matches, or schedules holds
// for triggers with a "for".
func (e *Engine) act(fires []fire, now time.Time) {
	for _, f := range fires {
		t, ok := e.triggerOf(f.owner, f.idx)
		if !ok {
			continue
		}
		if t.Type == TriggerState && t.For > 0 {
			already := slices.ContainsFunc(e.holds, func(h *hold) bool { return h.owner == f.owner && h.idx == f.idx && h.entity == f.info.Entity })
			if !already {
				e.holds = append(e.holds, &hold{owner: f.owner, idx: f.idx, entity: f.info.Entity, state: f.info.To, at: now.Add(t.For.Duration()), info: f.info})
			}
			continue
		}
		e.deliver(f)
	}
}

func (e *Engine) triggerOf(owner ownerRef, idx int) (Trigger, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if owner.waiter != 0 {
		w, ok := e.waiters[owner.waiter]
		if !ok || idx >= len(w.triggers) {
			return Trigger{}, false
		}
		return w.triggers[idx], true
	}
	a, ok := e.autos[owner.auto]
	if !ok || idx >= len(a.doc.Triggers) {
		return Trigger{}, false
	}
	return a.doc.Triggers[idx], true
}

// deliver hands a fired trigger to its owner.
func (e *Engine) deliver(f fire) {
	if f.owner.waiter != 0 {
		e.mu.RLock()
		w, ok := e.waiters[f.owner.waiter]
		e.mu.RUnlock()
		if ok {
			select {
			case w.ch <- f.info:
			default: // already woken
			}
		}
		return
	}
	e.start(f.owner.auto, f.info, false)
}

// nextDue is the earliest scheduled time, or zero when nothing is.
func (e *Engine) nextDue() time.Time {
	var next time.Time
	for _, t := range e.timed {
		if next.IsZero() || t.at.Before(next) {
			next = t.at
		}
	}
	for _, h := range e.holds {
		if next.IsZero() || h.at.Before(next) {
			next = h.at
		}
	}
	return next
}

// fireDue fires everything scheduled at or before now.
func (e *Engine) fireDue(now time.Time) {
	var fires []fire
	for _, t := range e.timed {
		if t.at.After(now) {
			continue
		}
		trig, ok := e.triggerOf(ownerRef{auto: t.auto}, t.idx)
		if !ok {
			continue
		}
		info := TriggerInfo{Index: t.idx, ID: trig.ID, Type: trig.Type, At: now}
		if trig.Type == TriggerTime {
			info.Description = "The time was " + trig.At
			t.at = nextAt(now, mustClock(trig.At), trig.Weekdays)
		} else {
			info.Description = trig.Every.String() + " passed"
			t.at = t.at.Add(trig.Every.Duration())
			if !t.at.After(now) {
				t.at = now.Add(trig.Every.Duration())
			}
		}
		fires = append(fires, fire{ownerRef{auto: t.auto}, t.idx, info})
	}
	kept := e.holds[:0]
	for _, h := range e.holds {
		if h.at.After(now) {
			kept = append(kept, h)
			continue
		}
		if cur, ok := e.states[h.entity]; ok && cur.State == h.state {
			info := h.info
			info.At = now
			e.deliver(fire{h.owner, h.idx, info})
		}
	}
	e.holds = kept
	for _, f := range fires {
		e.deliver(f)
	}
}

func mustClock(s string) clock {
	c, _ := parseClock(s)
	return c
}

// reschedule rebuilds the time and interval schedule from the enabled
// automations. Intervals restart from now.
func (e *Engine) reschedule(now time.Time) {
	e.timed = e.timed[:0]
	e.mu.RLock()
	defer e.mu.RUnlock()
	for id, a := range e.autos {
		if !a.doc.Enabled {
			continue
		}
		for i, t := range a.doc.Triggers {
			switch t.Type {
			case TriggerTime:
				e.timed = append(e.timed, &timed{auto: id, idx: i, at: nextAt(now, mustClock(t.At), t.Weekdays)})
			case TriggerInterval:
				e.timed = append(e.timed, &timed{auto: id, idx: i, at: now.Add(t.Every.Duration())})
			}
		}
	}
	// Holds of automations that were disabled or changed are dropped.
	e.holds = slices.DeleteFunc(e.holds, func(h *hold) bool {
		if h.owner.waiter != 0 {
			return false
		}
		a, ok := e.autos[h.owner.auto]
		return !ok || !a.doc.Enabled || h.idx >= len(a.doc.Triggers)
	})
}

// --- problems -------------------------------------------------------------

// refreshProblems recomputes the problems of automations that mention an
// entity and publishes those that changed.
func (e *Engine) refreshProblems(entityID string) {
	var changed []Status
	e.mu.Lock()
	for _, a := range e.autos {
		if !mentions(a.doc, entityID) {
			continue
		}
		problems := e.problemsLocked(a.doc)
		if !slices.Equal(problems, a.problems) {
			a.problems = problems
			changed = append(changed, e.statusLocked(a))
		}
	}
	e.mu.Unlock()
	for _, s := range changed {
		e.publish(s)
	}
}

func mentions(doc Automation, entityID string) bool {
	found := false
	walk(doc, func(t *Trigger, c *Condition, a *Action) {
		switch {
		case t != nil && t.Entity == entityID, c != nil && c.Entity == entityID, a != nil && a.Entity == entityID:
			found = true
		}
	})
	return found
}

// walk visits every trigger, condition and action in a document.
func walk(doc Automation, visit func(*Trigger, *Condition, *Action)) {
	for i := range doc.Triggers {
		visit(&doc.Triggers[i], nil, nil)
	}
	walkConditions(doc.Conditions, visit)
	walkActions(doc.Actions, visit)
}

func walkConditions(conds []Condition, visit func(*Trigger, *Condition, *Action)) {
	for i := range conds {
		visit(nil, &conds[i], nil)
		walkConditions(conds[i].Conditions, visit)
	}
}

func walkActions(actions []Action, visit func(*Trigger, *Condition, *Action)) {
	for i := range actions {
		a := &actions[i]
		visit(nil, nil, a)
		for j := range a.Triggers {
			visit(&a.Triggers[j], nil, nil)
		}
		walkConditions(a.Conditions, visit)
		walkConditions(a.While, visit)
		walkConditions(a.Until, visit)
		walkActions(a.Then, visit)
		walkActions(a.Else, visit)
		for _, ch := range a.Choices {
			walkConditions(ch.Conditions, visit)
			walkActions(ch.Actions, visit)
		}
		walkActions(a.Default, visit)
		walkActions(a.Actions, visit)
	}
}

// problemsLocked lists what keeps an automation from working: entities
// that do not exist, commands an entity does not have, states a kind
// never has.
func (e *Engine) problemsLocked(doc Automation) []string {
	var problems []string
	seen := make(map[string]bool)
	note := func(s string) {
		if !seen[s] {
			seen[s] = true
			problems = append(problems, s)
		}
	}
	lookup := func(id string) (entity.Entity, bool) {
		ent, ok := e.opts.Entities.Get(id)
		if !ok {
			note("There is no entity " + id)
		}
		return ent, ok
	}
	checkState := func(ent entity.Entity, state string) {
		if state == "" {
			return
		}
		def, ok := entity.Lookup(ent.Kind)
		if ok && len(def.States) > 0 && !slices.Contains(def.States, state) {
			note(fmt.Sprintf("%s is never %q; a %s is %s", ent.Name, state, def.Label, strings.Join(def.States, ", ")))
		}
	}
	walk(doc, func(t *Trigger, c *Condition, a *Action) {
		switch {
		case t != nil && t.Entity != "":
			if ent, ok := lookup(t.Entity); ok {
				checkState(ent, t.From)
				checkState(ent, t.To)
				if t.Type == TriggerLocomotive && ent.Kind != entity.KindReporter {
					note(ent.Name + " is not a reporter, so it never sees locomotives")
				}
			}
		case c != nil && c.Entity != "":
			if ent, ok := lookup(c.Entity); ok {
				checkState(ent, c.State)
			}
		case a != nil && a.Entity != "":
			if ent, ok := lookup(a.Entity); ok && !slices.Contains(ent.Commands, a.Command) {
				if len(ent.Commands) == 0 {
					note(ent.Name + " takes no commands")
				} else {
					note(fmt.Sprintf("%s has no %q command; it takes %s", ent.Name, a.Command, strings.Join(ent.Commands, ", ")))
				}
			}
		}
	})
	return problems
}

// --- starting and finishing runs ---------------------------------------------

// start begins a run of an automation for a trigger, honoring its mode.
func (e *Engine) start(id string, info TriggerInfo, skipConditions bool) *run {
	e.mu.Lock()
	a, ok := e.autos[id]
	if !ok {
		e.mu.Unlock()
		return nil
	}
	var cancel []*run
	switch a.doc.Mode {
	case ModeRestart:
		for _, r := range a.running {
			cancel = append(cancel, r)
		}
	case ModeQueued:
		if len(a.running) > 0 {
			if len(a.queue) < a.doc.MaxRuns() {
				a.queue = append(a.queue, info)
				s := e.statusLocked(a)
				e.mu.Unlock()
				e.publish(s)
				return nil
			}
			e.mu.Unlock()
			e.skip(id, info, "the queue is full")
			return nil
		}
	case ModeParallel:
		if len(a.running) >= a.doc.MaxRuns() {
			e.mu.Unlock()
			e.skip(id, info, fmt.Sprintf("%d runs are already going", len(a.running)))
			return nil
		}
	default: // single
		if len(a.running) > 0 {
			e.mu.Unlock()
			e.skip(id, info, "it is already running")
			return nil
		}
	}
	r := newRun(e, a.doc, info, skipConditions)
	a.running[r.trace.ID] = r
	a.last = r.trace.Started
	a.lastResult = ResultRunning
	a.lastTrace = r.trace.ID
	s := e.statusLocked(a)
	e.mu.Unlock()
	for _, old := range cancel {
		old.cancelWith("Started again")
	}
	e.publish(s)
	e.publishTrace(r.snapshot())
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		r.execute()
		e.finish(r)
	}()
	return r
}

// skip records a trace for a trigger that could not start a run.
func (e *Engine) skip(id string, info TriggerInfo, why string) {
	e.mu.RLock()
	a, ok := e.autos[id]
	var name string
	if ok {
		name = a.doc.Name
	}
	e.mu.RUnlock()
	if !ok {
		return
	}
	now := time.Now()
	tr := Trace{ID: newTraceID(), Automation: id, Name: name, Started: now, Finished: now, Result: ResultSkipped,
		Error: "Skipped because " + why, Trigger: info, Steps: []Step{}}
	e.log.Info("automation skipped", "automation", id, "reason", why)
	e.mu.Lock()
	if a, ok := e.autos[id]; ok {
		a.lastResult, a.lastTrace = ResultSkipped, tr.ID
	}
	s := e.statusLocked(a)
	e.mu.Unlock()
	e.save(tr)
	e.publish(s)
	e.publishTrace(tr)
}

// finish records a run's end and starts the next queued one.
func (e *Engine) finish(r *run) {
	tr := r.snapshot()
	e.save(tr)
	e.mu.Lock()
	a, ok := e.autos[tr.Automation]
	var next *TriggerInfo
	var s Status
	if ok {
		delete(a.running, tr.ID)
		if a.lastTrace == tr.ID {
			a.lastResult = tr.Result
		}
		if len(a.queue) > 0 && len(a.running) == 0 {
			info := a.queue[0]
			a.queue = a.queue[1:]
			next = &info
		}
		s = e.statusLocked(a)
	}
	e.mu.Unlock()
	e.publishTrace(tr)
	if ok {
		e.publish(s)
	}
	if next != nil {
		e.start(tr.Automation, *next, false)
	}
}

// save stores a finished trace and prunes old ones.
func (e *Engine) save(tr Trace) {
	if e.opts.Store == nil {
		return
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		e.log.Error("could not encode a trace", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rec := store.TraceRecord{ID: tr.ID, AutomationID: tr.Automation, StartedAt: tr.Started, FinishedAt: tr.Finished, Result: tr.Result, JSON: raw}
	if err := e.opts.Store.SaveTrace(ctx, rec); err != nil {
		e.log.Error("could not save a trace", "automation", tr.Automation, "error", err)
		return
	}
	if err := e.opts.Store.PruneTraces(ctx, tr.Automation, e.keep); err != nil {
		e.log.Warn("could not prune traces", "automation", tr.Automation, "error", err)
	}
}

// cancelAll stops every run.
func (e *Engine) cancelAll(why string) {
	e.mu.RLock()
	var runs []*run
	for _, a := range e.autos {
		for _, r := range a.running {
			runs = append(runs, r)
		}
	}
	e.mu.RUnlock()
	for _, r := range runs {
		r.cancelWith(why)
	}
}

// --- waiters ---------------------------------------------------------------

// waiter is a wait action listening for triggers.
type waiter struct {
	triggers []Trigger
	ch       chan TriggerInfo
}

func (e *Engine) addWaiter(triggers []Trigger) (int, *waiter) {
	w := &waiter{triggers: triggers, ch: make(chan TriggerInfo, 1)}
	e.mu.Lock()
	e.nextWait++
	id := e.nextWait
	e.waiters[id] = w
	e.mu.Unlock()
	return id, w
}

func (e *Engine) removeWaiter(id int) {
	e.mu.Lock()
	delete(e.waiters, id)
	e.mu.Unlock()
}

// --- names for descriptions ----------------------------------------------

func (e *Engine) names() names {
	return names{
		entity: func(id string) string {
			if ent, ok := e.opts.Entities.Get(id); ok {
				return ent.Name
			}
			return ""
		},
		node: func(id openlcb.NodeID) string {
			if e.opts.Nodes == nil {
				return ""
			}
			if info, ok := e.opts.Nodes.Get(id); ok {
				return info.Name()
			}
			return ""
		},
	}
}

// --- the API's view ----------------------------------------------------------

func (e *Engine) statusLocked(a *auto) Status {
	return Status{
		Automation: a.doc, Running: len(a.running), Queued: len(a.queue),
		LastTriggered: a.last, LastResult: a.lastResult, LastTrace: a.lastTrace,
		Problems: slices.Clone(a.problems),
	}
}

// List returns every automation by name.
func (e *Engine) List() []Status {
	e.mu.RLock()
	out := make([]Status, 0, len(e.autos))
	for _, a := range e.autos {
		out = append(out, e.statusLocked(a))
	}
	e.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one automation.
func (e *Engine) Get(id string) (Status, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.autos[id]
	if !ok {
		return Status{}, false
	}
	return e.statusLocked(a), true
}

// Create adds an automation. An ID left empty is made from the name and
// kept unique; an ID given must be free.
func (e *Engine) Create(doc Automation) (Status, error) {
	derived := strings.TrimSpace(doc.ID) == ""
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Status{}, err
	}
	e.mu.Lock()
	if _, exists := e.autos[doc.ID]; exists {
		if !derived {
			e.mu.Unlock()
			return Status{}, ErrExists
		}
		base := doc.ID
		for n := 2; ; n++ {
			doc.ID = fmt.Sprintf("%s_%d", base, n)
			if _, taken := e.autos[doc.ID]; !taken {
				break
			}
		}
	}
	if err := e.opts.File.Put(doc); err != nil {
		e.mu.Unlock()
		return Status{}, err
	}
	a := &auto{doc: doc, running: make(map[string]*run), problems: e.problemsLocked(doc)}
	e.autos[doc.ID] = a
	s := e.statusLocked(a)
	e.mu.Unlock()
	e.publish(s)
	e.push(signal{kind: sigReload})
	return s, nil
}

// Update replaces an automation. Runs going are left to finish unless
// the automation was disabled.
func (e *Engine) Update(id string, doc Automation) (Status, error) {
	doc.ID = id
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return Status{}, err
	}
	e.mu.Lock()
	a, ok := e.autos[id]
	if !ok {
		e.mu.Unlock()
		return Status{}, ErrNotFound
	}
	if err := e.opts.File.Put(doc); err != nil {
		e.mu.Unlock()
		return Status{}, err
	}
	a.doc = doc
	a.problems = e.problemsLocked(doc)
	var cancel []*run
	if !doc.Enabled {
		for _, r := range a.running {
			cancel = append(cancel, r)
		}
		a.queue = nil
	}
	s := e.statusLocked(a)
	e.mu.Unlock()
	for _, r := range cancel {
		r.cancelWith("The automation was turned off")
	}
	e.publish(s)
	e.push(signal{kind: sigReload})
	return s, nil
}

// SetEnabled turns an automation on or off.
func (e *Engine) SetEnabled(id string, enabled bool) (Status, error) {
	e.mu.RLock()
	a, ok := e.autos[id]
	var doc Automation
	if ok {
		doc = a.doc
	}
	e.mu.RUnlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	doc.Enabled = enabled
	return e.Update(id, doc)
}

// Delete removes an automation and its traces, stopping any run.
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	a, ok := e.autos[id]
	if !ok {
		e.mu.Unlock()
		return ErrNotFound
	}
	if err := e.opts.File.Remove(id); err != nil {
		e.mu.Unlock()
		return err
	}
	delete(e.autos, id)
	var cancel []*run
	for _, r := range a.running {
		cancel = append(cancel, r)
	}
	s := e.statusLocked(a)
	e.mu.Unlock()
	for _, r := range cancel {
		r.cancelWith("The automation was deleted")
	}
	if e.opts.Store != nil {
		ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := e.opts.Store.DeleteTraces(ctx, id); err != nil {
			e.log.Warn("could not delete traces", "automation", id, "error", err)
		}
	}
	s.Removed = true
	e.publish(s)
	e.push(signal{kind: sigReload})
	return nil
}

// Trigger runs an automation by hand, whether or not it is enabled, and
// returns the trace as it starts. The conditions are skipped unless
// checkConditions is set.
func (e *Engine) Trigger(id string, checkConditions bool) (Trace, error) {
	e.mu.RLock()
	_, ok := e.autos[id]
	e.mu.RUnlock()
	if !ok {
		return Trace{}, ErrNotFound
	}
	info := TriggerInfo{Index: -1, Type: "manual", Description: "Run by hand", At: time.Now()}
	r := e.start(id, info, !checkConditions)
	if r == nil {
		e.mu.RLock()
		a := e.autos[id]
		tr := Trace{ID: a.lastTrace, Automation: id, Result: a.lastResult}
		e.mu.RUnlock()
		return tr, nil
	}
	return r.snapshot(), nil
}

// Stop cancels an automation's runs and empties its queue.
func (e *Engine) Stop(id string) error {
	e.mu.Lock()
	a, ok := e.autos[id]
	if !ok {
		e.mu.Unlock()
		return ErrNotFound
	}
	var cancel []*run
	for _, r := range a.running {
		cancel = append(cancel, r)
	}
	a.queue = nil
	e.mu.Unlock()
	if len(cancel) == 0 {
		return ErrNotRunning
	}
	for _, r := range cancel {
		r.cancelWith("Stopped by hand")
	}
	return nil
}

// Traces returns an automation's traces, newest first: the runs going
// now, then the stored ones, at most limit in all (all when zero).
func (e *Engine) Traces(ctx context.Context, id string, limit int) ([]Trace, error) {
	e.mu.RLock()
	a, ok := e.autos[id]
	var out []Trace
	if ok {
		for _, r := range a.running {
			out = append(out, r.snapshot())
		}
	}
	e.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	if e.opts.Store != nil {
		recs, err := e.opts.Store.Traces(ctx, id, limit)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			var tr Trace
			if err := json.Unmarshal(rec.JSON, &tr); err != nil {
				e.log.Warn("skipping an unreadable trace", "trace", rec.ID, "error", err)
				continue
			}
			out = append(out, tr)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []Trace{}
	}
	return out, nil
}

// Trace returns one trace of an automation.
func (e *Engine) Trace(ctx context.Context, id, traceID string) (Trace, error) {
	e.mu.RLock()
	a, ok := e.autos[id]
	var r *run
	if ok {
		r = a.running[traceID]
	}
	e.mu.RUnlock()
	if !ok {
		return Trace{}, ErrNotFound
	}
	if r != nil {
		return r.snapshot(), nil
	}
	if e.opts.Store == nil {
		return Trace{}, ErrNotFound
	}
	rec, err := e.opts.Store.Trace(ctx, traceID)
	if err != nil {
		return Trace{}, err
	}
	if rec == nil || rec.AutomationID != id {
		return Trace{}, ErrNotFound
	}
	var tr Trace
	if err := json.Unmarshal(rec.JSON, &tr); err != nil {
		return Trace{}, fmt.Errorf("stored trace %s: %w", traceID, err)
	}
	return tr, nil
}

// Subscribe is told whenever an automation's status changes.
func (e *Engine) Subscribe(fn func(Status)) (unsubscribe func()) {
	e.mu.Lock()
	id := e.nextSub
	e.nextSub++
	e.subs[id] = fn
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
	}
}

// SubscribeTraces is told whenever a trace starts, takes a step or ends.
func (e *Engine) SubscribeTraces(fn func(Trace)) (unsubscribe func()) {
	e.mu.Lock()
	id := e.nextSub
	e.nextSub++
	e.traceSubs[id] = fn
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		delete(e.traceSubs, id)
		e.mu.Unlock()
	}
}

func (e *Engine) publish(s Status) {
	e.mu.RLock()
	subs := make([]func(Status), 0, len(e.subs))
	for _, fn := range e.subs {
		subs = append(subs, fn)
	}
	e.mu.RUnlock()
	for _, fn := range subs {
		fn(s)
	}
}

func (e *Engine) publishTrace(tr Trace) {
	e.mu.RLock()
	subs := make([]func(Trace), 0, len(e.traceSubs))
	for _, fn := range e.traceSubs {
		subs = append(subs, fn)
	}
	e.mu.RUnlock()
	for _, fn := range subs {
		fn(tr)
	}
}
