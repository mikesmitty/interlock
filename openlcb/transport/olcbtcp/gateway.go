package olcbtcp

import (
	"errors"
	"log/slog"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
)

// A Gateway stands between one TCP connection, which carries whole
// messages with full node IDs, and the CAN side of the hub, which carries
// frames with aliases. For every node that speaks on the TCP side it
// allocates and defends an alias on CAN (a proxy), the way the node would
// if it were on the bus itself; messages from CAN are assembled, their
// aliases resolved to node IDs, and passed to TCP.
//
// A gateway is driven by one goroutine, the link's: its methods are not
// safe for concurrent use.
type Gateway struct {
	id    openlcb.NodeID
	toCAN func(can.Frame)
	toTCP func(*openlcb.Message)
	log   *slog.Logger
	wait  time.Duration

	cache   *can.AliasCache // nodes on the CAN side
	asm     *can.Assembler
	proxies map[openlcb.NodeID]*proxy
	byAlias map[openlcb.Alias]*proxy
	held    []held
	asked   map[openlcb.Alias]time.Time // aliases we sent an AME for
	askedID map[openlcb.NodeID]time.Time

	// Dropped counts messages that could not be delivered either way.
	Dropped uint64
}

// Options configure a Gateway.
type Options struct {
	// ID is this gateway's node ID, written into the frames it sends.
	ID openlcb.NodeID
	// ToCAN sends a frame onto the CAN side; ToTCP a message onto TCP.
	// Both are called from the gateway's goroutine and must not block.
	ToCAN func(can.Frame)
	ToTCP func(*openlcb.Message)
	// CIDWait is how long a proxy's alias is checked before it is
	// reserved; the standard says at least 200 ms.
	CIDWait time.Duration
	Logger  *slog.Logger
}

type proxyState uint8

const (
	proxyChecking proxyState = iota
	proxyReady
)

// proxy is a TCP-side node's presence on CAN.
type proxy struct {
	id    openlcb.NodeID
	alias openlcb.Alias
	gen   *can.AliasGenerator
	state proxyState
	until time.Time
	queue []*openlcb.Message
}

// held is a message waiting for an alias or node ID to be learned.
type held struct {
	msg      *openlcb.Message
	fromTCP  bool
	deadline time.Time
}

const (
	holdFor      = time.Second
	askThrottle  = time.Second
	maxQueue     = 64
	maxHeld      = 256
	proxyRetries = 16
)

// NewGateway returns a gateway with no proxies.
func NewGateway(o Options) *Gateway {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.CIDWait == 0 {
		o.CIDWait = 200 * time.Millisecond
	}
	g := &Gateway{
		id: o.ID, toCAN: o.ToCAN, toTCP: o.ToTCP, log: o.Logger, wait: o.CIDWait,
		cache:   can.NewAliasCache(),
		proxies: make(map[openlcb.NodeID]*proxy),
		byAlias: make(map[openlcb.Alias]*proxy),
		asked:   make(map[openlcb.Alias]time.Time),
		askedID: make(map[openlcb.NodeID]time.Time),
	}
	g.asm = can.NewAssembler(g.resolve)
	return g
}

// resolve finds the node ID behind an alias on either side.
func (g *Gateway) resolve(alias openlcb.Alias) (openlcb.NodeID, bool) {
	if p, ok := g.byAlias[alias]; ok {
		return p.id, true
	}
	return g.cache.Lookup(alias)
}

// Proxies reports how many TCP-side nodes the gateway represents on CAN.
func (g *Gateway) Proxies() int { return len(g.proxies) }

// Cache returns the CAN-side alias table.
func (g *Gateway) Cache() *can.AliasCache { return g.cache }

// --- CAN to TCP -------------------------------------------------------------

// HandleFrame takes a frame from the CAN side.
func (g *Gateway) HandleFrame(f can.Frame, now time.Time) {
	if !f.Extended {
		return
	}
	src := f.SourceAlias()
	if p, ok := g.byAlias[src]; ok {
		// Someone else is using an alias of ours.
		if f.IsCID() && p.state == proxyReady {
			g.toCAN(can.RIDFrame(p.alias)) // defend it
			return
		}
		g.conflict(p, now)
		return
	}
	if f.IsControl() {
		g.handleControl(f, now)
		return
	}
	msg, err := g.asm.Push(f, now)
	if err != nil {
		var ae *can.AssemblyError
		if errors.As(err, &ae) {
			g.Dropped++
		}
		return
	}
	if msg != nil {
		g.fromCAN(msg, now)
	}
}

