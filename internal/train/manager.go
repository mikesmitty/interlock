package train

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
}

// Subscriber is told whenever a train changes.
type Subscriber func(Train)

// LayoutSubscriber is told whenever the layout state changes.
type LayoutSubscriber func(Layout)

// queryTimeout bounds each read-back query so a train that does not
// answer one does not hold a throttle for long.
const queryTimeout = 750 * time.Millisecond

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

	mu         sync.RWMutex
	trains     map[string]*train
	layout     Layout
	subs       map[int]Subscriber
	layoutSubs map[int]LayoutSubscriber
	nextSub    int
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
		subs:       make(map[int]Subscriber),
		layoutSubs: make(map[int]LayoutSubscriber),
	}
	if m.window <= 0 {
		m.window = time.Second
	}
	if m.fns <= 0 {
		m.fns = 29
	}
	now := time.Now()
	for _, e := range opts.File.List() {
		m.trains[e.ID] = &train{Train: Train{Entry: e, Roster: true, Direction: Forward, On: []int{}, Updated: now}, on: make(map[int]bool)}
	}
	m.opts.Bus.HandleTraction(m.handleTraction)
	return m
}

// --- following the bus ---------------------------------------------------

// HandleNode follows the registry: train nodes coming and going.
func (m *Manager) HandleNode(info registry.Info) {
	if _, _, ok := traction.DCCAddress(info.ID); !ok {
		return
	}
	m.mu.Lock()
	var changed *Train
	for _, tr := range m.trains {
		if tr.Node != info.ID {
			continue
		}
		online := info.Online && !info.Forgotten
		if tr.Online != online {
			tr.Online = online
			if !online {
				tr.Controlled = false
			}
			tr.Updated = time.Now()
			snapshot := tr.Train
			changed = &snapshot
		}
		break
	}
	m.mu.Unlock()
	if changed != nil {
		m.publish(*changed)
	}
}

