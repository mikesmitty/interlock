package node

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
)

const (
	ourID   openlcb.NodeID = 0x050101012200
	peerID  openlcb.NodeID = 0x050101012201
	peerAli openlcb.Alias  = 0x555
)

var ourSNIP = openlcb.SNIP{Manufacturer: "interlock", Model: "controller", HardwareVersion: "-", SoftwareVersion: "test", UserName: "Test", UserDescription: "Desk"}

// bus records every frame the node sends.
type bus struct {
	mu     sync.Mutex
	frames []can.Frame
	ch     chan can.Frame
}

func newBus() *bus { return &bus{ch: make(chan can.Frame, 1024)} }

func (b *bus) send(f can.Frame) {
	b.mu.Lock()
	b.frames = append(b.frames, f)
	b.mu.Unlock()
	b.ch <- f
}

// next returns the next sent frame or fails after a timeout.
func (b *bus) next(t *testing.T) can.Frame {
	t.Helper()
	select {
	case f := <-b.ch:
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return can.Frame{}
	}
}

// expect checks the next frames in order.
func (b *bus) expect(t *testing.T, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if got := b.next(t).String(); got != want {
			t.Fatalf("sent %s, want %s", got, want)
		}
	}
}

// expectNone fails if a frame is sent in the next 50 ms.
func (b *bus) expectNone(t *testing.T) {
	t.Helper()
	select {
	case f := <-b.ch:
		t.Fatalf("unexpected frame %s", f)
	case <-time.After(50 * time.Millisecond):
	}
}

// drainUntil discards frames until one satisfies match, returning it.
func (b *bus) drainUntil(t *testing.T, match func(can.Frame) bool) can.Frame {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f := <-b.ch:
			if match(f) {
				return f
			}
		case <-deadline:
			t.Fatal("timed out waiting for a matching frame")
			return can.Frame{}
		}
	}
}

type harness struct {
	n   *Node
	bus *bus
	ctx context.Context
}

func start(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	b := newBus()
	cfg := Config{
		ID:           ourID,
		SNIP:         ourSNIP,
		Send:         b.send,
		Logger:       slog.New(slog.DiscardHandler),
		CIDWait:      20 * time.Millisecond,
		ReplyTimeout: 500 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	n := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = n.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return &harness{n: n, bus: b, ctx: ctx}
}

// startInitialized starts a node and consumes its allocation frames.
func startInitialized(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	h := start(t, mutate)
	h.bus.drainUntil(t, func(f can.Frame) bool {
		return f.IsOpenLCB() && f.MTI() == openlcb.MTIInitializationComplete
	})
	if h.n.State() != StateInitialized {
		t.Fatalf("state = %v after Initialization Complete", h.n.State())
	}
	return h
}

func (h *harness) alias() openlcb.Alias { return h.n.Alias() }

// mapPeer announces the peer's alias and waits until the node knows it, so
// later frames from the peer do not trigger an identity query.
func (h *harness) mapPeer(t *testing.T) {
	t.Helper()
	h.inject(can.AMDFrame(peerAli, peerID))
	deadline := time.Now().Add(2 * time.Second)
	for {
		if id, ok := h.n.Cache().Lookup(peerAli); ok && id == peerID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("peer alias was not learned")
		}
		time.Sleep(time.Millisecond)
	}
}

// inject feeds frames to the node as if received from the bus.
func (h *harness) inject(frames ...can.Frame) {
	for _, f := range frames {
		h.n.HandleFrame(f)
	}
}

func TestAliasAllocation(t *testing.T) {
	h := start(t, nil)
	want := can.NewAliasGenerator(ourID).Next()
	h.bus.expect(t,
		can.CIDFrame(1, ourID, want).String(),
		can.CIDFrame(2, ourID, want).String(),
		can.CIDFrame(3, ourID, want).String(),
		can.CIDFrame(4, ourID, want).String(),
		can.RIDFrame(want).String(),
		can.AMDFrame(want, ourID).String(),
		can.GlobalFrame(openlcb.MTIInitializationComplete, want, ourID.AsSlice()).String(),
	)
	if h.alias() != want || h.n.State() != StateInitialized {
		t.Fatalf("alias %v state %v", h.alias(), h.n.State())
	}
	h.bus.expectNone(t)
}

