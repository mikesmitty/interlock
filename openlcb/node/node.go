// Package node is Interlock's own presence on the bus: it allocates a CAN
// alias for the configured node ID, answers the basic protocols (Verify
// Node ID, Protocol Support, SNIP), keeps the alias cache, exchanges
// datagrams, and offers client operations to the rest of the program.
//
// All protocol state lives in one goroutine, started by Run. Frames come
// in through HandleFrame, client calls are serialized onto that goroutine,
// and observers are called from it, so they must return quickly.
package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
)

// State is where the node is in alias allocation.
type State uint8

// States.
const (
	// StateInhibited: not started, or the alias was lost and not yet
	// replaced. The node cannot send messages.
	StateInhibited State = iota
	// StateChecking: Check ID frames are out and the node is waiting to see
	// whether anyone objects.
	StateChecking
	// StateInitialized: the alias is reserved and mapped and Initialization
	// Complete has been sent.
	StateInitialized
)

// String implements fmt.Stringer.
func (s State) String() string {
	switch s {
	case StateChecking:
		return "checking"
	case StateInitialized:
		return "initialized"
	default:
		return "inhibited"
	}
}

// Config describes the local node.
type Config struct {
	ID        openlcb.NodeID
	SNIP      openlcb.SNIP
	Protocols openlcb.Protocols
	// Send transmits one frame on the bus. It must not block for long.
	Send   func(can.Frame)
	Logger *slog.Logger

	// CIDWait is how long to listen for objections after sending Check ID
	// frames. The standard says at least 200 ms; tests shorten it.
	CIDWait time.Duration
	// ReplyTimeout bounds how long client operations wait for a reply.
	ReplyTimeout time.Duration
}

// Errors returned by client operations.
var (
	ErrNotRunning = errors.New("node: not running")
	ErrNotFound   = errors.New("node: no such node on the bus")
	ErrTimeout    = errors.New("node: timed out waiting for a reply")
	ErrBusy       = errors.New("node: a request of that kind to that node is already in progress")
)

// EventKind says what an Event reports.
type EventKind uint8

// Event kinds.
const (
	// EventMessage: a message was received (Message is set).
	EventMessage EventKind = iota
	// EventAliasMapped: a node announced its alias (Alias and NodeID set).
	EventAliasMapped
	// EventAliasReset: a node withdrew its alias (Alias set, NodeID if known).
	EventAliasReset
	// EventStateChanged: the local node's State changed (State set).
	EventStateChanged
	// EventDuplicateNodeID: another node is using our node ID (Alias set).
	EventDuplicateNodeID
)

// Event is what observers receive.
type Event struct {
	Kind    EventKind
	Message *openlcb.Message
	Alias   openlcb.Alias
	NodeID  openlcb.NodeID
	State   State
}

// Observer receives events on the node's goroutine; it must not block.
type Observer func(Event)

// DatagramFlags are the options a receiver returns in Datagram OK.
type DatagramFlags struct {
	// ReplyPending tells the sender a datagram reply will follow.
	ReplyPending bool
	// TimeoutExp is the reply timeout as a power of two seconds (0 = none).
	TimeoutExp uint8
}

// DatagramHandler handles a datagram addressed to this node whose first
// payload byte selected it. It returns the Datagram OK flags, or an
// *openlcb.Error whose code is sent in Datagram Rejected. It runs on the
// node's goroutine and must not block.
type DatagramHandler func(msg *openlcb.Message) (DatagramFlags, error)

// DatagramResult is the acknowledgment of a sent datagram.
type DatagramResult struct {
	ReplyPending bool
	// Timeout is how long the receiver asked us to wait for its reply.
	Timeout time.Duration
}

const (
	inQueue          = 1024
	opsQueue         = 64
	housekeepingTick = 250 * time.Millisecond
	resolveThrottle  = 2 * time.Second
)

