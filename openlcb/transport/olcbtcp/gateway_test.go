package olcbtcp_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/hub"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/transport"
	"github.com/mikesmitty/interlock/openlcb/transport/olcbtcp"
)

const tcpNode = openlcb.NodeID(0x050101018000)

// tcpSide plays a node on the TCP side of a gateway: it records what the
// gateway forwards and answers a few requests.
type tcpSide struct {
	mu   sync.Mutex
	got  []*openlcb.Message
	send func(*openlcb.Message)
}

func (s *tcpSide) receive(msg *openlcb.Message) {
	s.mu.Lock()
	s.got = append(s.got, msg)
	s.mu.Unlock()
	if msg.MTI == openlcb.MTISimpleNodeIdentInfoRequest && msg.Dest.ID == tcpNode {
		snip := openlcb.SNIP{Manufacturer: "Far", Model: "Away", UserName: "TCP node"}
		reply := &openlcb.Message{MTI: openlcb.MTISimpleNodeIdentInfoReply, Source: openlcb.NodeHandle{ID: tcpNode}, Dest: openlcb.NodeHandle{ID: msg.Source.ID}, Payload: snip.Encode()}
		// Answer from another goroutine, as a real peer would: receive runs
		// inside the gateway's turn.
		go s.send(reply)
	}
}

func (s *tcpSide) count(mti openlcb.MTI) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.got {
		if m.MTI == mti {
			n++
		}
	}
	return n
}

// TestGateway puts a local node on the CAN side of a gateway and a fake
// node on its TCP side, with the gateway driven by one goroutine as the
// link drives it.
func TestGateway(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	side := &tcpSide{}
	var mu sync.Mutex
	var local *node.Node
	var gw *olcbtcp.Gateway
	// Everything the gateway does happens under mu, standing in for the
	// link goroutine.
	drive := func(fn func(now time.Time)) {
		mu.Lock()
		defer mu.Unlock()
		fn(time.Now())
	}
	gw = olcbtcp.NewGateway(olcbtcp.Options{
		ID: 0x0A0000000001, CIDWait: 20 * time.Millisecond, Logger: logger,
		ToCAN: func(f can.Frame) { local.HandleFrame(f) },
		ToTCP: side.receive,
	})
	side.send = func(msg *openlcb.Message) { drive(func(now time.Time) { gw.HandleMessage(msg, now) }) }
	local = node.New(node.Config{ID: 0x0A0000000002, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { drive(func(now time.Time) { gw.HandleFrame(f, now) }) }})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() { _ = local.Run(ctx) }()
	stopTick := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stopTick:
				return
			case <-tick.C:
				drive(gw.Tick)
			}
		}
	}()
	defer close(stopTick)
	eventually(t, "local node initialized", func() bool { return local.State() == node.StateInitialized })

	// The TCP node announces itself: the gateway gives it an alias and
	// the local node learns it.
	side.send(&openlcb.Message{MTI: openlcb.MTIInitializationComplete, Source: openlcb.NodeHandle{ID: tcpNode}, Payload: tcpNode.AsSlice()})
	eventually(t, "proxy alias learned", func() bool { _, ok := local.Cache().LookupID(tcpNode); return ok })
	alias, _ := local.Cache().LookupID(tcpNode)
	if gw.Proxies() != 1 || alias == 0 || alias == local.Alias() {
		t.Fatalf("proxies = %d, alias %v", gw.Proxies(), alias)
	}

	// The local node asks the TCP node for its SNIP: the request crosses
	// to TCP with full node IDs, the reply comes back as frames.
	snip, err := local.SNIP(ctx, tcpNode)
	if err != nil || snip.UserName != "TCP node" {
		t.Fatalf("SNIP over the gateway = %+v, %v", snip, err)
	}
	side.mu.Lock()
	var req *openlcb.Message
	for _, m := range side.got {
		if m.MTI == openlcb.MTISimpleNodeIdentInfoRequest {
			req = m
		}
	}
	side.mu.Unlock()
	if req == nil || req.Source.ID != local.ID() || req.Dest.ID != tcpNode {
		t.Fatalf("request seen on TCP = %v", req)
	}

	// Global traffic from CAN reaches TCP with the sender's ID; the
	// gateway learned the local node's alias from its announcements.
	if err := local.SendEvent(ctx, 0x0102030405060708); err != nil {
		t.Fatal(err)
	}
	eventually(t, "event forwarded", func() bool { return side.count(openlcb.MTIEventReport) == 1 })

	// A message from TCP addressed to the CAN node arrives there.
	var seen []openlcb.MTI
	var seenMu sync.Mutex
	local.Observe(func(ev node.Event) {
		if ev.Kind == node.EventMessage && ev.Message.Source.ID == tcpNode {
			seenMu.Lock()
			seen = append(seen, ev.Message.MTI)
			seenMu.Unlock()
		}
	})
	side.send(&openlcb.Message{MTI: openlcb.MTIVerifyNodeIDAddressed, Source: openlcb.NodeHandle{ID: tcpNode}, Dest: openlcb.NodeHandle{ID: local.ID()}})
	eventually(t, "addressed message delivered", func() bool {
		seenMu.Lock()
		defer seenMu.Unlock()
		return len(seen) == 1 && seen[0] == openlcb.MTIVerifyNodeIDAddressed
	})
	// ... and the node's answer went back to TCP, addressed to the TCP node.
	eventually(t, "verified node id forwarded", func() bool { return side.count(openlcb.MTIVerifiedNodeID) >= 1 })

	// An AME for the TCP node is answered by the gateway on its behalf.
	before := local.Cache().Len()
	drive(func(now time.Time) { gw.HandleFrame(can.AMEFrame(local.Alias(), tcpNode), now) })
	if local.Cache().Len() < before {
		t.Error("the cache lost entries after an AME")
	}

	// Closing the gateway withdraws the alias.
	drive(func(time.Time) { gw.Close() })
	eventually(t, "alias withdrawn", func() bool { _, ok := local.Cache().LookupID(tcpNode); return !ok })
}