func TestConflictDuringChecking(t *testing.T) {
	h := start(t, func(c *Config) { c.CIDWait = 300 * time.Millisecond })
	first := h.bus.next(t).SourceAlias()
	for range 3 {
		h.bus.next(t)
	}
	// Someone already holds that alias.
	h.inject(can.RIDFrame(first))
	cid := h.bus.next(t)
	if !cid.IsCID() || cid.SourceAlias() == first {
		t.Fatalf("after conflict got %s, want a CID with a new alias", cid)
	}
	if h.n.State() != StateChecking {
		t.Fatalf("state = %v", h.n.State())
	}
}

func TestDefendsAlias(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	h.inject(can.CIDFrame(1, peerID, a))
	h.bus.expect(t, can.RIDFrame(a).String())
	if h.alias() != a || h.n.State() != StateInitialized {
		t.Fatal("alias was given up when it should have been defended")
	}
}

func TestConflictAfterInitialized(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	var events []Event
	var mu sync.Mutex
	h.n.Observe(func(ev Event) {
		if ev.Kind == EventStateChanged {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	})
	h.inject(can.AMDFrame(a, peerID))
	h.bus.expect(t, can.AMRFrame(a, ourID).String())
	cid := h.bus.next(t)
	if !cid.IsCID() || cid.SourceAlias() == a {
		t.Fatalf("got %s, want CID with a new alias", cid)
	}
	h.bus.drainUntil(t, func(f can.Frame) bool {
		return f.IsOpenLCB() && f.MTI() == openlcb.MTIInitializationComplete
	})
	if h.alias() == a || h.n.State() != StateInitialized {
		t.Fatalf("alias %v state %v after reallocation", h.alias(), h.n.State())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 2 || events[0].State != StateInhibited || events[len(events)-1].State != StateInitialized {
		t.Errorf("state events = %+v", events)
	}
}

func TestAliasMapEnquiry(t *testing.T) {
	h := startInitialized(t, nil)
	h.mapPeer(t)
	a := h.alias()
	h.inject(can.AMEFrame(peerAli, 0))
	h.bus.expect(t, can.AMDFrame(a, ourID).String())
	h.inject(can.AMEFrame(peerAli, ourID))
	h.bus.expect(t, can.AMDFrame(a, ourID).String())
	h.inject(can.AMEFrame(peerAli, peerID))
	h.bus.expectNone(t)
}

func TestVerifyNodeID(t *testing.T) {
	h := startInitialized(t, nil)
	h.mapPeer(t)
	a := h.alias()
	verified := can.GlobalFrame(openlcb.MTIVerifiedNodeID, a, ourID.AsSlice()).String()

	h.inject(can.GlobalFrame(openlcb.MTIVerifyNodeIDGlobal, peerAli, nil))
	h.bus.expect(t, verified)
	h.inject(can.GlobalFrame(openlcb.MTIVerifyNodeIDGlobal, peerAli, ourID.AsSlice()))
	h.bus.expect(t, verified)
	h.inject(can.GlobalFrame(openlcb.MTIVerifyNodeIDGlobal, peerAli, peerID.AsSlice()))
	h.bus.expectNone(t)

	h.inject(can.AddressedFrames(openlcb.MTIVerifyNodeIDAddressed, peerAli, a, nil)...)
	h.bus.expect(t, verified)
	h.inject(can.AddressedFrames(openlcb.MTIVerifyNodeIDAddressed, peerAli, 0x999, nil)...)
	h.bus.expectNone(t)
}

func TestProtocolSupportAndSNIP(t *testing.T) {
	protocols := openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolSimpleNodeInformation
	h := startInitialized(t, func(c *Config) { c.Protocols = protocols })
	h.mapPeer(t)
	a := h.alias()

	h.inject(can.AddressedFrames(openlcb.MTIProtocolSupportInquiry, peerAli, a, nil)...)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIProtocolSupportReply, a, peerAli, protocols.Bytes())[0].String())

	h.inject(can.AddressedFrames(openlcb.MTISimpleNodeIdentInfoRequest, peerAli, a, nil)...)
	asm := can.NewAssembler(nil)
	var msg *openlcb.Message
	for msg == nil {
		f := h.bus.next(t)
		if f.MTI() != openlcb.MTISimpleNodeIdentInfoReply {
			t.Fatalf("unexpected frame %s", f)
		}
		var err error
		msg, err = asm.Push(f, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if msg.Dest.Alias != peerAli {
		t.Errorf("reply addressed to %v", msg.Dest)
	}
	got, err := openlcb.ParseSNIP(msg.Payload)
	if err != nil || got != ourSNIP {
		t.Fatalf("SNIP reply = %+v, %v", got, err)
	}
}

func TestUnknownAddressedMTI(t *testing.T) {
	h := startInitialized(t, nil)
	h.mapPeer(t)
	a := h.alias()
	unknown := openlcb.MTI(0x0E88)
	h.inject(can.AddressedFrames(unknown, peerAli, a, []byte{1, 2})...)
	want := can.AddressedFrames(openlcb.MTIOptionalInteractionRejected, a, peerAli, []byte{0x10, 0x43, 0x0E, 0x88})[0]
	h.bus.expect(t, want.String())
}

func TestDatagramReceive(t *testing.T) {
	h := startInitialized(t, nil)
	h.mapPeer(t)
	a := h.alias()
	payload := []byte{0x20, 0x43, 0, 0, 0, 0, 0xFF, 64}

	h.inject(can.DatagramFrames(peerAli, a, payload)...)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIDatagramRejected, a, peerAli, []byte{0x10, 0x42})[0].String())

	var seen [][]byte
	h.n.HandleDatagrams(0x20, func(msg *openlcb.Message) (DatagramFlags, error) {
		seen = append(seen, msg.Payload)
		if msg.Payload[1] == 0xEE {
			return DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeAddressSpaceUnknown}
		}
		return DatagramFlags{ReplyPending: true, TimeoutExp: 3}, nil
	})
	h.inject(can.DatagramFrames(peerAli, a, payload)...)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIDatagramOK, a, peerAli, []byte{0x83})[0].String())
	h.inject(can.DatagramFrames(peerAli, a, []byte{0x20, 0xEE})...)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIDatagramRejected, a, peerAli, []byte{0x10, 0x81})[0].String())
	if len(seen) != 2 || string(seen[0]) != string(payload) {
		t.Errorf("handler saw %X", seen)
	}

	// A long datagram arrives in pieces; a stray middle frame is rejected.
	long := make([]byte, 20)
	long[0] = 0x20
	h.inject(can.DatagramFrames(peerAli, a, long)...)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIDatagramOK, a, peerAli, []byte{0x83})[0].String())
	middle := can.DatagramFrames(peerAli, a, long)[1]
	h.inject(middle)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIDatagramRejected, a, peerAli, []byte{0x20, 0x41})[0].String())

	// Datagrams for someone else are ignored.
	h.inject(can.DatagramFrames(peerAli, 0x999, payload)...)
	h.bus.expectNone(t)
}