// Node is the local OpenLCB node.
type Node struct {
	cfg   Config
	log   *slog.Logger
	gen   *can.AliasGenerator
	cache *can.AliasCache
	asm   *can.Assembler

	in      chan can.Frame
	ops     chan func()
	stopped chan struct{}
	dropped atomic.Uint64

	// Loop-owned state.
	alias       openlcb.Alias
	state       State
	cidTimer    *time.Timer
	pending     map[pendingKey]*pendingRequest
	resolving   map[openlcb.Alias]time.Time
	dgHandlers  map[byte]DatagramHandler
	traction    TractionHandler
	collectors  []*collector
	observers   map[int]Observer
	nextObs     int
	initialized chan struct{}

	// Snapshot of loop state for readers on other goroutines.
	snap struct {
		sync.RWMutex
		alias openlcb.Alias
		state State
	}
}

// New builds a node. Call Run to start it.
func New(cfg Config) *Node {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.CIDWait == 0 {
		cfg.CIDWait = 200 * time.Millisecond
	}
	if cfg.ReplyTimeout == 0 {
		cfg.ReplyTimeout = 3 * time.Second
	}
	if cfg.Protocols == 0 {
		cfg.Protocols = openlcb.ProtocolSimple | openlcb.ProtocolDatagram |
			openlcb.ProtocolEventExchange | openlcb.ProtocolSimpleNodeInformation
	}
	n := &Node{
		cfg:         cfg,
		log:         cfg.Logger.With("node", cfg.ID.String()),
		gen:         can.NewAliasGenerator(cfg.ID),
		cache:       can.NewAliasCache(),
		in:          make(chan can.Frame, inQueue),
		ops:         make(chan func(), opsQueue),
		stopped:     make(chan struct{}),
		pending:     make(map[pendingKey]*pendingRequest),
		resolving:   make(map[openlcb.Alias]time.Time),
		dgHandlers:  make(map[byte]DatagramHandler),
		observers:   make(map[int]Observer),
		initialized: make(chan struct{}),
	}
	n.asm = can.NewAssembler(n.cache.Lookup)
	return n
}

// ID returns the node ID.
func (n *Node) ID() openlcb.NodeID { return n.cfg.ID }

// Alias returns the current alias, or 0 while the node has none.
func (n *Node) Alias() openlcb.Alias {
	n.snap.RLock()
	defer n.snap.RUnlock()
	return n.snap.alias
}

// State returns the allocation state.
func (n *Node) State() State {
	n.snap.RLock()
	defer n.snap.RUnlock()
	return n.snap.state
}

// Cache returns the alias cache, which maps aliases seen on the bus to
// node IDs.
func (n *Node) Cache() *can.AliasCache { return n.cache }

// SNIPInfo returns what the node reports about itself.
func (n *Node) SNIPInfo() openlcb.SNIP {
	n.snap.RLock()
	defer n.snap.RUnlock()
	return n.cfg.SNIP
}

// SetUserSNIP changes the user name and description the node reports,
// for when the configuration they live in is written.
func (n *Node) SetUserSNIP(name, description string) {
	n.snap.Lock()
	defer n.snap.Unlock()
	n.cfg.SNIP.UserName = name
	n.cfg.SNIP.UserDescription = description
}

// Dropped counts frames discarded because the input queue was full.
func (n *Node) Dropped() uint64 { return n.dropped.Load() }

// HandleFrame queues a frame received from the bus. It never blocks; a
// full queue drops the frame and counts it.
func (n *Node) HandleFrame(f can.Frame) {
	select {
	case n.in <- f:
	default:
		if d := n.dropped.Add(1); d == 1 || d%1000 == 0 {
			n.log.Warn("input queue full, dropping frames", "dropped", d)
		}
	}
}

// Observe registers an observer and returns a function that removes it.
func (n *Node) Observe(obs Observer) (unobserve func()) {
	n.snap.Lock()
	id := n.nextObs
	n.nextObs++
	n.observers[id] = obs
	n.snap.Unlock()
	return func() {
		n.snap.Lock()
		delete(n.observers, id)
		n.snap.Unlock()
	}
}

