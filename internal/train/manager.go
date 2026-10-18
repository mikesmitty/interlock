package train

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Bus is what the manager needs from Interlock's node.
type Bus interface {
	ID() openlcb.NodeID
	Send(ctx context.Context, msg openlcb.Message) error
	SendEvent(ctx context.Context, id openlcb.EventID) error
	TractionCommand(ctx context.Context, train openlcb.NodeID, payload []byte, reply bool) ([]byte, error)
	Producers(ctx context.Context, ev openlcb.EventID, window time.Duration, alsoReport bool) ([]node.Producer, error)
	VerifyAlias(ctx context.Context, alias openlcb.Alias) (openlcb.NodeID, error)
	HandleTraction(node.TractionHandler)
}

// Nodes is the registry; it may be nil.
type Nodes interface {
	Get(id openlcb.NodeID) (registry.Info, bool)
}

// Options configure the manager.
type Options struct {
	Bus   Bus
	Nodes Nodes
	// File is trains.yaml; nil keeps the roster in memory.
	File   *File
	Logger *slog.Logger
	// SearchWindow is how long a search waits for answers; one second
	// when zero.
	SearchWindow time.Duration
	// Functions is how many functions are read back from a train after
	// taking control; 29 (F0 to F28) when zero.
	Functions int
	// CVs keeps the roster's decoder CV values; nil keeps none.
	CVs *CVStore
}

// Subscriber is told whenever a train changes.
type Subscriber func(Train)

// LayoutSubscriber is told whenever the layout state changes.
type LayoutSubscriber func(Layout)

// queryTimeout bounds each read-back query so a train that does not
// answer one does not hold a throttle for long.
const queryTimeout = 750 * time.Millisecond

// powerQueryDelay gathers the nodes that come onto the bus together, as
// at start, into one question about track power.
const powerQueryDelay = 500 * time.Millisecond

// train is a locomotive address on the bus: the train node there and
// what Interlock set or read back. Its Entry is the bare address; roster
// entries at the address share the train.
type train struct {
	Train
	on map[int]bool
	// acquire serializes finding the node and taking control.
	acquire sync.Mutex
}

// Manager drives the trains.
type Manager struct {
	opts   Options
	log    *slog.Logger
	window time.Duration
	fns    int

	mu sync.RWMutex
	// trains are the addresses driven or seen, by address ID; every
	// roster entry has one.
	trains map[string]*train
	// roster is the roster entries by ID.
	roster map[string]Entry
	layout Layout
	// stations is whether track power is on as each command station or
	// booster last said, by node; nodes that do not say are left out.
	stations map[openlcb.NodeID]bool
	// online is which other nodes are on the bus, to ask about track
	// power when one arrives.
	online     map[openlcb.NodeID]bool
	asking     bool
	subs       map[int]Subscriber
	layoutSubs map[int]LayoutSubscriber
	nextSub    int

	// consists are the consists by ID.
	consists map[string]Consist
	// consistNode is the lead's train node each made-up consist was put
	// together on, by consist ID; a consist missing here has to be put
	// together again before it is driven.
	consistNode map[string]openlcb.NodeID
	// consistProblem says why a consist could not be put together.
	consistProblem map[string]string
	consistSubs    map[int]ConsistSubscriber
	// consistOp serializes putting consists together and taking them
	// apart; it is taken before any train's acquire.
	consistOp sync.Mutex
}

// New builds a manager over opts, loading the roster, and installs itself
// as the node's handler for Traction Control Commands.
func New(opts Options) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	m := &Manager{
		opts:       opts,
		log:        opts.Logger,
		window:     opts.SearchWindow,
		fns:        opts.Functions,
		trains:     make(map[string]*train),
		roster:     make(map[string]Entry),
		stations:   make(map[openlcb.NodeID]bool),
		online:     make(map[openlcb.NodeID]bool),
		subs:       make(map[int]Subscriber),
		layoutSubs: make(map[int]LayoutSubscriber),

		consists:       make(map[string]Consist),
		consistNode:    make(map[string]openlcb.NodeID),
		consistProblem: make(map[string]string),
		consistSubs:    make(map[int]ConsistSubscriber),
	}
	if m.window <= 0 {
		m.window = time.Second
	}
	if m.fns <= 0 {
		m.fns = 29
	}
	for _, e := range opts.File.List() {
		m.roster[e.ID] = e.clone()
		m.trainAtLocked(e.Address, e.Long)
	}
	m.loadConsists()
	m.opts.Bus.HandleTraction(m.handleTraction)
	return m
}

// trainAtLocked returns the train at an address, making it if need be;
// the caller holds m.mu.
func (m *Manager) trainAtLocked(address uint16, long bool) *train {
	key := IDFor(address, long)
	if tr, ok := m.trains[key]; ok {
		return tr
	}
	e := Entry{Address: address, Long: long}.Normalize()
	e.ID = key
	tr := &train{Train: Train{Entry: e, Direction: Forward, On: []int{}, Updated: time.Now()}, on: make(map[int]bool)}
	m.trains[key] = tr
	return tr
}

