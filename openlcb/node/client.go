package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
)

// Client operations: things the rest of Interlock asks the node to do on
// the bus. Each may be called from any goroutine.

type pendingKind uint8

const (
	pendSNIP pendingKind = iota + 1
	pendPIP
	pendVerify
	pendDatagram
	pendResolve // waiting for a node ID to show up with an alias
)

type pendingKey struct {
	kind  pendingKind
	alias openlcb.Alias  // the node we expect to hear from
	id    openlcb.NodeID // for pendResolve
}

type reply struct {
	msg   *openlcb.Message
	alias openlcb.Alias
	err   error
}

type pendingRequest struct {
	ch       chan reply
	deadline time.Time
}

func (p *pendingRequest) finish(msg *openlcb.Message, err error) {
	select {
	case p.ch <- reply{msg: msg, err: err}:
	default:
	}
}

// completeRequest hands a reply to whoever is waiting for it.
func (n *Node) completeRequest(key pendingKey, msg *openlcb.Message, err error) {
	if req, ok := n.pending[key]; ok {
		delete(n.pending, key)
		req.finish(msg, err)
	}
}

// completeResolve wakes requests waiting to learn id's alias.
func (n *Node) completeResolve(id openlcb.NodeID, alias openlcb.Alias) {
	key := pendingKey{kind: pendResolve, id: id}
	if req, ok := n.pending[key]; ok {
		delete(n.pending, key)
		select {
		case req.ch <- reply{alias: alias}:
		default:
		}
	}
}

func (n *Node) failPending(err error) {
	for key, req := range n.pending {
		delete(n.pending, key)
		req.finish(nil, err)
	}
}

// request registers a pending reply under key, runs send on the node
// goroutine, and waits for the reply, ctx, or the reply timeout.
func (n *Node) request(ctx context.Context, key pendingKey, send func() error) (reply, error) {
	if err := n.waitInitialized(ctx); err != nil {
		return reply{}, err
	}
	ch := make(chan reply, 1)
	var sendErr error
	err := n.do(ctx, func() {
		if _, busy := n.pending[key]; busy {
			sendErr = ErrBusy
			return
		}
		n.pending[key] = &pendingRequest{ch: ch, deadline: time.Now().Add(n.cfg.ReplyTimeout)}
		if sendErr = send(); sendErr != nil {
			delete(n.pending, key)
		}
	})
	if err != nil {
		return reply{}, err
	}
	if sendErr != nil {
		return reply{}, sendErr
	}
	select {
	case r := <-ch:
		return r, r.err
	case <-ctx.Done():
		_ = n.do(context.Background(), func() { delete(n.pending, key) })
		return reply{}, ctx.Err()
	case <-n.stopped:
		return reply{}, ErrNotRunning
	}
}

// Send transmits a message from this node. Dest may carry only a node ID;
// its alias is looked up or discovered first.
func (n *Node) Send(ctx context.Context, msg openlcb.Message) error {
	if err := n.waitInitialized(ctx); err != nil {
		return err
	}
	if msg.MTI.IsAddressed() && msg.Dest.Alias == 0 {
		alias, err := n.ResolveAlias(ctx, msg.Dest.ID)
		if err != nil {
			return err
		}
		msg.Dest.Alias = alias
	}
	var sendErr error
	if err := n.do(ctx, func() { sendErr = n.sendMessage(&msg) }); err != nil {
		return err
	}
	return sendErr
}

// SendEvent produces an event report.
func (n *Node) SendEvent(ctx context.Context, id openlcb.EventID) error {
	return n.Send(ctx, openlcb.Message{MTI: openlcb.MTIEventReport, Payload: id.AsSlice()})
}

// VerifyNodeIDGlobal asks every node (or just target, if non-zero) to
// announce itself with Verified Node ID. Replies arrive as events.
func (n *Node) VerifyNodeIDGlobal(ctx context.Context, target openlcb.NodeID) error {
	var payload []byte
	if target != 0 {
		payload = target.AsSlice()
	}
	return n.Send(ctx, openlcb.Message{MTI: openlcb.MTIVerifyNodeIDGlobal, Payload: payload})
}

// AliasMapEnquiry asks every node to announce its alias with an AMD
// frame, filling the alias cache.
func (n *Node) AliasMapEnquiry(ctx context.Context) error {
	if err := n.waitInitialized(ctx); err != nil {
		return err
	}
	return n.do(ctx, func() { n.send(can.AMEFrame(n.alias, 0)) })
}

// ResolveAlias returns the alias a node ID is using, asking the bus when
// the cache does not know.
func (n *Node) ResolveAlias(ctx context.Context, id openlcb.NodeID) (openlcb.Alias, error) {
	if id == 0 {
		return 0, fmt.Errorf("node: resolve alias: %w", openlcb.ErrInvalidNodeID)
	}
	if alias, ok := n.cache.LookupID(id); ok {
		return alias, nil
	}
	ctx, cancel := context.WithTimeout(ctx, n.cfg.ReplyTimeout)
	defer cancel()
	key := pendingKey{kind: pendResolve, id: id}
	r, err := n.request(ctx, key, func() error {
		// An AMD may have been processed between the caller's cache check
		// and this point; the loop goroutine sees the cache consistently.
		if alias, ok := n.cache.LookupID(id); ok {
			n.completeResolve(id, alias)
			return nil
		}
		n.send(can.AMEFrame(n.alias, id))
		n.sendGlobal(openlcb.MTIVerifyNodeIDGlobal, id.AsSlice())
		return nil
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrTimeout) {
			return 0, fmt.Errorf("%w: %v", ErrNotFound, id)
		}
		return 0, err
	}
	return r.alias, nil
}