// HandleDatagrams registers the handler for datagrams whose first byte is
// contentType (0x20 for memory configuration, for example).
func (n *Node) HandleDatagrams(contentType byte, handler DatagramHandler) {
	n.snap.Lock()
	defer n.snap.Unlock()
	if handler == nil {
		delete(n.dgHandlers, contentType)
	} else {
		n.dgHandlers[contentType] = handler
	}
}

// Run operates the node until ctx ends. On the way out it withdraws its
// alias with an AMR frame.
func (n *Node) Run(ctx context.Context) error {
	defer close(n.stopped)
	housekeeping := time.NewTicker(housekeepingTick)
	defer housekeeping.Stop()
	n.cidTimer = time.NewTimer(time.Hour)
	n.cidTimer.Stop()
	defer n.cidTimer.Stop()

	n.startChecking()
	for {
		select {
		case <-ctx.Done():
			n.shutdown()
			return nil
		case f := <-n.in:
			n.handleFrame(f)
		case op := <-n.ops:
			op()
		case <-n.cidTimer.C:
			n.cidWaitElapsed()
		case now := <-housekeeping.C:
			n.housekeeping(now)
		}
	}
}

// do runs fn on the node goroutine and waits for it to finish.
func (n *Node) do(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	wrapped := func() {
		fn()
		close(done)
	}
	select {
	case n.ops <- wrapped:
	case <-ctx.Done():
		return ctx.Err()
	case <-n.stopped:
		return ErrNotRunning
	}
	select {
	case <-done:
		return nil
	case <-n.stopped:
		return ErrNotRunning
	}
}

func (n *Node) setState(state State) {
	if n.state == state {
		return
	}
	n.state = state
	n.snap.Lock()
	n.snap.state = state
	n.snap.alias = n.alias
	if state == StateInitialized {
		close(n.initialized)
	} else if n.initialized == nil || isClosed(n.initialized) {
		n.initialized = make(chan struct{})
	}
	n.snap.Unlock()
	n.notify(Event{Kind: EventStateChanged, State: state, Alias: n.alias})
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// waitInitialized blocks until the node can send messages.
func (n *Node) waitInitialized(ctx context.Context) error {
	n.snap.RLock()
	ch := n.initialized
	n.snap.RUnlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-n.stopped:
		return ErrNotRunning
	}
}

func (n *Node) notify(ev Event) {
	n.snap.RLock()
	observers := make([]Observer, 0, len(n.observers))
	for _, obs := range n.observers {
		observers = append(observers, obs)
	}
	n.snap.RUnlock()
	for _, obs := range observers {
		obs(ev)
	}
}

func (n *Node) send(f can.Frame) { n.cfg.Send(f) }

// sendMessage frames and transmits a message from this node. The caller
// supplies Dest.Alias for addressed messages.
func (n *Node) sendMessage(msg *openlcb.Message) error {
	if n.state != StateInitialized {
		return fmt.Errorf("node: cannot send %v while %v", msg.MTI, n.state)
	}
	msg.Source = openlcb.NodeHandle{ID: n.cfg.ID, Alias: n.alias}
	frames, err := can.Frames(msg)
	if err != nil {
		return err
	}
	for _, f := range frames {
		n.send(f)
	}
	return nil
}

func (n *Node) sendGlobal(mti openlcb.MTI, payload []byte) {
	if err := n.sendMessage(&openlcb.Message{MTI: mti, Payload: payload}); err != nil {
		n.log.Debug("send failed", "error", err)
	}
}

func (n *Node) sendAddressed(mti openlcb.MTI, dst openlcb.NodeHandle, payload []byte) {
	if err := n.sendMessage(&openlcb.Message{MTI: mti, Dest: dst, Payload: payload}); err != nil {
		n.log.Debug("send failed", "error", err)
	}
}

// --- alias allocation ---------------------------------------------------

func (n *Node) startChecking() {
	n.alias = n.gen.Next()
	n.setState(StateChecking)
	n.log.Debug("checking alias", "alias", n.alias)
	for i := 1; i <= 4; i++ {
		n.send(can.CIDFrame(i, n.cfg.ID, n.alias))
	}
	n.cidTimer.Reset(n.cfg.CIDWait)
}