// TestLinkAndListener joins two hubs, each with a local node, over an
// OpenLCB TCP connection: a Listener on one side, a dialing Link on the
// other. Node A then reads node B's SNIP through both gateways.
func TestLinkAndListener(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	newSide := func(id openlcb.NodeID, name string) (*hub.Hub, *node.Node) {
		h := hub.New(logger)
		n := node.New(node.Config{ID: id, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: 2 * time.Second, Send: h.Send,
			SNIP: openlcb.SNIP{UserName: name}})
		h.Subscribe(func(ev hub.Event) {
			if ev.Direction == hub.In {
				n.HandleFrame(ev.Frame)
			}
		})
		go func() { _ = n.Run(ctx) }()
		return h, n
	}
	hubA, nodeA := newSide(0x0A0000000011, "A")
	hubB, nodeB := newSide(0x0A0000000022, "B")
	defer hubA.Close()
	defer hubB.Close()

	ln := &olcbtcp.Listener{Address: "127.0.0.1:0", ID: nodeA.ID(), Logger: logger, Accept: func(l transport.Link) { hubA.AddLink(ctx, l) }}
	if err := ln.Listen(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Run(ctx) }()
	link := olcbtcp.NewLink("to-a", nodeB.ID(), transport.TCPDialer{Address: ln.Addr().String()}, logger)
	link.CIDWait = 20 * time.Millisecond
	hubB.AddLink(ctx, link)

	eventually(t, "nodes initialized", func() bool { return nodeA.State() == node.StateInitialized && nodeB.State() == node.StateInitialized })
	eventually(t, "link connected", func() bool { return link.Status().Connected })
	// B's announcement crossed to A's side, where A's gateway made a proxy
	// for it; now A can talk to B.
	if err := nodeA.VerifyNodeIDGlobal(ctx, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, "A knows B", func() bool { _, ok := nodeA.Cache().LookupID(nodeB.ID()); return ok })
	snip, err := nodeA.SNIP(ctx, nodeB.ID())
	if err != nil || snip.UserName != "B" {
		t.Fatalf("A reading B's SNIP = %+v, %v", snip, err)
	}
	snip, err = nodeB.SNIP(ctx, nodeA.ID())
	if err != nil || snip.UserName != "A" {
		t.Fatalf("B reading A's SNIP = %+v, %v", snip, err)
	}
	st := link.Status()
	if st.Kind != olcbtcp.Kind || st.FramesIn == 0 || st.FramesOut == 0 {
		t.Errorf("status = %+v", st)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
