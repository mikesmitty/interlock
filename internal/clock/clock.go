// Package clock runs Interlock's fast clock and broadcasts it on the bus
// with the Broadcast Time protocol: a time event every fast minute, date,
// year and rate events when they change, start and stop. Other nodes can
// set it with the protocol's set events, and the automations can use it
// in triggers and conditions. The clock's state is kept in the store so
// it comes back where it was.
package clock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/clock"
)

// Bus is what the clock needs from the local node.
type Bus interface {
	ID() openlcb.NodeID
	SendEvent(ctx context.Context, id openlcb.EventID) error
	Send(ctx context.Context, msg openlcb.Message) error
}

// Store keeps the clock between runs.
type Store interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Options configure a Server.
type Options struct {
	Bus    Bus
	Store  Store
	Logger *slog.Logger
	// Clock is which clock this is (default clock.DefaultFastClock).
	Clock openlcb.NodeID
	// SyncDelay is how long after a change the full set of events is
	// broadcast again (default 3 seconds, as OpenMRN does).
	SyncDelay time.Duration
	// Now tells the time; tests replace it.
	Now func() time.Time
}

// State is the clock as the API shows it. Time of day is at Updated; a
// running clock has moved on by the real time since, times Rate.
type State struct {
	Clock   openlcb.NodeID `json:"clock"`
	Running bool           `json:"running"`
	// Rate is the multiplier: 4 means a fast minute every 15 seconds.
	Rate float64 `json:"rate"`
	// Time is "HH:MM" and Date "YYYY-MM-DD" on the fast clock.
	Time    string `json:"time"`
	Date    string `json:"date"`
	Weekday string `json:"weekday"`
	Hour    int    `json:"hour"`
	Minute  int    `json:"minute"`
	Second  int    `json:"second"`
	// Updated is the real time this state was taken.
	Updated time.Time `json:"updated"`
}

// Fast returns the fast time as a time.Time (in UTC, since the fast clock
// has no zone).
func (s State) Fast() time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", fmt.Sprintf("%s %s:%02d", s.Date, s.Time, s.Second))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Change is what the API may set; nil fields are left alone.
type Change struct {
	Running *bool    `json:"running,omitempty"`
	Rate    *float64 `json:"rate,omitempty"`
	// Time is "HH:MM"; Date is "YYYY-MM-DD".
	Time *string `json:"time,omitempty"`
	Date *string `json:"date,omitempty"`
}

// InvalidError says a change made no sense.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "clock: " + e.Reason }

// Store keys.
const (
	keyFast    = "clock.fast"
	keyRate    = "clock.rate"
	keyRunning = "clock.running"
)

// Server is the fast clock.
type Server struct {
	opts Options
	log  *slog.Logger
	id   openlcb.NodeID

	mu      sync.Mutex
	fast    time.Time // fast time at mark (UTC)
	mark    time.Time // real time fast was pinned
	rate    clock.Rate
	running bool
	subs    map[int]func(State)
	minutes map[int]func(State)
	nextSub int

	wake chan struct{}
	sync chan struct{}
	ctx  context.Context
}

// New builds a clock, restoring its state from the store; missing state
// starts stopped at 06:00 today, running four times real time.
func New(ctx context.Context, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == 0 {
		opts.Clock = clock.DefaultFastClock
	}
	if opts.SyncDelay == 0 {
		opts.SyncDelay = 3 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Server{opts: opts, log: opts.Logger, id: opts.Clock, subs: make(map[int]func(State)), minutes: make(map[int]func(State)),
		wake: make(chan struct{}, 1), sync: make(chan struct{}, 1), ctx: context.Background()}
	now := opts.Now()
	s.mark = now
	s.fast = time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, time.UTC)
	s.rate = clock.RateOf(4)
	s.restore(ctx)
	return s
}