func (n *Node) cidWaitElapsed() {
	if n.state != StateChecking {
		return
	}
	n.send(can.RIDFrame(n.alias))
	n.send(can.AMDFrame(n.alias, n.cfg.ID))
	n.setState(StateInitialized)
	n.sendGlobal(openlcb.MTIInitializationComplete, n.cfg.ID.AsSlice())
	n.log.Info("node initialized", "alias", n.alias)
}

// aliasConflict handles another node using our alias: give it up and
// start over with the next one.
func (n *Node) aliasConflict(f can.Frame) {
	n.log.Warn("alias conflict, reallocating", "alias", n.alias, "frame", f.String())
	if n.state == StateInitialized {
		n.send(can.AMRFrame(n.alias, n.cfg.ID))
	}
	n.cidTimer.Stop()
	n.failPending(fmt.Errorf("node: alias conflict: %w", ErrTimeout))
	n.setState(StateInhibited)
	n.startChecking()
}

func (n *Node) shutdown() {
	n.cidTimer.Stop()
	if n.state == StateInitialized {
		n.send(can.AMRFrame(n.alias, n.cfg.ID))
	}
	n.failPending(ErrNotRunning)
	n.setState(StateInhibited)
}

// --- inbound frames -----------------------------------------------------

func (n *Node) handleFrame(f can.Frame) {
	if !f.Extended {
		return
	}
	src := f.SourceAlias()
	if n.state != StateInhibited && src == n.alias {
		if f.IsCID() && n.state == StateInitialized {
			n.send(can.RIDFrame(n.alias)) // defend the reserved alias
			return
		}
		n.aliasConflict(f)
		return
	}
	if f.IsControl() {
		n.handleControl(f)
		return
	}
	msg, err := n.asm.Push(f, time.Now())
	if err != nil {
		var ae *can.AssemblyError
		if errors.As(err, &ae) && n.state == StateInitialized {
			n.rejectFrame(f, ae)
		}
		n.log.Debug("frame dropped", "error", err)
		return
	}
	if msg != nil {
		n.handleMessage(msg)
	}
}

// rejectFrame answers a frame that could not be assembled when the
// standard asks for a rejection: datagram fragments out of order get a
// Datagram Rejected if they were for us.
func (n *Node) rejectFrame(f can.Frame, ae *can.AssemblyError) {
	switch f.Type() {
	case can.FrameTypeDatagramFirst, can.FrameTypeDatagramMiddle, can.FrameTypeDatagramLast:
		if f.DatagramDest() == n.alias {
			n.sendAddressed(openlcb.MTIDatagramRejected, n.cache.Handle(f.SourceAlias()), errorPayload(ae.Code))
		}
	}
}

func (n *Node) handleControl(f can.Frame) {
	switch {
	case f.IsCID():
		// Someone else checking an alias that is not ours.
	case f.ControlField() == can.ControlAMD:
		id, ok := f.PayloadNodeID()
		if !ok {
			return
		}
		n.learnAlias(f.SourceAlias(), id)
	case f.ControlField() == can.ControlAMR:
		id, _ := n.cache.Lookup(f.SourceAlias())
		n.cache.Remove(f.SourceAlias())
		n.notify(Event{Kind: EventAliasReset, Alias: f.SourceAlias(), NodeID: id})
	case f.ControlField() == can.ControlAME:
		if n.state != StateInitialized {
			return
		}
		if id, ok := f.PayloadNodeID(); !ok || id == n.cfg.ID {
			n.send(can.AMDFrame(n.alias, n.cfg.ID))
		}
	}
}