func TestLearnsAliases(t *testing.T) {
	h := startInitialized(t, nil)
	var mu sync.Mutex
	var events []Event
	h.n.Observe(func(ev Event) {
		if ev.Kind != EventMessage && ev.Kind != EventStateChanged {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	})
	other := openlcb.NodeID(0x050101012202)
	h.inject(can.AMDFrame(peerAli, peerID))
	h.inject(can.GlobalFrame(openlcb.MTIInitializationComplete, 0x666, other.AsSlice()))
	h.inject(can.GlobalFrame(openlcb.MTIVerifiedNodeID, 0x777, openlcb.NodeID(0x050101012203).AsSlice()))
	h.inject(can.AMRFrame(peerAli, peerID))
	// Another node claiming our ID.
	h.inject(can.AMDFrame(0x888, ourID))

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 5 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 5 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Kind != EventAliasMapped || events[0].Alias != peerAli || events[0].NodeID != peerID {
		t.Errorf("event 0 = %+v", events[0])
	}
	if events[1].Kind != EventAliasMapped || events[1].NodeID != other {
		t.Errorf("event 1 = %+v", events[1])
	}
	if events[3].Kind != EventAliasReset || events[3].Alias != peerAli || events[3].NodeID != peerID {
		t.Errorf("event 3 = %+v", events[3])
	}
	if events[4].Kind != EventDuplicateNodeID || events[4].Alias != 0x888 {
		t.Errorf("event 4 = %+v", events[4])
	}
	if _, ok := h.n.Cache().Lookup(peerAli); ok {
		t.Error("AMR did not remove the alias")
	}
	if id, _ := h.n.Cache().Lookup(0x666); id != other {
		t.Error("Initialization Complete did not map the alias")
	}
	if _, ok := h.n.Cache().Lookup(0x888); ok {
		t.Error("duplicate of our ID was cached")
	}
}

func TestResolvesUnknownAliasOnce(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	report := can.GlobalFrame(openlcb.MTIEventReport, 0x777, openlcb.EventEmergencyStop.AsSlice())
	h.inject(report, report)
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIVerifyNodeIDAddressed, a, 0x777, nil)[0].String())
	h.bus.expectNone(t)
	// Once the node answers, no more queries.
	h.inject(can.GlobalFrame(openlcb.MTIVerifiedNodeID, 0x777, peerID.AsSlice()))
	h.inject(report)
	h.bus.expectNone(t)
}

