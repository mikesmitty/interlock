package sim_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

// inbox collects the messages Interlock's node receives.
type inbox struct {
	mu   sync.Mutex
	msgs []*openlcb.Message
}

func (b *inbox) observe(ev node.Event) {
	if ev.Kind != node.EventMessage {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, ev.Message)
}

// wait blocks until a message satisfies pred.
func (b *inbox) wait(t *testing.T, what string, pred func(*openlcb.Message) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b.mu.Lock()
		for _, m := range b.msgs {
			if pred(m) {
				b.mu.Unlock()
				return
			}
		}
		b.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *inbox) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = nil
}

func (b *inbox) count(pred func(*openlcb.Message) bool) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.msgs {
		if pred(m) {
			n++
		}
	}
	return n
}

func withEvent(mti openlcb.MTI, ev openlcb.EventID) func(*openlcb.Message) bool {
	return func(m *openlcb.Message) bool {
		id, ok := m.EventID()
		return m.MTI == mti && ok && id == ev
	}
}

func simOutpost(t *testing.T) (*node.Node, *memconfig.Client, *sim.Outpost, *inbox, context.Context) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	var local *node.Node
	var peer *sim.Outpost
	local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); peer.HandleFrame(f) }})
	var err error
	peer, err = sim.NewOutpost(node.Config{ID: peerID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		SNIP: openlcb.SNIP{UserName: "Yard"},
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); local.HandleFrame(f) }})
	if err != nil {
		t.Fatal(err)
	}
	box := &inbox{}
	local.Observe(box.observe)
	client := memconfig.NewClient(local, memconfig.WithLogger(logger), memconfig.WithReplyTimeout(500*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	go func() { _ = local.Run(ctx) }()
	go func() { _ = peer.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for local.State() != node.StateInitialized || peer.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return local, client, peer, box, ctx
}

func TestOutpostBehavior(t *testing.T) {
	local, client, peer, box, ctx := simOutpost(t)
	throw, closeEv := peerID.Event(0), peerID.Event(1)
	thrownFB, closedFB := peerID.Event(2), peerID.Event(3)
	occupied, clearEv := peerID.Event(0x20), peerID.Event(0x21)
	rng := detector.Range((peerID + 1).Event(0))

	// Power on: every servo goes to its default, closed, at once.
	box.wait(t, "closed feedback", withEvent(openlcb.MTIEventReport, closedFB))
	if peer.Turnout(0) != "closed" {
		t.Errorf("servo 1 after power on = %s", peer.Turnout(0))
	}

	// Identify Events lists everything with its state.
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyEventsAddressed, Dest: openlcb.NodeHandle{ID: peerID}}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "consumer identified valid close", withEvent(openlcb.MTIConsumerIdentifiedValid, closeEv))
	box.wait(t, "consumer identified invalid throw", withEvent(openlcb.MTIConsumerIdentifiedInvalid, throw))
	box.wait(t, "producer identified valid closed feedback", withEvent(openlcb.MTIProducerIdentifiedValid, closedFB))
	box.wait(t, "producer identified unknown occupied", withEvent(openlcb.MTIProducerIdentifiedUnknown, occupied))
	box.wait(t, "producer range identified", withEvent(openlcb.MTIProducerRangeIdentified, rng.Encode()))
	time.Sleep(50 * time.Millisecond)
	identified := box.count(func(m *openlcb.Message) bool { return m.MTI != openlcb.MTIEventReport })
	// Servo, track and reporter events, and the signal test's start and stop
	if identified != 4*5+4*2+4+2 {
		t.Errorf("identified messages = %d, want 34", identified)
	}

	// A throw with the default 3 s throw time: moving, then feedback.
	// Shorten the time first through the configuration space.
	if err := client.Write(ctx, peerID, memconfig.SpaceConfig, 178, []byte{1}); err != nil { // Throw Time, servo 1: 0.1 s
		t.Fatal(err)
	}
	box.clear()
	if err := local.SendEvent(ctx, throw); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for peer.Turnout(0) != "moving" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	box.wait(t, "thrown feedback", withEvent(openlcb.MTIEventReport, thrownFB))
	if peer.Turnout(0) != "thrown" {
		t.Errorf("servo 1 after throw = %s", peer.Turnout(0))
	}
	// Asked while thrown: throw is valid, closed feedback invalid.
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyConsumer, Payload: throw.AsSlice()}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "throw valid", withEvent(openlcb.MTIConsumerIdentifiedValid, throw))
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyProducer, Payload: closedFB.AsSlice()}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "closed feedback invalid", withEvent(openlcb.MTIProducerIdentifiedInvalid, closedFB))

	// A locomotive arrives on track 1: an entry report, then occupied.
	box.clear()
	loco := &sim.Locomotive{Address: detector.Address{Type: detector.Long, Number: 1234}, Facing: "east"}
	peer.SetTrack(0, loco)
	entry := detector.Report{Kind: detector.Entry, Address: loco.Address}.Event(rng)
	box.wait(t, "entry report", withEvent(openlcb.MTIEventReport, entry))
	box.wait(t, "occupied", withEvent(openlcb.MTIEventReport, occupied))
	// Asked about the range, the detector names the locomotive first.
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyProducer, Payload: rng.Base.AsSlice()}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "locomotive present", withEvent(openlcb.MTIProducerIdentifiedValid, entry))
	box.wait(t, "address 0 not here", withEvent(openlcb.MTIProducerIdentifiedValid, rng.Base))
	box.wait(t, "range identified again", withEvent(openlcb.MTIProducerRangeIdentified, rng.Encode()))
	// Placing it again changes nothing; another locomotive replaces it.
	box.clear()
	peer.SetTrack(0, loco)
	other := &sim.Locomotive{Address: detector.Address{Type: detector.Short, Number: 3}}
	peer.SetTrack(0, other)
	box.wait(t, "first locomotive exit", withEvent(openlcb.MTIEventReport, detector.Report{Kind: detector.Exit, Address: loco.Address}.Event(rng)))
	box.wait(t, "second locomotive entry", withEvent(openlcb.MTIEventReport, detector.Report{Kind: detector.Entry, Address: other.Address}.Event(rng)))
	time.Sleep(20 * time.Millisecond)
	if n := box.count(withEvent(openlcb.MTIEventReport, occupied)); n != 0 {
		t.Errorf("occupied sent again %d times while still occupied", n)
	}
	// With orientation reporting on, the entry says which way it faces.
	if err := client.Write(ctx, peerID, memconfig.SpaceConfig, 700, []byte{1}); err != nil { // Orientation, track 1
		t.Fatal(err)
	}
	box.clear()
	peer.SetTrack(0, loco)
	box.wait(t, "entry facing east", withEvent(openlcb.MTIEventReport, detector.Report{Kind: detector.EntryEast, Address: loco.Address}.Event(rng)))
	// It leaves: exit report, then clear.
	box.clear()
	peer.SetTrack(0, nil)
	box.wait(t, "exit report", withEvent(openlcb.MTIEventReport, detector.Report{Kind: detector.Exit, Address: loco.Address}.Event(rng)))
	box.wait(t, "clear", withEvent(openlcb.MTIEventReport, clearEv))
}