func (g *Gateway) handleControl(f can.Frame, now time.Time) {
	switch f.ControlField() {
	case can.ControlAMD:
		if id, ok := f.PayloadNodeID(); ok {
			g.learn(f.SourceAlias(), id, now)
		}
	case can.ControlAMR:
		g.cache.Remove(f.SourceAlias())
	case can.ControlAME:
		id, ok := f.PayloadNodeID()
		for _, p := range g.proxies {
			if p.state == proxyReady && (!ok || id == p.id) {
				g.toCAN(can.AMDFrame(p.alias, p.id))
			}
		}
	}
}

// learn records a CAN-side alias and releases messages waiting on it.
func (g *Gateway) learn(alias openlcb.Alias, id openlcb.NodeID, now time.Time) {
	if p, ok := g.proxies[id]; ok {
		g.log.Warn("a node on CAN uses a node ID that is also on TCP", "node", id, "alias", alias, "proxy_alias", p.alias)
		return
	}
	g.cache.Add(alias, id)
	delete(g.asked, alias)
	delete(g.askedID, id)
	g.release(now)
}

// fromCAN forwards an assembled message to TCP once its node IDs are
// known.
func (g *Gateway) fromCAN(msg *openlcb.Message, now time.Time) {
	switch msg.MTI {
	case openlcb.MTIInitializationComplete, openlcb.MTIInitializationCompleteSimple,
		openlcb.MTIVerifiedNodeID, openlcb.MTIVerifiedNodeIDSimple:
		if id, ok := msg.PayloadNodeID(); ok && msg.Source.ID == 0 {
			g.learn(msg.Source.Alias, id, now)
			msg.Source.ID = id
		}
	}
	if msg.MTI.IsAddressed() {
		p, ok := g.byAlias[msg.Dest.Alias]
		if !ok {
			return // for a node on the CAN side; TCP does not need it
		}
		msg.Dest.ID = p.id
	}
	if msg.Source.ID == 0 {
		g.ask(msg.Source.Alias, now)
		g.hold(msg, false, now)
		return
	}
	g.toTCP(msg)
}

// ask sends an Alias Map Enquiry for an alias we do not know, at most
// once a second.
func (g *Gateway) ask(alias openlcb.Alias, now time.Time) {
	if t, ok := g.asked[alias]; ok && now.Sub(t) < askThrottle {
		return
	}
	g.asked[alias] = now
	from := g.anyAlias()
	if from == 0 {
		return
	}
	g.toCAN(can.AMEFrame(from, 0)) // with no node ID, every node answers
}

// askID asks the node with id to announce its alias.
func (g *Gateway) askID(id openlcb.NodeID, from openlcb.Alias, now time.Time) {
	if t, ok := g.askedID[id]; ok && now.Sub(t) < askThrottle {
		return
	}
	g.askedID[id] = now
	g.toCAN(can.AMEFrame(from, id))
}

// anyAlias returns an alias the gateway may send control frames from.
func (g *Gateway) anyAlias() openlcb.Alias {
	for _, p := range g.proxies {
		if p.state == proxyReady {
			return p.alias
		}
	}
	return 0
}

func (g *Gateway) hold(msg *openlcb.Message, fromTCP bool, now time.Time) {
	if len(g.held) >= maxHeld {
		g.held = g.held[1:]
		g.Dropped++
	}
	g.held = append(g.held, held{msg: msg, fromTCP: fromTCP, deadline: now.Add(holdFor)})
}

// release retries held messages after something was learned.
func (g *Gateway) release(now time.Time) {
	pending := g.held
	g.held = nil
	for _, h := range pending {
		if h.fromTCP {
			g.HandleMessage(h.msg, now)
		} else {
			g.fromCAN(h.msg, now)
		}
	}
}

// --- TCP to CAN -------------------------------------------------------------