// learnAlias records an alias to node ID mapping seen on the bus.
func (n *Node) learnAlias(alias openlcb.Alias, id openlcb.NodeID) {
	if id == n.cfg.ID && alias != n.alias {
		n.log.Error("another node is using our node ID", "alias", alias)
		n.notify(Event{Kind: EventDuplicateNodeID, Alias: alias, NodeID: id})
		return
	}
	old, known := n.cache.Lookup(alias)
	n.cache.Add(alias, id)
	delete(n.resolving, alias)
	n.completeResolve(id, alias)
	if !known || old != id {
		n.notify(Event{Kind: EventAliasMapped, Alias: alias, NodeID: id})
	}
}

func (n *Node) handleMessage(msg *openlcb.Message) {
	switch msg.MTI {
	case openlcb.MTIInitializationComplete, openlcb.MTIInitializationCompleteSimple,
		openlcb.MTIVerifiedNodeID, openlcb.MTIVerifiedNodeIDSimple:
		if id, ok := msg.PayloadNodeID(); ok {
			n.learnAlias(msg.Source.Alias, id)
			msg.Source.ID = id
		}
	}
	if msg.Source.ID == 0 {
		n.resolveUnknownAlias(msg.Source.Alias)
	}
	n.notify(Event{Kind: EventMessage, Message: msg})

	if n.state != StateInitialized {
		return
	}
	if msg.IsGlobal() {
		n.handleGlobal(msg)
		return
	}
	if msg.Dest.Alias != n.alias {
		return
	}
	n.handleAddressed(msg)
}

func (n *Node) handleGlobal(msg *openlcb.Message) {
	switch msg.MTI {
	case openlcb.MTIVerifyNodeIDGlobal:
		if id, ok := msg.PayloadNodeID(); !ok || id == n.cfg.ID {
			n.sendGlobal(openlcb.MTIVerifiedNodeID, n.cfg.ID.AsSlice())
		}
	case openlcb.MTIVerifiedNodeID, openlcb.MTIVerifiedNodeIDSimple:
		n.completeRequest(pendingKey{kind: pendVerify, alias: msg.Source.Alias}, msg, nil)
	case openlcb.MTIProducerIdentifiedValid:
		n.deliverProducer(msg, "valid")
	case openlcb.MTIProducerIdentifiedInvalid:
		n.deliverProducer(msg, "invalid")
	case openlcb.MTIProducerIdentifiedUnknown:
		n.deliverProducer(msg, "unknown")
	case openlcb.MTIIdentifyEventsGlobal:
		// Interlock registers no events of its own.
	}
}

func (n *Node) handleAddressed(msg *openlcb.Message) {
	switch msg.MTI {
	case openlcb.MTIVerifyNodeIDAddressed:
		n.sendGlobal(openlcb.MTIVerifiedNodeID, n.cfg.ID.AsSlice())
	case openlcb.MTIProtocolSupportInquiry:
		n.sendAddressed(openlcb.MTIProtocolSupportReply, msg.Source, n.cfg.Protocols.Bytes())
	case openlcb.MTISimpleNodeIdentInfoRequest:
		n.sendAddressed(openlcb.MTISimpleNodeIdentInfoReply, msg.Source, n.SNIPInfo().Encode())
	case openlcb.MTIIdentifyEventsAddressed:
		// Interlock registers no events of its own.
	case openlcb.MTIDatagram:
		n.handleDatagram(msg)
	case openlcb.MTIDatagramOK, openlcb.MTIDatagramRejected:
		n.completeRequest(pendingKey{kind: pendDatagram, alias: msg.Source.Alias}, msg, nil)
	case openlcb.MTIProtocolSupportReply:
		n.completeRequest(pendingKey{kind: pendPIP, alias: msg.Source.Alias}, msg, nil)
	case openlcb.MTISimpleNodeIdentInfoReply:
		n.completeRequest(pendingKey{kind: pendSNIP, alias: msg.Source.Alias}, msg, nil)
	case openlcb.MTIOptionalInteractionRejected, openlcb.MTITerminateDueToError:
		n.handleRejection(msg)
	case openlcb.MTITractionControlReply:
		n.completeRequest(pendingKey{kind: pendTraction, alias: msg.Source.Alias, code: tractionCode(msg.Payload)}, msg, nil)
	case openlcb.MTITractionControlCommand:
		n.handleTractionCommand(msg)
	default:
		n.sendAddressed(openlcb.MTIOptionalInteractionRejected, msg.Source,
			append(errorPayload(openlcb.ErrCodeNotImplementedMTI), byte(msg.MTI>>8), byte(msg.MTI)))
	}
}