func TestOutpostEnableHeads(t *testing.T) {
	_, _, peer, _, _ := simOutpost(t)
	if err := peer.EnableHeads(2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for peer.Head(1) != "Red" || peer.Head(2) != "Dark" {
		if time.Now().After(deadline) {
			t.Fatalf("heads show %s and %s", peer.Head(1), peer.Head(2))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestOutpostSignalHeads(t *testing.T) {
	local, _, peer, box, ctx := simOutpost(t)
	darkEv, red, yellow, lunar := peerID.Event(0x100), peerID.Event(0x101), peerID.Event(0x102), peerID.Event(0x104)
	flashingRed := peerID.Event(0x108)
	second := peerID.Event(0x111)

	set := func(path string, value byte) {
		t.Helper()
		for _, v := range peer.Doc.Variables() {
			if strings.Join(v.Path, "/") == path {
				if err := peer.Config.Write(v.Address, []byte{value}); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("no variable %s", path)
	}
	identify := func() {
		t.Helper()
		box.clear()
		if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyEventsAddressed, Dest: openlcb.NodeHandle{ID: peerID}}); err != nil {
			t.Fatal(err)
		}
	}
	send := func(ev openlcb.EventID) {
		t.Helper()
		if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIEventReport, Payload: ev.AsSlice()}); err != nil {
			t.Fatal(err)
		}
	}
	waitHead := func(want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for peer.Head(0) != want {
			if time.Now().After(deadline) {
				t.Fatalf("head 1 = %s, want %s", peer.Head(0), want)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	// Heads start disabled: dark, deaf and off the bus.
	send(red)
	identify()
	box.wait(t, "the track events", withEvent(openlcb.MTIProducerIdentifiedUnknown, peerID.Event(0x27)))
	if n := box.count(withEvent(openlcb.MTIConsumerIdentifiedInvalid, red)); n != 0 {
		t.Error("a disabled head is on the bus")
	}
	if peer.Head(0) != "Dark" {
		t.Errorf("disabled head = %s", peer.Head(0))
	}

	// Enabled, it starts on its power-on aspect.
	set("Signal Heads/Head 1/Enabled", 1)
	waitHead("Red")

	// Identify gives the aspect it shows as valid, the others invalid, and
	// leaves out those it has no lamp for.
	identify()
	box.wait(t, "red valid", withEvent(openlcb.MTIConsumerIdentifiedValid, red))
	box.wait(t, "yellow invalid", withEvent(openlcb.MTIConsumerIdentifiedInvalid, yellow))
	box.wait(t, "dark invalid", withEvent(openlcb.MTIConsumerIdentifiedInvalid, darkEv))
	if n := box.count(withEvent(openlcb.MTIConsumerIdentifiedInvalid, lunar)); n != 0 {
		t.Error("the lunar event is on the bus with no lunar lamp")
	}
	if n := box.count(withEvent(openlcb.MTIConsumerIdentifiedInvalid, second)); n != 0 {
		t.Error("a disabled head's events are on the bus")
	}

	// Aspect events choose the aspect; one it can't show does nothing.
	send(yellow)
	waitHead("Yellow")
	send(lunar)
	send(flashingRed)
	waitHead("Flashing Red")

	// Asked about one event, it answers for that event.
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyConsumer, Payload: flashingRed.AsSlice()}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "flashing red valid", withEvent(openlcb.MTIConsumerIdentifiedValid, flashingRed))

	// A lamp moved onto an output brings its aspect onto the bus.
	set("Signal Heads/Head 1/Y Lamp", 4)
	deadline := time.Now().Add(2 * time.Second)
	for {
		send(lunar)
		if peer.Head(0) == "Lunar" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("head 1 after adding a lunar lamp = %s", peer.Head(0))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Disabling it puts it dark, and enabling it again starts it over.
	set("Signal Heads/Head 1/Enabled", 0)
	waitHead("Dark")
	set("Signal Heads/Head 1/Power-On Aspect", 3)
	set("Signal Heads/Head 1/Enabled", 1)
	waitHead("Green")
}

// setConfig writes one byte of the outpost's configuration by path.
func setConfig(t *testing.T, peer *sim.Outpost, path string, value byte) {
	t.Helper()
	for _, v := range peer.Doc.Variables() {
		if strings.Join(v.Path, "/") == path {
			if err := peer.Config.Write(v.Address, []byte{value}); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no variable %s", path)
}

func TestOutpostSignalTest(t *testing.T) {
	local, _, peer, box, ctx := simOutpost(t)
	start, stop := peerID.Event(0x200), peerID.Event(0x201)
	send := func(ev openlcb.EventID) {
		t.Helper()
		if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIEventReport, Payload: ev.AsSlice()}); err != nil {
			t.Fatal(err)
		}
	}
	ask := func(ev openlcb.EventID) {
		t.Helper()
		box.clear()
		if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyConsumer, Payload: ev.AsSlice()}); err != nil {
			t.Fatal(err)
		}
	}
	waitTest := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for peer.SignalTest() != want {
			if time.Now().After(deadline) {
				t.Fatalf("signal test running = %v, want %v", peer.SignalTest(), want)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	// On the bus with no head enabled, and not running.
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyEventsAddressed, Dest: openlcb.NodeHandle{ID: peerID}}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "start invalid", withEvent(openlcb.MTIConsumerIdentifiedInvalid, start))
	box.wait(t, "stop valid", withEvent(openlcb.MTIConsumerIdentifiedValid, stop))

	send(start)
	waitTest(true)
	ask(start)
	box.wait(t, "start valid while running", withEvent(openlcb.MTIConsumerIdentifiedValid, start))
	ask(stop)
	box.wait(t, "stop invalid while running", withEvent(openlcb.MTIConsumerIdentifiedInvalid, stop))

	send(stop)
	waitTest(false)
	ask(start)
	box.wait(t, "start invalid after", withEvent(openlcb.MTIConsumerIdentifiedInvalid, start))
}

// A servo output that carries the signal chain leaves its switch machine
// out of use: commands do nothing and its state is unknown.
func TestOutpostChainOnServo(t *testing.T) {
	local, _, peer, box, ctx := simOutpost(t)
	throw, closedFB := peerID.Event(0), peerID.Event(3)
	box.wait(t, "closed feedback at power on", withEvent(openlcb.MTIEventReport, closedFB))

	setConfig(t, peer, "Switch Machines/Servo 1/Throw Time", 0)
	setConfig(t, peer, "Signal Heads/Connected To", 1)
	deadline := time.Now().Add(2 * time.Second)
	for peer.Turnout(0) != "unknown" {
		if time.Now().After(deadline) {
			t.Fatalf("servo 1 with the chain on it = %s", peer.Turnout(0))
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := local.SendEvent(ctx, throw); err != nil {
		t.Fatal(err)
	}
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyConsumer, Payload: throw.AsSlice()}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "throw unknown", withEvent(openlcb.MTIConsumerIdentifiedUnknown, throw))
	if peer.Turnout(0) != "unknown" {
		t.Errorf("servo 1 moved with the chain on it: %s", peer.Turnout(0))
	}

	// Back on the LED header, it works again.
	setConfig(t, peer, "Signal Heads/Connected To", 0)
	deadline = time.Now().Add(2 * time.Second)
	for peer.Turnout(0) != "thrown" {
		if err := local.SendEvent(ctx, throw); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("servo 1 back in service = %s", peer.Turnout(0))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An output set to Own Events (lamp 8) follows its On and Off events while
// its head is enabled, and a head with only such outputs has no aspects on
// the bus, dark included.
func TestOutpostOwnEvents(t *testing.T) {
	local, _, peer, box, ctx := simOutpost(t)
	on, off := peerID.Event(0x304), peerID.Event(0x305) // head 1, output Y
	send := func(ev openlcb.EventID) {
		t.Helper()
		if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIEventReport, Payload: ev.AsSlice()}); err != nil {
			t.Fatal(err)
		}
	}
	ask := func(ev openlcb.EventID) {
		t.Helper()
		box.clear()
		if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyConsumer, Payload: ev.AsSlice()}); err != nil {
			t.Fatal(err)
		}
	}
	waitLamp := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for peer.Lamp(0, 2) != want {
			if time.Now().After(deadline) {
				t.Fatalf("head 1 output Y lit = %v, want %v", peer.Lamp(0, 2), want)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	// Ignored while the head is disabled: by the time the signal test's
	// start event is identified, the On event has been handled
	setConfig(t, peer, "Signal Heads/Head 1/Y Lamp", 8)
	send(on)
	ask(peerID.Event(0x200))
	box.wait(t, "signal test start identified", withEvent(openlcb.MTIConsumerIdentifiedInvalid, peerID.Event(0x200)))
	if peer.Lamp(0, 2) {
		t.Error("a disabled head's output followed its On event")
	}
	setConfig(t, peer, "Signal Heads/Head 1/Enabled", 1)
	ask(on)
	box.wait(t, "on invalid: the disabled head stayed dark", withEvent(openlcb.MTIConsumerIdentifiedInvalid, on))

	send(on)
	waitLamp(true)
	ask(on)
	box.wait(t, "on valid while lit", withEvent(openlcb.MTIConsumerIdentifiedValid, on))
	ask(off)
	box.wait(t, "off invalid while lit", withEvent(openlcb.MTIConsumerIdentifiedInvalid, off))
	send(off)
	waitLamp(false)

	// Set to another lamp, it goes dark
	send(on)
	waitLamp(true)
	setConfig(t, peer, "Signal Heads/Head 1/Y Lamp", 2)
	waitLamp(false)

	// Head 2 has only own outputs: its own events are on the bus, its dark
	// isn't
	setConfig(t, peer, "Signal Heads/Head 2/R Lamp", 8)
	setConfig(t, peer, "Signal Heads/Head 2/G Lamp", 8)
	setConfig(t, peer, "Signal Heads/Head 2/Y Lamp", 0)
	setConfig(t, peer, "Signal Heads/Head 2/Enabled", 1)
	box.clear()
	if err := local.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyEventsAddressed, Dest: openlcb.NodeHandle{ID: peerID}}); err != nil {
		t.Fatal(err)
	}
	box.wait(t, "head 2 output G off valid", withEvent(openlcb.MTIConsumerIdentifiedValid, peerID.Event(0x30B)))
	dark := peerID.Event(0x110)
	if n := box.count(withEvent(openlcb.MTIConsumerIdentifiedInvalid, dark)) + box.count(withEvent(openlcb.MTIConsumerIdentifiedValid, dark)); n != 0 {
		t.Error("a head with only own outputs identified dark")
	}
}