// HandleMessage takes a message from the TCP side.
func (g *Gateway) HandleMessage(msg *openlcb.Message, now time.Time) {
	src := msg.Source.ID
	if src == 0 || src == g.id {
		return
	}
	if _, onCAN := g.cache.LookupID(src); onCAN {
		return // our own CAN traffic coming back through a TCP hub
	}
	p := g.proxyFor(src, now)
	if msg.MTI.IsAddressed() {
		if _, ok := g.proxies[msg.Dest.ID]; ok {
			return // between two TCP nodes; the TCP side delivers it
		}
		alias, ok := g.cache.LookupID(msg.Dest.ID)
		if !ok {
			if p.state == proxyReady {
				g.askID(msg.Dest.ID, p.alias, now)
			}
			g.hold(msg, true, now)
			return
		}
		msg.Dest.Alias = alias
	}
	if p.state != proxyReady {
		if len(p.queue) >= maxQueue {
			p.queue = p.queue[1:]
			g.Dropped++
		}
		p.queue = append(p.queue, msg)
		return
	}
	g.sendFrom(p, msg)
}

func (g *Gateway) sendFrom(p *proxy, msg *openlcb.Message) {
	msg.Source.Alias = p.alias
	frames, err := can.Frames(msg)
	if err != nil {
		g.log.Debug("message cannot go on CAN", "error", err)
		g.Dropped++
		return
	}
	for _, f := range frames {
		g.toCAN(f)
	}
}

// proxyFor returns the proxy for a TCP-side node, starting its alias
// allocation when it is new.
func (g *Gateway) proxyFor(id openlcb.NodeID, now time.Time) *proxy {
	if p, ok := g.proxies[id]; ok {
		return p
	}
	p := &proxy{id: id, gen: can.NewAliasGenerator(id)}
	g.proxies[id] = p
	g.check(p, now)
	return p
}

// check picks the next free alias for p and starts checking it.
func (g *Gateway) check(p *proxy, now time.Time) {
	for range proxyRetries {
		alias := p.gen.Next()
		if _, taken := g.byAlias[alias]; taken {
			continue
		}
		if _, taken := g.cache.Lookup(alias); taken {
			continue
		}
		p.alias = alias
		break
	}
	g.byAlias[p.alias] = p
	p.state = proxyChecking
	p.until = now.Add(g.wait)
	for i := 1; i <= 4; i++ {
		g.toCAN(can.CIDFrame(i, p.id, p.alias))
	}
	g.log.Debug("checking an alias for a TCP node", "node", p.id, "alias", p.alias)
}

// conflict gives up an alias someone else used.
func (g *Gateway) conflict(p *proxy, now time.Time) {
	g.log.Info("alias conflict for a TCP node", "node", p.id, "alias", p.alias)
	if p.state == proxyReady {
		g.toCAN(can.AMRFrame(p.alias, p.id))
	}
	delete(g.byAlias, p.alias)
	g.check(p, now)
}

// reserve finishes a proxy's allocation and sends what waited for it.
func (g *Gateway) reserve(p *proxy, now time.Time) {
	g.toCAN(can.RIDFrame(p.alias))
	g.toCAN(can.AMDFrame(p.alias, p.id))
	p.state = proxyReady
	g.log.Info("TCP node joined the CAN side", "node", p.id, "alias", p.alias)
	queue := p.queue
	p.queue = nil
	for _, msg := range queue {
		g.HandleMessage(msg, now)
	}
}

// Tick advances timers: alias checks that are over, held messages that
// have waited too long, partial messages that will never finish.
func (g *Gateway) Tick(now time.Time) {
	for _, p := range g.proxies {
		if p.state == proxyChecking && !now.Before(p.until) {
			g.reserve(p, now)
		}
	}
	kept := g.held[:0]
	for _, h := range g.held {
		if now.Before(h.deadline) {
			kept = append(kept, h)
		} else {
			g.Dropped++
		}
	}
	g.held = kept
	g.asm.Expire(now)
}

// Close withdraws every proxy's alias, for when the TCP side went away.
func (g *Gateway) Close() {
	for _, p := range g.proxies {
		if p.state == proxyReady {
			g.toCAN(can.AMRFrame(p.alias, p.id))
		}
		delete(g.byAlias, p.alias)
	}
	g.proxies = make(map[openlcb.NodeID]*proxy)
	g.held = nil
}