func (s *Server) restore(ctx context.Context) {
	if s.opts.Store == nil {
		return
	}
	if v, ok, err := s.opts.Store.Setting(ctx, keyFast); err == nil && ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			s.fast = t.UTC()
		}
	}
	if v, ok, err := s.opts.Store.Setting(ctx, keyRate); err == nil && ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			s.rate = clock.RateOf(f)
		}
	}
	if v, ok, err := s.opts.Store.Setting(ctx, keyRunning); err == nil && ok {
		s.running = v == "true"
	}
}

func (s *Server) persist() {
	if s.opts.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.mu.Lock()
	fast, rate, running := s.fast, s.rate, s.running
	s.mu.Unlock()
	err := errors.Join(
		s.opts.Store.SetSetting(ctx, keyFast, fast.Format(time.RFC3339)),
		s.opts.Store.SetSetting(ctx, keyRate, strconv.FormatFloat(rate.Float(), 'f', -1, 64)),
		s.opts.Store.SetSetting(ctx, keyRunning, strconv.FormatBool(running)),
	)
	if err != nil {
		s.log.Warn("could not save the clock", "error", err)
	}
}

// ID returns the clock's ID.
func (s *Server) ID() openlcb.NodeID { return s.id }

// nowLocked returns the fast time at real time t; the caller holds mu.
func (s *Server) nowLocked(t time.Time) time.Time {
	if !s.running {
		return s.fast
	}
	elapsed := t.Sub(s.mark)
	return s.fast.Add(time.Duration(float64(elapsed) * s.rate.Float()))
}

// Now returns the fast time, in UTC.
func (s *Server) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nowLocked(s.opts.Now())
}

// State snapshots the clock.
func (s *Server) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(s.opts.Now())
}

func (s *Server) stateLocked(at time.Time) State {
	t := s.nowLocked(at)
	return State{
		Clock: s.id, Running: s.running, Rate: s.rate.Float(),
		Time: t.Format("15:04"), Date: t.Format("2006-01-02"), Weekday: t.Weekday().String(),
		Hour: t.Hour(), Minute: t.Minute(), Second: t.Second(), Updated: at,
	}
}

// pinLocked fixes the fast time at real time t; the caller holds mu.
func (s *Server) pinLocked(t time.Time) {
	s.fast = s.nowLocked(t)
	s.mark = t
}

// Subscribe registers fn for every change, including each fast minute.
func (s *Server) Subscribe(fn func(State)) (unsubscribe func()) {
	return s.subscribe(s.subs, fn)
}

// OnMinute registers fn for each fast minute the clock reaches while it
// runs and for jumps made by setting it, which is what time triggers need.
func (s *Server) OnMinute(fn func(State)) (unsubscribe func()) {
	return s.subscribe(s.minutes, fn)
}

func (s *Server) subscribe(m map[int]func(State), fn func(State)) func() {
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	m[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(m, id)
		s.mu.Unlock()
	}
}

