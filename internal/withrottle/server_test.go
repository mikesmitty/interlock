package withrottle_test

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/clock"
	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/internal/withrottle"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

const (
	localID   = openlcb.NodeID(0x0A0000000001)
	stationID = openlcb.NodeID(0x0A0000000077)
)

type bench struct {
	station  *sim.Station
	trains   *train.Manager
	turnouts *turnouts
	server   *withrottle.Server
	ctx      context.Context
}

// newBench runs a train manager over a simulated command station with a
// locomotive at 1234, one roster entry for it and one turnout, and a
// server for them.
func newBench(t *testing.T, opts withrottle.Options) *bench {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	b := &bench{}
	local := node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); b.station.HandleFrame(f) }})
	var err error
	b.station, err = sim.NewStation(node.Config{ID: stationID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); local.HandleFrame(f) }},
		sim.StationOptions{Decoders: []*sim.Decoder{sim.NewDecoder(1234, true)}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := train.OpenFile(filepath.Join(t.TempDir(), "trains.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The trains follow the bus from the start, so they hear the station
	// say its power is off.
	b.trains = train.New(train.Options{Bus: local, File: f, Logger: logger, SearchWindow: 150 * time.Millisecond, Functions: 5})
	local.Observe(func(ev node.Event) {
		if ev.Kind == node.EventMessage {
			b.trains.HandleMessage(ev.Message)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b.ctx = ctx
	go func() { _ = local.Run(ctx) }()
	go func() { _ = b.station.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for local.State() != node.StateInitialized || b.station.State() != node.StateInitialized || !b.trains.Layout().PowerKnown {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := b.trains.Create(train.Entry{Name: "Challenger", Address: 1234,
		Functions: map[int]string{0: "Headlight", 1: "Bell", 2: "Horn"}, Momentary: []int{2}}); err != nil {
		t.Fatal(err)
	}
	b.turnouts = &turnouts{ents: []entity.Entity{
		{ID: "t1", Kind: entity.KindTurnout, Name: "Yard lead", State: entity.StateClosed, Available: true},
		{ID: "t2", Kind: entity.KindTurnout, Name: "Guess", State: entity.StateClosed, Suggested: true},
	}}
	opts.Listen = "127.0.0.1:0"
	opts.Trains = b.trains
	opts.Turnouts = b.turnouts
	opts.Logger = logger
	b.server = withrottle.New(opts)
	if err := b.server.Listen(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = b.server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return b
}

// app is a throttle app on the other end of a connection.
type app struct {
	t     *testing.T
	conn  net.Conn
	lines chan string
}

func (b *bench) dial(t *testing.T) *app {
	t.Helper()
	conn, err := net.Dial("tcp", b.server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	a := &app{t: t, conn: conn, lines: make(chan string, 1000)}
	go func() {
		defer close(a.lines)
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			if line := sc.Text(); line != "" {
				a.lines <- line
			}
		}
	}()
	return a
}

func (a *app) send(line string) {
	a.t.Helper()
	// Some apps end lines with CR LF and send a blank one first.
	if _, err := a.conn.Write([]byte("\r\n" + line + "\r\n")); err != nil {
		a.t.Fatal(err)
	}
}

// expect reads the next lines, which must be want in order.
func (a *app) expect(want ...string) {
	a.t.Helper()
	for _, w := range want {
		select {
		case got, ok := <-a.lines:
			if !ok {
				a.t.Fatalf("connection closed waiting for %q", w)
			}
			if got != w {
				a.t.Fatalf("got %q, want %q", got, w)
			}
		case <-time.After(3 * time.Second):
			a.t.Fatalf("timed out waiting for %q", w)
		}
	}
}

// until reads lines up to and including want, returning those before it.
func (a *app) until(want string) []string {
	a.t.Helper()
	var skipped []string
	for {
		select {
		case got, ok := <-a.lines:
			if !ok {
				a.t.Fatalf("connection closed waiting for %q", want)
			}
			if got == want {
				return skipped
			}
			skipped = append(skipped, got)
		case <-time.After(3 * time.Second):
			a.t.Fatalf("timed out waiting for %q after %q", want, skipped)
		}
	}
}

// quiet checks that nothing arrives for a while.
func (a *app) quiet() {
	a.t.Helper()
	select {
	case got := <-a.lines:
		a.t.Fatalf("unexpected %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

// closed waits for the server to end the connection.
func (a *app) closed() {
	a.t.Helper()
	for {
		select {
		case _, ok := <-a.lines:
			if !ok {
				return
			}
		case <-time.After(3 * time.Second):
			a.t.Fatal("the connection stayed open")
		}
	}
}

// greeted reads the greeting and introduces the app.
func (a *app) greeted(device string) {
	a.t.Helper()
	a.until("*10")
	a.send("NTest phone")
	a.send("HU" + device)
	a.expect("*10")
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// added is what an app is sent when it takes the Challenger, as it stands
// after its first read back.
func added(th string, labels bool, speed string) []string {
	out := []string{"M" + th + "+L1234<;>"}
	if labels {
		out = append(out, "M"+th+"LL1234<;>]\\[Headlight]\\[Bell]\\[Horn"+strings.Repeat("]\\[", 26))
	}
	for fn := range 29 {
		out = append(out, "M"+th+"AL1234<;>F0"+strconv.Itoa(fn))
	}
	return append(out, "M"+th+"AL1234<;>V"+speed, "M"+th+"AL1234<;>R1", "M"+th+"AL1234<;>s1")
}

func TestGreeting(t *testing.T) {
	b := newBench(t, withrottle.Options{Version: "1.2.3", WebPort: 8080})
	a := b.dial(t)
	a.expect(
		"VN2.0",
		"HTInterlock",
		"HtInterlock 1.2.3",
		"RL1]\\[Challenger}|{1234}|{L",
		"PPA0",
		"PTT]\\[Turnouts}|{Turnout]\\[Closed}|{2]\\[Thrown}|{4]\\[Unknown}|{1]\\[Inconsistent}|{8",
		"PTL]\\[t1}|{Yard lead}|{2",
		"PW8080",
		"*10",
	)
	// The name is answered every time, since apps send it to be answered.
	a.send("NTest phone")
	a.expect("*10")
	a.send("NTest phone")
	a.expect("*10")
	// A new roster entry reaches the app.
	if _, err := b.trains.Create(train.Entry{Name: "Big Boy", Address: 4014}); err != nil {
		t.Fatal(err)
	}
	a.until("RL2]\\[Big Boy}|{4014}|{L]\\[Challenger}|{1234}|{L")
}

func TestDrive(t *testing.T) {
	b := newBench(t, withrottle.Options{})
	a := b.dial(t)
	a.greeted("1")
	a.send("MT+L1234<;>EChallenger")
	a.expect(added("T", true, "0")...)
	if tr, _ := b.trains.Get("challenger"); !tr.Controlled {
		t.Fatalf("not controlled after the add: %+v", tr)
	}

	// Speed goes to the train and does not come back to the sender.
	a.send("MTA*<;>V50")
	eventually(t, "decoder speed", func() bool { return b.station.DecoderState(1234, true).Speed.Value == 50 })
	a.quiet()

	// A second app on the same locomotive sees the first one's changes.
	p := b.dial(t)
	p.greeted("2")
	p.send("M0+L1234<;>L1234")
	p.expect(added("0", true, "50")...)
	a.send("MTA*<;>V60")
	p.expect("M0AL1234<;>V60")
	a.send("MTAL1234<;>R0")
	a.expect("MTAL1234<;>R0")
	p.expect("M0AL1234<;>R0")
	eventually(t, "reverse", func() bool { return b.station.DecoderState(1234, true).Speed.Reverse })

	// F1 latches: each press changes it, releases do nothing.
	a.send("MTAL1234<;>F11")
	a.expect("MTAL1234<;>F11")
	p.expect("M0AL1234<;>F11")
	a.send("MTAL1234<;>F01")
	a.send("MTAL1234<;>F11")
	a.expect("MTAL1234<;>F01")
	p.expect("M0AL1234<;>F01")
	a.send("MTAL1234<;>F01")
	// F2 is momentary in the roster: on while held.
	a.send("MTAL1234<;>F12")
	a.expect("MTAL1234<;>F12")
	a.send("MTAL1234<;>F02")
	a.expect("MTAL1234<;>F02")
	p.expect("M0AL1234<;>F12", "M0AL1234<;>F02")
	// Forced states and the momentary flag.
	a.send("MTAL1234<;>m11")
	a.send("MTAL1234<;>f13")
	a.expect("MTAL1234<;>F13")
	p.expect("M0AL1234<;>F13")
	a.send("MTAL1234<;>F11")
	a.expect("MTAL1234<;>F11")
	a.send("MTAL1234<;>F01")
	a.expect("MTAL1234<;>F01")
	p.expect("M0AL1234<;>F11", "M0AL1234<;>F01")

	// Queries answer from what the app was told.
	a.send("MTA*<;>qV")
	a.expect("MTAL1234<;>V60")
	a.send("MTA*<;>qR")
	a.expect("MTAL1234<;>R0")

	// An emergency stop shows on the other app.
	a.send("MTA*<;>X")
	p.expect("M0AL1234<;>V0")
	if tr, _ := b.trains.Get("1234"); !tr.EStop {
		t.Fatalf("not stopped: %+v", tr)
	}

	// Interlock keeps the train until the last app lets it go.
	a.send("MT-L1234<;>r")
	a.expect("MT-L1234<;>")
	if tr, _ := b.trains.Get("1234"); !tr.Controlled {
		t.Fatal("released while another app held it")
	}
	a.send("MTA*<;>V10")
	a.quiet()
	p.send("M0-*<;>r")
	p.expect("M0-L1234<;>")
	eventually(t, "release", func() bool { tr, _ := b.trains.Get("1234"); return !tr.Controlled })
}

func TestAddress(t *testing.T) {
	b := newBench(t, withrottle.Options{})
	a := b.dial(t)
	a.greeted("1")
	// A locomotive outside the roster: no labels, and F2 is momentary.
	a.send("MT+S3<;>S3")
	a.until("MTAS3<;>s1")
	a.send("MTAS3<;>F12")
	a.expect("MTAS3<;>F12")
	a.send("MTAS3<;>F02")
	a.expect("MTAS3<;>F02")
	// Adding it again repeats its state.
	a.send("MT+S3<;>S3")
	if lines := a.until("MTAS3<;>s1"); lines[0] != "MT+S3<;>" {
		t.Errorf("again = %q", lines)
	}
	// Addresses the protocol does not have.
	a.send("MT+S200<;>S200")
	a.expect("HMS200 is not a locomotive address")
	// Lines that make no sense are ignored.
	for _, line := range []string{"M", "MT", "MTA", "MTAS3<;>", "MTAS3<;>Vx", "MTAS3<;>F1x", "PTA", "PPA7", "X"} {
		a.send(line)
	}
	a.send("NTest phone")
	a.expect("*10")
	// Throttle 6 only collects labels, so the train is not taken.
	a.send("M6+L1234<;>EChallenger")
	a.until("M6AL1234<;>s1")
	if tr, _ := b.trains.Get("challenger"); tr.Controlled {
		t.Fatal("taken for its labels")
	}
	a.send("M6-L1234<;>r")
	a.expect("M6-L1234<;>")
}

func TestLayout(t *testing.T) {
	b := newBench(t, withrottle.Options{})
	a := b.dial(t)
	a.greeted("1")
	p := b.dial(t)
	p.greeted("2")

	a.send("PTATt1")
	a.expect("PTA4t1")
	p.expect("PTA4t1")
	a.send("PTA2t1")
	a.expect("PTA2t1")
	p.expect("PTA2t1")
	a.send("PTACt1")
	a.quiet()
	a.send("PTATt2")
	a.expect("HMThere is no turnout t2")
	if got := b.turnouts.sent(); strings.Join(got, ",") != "t1 throw,t1 close,t1 close" {
		t.Errorf("commands = %q", got)
	}
	// A turnout confirmed in Interlock joins the list.
	b.turnouts.change("t2", func(e *entity.Entity) { e.Suggested = false })
	a.expect("PTL]\\[t1}|{Yard lead}|{2]\\[t2}|{Guess}|{2")
	p.expect("PTL]\\[t1}|{Yard lead}|{2]\\[t2}|{Guess}|{2")

	a.send("PPA1")
	a.expect("PPA1")
	p.expect("PPA1")
	a.send("PPA0")
	a.expect("PPA0")
	p.expect("PPA0")
}

func TestHeartbeat(t *testing.T) {
	b := newBench(t, withrottle.Options{Heartbeat: 200 * time.Millisecond})
	a := b.dial(t)
	a.until("*1")
	a.send("MT+L1234<;>L1234")
	a.until("MTAL1234<;>s1")
	a.send("MTA*<;>V30")
	a.send("*+")
	eventually(t, "speed", func() bool { return b.station.DecoderState(1234, true).Speed.Value == 30 })
	// Silence stops the train, then closes the connection.
	eventually(t, "the stop", func() bool { tr, _ := b.trains.Get("1234"); return tr.EStop })
	a.closed()
	eventually(t, "release", func() bool { tr, _ := b.trains.Get("1234"); return !tr.Controlled })
}

func TestReconnect(t *testing.T) {
	b := newBench(t, withrottle.Options{})
	a := b.dial(t)
	a.greeted("phone")
	a.send("*+")
	a.send("MT+L1234<;>L1234")
	a.until("MTAL1234<;>s1")
	a.send("MTA*<;>V20")
	eventually(t, "speed", func() bool { return b.station.DecoderState(1234, true).Speed.Value == 20 })

	// The phone comes back on a new connection: the old one closes and its
	// train keeps running.
	again := b.dial(t)
	again.greeted("phone")
	a.closed()
	again.send("MT+L1234<;>L1234")
	again.until("MTAL1234<;>V20")
	if tr, _ := b.trains.Get("1234"); tr.EStop || tr.Speed != 20 {
		t.Fatalf("stopped by the reconnect: %+v", tr)
	}

	// Leaving with Q lets the train go without stopping it.
	again.send("Q")
	again.closed()
	eventually(t, "release", func() bool { tr, _ := b.trains.Get("1234"); return !tr.Controlled })
	if tr, _ := b.trains.Get("1234"); tr.EStop || tr.Speed != 20 {
		t.Fatalf("stopped on leaving: %+v", tr)
	}
}

func TestClock(t *testing.T) {
	st := clock.State{Running: true, Rate: 4, Date: "2026-10-18", Time: "06:30", Second: 15}
	b := newBench(t, withrottle.Options{Clock: fixedClock{st}})
	a := b.dial(t)
	// The fast clock's hours read as UTC, which is how the apps show them.
	want := time.Date(2026, 10, 18, 6, 30, 15, 0, time.UTC).Unix()
	a.until("PFT" + strconv.FormatInt(want, 10) + "<;>4.0")
}

type fixedClock struct{ st clock.State }

func (c fixedClock) State() clock.State                     { return c.st }
func (c fixedClock) Subscribe(func(clock.State)) (u func()) { return func() {} }

// turnouts stands in for the entity manager.
type turnouts struct {
	mu       sync.Mutex
	ents     []entity.Entity
	subs     []entity.Subscriber
	commands []string
}

func (f *turnouts) List() []entity.Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]entity.Entity(nil), f.ents...)
}

func (f *turnouts) Command(_ context.Context, id, command string) (entity.Entity, error) {
	f.mu.Lock()
	f.commands = append(f.commands, id+" "+command)
	f.mu.Unlock()
	state := entity.StateThrown
	if command == "close" {
		state = entity.StateClosed
	}
	return f.change(id, func(e *entity.Entity) { e.State = state }), nil
}

func (f *turnouts) change(id string, change func(*entity.Entity)) entity.Entity {
	f.mu.Lock()
	var out entity.Entity
	for i := range f.ents {
		if f.ents[i].ID == id {
			change(&f.ents[i])
			out = f.ents[i]
		}
	}
	subs := append([]entity.Subscriber(nil), f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		sub(out)
	}
	return out
}

func (f *turnouts) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *turnouts) Subscribe(sub entity.Subscriber) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, sub)
	return func() {}
}
