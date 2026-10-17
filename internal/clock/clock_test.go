package clock_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	iclock "github.com/mikesmitty/interlock/internal/clock"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/clock"
)

type fakeBus struct {
	mu     sync.Mutex
	events []openlcb.EventID
	msgs   []openlcb.Message
}

func (b *fakeBus) ID() openlcb.NodeID { return 0x0A0000000001 }
func (b *fakeBus) SendEvent(_ context.Context, id openlcb.EventID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, id)
	return nil
}

func (b *fakeBus) Send(_ context.Context, msg openlcb.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, msg)
	return nil
}

// kinds lists the clock events sent so far as "time 06:01" style words.
func (b *fakeBus) kinds() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, id := range b.events {
		ev, ok := clock.Parse(id)
		if !ok {
			out = append(out, id.String())
			continue
		}
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(ev.String(), "clock reports "), "clock "))
	}
	return out
}

func (b *fakeBus) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = nil
}

type fakeStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *fakeStore) Setting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *fakeStore) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := &fakeBus{}
	store := &fakeStore{m: map[string]string{}}
	s := iclock.New(ctx, iclock.Options{Bus: bus, Store: store, Logger: slog.New(slog.DiscardHandler), SyncDelay: 20 * time.Millisecond})
	st := s.State()
	if st.Time != "06:00" || st.Rate != 4 || st.Running || st.Clock != clock.DefaultFastClock {
		t.Fatalf("fresh clock = %+v", st)
	}
	var minutes []string
	var mu sync.Mutex
	s.OnMinute(func(st iclock.State) { mu.Lock(); minutes = append(minutes, st.Time); mu.Unlock() })
	go func() { _ = s.Run(ctx) }()

	// Run announces its range and broadcasts its state.
	eventually(t, "startup broadcast", func() bool { return len(bus.kinds()) >= 5 })
	if got := bus.kinds(); strings.Join(got, ",") != "stop,rate 4×,year "+st.Date[:4]+",date "+st.Date[5:7]+"-"+st.Date[8:]+",time 06:00" {
		t.Errorf("startup events = %v", got)
	}
	bus.mu.Lock()
	ranges := len(bus.msgs)
	bus.mu.Unlock()
	if ranges != 2 {
		t.Errorf("announced %d range messages, want producer and consumer", ranges)
	}

	// Set the time and date and start it fast enough to see minutes pass.
	bus.reset()
	tm, date, rate, running := "23:58", "1957-12-31", 240.0, true
	st, err := s.Apply(iclock.Change{Time: &tm, Date: &date, Rate: &rate, Running: &running})
	if err != nil || st.Time != "23:58" || st.Date != "1957-12-31" || !st.Running || st.Rate != 240 {
		t.Fatalf("Apply = %+v, %v", st, err)
	}
	if got := bus.kinds()[:5]; strings.Join(got, ",") != "rate 240×,time 23:58,year 1957,date 12-31,start" {
		t.Errorf("change events = %v", got)
	}
	// A fast minute every quarter second: midnight comes within a second.
	// The state's date turns over a moment before the minute tick sends
	// its events and runs the callbacks, so wait for those instead.
	eventually(t, "the midnight broadcast", func() bool { return strings.Contains(strings.Join(bus.kinds(), ","), "time 00:00") })
	if d := s.State().Date; d != "1958-01-01" {
		t.Errorf("date after midnight = %s", d)
	}
	kinds := strings.Join(bus.kinds(), ",")
	if !strings.Contains(kinds, "time 23:59,date rollover,year 1958,date 01-01,time 00:00") {
		t.Errorf("rollover events = %v", kinds)
	}
	callbacks := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(minutes, ",")
	}
	eventually(t, "the midnight callback", func() bool { return strings.Contains(callbacks(), "00:00") })
	if seen := callbacks(); !strings.Contains(seen, "23:59,00:00") {
		t.Errorf("minute callbacks = %v", seen)
	}
	// The broadcast after the change carries the whole state in order.
	if !strings.Contains(kinds, "start,rate 240×,year 195") {
		t.Errorf("no sync broadcast in %v", kinds)
	}

	// Another node sets the rate and stops the clock through events.
	bus.reset()
	s.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: clock.RateEvent(clock.DefaultFastClock, clock.RateOf(2), true).AsSlice()})
	eventually(t, "rate set from the bus", func() bool { return s.State().Rate == 2 })
	s.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: clock.StopEvent(clock.DefaultFastClock).AsSlice()})
	eventually(t, "stop from the bus", func() bool { return !s.State().Running })
	stopped := s.State()
	time.Sleep(30 * time.Millisecond)
	if again := s.State(); again.Time != stopped.Time || again.Second != stopped.Second {
		t.Errorf("a stopped clock moved from %s:%02d to %s:%02d", stopped.Time, stopped.Second, again.Time, again.Second)
	}
	// Events for another clock are ignored.
	s.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: clock.StartEvent(clock.DefaultRealTimeClock).AsSlice()})
	time.Sleep(20 * time.Millisecond)
	if s.State().Running {
		t.Error("a start for another clock started this one")
	}
	// A query brings a broadcast.
	bus.reset()
	s.HandleMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: clock.QueryEvent(clock.DefaultFastClock).AsSlice()})
	eventually(t, "query answer", func() bool { return len(bus.kinds()) >= 5 })
	if got := bus.kinds(); got[0] != "stop" || got[1] != "rate 2×" {
		t.Errorf("query answer = %v", got)
	}

	// Bad changes are refused with a reason.
	bad := "25:00"
	var invalid *iclock.InvalidError
	if _, err := s.Apply(iclock.Change{Time: &bad}); !errors.As(err, &invalid) {
		t.Errorf("Apply 25:00 = %v", err)
	}

	// The state survives a restart.
	cancel()
	time.Sleep(20 * time.Millisecond)
	again := iclock.New(context.Background(), iclock.Options{Bus: bus, Store: store, Logger: slog.New(slog.DiscardHandler)})
	if got := again.State(); got.Rate != 2 || got.Running || got.Date != "1958-01-01" {
		t.Errorf("restored clock = %+v", got)
	}
}