// Verify checks that a node is on the bus by asking it directly. It
// returns the node's handle with both ID and alias.
func (n *Node) Verify(ctx context.Context, id openlcb.NodeID) (openlcb.NodeHandle, error) {
	alias, err := n.ResolveAlias(ctx, id)
	if err != nil {
		return openlcb.NodeHandle{}, err
	}
	dest := openlcb.NodeHandle{ID: id, Alias: alias}
	r, err := n.request(ctx, pendingKey{kind: pendVerify, alias: alias}, func() error {
		return n.sendMessage(&openlcb.Message{MTI: openlcb.MTIVerifyNodeIDAddressed, Dest: dest})
	})
	if err != nil {
		return openlcb.NodeHandle{}, err
	}
	if got, ok := r.msg.PayloadNodeID(); ok && got != id {
		return openlcb.NodeHandle{}, fmt.Errorf("node: alias %v answered as %v, expected %v", alias, got, id)
	}
	return dest, nil
}

// SNIP fetches a node's Simple Node Identification Information.
func (n *Node) SNIP(ctx context.Context, id openlcb.NodeID) (openlcb.SNIP, error) {
	alias, err := n.ResolveAlias(ctx, id)
	if err != nil {
		return openlcb.SNIP{}, err
	}
	dest := openlcb.NodeHandle{ID: id, Alias: alias}
	r, err := n.request(ctx, pendingKey{kind: pendSNIP, alias: alias}, func() error {
		return n.sendMessage(&openlcb.Message{MTI: openlcb.MTISimpleNodeIdentInfoRequest, Dest: dest})
	})
	if err != nil {
		return openlcb.SNIP{}, fmt.Errorf("SNIP from %v: %w", id, err)
	}
	snip, err := openlcb.ParseSNIP(r.msg.Payload)
	if err != nil {
		return snip, fmt.Errorf("SNIP from %v: %w", id, err)
	}
	return snip, nil
}

// Protocols fetches the protocols a node supports.
func (n *Node) Protocols(ctx context.Context, id openlcb.NodeID) (openlcb.Protocols, error) {
	alias, err := n.ResolveAlias(ctx, id)
	if err != nil {
		return 0, err
	}
	dest := openlcb.NodeHandle{ID: id, Alias: alias}
	r, err := n.request(ctx, pendingKey{kind: pendPIP, alias: alias}, func() error {
		return n.sendMessage(&openlcb.Message{MTI: openlcb.MTIProtocolSupportInquiry, Dest: dest})
	})
	if err != nil {
		return 0, fmt.Errorf("protocols of %v: %w", id, err)
	}
	return openlcb.ParseProtocols(r.msg.Payload), nil
}

// datagramRetries is how many times a datagram is resent after a
// temporary rejection.
const datagramRetries = 3

// SendDatagram sends a datagram and waits for the receiver's Datagram OK,
// retrying after temporary rejections. A permanent rejection comes back as
// an *openlcb.Error.
func (n *Node) SendDatagram(ctx context.Context, id openlcb.NodeID, payload []byte) (DatagramResult, error) {
	if len(payload) > can.MaxDatagram {
		return DatagramResult{}, fmt.Errorf("node: datagram of %d bytes exceeds %d", len(payload), can.MaxDatagram)
	}
	alias, err := n.ResolveAlias(ctx, id)
	if err != nil {
		return DatagramResult{}, err
	}
	dest := openlcb.NodeHandle{ID: id, Alias: alias}
	op := fmt.Sprintf("datagram to %v", id)
	for attempt := 0; ; attempt++ {
		r, err := n.request(ctx, pendingKey{kind: pendDatagram, alias: alias}, func() error {
			return n.sendMessage(&openlcb.Message{MTI: openlcb.MTIDatagram, Dest: dest, Payload: payload})
		})
		if err != nil {
			return DatagramResult{}, fmt.Errorf("%s: %w", op, err)
		}
		if r.msg.MTI == openlcb.MTIDatagramOK {
			var result DatagramResult
			if len(r.msg.Payload) > 0 {
				flags := r.msg.Payload[0]
				result.ReplyPending = flags&0x80 != 0
				if exp := flags & 0x0F; exp != 0 {
					result.Timeout = time.Duration(1<<exp) * time.Second
				}
			}
			return result, nil
		}
		code := openlcb.ErrCodePermanent
		if len(r.msg.Payload) >= 2 {
			code = openlcb.ErrorCode(uint16(r.msg.Payload[0])<<8 | uint16(r.msg.Payload[1]))
		}
		rejection := &openlcb.Error{Code: code, Op: op}
		if !code.Temporary() || attempt >= datagramRetries {
			return DatagramResult{}, rejection
		}
		n.log.Debug("datagram rejected, retrying", "error", rejection, "attempt", attempt+1)
		select {
		case <-time.After(time.Duration(50*(attempt+1)) * time.Millisecond):
		case <-ctx.Done():
			return DatagramResult{}, ctx.Err()
		}
	}
}