func TestClientSNIP(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	h.mapPeer(t)
	peerSNIP := openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", HardwareVersion: "Rev A", SoftwareVersion: "0.2.1", UserName: "Yard", UserDescription: "West"}

	type result struct {
		snip openlcb.SNIP
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snip, err := h.n.SNIP(h.ctx, peerID)
		done <- result{snip, err}
	}()
	h.bus.expect(t, can.AddressedFrames(openlcb.MTISimpleNodeIdentInfoRequest, a, peerAli, nil)[0].String())
	h.inject(can.AddressedFrames(openlcb.MTISimpleNodeIdentInfoReply, peerAli, a, peerSNIP.Encode())...)
	r := <-done
	if r.err != nil || r.snip != peerSNIP {
		t.Fatalf("SNIP = %+v, %v", r.snip, r.err)
	}

	// Timeout when nobody answers.
	start := time.Now()
	if _, err := h.n.SNIP(h.ctx, peerID); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	} else if time.Since(start) > 2*time.Second {
		t.Fatal("timeout took too long")
	}
	h.bus.next(t) // the unanswered request

	// Rejected with an error code.
	go func() {
		_, err := h.n.SNIP(h.ctx, peerID)
		done <- result{err: err}
	}()
	h.bus.next(t)
	h.inject(can.AddressedFrames(openlcb.MTIOptionalInteractionRejected, peerAli, a, []byte{0x10, 0x43, 0x0D, 0xE8})...)
	r = <-done
	var oerr *openlcb.Error
	if !errors.As(r.err, &oerr) || oerr.Code != openlcb.ErrCodeNotImplementedMTI {
		t.Fatalf("err = %v, want OpenLCB error 0x1043", r.err)
	}
}

func TestClientProtocols(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	h.mapPeer(t)
	want := openlcb.ProtocolSimple | openlcb.ProtocolCDI
	done := make(chan openlcb.Protocols, 1)
	go func() {
		p, err := h.n.Protocols(h.ctx, peerID)
		if err != nil {
			t.Error(err)
		}
		done <- p
	}()
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIProtocolSupportInquiry, a, peerAli, nil)[0].String())
	h.inject(can.AddressedFrames(openlcb.MTIProtocolSupportReply, peerAli, a, want.Bytes())...)
	if got := <-done; got != want {
		t.Fatalf("Protocols = %v, want %v", got, want)
	}
}