// HandleMessage watches the bus for the emergency events. It runs on the
// node goroutine and must not block.
func (m *Manager) HandleMessage(msg *openlcb.Message) {
	if msg.MTI != openlcb.MTIEventReport {
		return
	}
	ev, ok := msg.EventID()
	if !ok {
		return
	}
	switch ev {
	case openlcb.EventEmergencyOff:
		m.setLayout(func(l *Layout) { l.PowerOff = true })
	case openlcb.EventClearEmergencyOff:
		m.setLayout(func(l *Layout) { l.PowerOff = false })
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
	var snapshot Train
	if changed {
		tr.Updated = time.Now()
		snapshot = tr.Train
	}
	m.mu.Unlock()
	if changed {
		go m.publish(snapshot)
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
	out := make([]Train, 0, len(m.trains))
	for _, tr := range m.trains {
		out = append(out, tr.Train)
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

// Get returns one train by ID, or by bare address.
func (m *Manager) Get(id string) (Train, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tr, ok := m.lookupLocked(id)
	if !ok {
		return Train{}, false
	}
	return tr.Train, true
}

func (m *Manager) lookupLocked(id string) (*train, bool) {
	if tr, ok := m.trains[id]; ok {
		return tr, true
	}
	if address, long, ok := ParseID(id); ok {
		tr, ok := m.trains[IDFor(address, long)]
		return tr, ok
	}
	return nil, false
}

// get finds a train, creating one outside the roster when id names an
// address that is not in it.
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
	return m.placeLocked(address, long), nil
}

// placeLocked creates a train outside the roster; the caller holds m.mu.
func (m *Manager) placeLocked(address uint16, long bool) *train {
	e := Entry{Address: address, Long: long}.Normalize()
	tr := &train{Train: Train{Entry: e, Direction: Forward, On: []int{}, Updated: time.Now()}, on: make(map[int]bool)}
	m.trains[e.ID] = tr
	snapshot := tr.Train
	go m.publish(snapshot)
	return tr
}

// Create adds a locomotive to the roster. A train already driven outside
// the roster keeps its state.
func (m *Manager) Create(e Entry) (Train, error) {
	e = e.Normalize()
	if err := e.Validate(); err != nil {
		return Train{}, err
	}
	m.mu.Lock()
	tr, exists := m.trains[e.ID]
	if exists && tr.Roster {
		m.mu.Unlock()
		return Train{}, fmt.Errorf("%w: %s", ErrExists, e.ID)
	}
	if !exists {
		tr = &train{Train: Train{Direction: Forward, On: []int{}}, on: make(map[int]bool)}
		m.trains[e.ID] = tr
	}
	tr.Entry = e
	tr.Roster = true
	tr.Updated = time.Now()
	snapshot := tr.Train
	m.mu.Unlock()
	if err := m.opts.File.Put(e); err != nil {
		return snapshot, err
	}
	m.publish(snapshot)
	return snapshot, nil
}

// Update changes a roster entry's name, function labels and momentary
// functions. The address stays; make a new train for another address.
func (m *Manager) Update(id string, e Entry) (Train, error) {
	m.mu.Lock()
	tr, ok := m.lookupLocked(id)
	if !ok {
		m.mu.Unlock()
		return Train{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next := tr.Entry
	next.Name, next.Functions, next.Momentary = e.Name, e.Functions, e.Momentary
	next = next.Normalize()
	if err := next.Validate(); err != nil {
		m.mu.Unlock()
		return Train{}, err
	}
	tr.Entry = next
	tr.Roster = true
	tr.Updated = time.Now()
	snapshot := tr.Train
	m.mu.Unlock()
	if err := m.opts.File.Put(next); err != nil {
		return snapshot, err
	}
	m.publish(snapshot)
	return snapshot, nil
}

// Delete removes a train, releasing it first when Interlock controls it.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if _, err := m.Release(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		m.log.Debug("release before delete failed", "train", id, "error", err)
	}
	m.mu.Lock()
	tr, ok := m.lookupLocked(id)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(m.trains, tr.ID)
	removed := tr.Train
	m.mu.Unlock()
	if err := m.opts.File.Remove(removed.ID); err != nil {
		return err
	}
	removed.Removed = true
	removed.Updated = time.Now()
	m.publish(removed)
	return nil
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
		if tr, ok := m.Get(f.ID); ok {
			f.Roster = tr.Roster
			if tr.Roster {
				f.Name = tr.Name
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
		return 0, fmt.Errorf("take control of %s: %w", tr.Name, err)
	}
	result, err := traction.ParseAssignReply(reply)
	if err != nil {
		return 0, err
	}
	if result != traction.AssignOK {
		m.mu.Lock()
		tr.Controlled = false
		snapshot := tr.Train
		m.mu.Unlock()
		m.publish(snapshot)
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
func (m *Manager) control(ctx context.Context, id string) (*train, openlcb.NodeID, error) {
	tr, err := m.get(id)
	if err != nil {
		return nil, 0, err
	}
	tr.acquire.Lock()
	defer tr.acquire.Unlock()
	nodeID, err := m.ensureControl(ctx, tr)
	return tr, nodeID, err
}

// Acquire finds a train's node, takes control of it and reads its state.
func (m *Manager) Acquire(ctx context.Context, id string) (Train, error) {
	tr, _, err := m.control(ctx, id)
	if err != nil {
		if tr != nil {
			return m.snapshot(tr), err
		}
		return Train{}, err
	}
	snapshot := m.snapshot(tr)
	m.publish(snapshot)
	return snapshot, nil
}

// Release gives a train up. Its last known state stays.
func (m *Manager) Release(ctx context.Context, id string) (Train, error) {
	m.mu.RLock()
	tr, ok := m.lookupLocked(id)
	m.mu.RUnlock()
	if !ok {
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
	snapshot := tr.Train
	m.mu.Unlock()
	if controlled && nodeID != 0 {
		if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.ReleaseController(m.opts.Bus.ID()), false); err != nil {
			m.log.Debug("release failed", "train", tr.ID, "error", err)
		}
	}
	m.publish(snapshot)
	return snapshot, nil
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
	tr, nodeID, err := m.control(ctx, id)
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
		return m.snapshot(tr), err
	}
	m.mu.Lock()
	tr.setSpeed(s)
	tr.Updated = time.Now()
	snapshot := tr.Train
	m.mu.Unlock()
	m.publish(snapshot)
	return snapshot, nil
}

// EmergencyStop stops a train at once.
func (m *Manager) EmergencyStop(ctx context.Context, id string) (Train, error) {
	tr, nodeID, err := m.control(ctx, id)
	if err != nil {
		return Train{}, err
	}
	if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.EmergencyStop(), false); err != nil {
		return m.snapshot(tr), err
	}
	m.mu.Lock()
	tr.EStop, tr.Speed = true, 0
	tr.Updated = time.Now()
	snapshot := tr.Train
	m.mu.Unlock()
	m.publish(snapshot)
	return snapshot, nil
}

// SetFunction turns a function on or off.
func (m *Manager) SetFunction(ctx context.Context, id string, fn int, on bool) (Train, error) {
	if fn < 0 || fn > MaxFunction {
		return Train{}, invalid("there is no function F%d; functions run from F0 to F%d", fn, MaxFunction)
	}
	tr, nodeID, err := m.control(ctx, id)
	if err != nil {
		return Train{}, err
	}
	var value uint16
	if on {
		value = 1
	}
	if _, err := m.opts.Bus.TractionCommand(ctx, nodeID, traction.SetFunction(uint32(fn), value), false); err != nil {
		return m.snapshot(tr), err
	}
	m.mu.Lock()
	tr.setFunction(fn, on)
	tr.Updated = time.Now()
	snapshot := tr.Train
	m.mu.Unlock()
	m.publish(snapshot)
	return snapshot, nil
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
		return m.snapshot(tr), err
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
	snapshot := m.snapshot(tr)
	m.publish(snapshot)
	return snapshot, nil
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
	return nodeID, m.snapshot(tr), err
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
// name, else the address.
func (m *Manager) Name(address uint16, long bool) string {
	if tr, ok := m.Get(IDFor(address, long || address > traction.ShortAddressMax)); ok && tr.Roster {
		return tr.Name
	}
	return "locomotive " + traction.AddressName(address, long || address > traction.ShortAddressMax)
}

func (m *Manager) snapshot(tr *train) Train {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return tr.Train
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
	return m.setLayout(func(l *Layout) { l.PowerOff = !on }), nil
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
	var changed []Train
	for _, tr := range m.trains {
		if tr.EStop && tr.Speed == 0 {
			continue
		}
		tr.EStop, tr.Speed = true, 0
		tr.Updated = time.Now()
		changed = append(changed, tr.Train)
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
	if m.layout.PowerOff == before.PowerOff && m.layout.EmergencyStop == before.EmergencyStop && !before.Updated.IsZero() {
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

func (m *Manager) publish(tr Train) {
	m.mu.RLock()
	subs := make([]Subscriber, 0, len(m.subs))
	for _, sub := range m.subs {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()
	for _, sub := range subs {
		sub(tr)
	}
}