// entriesAtLocked lists the roster entries at a train's address by name.
func (m *Manager) entriesAtLocked(key string) []Entry {
	var out []Entry
	for _, e := range m.roster {
		if e.key() == key {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// viewLocked is a roster entry with its train's state.
func (m *Manager) viewLocked(e Entry) Train {
	t := Train{Direction: Forward, On: []int{}}
	if tr, ok := m.trains[e.key()]; ok {
		t = tr.Train
	}
	t.Entry = e.clone()
	t.Roster = true
	if c, _, ok := m.madeUpAtLocked(e.key(), ""); ok {
		t.Consist = c.ID
	}
	return t
}

// viewsLocked is how a train shows: once for each roster entry at its
// address, or by its address when there is none.
func (m *Manager) viewsLocked(tr *train) []Train {
	es := m.entriesAtLocked(tr.ID)
	if len(es) == 0 {
		return []Train{tr.Train}
	}
	out := make([]Train, len(es))
	for i, e := range es {
		out[i] = m.viewLocked(e)
	}
	return out
}

// viewForLocked is the view of tr that id asked for.
func (m *Manager) viewForLocked(id string, tr *train) Train {
	if e, ok := m.roster[id]; ok {
		return m.viewLocked(e)
	}
	return m.viewsLocked(tr)[0]
}

func (m *Manager) viewFor(id string, tr *train) Train {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.viewForLocked(id, tr)
}

// --- following the bus ---------------------------------------------------

// HandleNode follows the registry: train nodes coming and going, and the
// other nodes, which may say whether track power is on.
func (m *Manager) HandleNode(info registry.Info) {
	if _, _, ok := traction.DCCAddress(info.ID); !ok {
		m.followNode(info)
		return
	}
	m.mu.Lock()
	var changed *train
	for _, tr := range m.trains {
		if tr.Node != info.ID {
			continue
		}
		online := info.Online && !info.Forgotten
		if tr.Online != online {
			tr.Online = online
			if !online {
				tr.Controlled = false
				m.forgetAttachedLocked(tr)
			}
			tr.Updated = time.Now()
			changed = tr
		}
		break
	}
	m.mu.Unlock()
	if changed != nil {
		m.publish(changed)
	}
}

// HandleMessage watches the bus for the emergency events, and for command
// stations saying whether track power is on: Emergency Off is valid at a
// station whose power is off, Clear Emergency Off at one whose power is
// on. It runs on the node goroutine and must not block.
func (m *Manager) HandleMessage(msg *openlcb.Message) {
	ev, ok := msg.EventID()
	if !ok {
		return
	}
	switch msg.MTI {
	case openlcb.MTIEventReport:
	case openlcb.MTIConsumerIdentifiedValid, openlcb.MTIConsumerIdentifiedInvalid:
		valid := msg.MTI == openlcb.MTIConsumerIdentifiedValid
		switch ev {
		case openlcb.EventEmergencyOff:
			m.powerReported(msg.Source.ID, !valid)
		case openlcb.EventClearEmergencyOff:
			m.powerReported(msg.Source.ID, valid)
		}
		return
	default:
		return
	}
	switch ev {
	case openlcb.EventEmergencyOff:
		m.powerSwitched(false)
	case openlcb.EventClearEmergencyOff:
		m.powerSwitched(true)
	case openlcb.EventEmergencyStop:
		m.setLayout(func(l *Layout) { l.EmergencyStop = true })
		m.stopAll()
	case openlcb.EventClearEmergencyStop:
		m.setLayout(func(l *Layout) { l.EmergencyStop = false })
	}
}

// handleTraction answers Traction Control Commands sent to Interlock: a
// train telling us another controller wants it, which we allow, and
// commands forwarded to us as a listener, which update the train's state.
// It runs on the node goroutine.
func (m *Manager) handleTraction(src openlcb.NodeHandle, payload []byte) []byte {
	c, err := traction.ParseCommand(payload)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	var tr *train
	for _, t := range m.trains {
		if t.Node != 0 && t.Node == src.ID {
			tr = t
			break
		}
	}
	if tr == nil {
		m.mu.Unlock()
		if c.Kind == traction.CmdController && c.Sub == traction.ControllerChanged {
			return traction.ControllerChangedReply(true)
		}
		return nil
	}
	var reply []byte
	changed := true
	switch {
	case c.Kind == traction.CmdController && c.Sub == traction.ControllerChanged:
		tr.Controlled = false
		tr.Controller = c.Node
		reply = traction.ControllerChangedReply(true)
		m.log.Info("another throttle took a train", "train", tr.ID, "controller", c.Node)
	case c.Listener && c.Kind == traction.CmdSetSpeed:
		tr.setSpeed(c.Speed)
	case c.Listener && c.Kind == traction.CmdSetFunction:
		tr.setFunction(int(c.Function), c.Value != 0)
	case c.Listener && c.Kind == traction.CmdEmergencyStop:
		tr.EStop, tr.Speed = true, 0
	default:
		changed = false
	}
	if changed {
		tr.Updated = time.Now()
	}
	m.mu.Unlock()
	if changed {
		go m.publish(tr)
	}
	return reply
}

func (t *train) setSpeed(s traction.Speed) {
	t.Speed = s.Value
	t.Direction = Forward
	if s.Reverse {
		t.Direction = Reverse
	}
	t.EStop = false
}

func (t *train) setFunction(fn int, on bool) {
	if on {
		t.on[fn] = true
	} else {
		delete(t.on, fn)
	}
	t.On = make([]int, 0, len(t.on))
	for k := range t.on {
		t.On = append(t.On, k)
	}
	sort.Ints(t.On)
}

// --- the roster ------------------------------------------------------------

// List returns every train, roster entries first, by name.
func (m *Manager) List() []Train {
	m.mu.RLock()
	out := make([]Train, 0, len(m.roster)+len(m.trains))
	for _, e := range m.roster {
		out = append(out, m.viewLocked(e))
	}
	for key, tr := range m.trains {
		if len(m.entriesAtLocked(key)) == 0 {
			out = append(out, tr.Train)
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Roster != out[j].Roster {
			return out[i].Roster
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one train by roster ID, or by address.
func (m *Manager) Get(id string) (Train, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.roster[id]; ok {
		return m.viewLocked(e), true
	}
	address, long, ok := ParseID(id)
	if !ok {
		return Train{}, false
	}
	tr, ok := m.trains[IDFor(address, long)]
	if !ok {
		return Train{}, false
	}
	return m.viewsLocked(tr)[0], true
}

// Entry returns a roster entry by ID.
func (m *Manager) Entry(id string) (Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.roster[id]
	return e.clone(), ok
}

// lookupLocked finds the train id names: a roster entry's, or the one at
// an address.
func (m *Manager) lookupLocked(id string) (*train, bool) {
	if e, ok := m.roster[id]; ok {
		return m.trains[e.key()], true
	}
	if address, long, ok := ParseID(id); ok {
		tr, ok := m.trains[IDFor(address, long)]
		return tr, ok
	}
	return nil, false
}

// get finds a train, creating one outside the roster when id names an
// address Interlock has not met.
func (m *Manager) get(id string) (*train, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tr, ok := m.lookupLocked(id); ok {
		return tr, nil
	}
	address, long, ok := ParseID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	tr := m.trainAtLocked(address, long)
	go m.publish(tr)
	return tr, nil
}

// newIDLocked makes a roster ID from a name. It never looks like an
// address, so addresses and roster IDs cannot be confused.
func (m *Manager) newIDLocked(name string) string {
	base := slug(name)
	if base == "" {
		base = "locomotive"
	}
	if _, _, ok := ParseID(base); ok {
		base = "locomotive-" + base
	}
	return uniqueID(base, func(id string) bool {
		_, taken := m.roster[id]
		_, _, address := ParseID(id)
		return taken || address
	})
}

// newDecoderIDLocked makes a decoder ID, unique in the roster, from a name.
func (m *Manager) newDecoderIDLocked(name string, also []Decoder) string {
	base := slug(name)
	if base == "" || reservedNames.MatchString(base) {
		base = strings.TrimPrefix(base+"-decoder", "-")
	}
	return uniqueID(base, func(id string) bool {
		for _, d := range also {
			if d.ID == id {
				return true
			}
		}
		return m.decoderOwnerLocked(id) != ""
	})
}

// decoderOwnerLocked returns the ID of the entry with a decoder, if any.
func (m *Manager) decoderOwnerLocked(decoder string) string {
	for id, e := range m.roster {
		if _, ok := e.Decoder(decoder); ok {
			return id
		}
	}
	return ""
}

// Create adds a locomotive to the roster under a new ID made from its
// name. A train already driven at its address keeps its state. Decoders
// without an ID get one.
func (m *Manager) Create(e Entry) (Train, error) {
	e = e.clone().Normalize()
	m.mu.Lock()
	e.ID = m.newIDLocked(e.Name)
	for i := range e.Decoders {
		if e.Decoders[i].ID == "" {
			e.Decoders[i].ID = m.newDecoderIDLocked(e.Decoders[i].Title(), e.Decoders)
		} else if owner := m.decoderOwnerLocked(e.Decoders[i].ID); owner != "" {
			m.mu.Unlock()
			return Train{}, invalid("the decoder %s already belongs to %s", e.Decoders[i].ID, m.roster[owner].Name)
		}
	}
	if err := e.Validate(); err != nil {
		m.mu.Unlock()
		return Train{}, err
	}
	key := e.key()
	_, existed := m.trains[key]
	bare := existed && len(m.entriesAtLocked(key)) == 0
	tr := m.trainAtLocked(e.Address, e.Long)
	var hidden Train
	if bare {
		hidden = tr.Train
	}
	m.roster[e.ID] = e
	view := m.viewLocked(e)
	m.mu.Unlock()
	if err := m.opts.File.Put(e); err != nil {
		return view, err
	}
	if bare {
		// The train driven by its address is the roster entry's now.
		hidden.Removed = true
		m.send(hidden)
	}
	m.send(view)
	return view, nil
}

// Update changes a roster entry: change edits a copy, which is checked
// and saved. A new address moves the entry to the train there; an entry
// known by its address gets an ID of its own then.
func (m *Manager) Update(ctx context.Context, id string, change func(*Entry) error) (Train, error) {
	return m.edit(ctx, id, func(e *Entry) error {
		decoders, installed := e.Decoders, e.Installed
		if err := change(e); err != nil {
			return err
		}
		// Decoders change through their own methods.
		e.Decoders, e.Installed = decoders, installed
		return nil
	})
}

// edit applies a change to a roster entry and settles what follows from
// it: the ID, the train the entry drives, the file and the subscribers.
func (m *Manager) edit(ctx context.Context, id string, change func(*Entry) error) (Train, error) {
	m.mu.Lock()
	old, ok := m.roster[id]
	if !ok {
		m.mu.Unlock()
		return Train{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next := old.clone()
	if err := change(&next); err != nil {
		m.mu.Unlock()
		return Train{}, err
	}
	next.ID = old.ID
	next = next.Normalize()
	if _, _, address := ParseID(next.ID); address && next.key() != next.ID {
		next.ID = m.newIDLocked(next.Name)
	}
	if err := next.Validate(); err != nil {
		m.mu.Unlock()
		return Train{}, err
	}
	for _, d := range next.Decoders {
		if owner := m.decoderOwnerLocked(d.ID); owner != "" && owner != old.ID {
			m.mu.Unlock()
			return Train{}, invalid("the decoder %s already belongs to %s", d.ID, m.roster[owner].Name)
		}
	}
	if c, _, ok := m.madeUpAtLocked(old.key(), ""); ok && next.key() != old.key() {
		m.mu.Unlock()
		return Train{}, invalid("%s runs in %s; break that up before changing its address", old.Name, c.Name)
	}
	delete(m.roster, old.ID)
	m.roster[next.ID] = next
	var consists []Consist
	if next.ID != old.ID {
		consists = m.renameMemberLocked(old.ID, next.ID)
	}
	m.trainAtLocked(next.Address, next.Long)
	// A train no entry drives any more goes, as a deleted entry's does.
	var orphan *train
	if oldKey := old.key(); oldKey != next.key() && len(m.entriesAtLocked(oldKey)) == 0 {
		orphan = m.trains[oldKey]
		delete(m.trains, oldKey)
	}
	view := m.viewLocked(next)
	m.mu.Unlock()
	if err := m.opts.File.Replace(old.ID, next); err != nil {
		return view, err
	}
	if err := m.saveConsists(consists); err != nil {
		return view, err
	}
	if orphan != nil {
		m.release(ctx, orphan)
	}
	if next.ID != old.ID {
		gone := view
		gone.Entry = old
		gone.Removed = true
		m.send(gone)
	}
	m.send(view)
	return view, nil
}

// Delete removes a train: a roster entry with its decoders' CV values, or
// a train driven outside the roster. The train is released when no other
// entry drives it.
func (m *Manager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	tr, ok := m.lookupLocked(id)
	if !ok || tr == nil {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	e, inRoster := m.roster[id]
	if !inRoster && len(m.entriesAtLocked(tr.ID)) > 0 {
		m.mu.Unlock()
		return invalid("locomotives in the roster drive %s; remove them from the roster instead", traction.AddressName(tr.Address, tr.Long))
	}
	var consists []Consist
	if inRoster {
		for _, c := range m.consists {
			if c.MadeUp && slices.ContainsFunc(c.Members, func(mb Member) bool { return mb.Train == id }) {
				m.mu.Unlock()
				return invalid("%s runs in %s; break that up first", e.Name, c.Name)
			}
		}
	}
	removed := m.viewForLocked(id, tr)
	if inRoster {
		delete(m.roster, id)
		consists = m.dropMemberLocked(id)
	}
	last := len(m.entriesAtLocked(tr.ID)) == 0
	if last {
		delete(m.trains, tr.ID)
	}
	m.mu.Unlock()
	if last {
		m.release(ctx, tr)
	}
	if inRoster {
		if err := m.opts.File.Remove(id); err != nil {
			return err
		}
		if err := m.saveConsists(consists); err != nil {
			return err
		}
		for _, d := range e.Decoders {
			if err := m.opts.CVs.Delete(d.ID); err != nil {
				m.log.Warn("removing CV values failed", "decoder", d.ID, "error", err)
			}
		}
	}
	removed.Removed = true
	removed.Updated = time.Now()
	m.send(removed)
	return nil
}

// release gives up a train that is leaving, if Interlock controls it.
func (m *Manager) release(ctx context.Context, tr *train) {
	tr.acquire.Lock()
	defer tr.acquire.Unlock()
	m.mu.Lock()
	controlled, nodeID := tr.Controlled, tr.Node
	tr.Controlled = false
	m.mu.Unlock()
	if controlled && nodeID != 0 {
		if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.ReleaseController(m.opts.Bus.ID()), false); err != nil {
			m.log.Debug("release failed", "train", tr.ID, "error", err)
		}
	}
}

// --- finding and driving ---------------------------------------------------

// Search asks the bus which trains match a query: an address or the
// start of one. Trains that exist answer; nothing is created.
func (m *Manager) Search(ctx context.Context, query string) ([]Found, error) {
	ev, err := traction.SearchEvent(query, 0)
	if err != nil {
		return nil, &InvalidError{Reason: "search for an address: one to six digits"}
	}
	producers, err := m.opts.Bus.Producers(ctx, ev, m.window, true)
	if err != nil {
		return nil, err
	}
	found := make([]Found, 0, len(producers))
	for _, p := range producers {
		id := p.Node
		if id == 0 {
			id, _ = m.opts.Bus.VerifyAlias(ctx, p.Alias)
		}
		if id == 0 {
			continue
		}
		f := Found{Node: id}
		if address, long, ok := traction.DCCAddress(id); ok {
			f.Address, f.Long = address, long
			f.ID = IDFor(address, long)
			f.Name = traction.AddressName(address, long)
		} else {
			f.ID = id.String()
			f.Name = id.String()
		}
		if m.opts.Nodes != nil {
			if info, ok := m.opts.Nodes.Get(id); ok && info.SNIP != nil && info.SNIP.UserName != "" {
				f.Name = info.SNIP.UserName
			}
		}
		if f.Address != 0 {
			if tr, ok := m.Get(f.ID); ok && tr.Roster {
				f.ID, f.Name, f.Roster = tr.ID, m.Name(f.Address, f.Long), true
			}
		}
		found = append(found, f)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Address < found[j].Address })
	return found, nil
}

// find locates the train node, having the command station create it if
// it has to. The caller holds tr.acquire.
func (m *Manager) find(ctx context.Context, tr *train) (openlcb.NodeID, error) {
	m.mu.RLock()
	address, long := tr.Address, tr.Long
	m.mu.RUnlock()
	ev := traction.AddressSearch(address, long, true)
	producers, err := m.opts.Bus.Producers(ctx, ev, m.window, true)
	if err != nil {
		return 0, err
	}
	var found openlcb.NodeID
	for _, p := range producers {
		id := p.Node
		if id == 0 {
			id, _ = m.opts.Bus.VerifyAlias(ctx, p.Alias)
		}
		if id == 0 {
			continue
		}
		if a, l, ok := traction.DCCAddress(id); ok && a == address && l == long {
			found = id
			break
		}
		if found == 0 {
			found = id
		}
	}
	if found == 0 {
		return 0, fmt.Errorf("%w %s", ErrNoNode, traction.AddressName(address, long))
	}
	m.mu.Lock()
	tr.Node, tr.Online = found, true
	tr.Updated = time.Now()
	m.mu.Unlock()
	return found, nil
}

// ensureNode returns the train's node, finding it when it is not known or
// not on the bus. The caller holds tr.acquire.
func (m *Manager) ensureNode(ctx context.Context, tr *train) (openlcb.NodeID, error) {
	m.mu.RLock()
	id, online := tr.Node, tr.Online
	m.mu.RUnlock()
	if id != 0 && m.opts.Nodes != nil {
		if info, ok := m.opts.Nodes.Get(id); ok {
			online = info.Online
		}
	}
	if id != 0 && online {
		return id, nil
	}
	return m.find(ctx, tr)
}

// ensureControl makes Interlock the train's controller, finding the node
// first and reading the train's state back afterwards. The caller holds
// tr.acquire.
func (m *Manager) ensureControl(ctx context.Context, tr *train) (openlcb.NodeID, error) {
	id, err := m.ensureNode(ctx, tr)
	if err != nil {
		return 0, err
	}
	m.mu.RLock()
	controlled := tr.Controlled
	m.mu.RUnlock()
	if controlled {
		return id, nil
	}
	reply, err := m.opts.Bus.TractionCommand(ctx, id, traction.AssignController(m.opts.Bus.ID()), true)
	if err != nil {
		m.mu.Lock()
		tr.Online = false
		m.mu.Unlock()
		return 0, fmt.Errorf("take control of %s: %w", m.Name(tr.Address, tr.Long), err)
	}
	result, err := traction.ParseAssignReply(reply)
	if err != nil {
		return 0, err
	}
	if result != traction.AssignOK {
		m.mu.Lock()
		tr.Controlled = false
		m.mu.Unlock()
		m.publish(tr)
		why := "another throttle holds it"
		if result == traction.AssignRefusedByTrain {
			why = "the train is not available"
		}
		return 0, fmt.Errorf("%w: %s", ErrRefused, why)
	}
	m.mu.Lock()
	tr.Controlled = true
	tr.Controller = m.opts.Bus.ID()
	tr.Updated = time.Now()
	m.mu.Unlock()
	m.readBack(ctx, tr, id)
	m.log.Info("took control of a train", "train", tr.ID, "node", id)
	return id, nil
}

// readBack asks the train for its speed and functions.
func (m *Manager) readBack(ctx context.Context, tr *train, id openlcb.NodeID) {
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	reply, err := m.opts.Bus.TractionCommand(qctx, id, traction.QuerySpeeds(), true)
	cancel()
	if err != nil {
		m.log.Debug("train did not report its speed", "train", tr.ID, "error", err)
		return
	}
	speeds, err := traction.ParseSpeeds(reply)
	if err != nil {
		return
	}
	on := make(map[int]bool)
	for fn := range m.fns {
		qctx, cancel := context.WithTimeout(ctx, queryTimeout)
		reply, err := m.opts.Bus.TractionCommand(qctx, id, traction.QueryFunction(uint32(fn)), true)
		cancel()
		if err != nil {
			m.log.Debug("train did not report a function", "train", tr.ID, "function", fn, "error", err)
			break
		}
		if _, v, err := traction.ParseFunction(reply); err == nil && v != 0 {
			on[fn] = true
		}
	}
	m.mu.Lock()
	tr.setSpeed(speeds.Set)
	tr.EStop = speeds.EStop
	tr.on = on
	tr.setFunction(-1, false)
	tr.Known = true
	tr.Updated = time.Now()
	m.mu.Unlock()
}

// control gets a train under Interlock's control, ready for a command.
// With consist set, a consist the train leads is put together first if
// the command station may have lost it.
func (m *Manager) control(ctx context.Context, id string, consist bool) (*train, openlcb.NodeID, error) {
	tr, err := m.get(id)
	if err != nil {
		return nil, 0, err
	}
	if consist {
		if err := m.ensureConsist(ctx, tr); err != nil {
			return tr, 0, err
		}
	}
	tr.acquire.Lock()
	defer tr.acquire.Unlock()
	nodeID, err := m.ensureControl(ctx, tr)
	return tr, nodeID, err
}

// Acquire finds a train's node, takes control of it and reads its state.
func (m *Manager) Acquire(ctx context.Context, id string) (Train, error) {
	tr, _, err := m.control(ctx, id, true)
	if err != nil {
		if tr != nil {
			return m.viewFor(id, tr), err
		}
		return Train{}, err
	}
	m.publish(tr)
	return m.viewFor(id, tr), nil
}

// Release gives a train up. Its last known state stays.
func (m *Manager) Release(ctx context.Context, id string) (Train, error) {
	m.mu.RLock()
	tr, ok := m.lookupLocked(id)
	m.mu.RUnlock()
	if !ok || tr == nil {
		return Train{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	tr.acquire.Lock()
	defer tr.acquire.Unlock()
	m.mu.Lock()
	controlled, nodeID := tr.Controlled, tr.Node
	tr.Controlled = false
	if tr.Controller == m.opts.Bus.ID() {
		tr.Controller = 0
	}
	tr.Updated = time.Now()
	m.mu.Unlock()
	if controlled && nodeID != 0 {
		if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.ReleaseController(m.opts.Bus.ID()), false); err != nil {
			m.log.Debug("release failed", "train", tr.ID, "error", err)
		}
	}
	m.publish(tr)
	return m.viewFor(id, tr), nil
}

// SetSpeed sets a train's speed, 0 to MaxSpeed, and direction; an empty
// direction keeps the current one.
func (m *Manager) SetSpeed(ctx context.Context, id string, speed float32, direction string) (Train, error) {
	if speed < 0 || speed > MaxSpeed {
		return Train{}, invalid("the speed must be between 0 and %d", MaxSpeed)
	}
	if direction != "" && direction != Forward && direction != Reverse {
		return Train{}, invalid("the direction must be forward or reverse")
	}
	if tr, err := m.get(id); err == nil {
		if err := m.following(tr); err != nil {
			return m.viewFor(id, tr), err
		}
	}
	tr, nodeID, err := m.control(ctx, id, true)
	if err != nil {
		return Train{}, err
	}
	m.mu.RLock()
	if direction == "" {
		direction = tr.Direction
	}
	m.mu.RUnlock()
	s := traction.Speed{Value: speed, Reverse: direction == Reverse}
	if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.SetSpeed(s), false); err != nil {
		return m.viewFor(id, tr), err
	}
	m.mu.Lock()
	tr.setSpeed(s)
	tr.Updated = time.Now()
	m.mu.Unlock()
	m.follow(tr, func(member *train, mb Member) {
		member.setSpeed(traction.Speed{Value: s.Value, Reverse: s.Reverse != mb.Reverse})
	})
	m.publish(tr)
	return m.viewFor(id, tr), nil
}

// EmergencyStop stops a train at once.
func (m *Manager) EmergencyStop(ctx context.Context, id string) (Train, error) {
	tr, nodeID, err := m.control(ctx, id, false)
	if err != nil {
		return Train{}, err
	}
	if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.EmergencyStop(), false); err != nil {
		return m.viewFor(id, tr), err
	}
	m.mu.Lock()
	tr.EStop, tr.Speed = true, 0
	tr.Updated = time.Now()
	m.mu.Unlock()
	m.follow(tr, func(member *train, _ Member) { member.EStop, member.Speed = true, 0 })
	m.publish(tr)
	return m.viewFor(id, tr), nil
}

// SetFunction turns a function on or off.
func (m *Manager) SetFunction(ctx context.Context, id string, fn int, on bool) (Train, error) {
	if fn < 0 || fn > MaxFunction {
		return Train{}, invalid("there is no function F%d; functions run from F0 to F%d", fn, MaxFunction)
	}
	tr, nodeID, err := m.control(ctx, id, true)
	if err != nil {
		return Train{}, err
	}
	var value uint16
	if on {
		value = 1
	}
	if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.SetFunction(uint32(fn), value), false); err != nil {
		return m.viewFor(id, tr), err
	}
	m.mu.Lock()
	tr.setFunction(fn, on)
	tr.Updated = time.Now()
	m.mu.Unlock()
	m.follow(tr, func(member *train, mb Member) {
		if mb.follows(fn) {
			member.setFunction(fn, on)
		}
	})
	m.publish(tr)
	return m.viewFor(id, tr), nil
}

// Refresh reads a train's state back from its node without taking
// control of it.
func (m *Manager) Refresh(ctx context.Context, id string) (Train, error) {
	tr, err := m.get(id)
	if err != nil {
		return Train{}, err
	}
	tr.acquire.Lock()
	defer tr.acquire.Unlock()
	nodeID, err := m.ensureNode(ctx, tr)
	if err != nil {
		return m.viewFor(id, tr), err
	}
	m.readBack(ctx, tr, nodeID)
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	reply, err := m.opts.Bus.TractionCommand(qctx, nodeID, traction.QueryController(), true)
	cancel()
	if err == nil {
		if controller, err := traction.ParseControllerReply(reply); err == nil {
			m.mu.Lock()
			tr.Controller = controller
			tr.Controlled = controller == m.opts.Bus.ID()
			m.mu.Unlock()
		}
	}
	m.publish(tr)
	return m.viewFor(id, tr), nil
}

// NodeFor returns a train's node, finding it if need be, for programming
// on the main.
func (m *Manager) NodeFor(ctx context.Context, id string) (openlcb.NodeID, Train, error) {
	tr, err := m.get(id)
	if err != nil {
		return 0, Train{}, err
	}
	tr.acquire.Lock()
	defer tr.acquire.Unlock()
	nodeID, err := m.ensureNode(ctx, tr)
	return nodeID, m.viewFor(id, tr), err
}

// Command carries out an automation's train action.
func (m *Manager) Command(ctx context.Context, cmd Command) (Train, error) {
	if err := cmd.Validate(); err != nil {
		return Train{}, err
	}
	id := IDFor(cmd.Address, cmd.Long || cmd.Address > traction.ShortAddressMax)
	switch cmd.Kind {
	case CommandSpeed:
		return m.SetSpeed(ctx, id, cmd.Speed, cmd.Direction)
	case CommandStop:
		return m.SetSpeed(ctx, id, 0, "")
	case CommandEStop:
		return m.EmergencyStop(ctx, id)
	default:
		return m.SetFunction(ctx, id, cmd.Function, cmd.On)
	}
}

// Name returns how a locomotive at an address is called: its roster
// name (the names of all that share the address), else the address.
func (m *Manager) Name(address uint16, long bool) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nameLocked(address, long)
}

// nameLocked is Name for a caller holding m.mu.
func (m *Manager) nameLocked(address uint16, long bool) string {
	long = long || address > traction.ShortAddressMax
	es := m.entriesAtLocked(IDFor(address, long))
	if len(es) == 0 {
		return "locomotive " + traction.AddressName(address, long)
	}
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name
	}
	return strings.Join(names, " / ")
}

// --- the layout --------------------------------------------------------------

// Layout returns the layout state.
func (m *Manager) Layout() Layout {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.layout
}

// SetPower produces Emergency Off or Clear Emergency Off, which command
// stations answer by cutting or restoring track power.
func (m *Manager) SetPower(ctx context.Context, on bool) (Layout, error) {
	ev := openlcb.EventEmergencyOff
	if on {
		ev = openlcb.EventClearEmergencyOff
	}
	if err := m.opts.Bus.SendEvent(ctx, ev); err != nil {
		return m.Layout(), err
	}
	return m.powerSwitched(on), nil
}

// powerSwitched follows an Emergency Off or Clear Emergency Off, which
// every command station obeys.
func (m *Manager) powerSwitched(on bool) Layout {
	return m.setLayout(func(l *Layout) {
		for id := range m.stations {
			m.stations[id] = on
		}
		l.PowerOff, l.PowerKnown = !on, true
	})
}

// powerReported follows a command station saying whether its track power
// is on.
func (m *Manager) powerReported(station openlcb.NodeID, on bool) {
	m.setLayout(func(l *Layout) {
		m.stations[station] = on
		m.powerFromStationsLocked(l)
	})
}

// powerFromStationsLocked sets the layout's power from what the command
// stations said: unknown when they disagree or none is left. The caller
// holds m.mu.
func (m *Manager) powerFromStationsLocked(l *Layout) {
	l.PowerKnown = false
	seen, on := false, false
	for _, s := range m.stations {
		if seen && s != on {
			return
		}
		seen, on = true, s
	}
	if seen {
		l.PowerOff, l.PowerKnown = !on, true
	}
}

// followNode asks about track power when a node arrives, and forgets what
// a node said about it when the node leaves.
func (m *Manager) followNode(info registry.Info) {
	if info.Local || info.ID == m.opts.Bus.ID() {
		return
	}
	online := info.Online && !info.Forgotten
	m.mu.Lock()
	arrived := online && !m.online[info.ID]
	_, said := m.stations[info.ID]
	if online {
		m.online[info.ID] = true
	} else {
		delete(m.online, info.ID)
	}
	m.mu.Unlock()
	if !online && said {
		m.setLayout(func(l *Layout) {
			delete(m.stations, info.ID)
			m.powerFromStationsLocked(l)
		})
	}
	if arrived {
		m.askPower()
	}
}

// askPower asks every command station whether track power is on, once
// for the nodes arriving together.
func (m *Manager) askPower() {
	m.mu.Lock()
	if m.asking {
		m.mu.Unlock()
		return
	}
	m.asking = true
	m.mu.Unlock()
	time.AfterFunc(powerQueryDelay, func() {
		m.mu.Lock()
		m.asking = false
		m.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg := openlcb.Message{MTI: openlcb.MTIIdentifyConsumer, Payload: openlcb.EventEmergencyOff.AsSlice()}
		if err := m.opts.Bus.Send(ctx, msg); err != nil {
			m.log.Debug("asking about track power failed", "error", err)
		}
	})
}

// SetEmergencyStop produces Emergency Stop, which stops every train, or
// clears it.
func (m *Manager) SetEmergencyStop(ctx context.Context, stop bool) (Layout, error) {
	ev := openlcb.EventClearEmergencyStop
	if stop {
		ev = openlcb.EventEmergencyStop
	}
	if err := m.opts.Bus.SendEvent(ctx, ev); err != nil {
		return m.Layout(), err
	}
	if stop {
		m.stopAll()
	}
	return m.setLayout(func(l *Layout) { l.EmergencyStop = stop }), nil
}

// stopAll marks every train as emergency stopped.
func (m *Manager) stopAll() {
	m.mu.Lock()
	var changed []*train
	for _, tr := range m.trains {
		if tr.EStop && tr.Speed == 0 {
			continue
		}
		tr.EStop, tr.Speed = true, 0
		tr.Updated = time.Now()
		changed = append(changed, tr)
	}
	m.mu.Unlock()
	for _, tr := range changed {
		m.publish(tr)
	}
}

func (m *Manager) setLayout(change func(*Layout)) Layout {
	m.mu.Lock()
	before := m.layout
	change(&m.layout)
	if m.layout.PowerOff == before.PowerOff && m.layout.PowerKnown == before.PowerKnown &&
		m.layout.EmergencyStop == before.EmergencyStop && !before.Updated.IsZero() {
		m.mu.Unlock()
		return before
	}
	m.layout.Updated = time.Now()
	l := m.layout
	subs := make([]LayoutSubscriber, 0, len(m.layoutSubs))
	for _, sub := range m.layoutSubs {
		subs = append(subs, sub)
	}
	m.mu.Unlock()
	for _, sub := range subs {
		sub(l)
	}
	return l
}

// --- subscriptions ----------------------------------------------------------

// Subscribe registers a subscriber and returns a function that removes it.
func (m *Manager) Subscribe(sub Subscriber) (unsubscribe func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = sub
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

// SubscribeLayout registers a layout subscriber.
func (m *Manager) SubscribeLayout(sub LayoutSubscriber) (unsubscribe func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.layoutSubs[id] = sub
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.layoutSubs, id)
		m.mu.Unlock()
	}
}

// publish tells subscribers about a train as each roster entry at its
// address sees it, or as the address alone.
func (m *Manager) publish(tr *train) {
	m.mu.RLock()
	if m.trains[tr.ID] != tr {
		// Gone since the change.
		m.mu.RUnlock()
		return
	}
	views := m.viewsLocked(tr)
	m.mu.RUnlock()
	for _, v := range views {
		m.send(v)
	}
}

// send tells subscribers about one view of a train.
func (m *Manager) send(t Train) {
	m.mu.RLock()
	subs := make([]Subscriber, 0, len(m.subs))
	for _, sub := range m.subs {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()
	for _, sub := range subs {
		sub(t)
	}
}