func TestResolveAlias(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	done := make(chan openlcb.Alias, 1)
	go func() {
		alias, err := h.n.ResolveAlias(h.ctx, peerID)
		if err != nil {
			t.Error(err)
		}
		done <- alias
	}()
	h.bus.expect(t,
		can.AMEFrame(a, peerID).String(),
		can.GlobalFrame(openlcb.MTIVerifyNodeIDGlobal, a, peerID.AsSlice()).String(),
	)
	h.inject(can.AMDFrame(peerAli, peerID))
	if got := <-done; got != peerAli {
		t.Fatalf("ResolveAlias = %v, want %v", got, peerAli)
	}
	if got, err := h.n.ResolveAlias(h.ctx, peerID); err != nil || got != peerAli {
		t.Fatalf("cached ResolveAlias = %v, %v", got, err)
	}

	missing := openlcb.NodeID(0x050101012299)
	if _, err := h.n.ResolveAlias(h.ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := h.n.SNIP(h.ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SNIP of missing node: %v", err)
	}
}

func TestSendDatagram(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	h.mapPeer(t)
	payload := []byte{0x20, 0x43, 0, 0, 0, 0, 0xFF, 64, 1, 2}
	frames := can.DatagramFrames(a, peerAli, payload)

	type result struct {
		res DatagramResult
		err error
	}
	done := make(chan result, 1)
	sendIt := func() {
		go func() {
			res, err := h.n.SendDatagram(h.ctx, peerID, payload)
			done <- result{res, err}
		}()
	}

	sendIt()
	h.bus.expect(t, frames[0].String(), frames[1].String())
	h.inject(can.AddressedFrames(openlcb.MTIDatagramOK, peerAli, a, []byte{0x83})...)
	r := <-done
	if r.err != nil || !r.res.ReplyPending || r.res.Timeout != 8*time.Second {
		t.Fatalf("result = %+v, %v", r.res, r.err)
	}

	// Temporary rejection, then success on the retry.
	sendIt()
	h.bus.expect(t, frames[0].String(), frames[1].String())
	h.inject(can.AddressedFrames(openlcb.MTIDatagramRejected, peerAli, a, []byte{0x20, 0x40})...)
	h.bus.expect(t, frames[0].String(), frames[1].String())
	h.inject(can.AddressedFrames(openlcb.MTIDatagramOK, peerAli, a, nil)...)
	r = <-done
	if r.err != nil || r.res.ReplyPending {
		t.Fatalf("after retry: %+v, %v", r.res, r.err)
	}

	// Permanent rejection.
	sendIt()
	h.bus.expect(t, frames[0].String(), frames[1].String())
	h.inject(can.AddressedFrames(openlcb.MTIDatagramRejected, peerAli, a, []byte{0x10, 0x81})...)
	r = <-done
	var oerr *openlcb.Error
	if !errors.As(r.err, &oerr) || oerr.Code != openlcb.ErrCodeAddressSpaceUnknown {
		t.Fatalf("err = %v, want 0x1081", r.err)
	}

	if _, err := h.n.SendDatagram(h.ctx, peerID, make([]byte, 73)); err == nil {
		t.Fatal("73-byte datagram accepted")
	}
}

func TestSendResolvesDestination(t *testing.T) {
	h := startInitialized(t, nil)
	a := h.alias()
	h.mapPeer(t)
	err := h.n.Send(h.ctx, openlcb.Message{MTI: openlcb.MTIIdentifyEventsAddressed, Dest: openlcb.NodeHandle{ID: peerID}})
	if err != nil {
		t.Fatal(err)
	}
	h.bus.expect(t, can.AddressedFrames(openlcb.MTIIdentifyEventsAddressed, a, peerAli, nil)[0].String())
	if err := h.n.SendEvent(h.ctx, openlcb.EventEmergencyStop); err != nil {
		t.Fatal(err)
	}
	h.bus.expect(t, can.GlobalFrame(openlcb.MTIEventReport, a, openlcb.EventEmergencyStop.AsSlice()).String())
	if err := h.n.VerifyNodeIDGlobal(h.ctx, 0); err != nil {
		t.Fatal(err)
	}
	h.bus.expect(t, can.GlobalFrame(openlcb.MTIVerifyNodeIDGlobal, a, nil).String())
	if err := h.n.AliasMapEnquiry(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.bus.expect(t, can.AMEFrame(a, 0).String())
}

func TestShutdownWithdrawsAlias(t *testing.T) {
	b := newBus()
	n := New(Config{ID: ourID, Send: b.send, Logger: slog.New(slog.DiscardHandler), CIDWait: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = n.Run(ctx)
		close(done)
	}()
	b.drainUntil(t, func(f can.Frame) bool { return f.IsOpenLCB() })
	a := n.Alias()
	cancel()
	<-done
	b.expect(t, can.AMRFrame(a, ourID).String())
	if n.State() != StateInhibited {
		t.Errorf("state after shutdown = %v", n.State())
	}
	if _, err := n.SNIP(context.Background(), peerID); !errors.Is(err, ErrNotRunning) {
		t.Errorf("SNIP after shutdown: %v, want ErrNotRunning", err)
	}
}

func TestHandleFrameDropsWhenFull(t *testing.T) {
	n := New(Config{ID: ourID, Send: func(can.Frame) {}, Logger: slog.New(slog.DiscardHandler)})
	for range inQueue + 10 {
		n.HandleFrame(can.RIDFrame(1))
	}
	if n.Dropped() != 10 {
		t.Fatalf("Dropped = %d, want 10", n.Dropped())
	}
}

// TestTwoNodes wires two nodes back to back and exercises the client
// operations against a real peer.
func TestTwoNodes(t *testing.T) {
	var a, b *Node
	logger := slog.New(slog.DiscardHandler)
	bSNIP := openlcb.SNIP{Manufacturer: "sim", Model: "peer", HardwareVersion: "1", SoftwareVersion: "2", UserName: "Peer", UserDescription: "Simulated"}
	a = New(Config{ID: ourID, SNIP: ourSNIP, Logger: logger, CIDWait: 20 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { b.HandleFrame(f) }})
	b = New(Config{ID: peerID, SNIP: bSNIP, Logger: logger, CIDWait: 20 * time.Millisecond, ReplyTimeout: time.Second,
		Protocols: openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolCDI,
		Send:      func(f can.Frame) { a.HandleFrame(f) }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()
	go func() { _ = b.Run(ctx) }()

	var got []byte
	b.HandleDatagrams(0x20, func(msg *openlcb.Message) (DatagramFlags, error) {
		got = append([]byte(nil), msg.Payload...)
		return DatagramFlags{ReplyPending: true, TimeoutExp: 2}, nil
	})

	opCtx, opCancel := context.WithTimeout(ctx, 5*time.Second)
	defer opCancel()

	snip, err := a.SNIP(opCtx, peerID)
	if err != nil || snip != bSNIP {
		t.Fatalf("SNIP = %+v, %v", snip, err)
	}
	protocols, err := a.Protocols(opCtx, peerID)
	if err != nil || !protocols.Has(openlcb.ProtocolCDI) {
		t.Fatalf("Protocols = %v, %v", protocols, err)
	}
	handle, err := b.Verify(opCtx, ourID)
	if err != nil || handle.ID != ourID || handle.Alias != a.Alias() {
		t.Fatalf("Verify = %+v, %v", handle, err)
	}
	payload := []byte{0x20, 0x43, 0, 0, 0, 0, 0xFF, 64, 9, 9, 9, 9}
	res, err := a.SendDatagram(opCtx, peerID, payload)
	if err != nil || !res.ReplyPending || res.Timeout != 4*time.Second {
		t.Fatalf("SendDatagram = %+v, %v", res, err)
	}
	if string(got) != string(payload) {
		t.Fatalf("peer received %X", got)
	}
	if _, err := b.SendDatagram(opCtx, ourID, payload); err == nil {
		t.Fatal("datagram to a node without a handler should be rejected")
	} else if !strings.Contains(err.Error(), "0x1042") {
		t.Fatalf("rejection = %v", err)
	}
	if a.Alias() == b.Alias() {
		t.Fatal("both nodes ended up with the same alias")
	}
	if id, ok := a.Cache().Lookup(b.Alias()); !ok || id != peerID {
		t.Errorf("a's cache does not know b: %v %v", id, ok)
	}
}

func TestTractionAndProducers(t *testing.T) {
	var a, b *Node
	logger := slog.New(slog.DiscardHandler)
	a = New(Config{ID: ourID, SNIP: ourSNIP, Logger: logger, CIDWait: 20 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { b.HandleFrame(f) }})
	b = New(Config{ID: peerID, SNIP: ourSNIP, Logger: logger, CIDWait: 20 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { a.HandleFrame(f) }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()
	go func() { _ = b.Run(ctx) }()
	opCtx, opCancel := context.WithTimeout(ctx, 5*time.Second)
	defer opCancel()

	// Without a handler, b rejects traction commands and a's request fails.
	if _, err := a.TractionCommand(opCtx, peerID, []byte{0x10}, true); err == nil {
		t.Fatal("command to a node without traction support should fail")
	} else if !strings.Contains(err.Error(), "0x1043") {
		t.Fatalf("error = %v", err)
	}

	// b acts as a train: it answers queries and notes set speed.
	var mu sync.Mutex
	var got [][]byte
	b.HandleTraction(func(_ openlcb.NodeHandle, payload []byte) []byte {
		mu.Lock()
		got = append(got, append([]byte(nil), payload...))
		mu.Unlock()
		switch payload[0] {
		case 0x10:
			return []byte{0x10, 0x4F, 0x80, 0x00}
		case 0x20:
			return []byte{0x20, payload[1], 0x00}
		}
		return nil
	})
	if _, err := a.TractionCommand(opCtx, peerID, []byte{0x00, 0x4F, 0x80}, false); err != nil {
		t.Fatal(err)
	}
	reply, err := a.TractionCommand(opCtx, peerID, []byte{0x10}, true)
	if err != nil || string(reply) != string([]byte{0x10, 0x4F, 0x80, 0x00}) {
		t.Fatalf("query speeds reply = %X, %v", reply, err)
	}
	reply, err = a.TractionCommand(opCtx, peerID, []byte{0x20, 0x01, 0, 5, 1, 1, 1, 0x22, 0}, true)
	if err != nil || string(reply) != string([]byte{0x20, 0x01, 0x00}) {
		t.Fatalf("assign reply = %X, %v", reply, err)
	}
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 3 {
		t.Fatalf("b saw %d commands, want 3", n)
	}

	// b produces an event: it answers Identify Producer from a goroutine,
	// as a real implementation would.
	ev := openlcb.EventID(0x0102030405060708)
	b.Observe(func(e Event) {
		if e.Kind == EventMessage && e.Message.MTI == openlcb.MTIIdentifyProducer {
			if id, ok := e.Message.EventID(); ok && id == ev {
				go func() {
					_ = b.Send(ctx, openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: ev.AsSlice()})
				}()
			}
		}
	})
	producers, err := a.Producers(opCtx, ev, 200*time.Millisecond, false)
	if err != nil || len(producers) != 1 || producers[0].Node != peerID || producers[0].State != "valid" {
		t.Fatalf("producers = %+v, %v", producers, err)
	}
	producers, err = a.Producers(opCtx, ev+1, 100*time.Millisecond, true)
	if err != nil || len(producers) != 0 {
		t.Fatalf("producers of another event = %+v, %v", producers, err)
	}
	if id, err := a.VerifyAlias(opCtx, b.Alias()); err != nil || id != peerID {
		t.Fatalf("VerifyAlias = %v, %v", id, err)
	}
}
