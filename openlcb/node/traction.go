package node

import (
	"context"
	"fmt"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

// Train control and event identification: what a throttle needs from the
// node beyond datagrams.

// TractionHandler answers Traction Control Commands addressed to this
// node: notices from trains it controls, and commands a consist forwards
// to it as a listener. It returns the reply payload, or nil when the
// command takes none. It runs on the node's goroutine and must not block.
type TractionHandler func(src openlcb.NodeHandle, payload []byte) (reply []byte)

// HandleTraction installs the handler for Traction Control Commands. With
// none installed such commands are rejected as not implemented.
func (n *Node) HandleTraction(h TractionHandler) {
	n.snap.Lock()
	defer n.snap.Unlock()
	n.traction = h
}

// tractionCode identifies which request a traction payload belongs to:
// the command byte and, for the commands with subcommands, that too.
func tractionCode(payload []byte) uint16 {
	if len(payload) == 0 {
		return 0
	}
	code := uint16(payload[0]&0x7F) << 8
	switch payload[0] & 0x7F {
	case 0x20, 0x30, 0x40:
		if len(payload) > 1 {
			code |= uint16(payload[1])
		}
	}
	return code
}

// TractionCommand sends a Traction Control Command to a train node. When
// reply is true it waits for the Traction Control Reply that answers it
// and returns its payload; set speed, set function, emergency stop and
// controller release take no reply.
func (n *Node) TractionCommand(ctx context.Context, train openlcb.NodeID, payload []byte, reply bool) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("node: empty traction command")
	}
	alias, err := n.ResolveAlias(ctx, train)
	if err != nil {
		return nil, err
	}
	dest := openlcb.NodeHandle{ID: train, Alias: alias}
	msg := openlcb.Message{MTI: openlcb.MTITractionControlCommand, Dest: dest, Payload: payload}
	if !reply {
		var sendErr error
		if err := n.do(ctx, func() { sendErr = n.sendMessage(&msg) }); err != nil {
			return nil, err
		}
		return nil, sendErr
	}
	key := pendingKey{kind: pendTraction, alias: alias, code: tractionCode(payload)}
	r, err := n.request(ctx, key, func() error { return n.sendMessage(&msg) })
	if err != nil {
		return nil, fmt.Errorf("traction command 0x%02X to %v: %w", payload[0], train, err)
	}
	return r.msg.Payload, nil
}

// handleTractionCommand answers a command addressed to this node.
func (n *Node) handleTractionCommand(msg *openlcb.Message) {
	n.snap.RLock()
	h := n.traction
	n.snap.RUnlock()
	if h == nil {
		n.sendAddressed(openlcb.MTIOptionalInteractionRejected, msg.Source,
			append(errorPayload(openlcb.ErrCodeNotImplementedMTI), byte(msg.MTI>>8), byte(msg.MTI)))
		return
	}
	if reply := h(msg.Source, msg.Payload); reply != nil {
		n.sendAddressed(openlcb.MTITractionControlReply, msg.Source, reply)
	}
}

// failTraction fails every traction request waiting on alias, when the
// train rejects the interaction.
func (n *Node) failTraction(alias openlcb.Alias, err error) {
	for key, req := range n.pending {
		if key.kind == pendTraction && key.alias == alias {
			delete(n.pending, key)
			req.finish(nil, err)
		}
	}
}

// --- who produces an event ----------------------------------------------

// Producer is a node's answer to Identify Producer.
type Producer struct {
	// Node is zero when the node's ID is not known yet; Alias always is.
	Node  openlcb.NodeID
	Alias openlcb.Alias
	// State is "valid", "invalid" or "unknown".
	State string
}

type collector struct {
	event openlcb.EventID
	ch    chan Producer
}

// Producers asks who produces ev and collects the answers that arrive
// within window. The question is Identify Producer, and when alsoReport
// is set an Event Report too, which is what the Train Search protocol
// listens for on some command stations. Replies whose source is not yet
// known by node ID are resolved as far as the alias cache allows.
func (n *Node) Producers(ctx context.Context, ev openlcb.EventID, window time.Duration, alsoReport bool) ([]Producer, error) {
	if err := n.waitInitialized(ctx); err != nil {
		return nil, err
	}
	c := &collector{event: ev, ch: make(chan Producer, 64)}
	var sendErr error
	err := n.do(ctx, func() {
		n.collectors = append(n.collectors, c)
		sendErr = n.sendMessage(&openlcb.Message{MTI: openlcb.MTIIdentifyProducer, Payload: ev.AsSlice()})
		if sendErr == nil && alsoReport {
			sendErr = n.sendMessage(&openlcb.Message{MTI: openlcb.MTIEventReport, Payload: ev.AsSlice()})
		}
	})
	defer func() {
		_ = n.do(context.Background(), func() {
			for i, other := range n.collectors {
				if other == c {
					n.collectors = append(n.collectors[:i], n.collectors[i+1:]...)
					break
				}
			}
		})
	}()
	if err != nil {
		return nil, err
	}
	if sendErr != nil {
		return nil, sendErr
	}
	timer := time.NewTimer(window)
	defer timer.Stop()
	var out []Producer
	seen := make(map[openlcb.Alias]int)
	for {
		select {
		case p := <-c.ch:
			if i, ok := seen[p.Alias]; ok {
				out[i] = p
				continue
			}
			seen[p.Alias] = len(out)
			out = append(out, p)
		case <-timer.C:
			for i := range out {
				if out[i].Node == 0 {
					if id, ok := n.cache.Lookup(out[i].Alias); ok {
						out[i].Node = id
					}
				}
			}
			return out, nil
		case <-ctx.Done():
			return out, ctx.Err()
		case <-n.stopped:
			return out, ErrNotRunning
		}
	}
}

// deliverProducer hands a Producer Identified reply to the collectors
// waiting for its event.
func (n *Node) deliverProducer(msg *openlcb.Message, state string) {
	if len(n.collectors) == 0 {
		return
	}
	ev, ok := msg.EventID()
	if !ok {
		return
	}
	p := Producer{Node: msg.Source.ID, Alias: msg.Source.Alias, State: state}
	for _, c := range n.collectors {
		if c.event != ev {
			continue
		}
		select {
		case c.ch <- p:
		default:
		}
	}
}

// VerifyAlias asks the node using alias for its ID.
func (n *Node) VerifyAlias(ctx context.Context, alias openlcb.Alias) (openlcb.NodeID, error) {
	if id, ok := n.cache.Lookup(alias); ok {
		return id, nil
	}
	r, err := n.request(ctx, pendingKey{kind: pendVerify, alias: alias}, func() error {
		return n.sendMessage(&openlcb.Message{MTI: openlcb.MTIVerifyNodeIDAddressed, Dest: openlcb.NodeHandle{Alias: alias}})
	})
	if err != nil {
		return 0, err
	}
	id, ok := r.msg.PayloadNodeID()
	if !ok {
		return 0, fmt.Errorf("node: alias %v answered without a node ID", alias)
	}
	return id, nil
}