func (s *Server) publish(st State, minute bool) {
	s.mu.Lock()
	fns := make([]func(State), 0, len(s.subs)+len(s.minutes))
	for _, fn := range s.subs {
		fns = append(fns, fn)
	}
	if minute {
		for _, fn := range s.minutes {
			fns = append(fns, fn)
		}
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// Apply makes the changes in c, broadcasting what changed. It returns the
// new state.
func (s *Server) Apply(c Change) (State, error) {
	var events []openlcb.EventID
	now := s.opts.Now()
	s.mu.Lock()
	s.pinLocked(now)
	jumped := false
	if c.Rate != nil {
		if *c.Rate < clock.MinRate.Float() || *c.Rate > clock.MaxRate.Float() {
			s.mu.Unlock()
			return State{}, &InvalidError{fmt.Sprintf("the rate must be between %g and %g", clock.MinRate.Float(), clock.MaxRate.Float())}
		}
		s.rate = clock.RateOf(*c.Rate)
		events = append(events, clock.RateEvent(s.id, s.rate, false))
	}
	if c.Time != nil {
		t, err := time.Parse("15:04", *c.Time)
		if err != nil {
			s.mu.Unlock()
			return State{}, &InvalidError{fmt.Sprintf("%q is not a time such as 07:30", *c.Time)}
		}
		f := s.fast
		s.fast = time.Date(f.Year(), f.Month(), f.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
		events = append(events, clock.TimeEvent(s.id, t.Hour(), t.Minute(), false))
		jumped = true
	}
	if c.Date != nil {
		d, err := time.Parse("2006-01-02", *c.Date)
		if err != nil || d.Year() > 4095 {
			s.mu.Unlock()
			return State{}, &InvalidError{fmt.Sprintf("%q is not a date such as 1957-06-21", *c.Date)}
		}
		f := s.fast
		s.fast = time.Date(d.Year(), d.Month(), d.Day(), f.Hour(), f.Minute(), f.Second(), 0, time.UTC)
		events = append(events, clock.YearEvent(s.id, d.Year(), false), clock.DateEvent(s.id, int(d.Month()), d.Day(), false))
		jumped = true
	}
	if c.Running != nil && *c.Running != s.running {
		s.running = *c.Running
		if s.running {
			events = append(events, clock.StartEvent(s.id))
		} else {
			events = append(events, clock.StopEvent(s.id))
		}
	}
	st := s.stateLocked(now)
	s.mu.Unlock()
	s.log.Info("clock changed", "time", st.Time, "date", st.Date, "rate", st.Rate, "running", st.Running)
	s.persist()
	s.send(events...)
	s.publish(st, jumped)
	s.requestSync()
	s.poke()
	return st, nil
}

// HandleMessage follows the bus: set, start, stop and query events for
// this clock, and identification requests for its range.
func (s *Server) HandleMessage(msg *openlcb.Message) {
	switch msg.MTI {
	case openlcb.MTIEventReport:
		id, ok := msg.EventID()
		if !ok || !clock.IsClock(s.id, id) {
			return
		}
		ev, ok := clock.Parse(id)
		if !ok {
			return
		}
		go s.command(ev)
	case openlcb.MTIIdentifyEventsGlobal, openlcb.MTIIdentifyEventsAddressed:
		go s.announce()
	case openlcb.MTIIdentifyProducer, openlcb.MTIIdentifyConsumer:
		id, ok := msg.EventID()
		if ok && clock.IsClock(s.id, id) {
			go s.announce()
		}
	case openlcb.MTIConsumerRangeIdentified:
		id, ok := msg.EventID()
		if ok && openlcb.ParseEventRange(id).Contains(s.id.Event(0)) {
			// A clock client appeared; tell it the time.
			s.requestSync()
		}
	}
}

// command applies an event another node sent.
func (s *Server) command(ev clock.Event) {
	switch {
	case ev.Kind == clock.KindQuery:
		s.requestSync()
	case ev.Kind == clock.KindStart:
		running := true
		_, _ = s.Apply(Change{Running: &running})
	case ev.Kind == clock.KindStop:
		running := false
		_, _ = s.Apply(Change{Running: &running})
	case ev.Set && ev.Kind == clock.KindTime:
		t := fmt.Sprintf("%02d:%02d", ev.Hour, ev.Minute)
		_, _ = s.Apply(Change{Time: &t})
	case ev.Set && ev.Kind == clock.KindDate:
		f := s.Now()
		d := fmt.Sprintf("%04d-%02d-%02d", f.Year(), ev.Month, ev.Day)
		_, _ = s.Apply(Change{Date: &d})
	case ev.Set && ev.Kind == clock.KindYear:
		f := s.Now()
		d := fmt.Sprintf("%04d-%02d-%02d", ev.Year, int(f.Month()), f.Day())
		_, _ = s.Apply(Change{Date: &d})
	case ev.Set && ev.Kind == clock.KindRate:
		r := ev.Rate.Float()
		_, _ = s.Apply(Change{Rate: &r})
	}
}

// announce tells the bus this node produces and consumes the clock's
// whole event range.
func (s *Server) announce() {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	id := clock.Range(s.id).Encode().AsSlice()
	for _, mti := range []openlcb.MTI{openlcb.MTIProducerRangeIdentified, openlcb.MTIConsumerRangeIdentified} {
		if err := s.opts.Bus.Send(ctx, openlcb.Message{MTI: mti, Payload: id}); err != nil {
			s.log.Debug("clock announce failed", "error", err)
			return
		}
	}
}

// send broadcasts events in order.
func (s *Server) send(events ...openlcb.EventID) {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	for _, ev := range events {
		if err := s.opts.Bus.SendEvent(ctx, ev); err != nil {
			s.log.Debug("clock event not sent", "event", ev, "error", err)
			return
		}
	}
}

// broadcast sends the whole state: running or stopped, rate, year, date
// and time, the order OpenMRN uses.
func (s *Server) broadcast() {
	s.mu.Lock()
	t := s.nowLocked(s.opts.Now())
	running, rate := s.running, s.rate
	s.mu.Unlock()
	state := clock.StopEvent(s.id)
	if running {
		state = clock.StartEvent(s.id)
	}
	s.send(state, clock.RateEvent(s.id, rate, false), clock.YearEvent(s.id, t.Year(), false),
		clock.DateEvent(s.id, int(t.Month()), t.Day(), false), clock.TimeEvent(s.id, t.Hour(), t.Minute(), false))
}

// requestSync asks the run loop for a broadcast after SyncDelay, so a
// flurry of changes makes one.
func (s *Server) requestSync() {
	select {
	case s.sync <- struct{}{}:
	default:
	}
}

func (s *Server) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run ticks the clock until ctx ends: a time event every fast minute,
// with the date rollover when a day turns.
func (s *Server) Run(ctx context.Context) error {
	s.ctx = ctx
	s.announce()
	s.broadcast()
	tick := time.NewTimer(time.Hour)
	defer tick.Stop()
	var syncTimer <-chan time.Time
	for {
		delay := s.untilNextMinute()
		if !tick.Stop() {
			select {
			case <-tick.C:
			default:
			}
		}
		if delay >= 0 {
			tick.Reset(delay)
		}
		select {
		case <-ctx.Done():
			s.persist()
			return nil
		case <-s.wake:
		case <-s.sync:
			if syncTimer == nil {
				syncTimer = time.After(s.opts.SyncDelay)
			}
		case <-syncTimer:
			syncTimer = nil
			s.broadcast()
		case <-tick.C:
			s.minute()
		}
	}
}

// untilNextMinute is the real time until the fast clock reaches its next
// whole minute, or -1 when it is not moving.
func (s *Server) untilNextMinute() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.rate == 0 {
		return -1
	}
	now := s.opts.Now()
	t := s.nowLocked(now)
	var fastLeft time.Duration
	if s.rate > 0 {
		fastLeft = t.Truncate(time.Minute).Add(time.Minute).Sub(t)
	} else {
		fastLeft = t.Sub(t.Truncate(time.Minute))
		if fastLeft == 0 {
			fastLeft = time.Minute
		}
	}
	wall := time.Duration(float64(fastLeft) / abs(s.rate.Float()))
	return max(wall, time.Millisecond)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// minute runs when the fast clock reaches a whole minute.
func (s *Server) minute() {
	now := s.opts.Now()
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	before := s.fast
	s.pinLocked(now)
	// Land exactly on the minute the timer aimed at.
	s.fast = s.fast.Round(time.Minute)
	t := s.fast
	st := s.stateLocked(now)
	s.mu.Unlock()

	var events []openlcb.EventID
	if t.YearDay() != before.YearDay() || t.Year() != before.Year() {
		events = append(events, clock.DateRolloverEvent(s.id))
		if t.Year() != before.Year() {
			events = append(events, clock.YearEvent(s.id, t.Year(), false))
		}
		events = append(events, clock.DateEvent(s.id, int(t.Month()), t.Day(), false))
	}
	events = append(events, clock.TimeEvent(s.id, t.Hour(), t.Minute(), false))
	s.send(events...)
	s.persist()
	s.publish(st, true)
}