// handleRejection routes an Optional Interaction Rejected or Terminate Due
// To Error to the request it answers, identified by the MTI in its payload.
func (n *Node) handleRejection(msg *openlcb.Message) {
	if len(msg.Payload) < 4 {
		return
	}
	code := openlcb.ErrorCode(uint16(msg.Payload[0])<<8 | uint16(msg.Payload[1]))
	rejected := openlcb.MTI(uint16(msg.Payload[2])<<8 | uint16(msg.Payload[3]))
	err := &openlcb.Error{Code: code, Op: rejected.String() + " to " + msg.Source.String()}
	var kind pendingKind
	switch rejected {
	case openlcb.MTISimpleNodeIdentInfoRequest:
		kind = pendSNIP
	case openlcb.MTIProtocolSupportInquiry:
		kind = pendPIP
	case openlcb.MTIDatagram:
		kind = pendDatagram
	case openlcb.MTIVerifyNodeIDAddressed:
		kind = pendVerify
	case openlcb.MTITractionControlCommand:
		n.failTraction(msg.Source.Alias, err)
		return
	default:
		return
	}
	n.completeRequest(pendingKey{kind: kind, alias: msg.Source.Alias}, nil, err)
}

func (n *Node) handleDatagram(msg *openlcb.Message) {
	if len(msg.Payload) == 0 {
		n.sendAddressed(openlcb.MTIDatagramRejected, msg.Source, errorPayload(openlcb.ErrCodeInvalidArguments))
		return
	}
	n.snap.RLock()
	handler := n.dgHandlers[msg.Payload[0]]
	n.snap.RUnlock()
	if handler == nil {
		n.sendAddressed(openlcb.MTIDatagramRejected, msg.Source, errorPayload(openlcb.ErrCodeNotImplementedDatagram))
		return
	}
	flags, err := handler(msg)
	if err != nil {
		code := openlcb.ErrCodePermanent
		var oerr *openlcb.Error
		if errors.As(err, &oerr) {
			code = oerr.Code
		}
		n.sendAddressed(openlcb.MTIDatagramRejected, msg.Source, errorPayload(code))
		return
	}
	var payload []byte
	if flags.ReplyPending || flags.TimeoutExp != 0 {
		b := flags.TimeoutExp & 0x0F
		if flags.ReplyPending {
			b |= 0x80
		}
		payload = []byte{b}
	}
	n.sendAddressed(openlcb.MTIDatagramOK, msg.Source, payload)
}

func errorPayload(code openlcb.ErrorCode) []byte {
	return []byte{byte(code >> 8), byte(code)}
}

// resolveUnknownAlias asks a node we have not identified who it is, so the
// monitor and registry can show node IDs instead of aliases. One query per
// alias per throttle period.
func (n *Node) resolveUnknownAlias(alias openlcb.Alias) {
	if n.state != StateInitialized || alias == 0 {
		return
	}
	if _, ok := n.cache.Lookup(alias); ok {
		return
	}
	if last, ok := n.resolving[alias]; ok && time.Since(last) < resolveThrottle {
		return
	}
	n.resolving[alias] = time.Now()
	n.sendAddressed(openlcb.MTIVerifyNodeIDAddressed, openlcb.NodeHandle{Alias: alias}, nil)
}

func (n *Node) housekeeping(now time.Time) {
	n.asm.Expire(now)
	for alias, t := range n.resolving {
		if now.Sub(t) > resolveThrottle {
			delete(n.resolving, alias)
		}
	}
	for key, req := range n.pending {
		if now.After(req.deadline) {
			delete(n.pending, key)
			req.finish(nil, ErrTimeout)
		}
	}
}
