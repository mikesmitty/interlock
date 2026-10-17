package entity

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/detector"
)

// Bus sends messages from Interlock's node.
type Bus interface {
	SendEvent(ctx context.Context, id openlcb.EventID) error
	Send(ctx context.Context, msg openlcb.Message) error
}

// Nodes is the registry.
type Nodes interface {
	Get(id openlcb.NodeID) (registry.Info, bool)
	List() []registry.Info
}

// Layouts supplies parsed CDIs.
type Layouts interface {
	CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool)
}

// Values is the named event table's view of a node's configuration.
type Values interface {
	Values(id openlcb.NodeID) (eventtable.Values, bool)
}

// Builder turns a node's configuration into entity blueprints. The
// profile set implements it.
type Builder interface {
	Build(node NodeInfo, doc *cdi.CDI, values map[string]string) []Spec
}

// Options configure the manager.
type Options struct {
	Bus     Bus
	Nodes   Nodes
	Layouts Layouts
	Values  Values
	Builder Builder
	// File keeps overrides and manual entities; nil keeps them in memory.
	File   *File
	Logger *slog.Logger
}

// Subscriber is told whenever an entity changes.
type Subscriber func(Entity)

// identifyPause spaces Identify Events requests to different nodes so a
// bus full of nodes is not asked all at once.
const identifyPause = 100 * time.Millisecond

// Manager holds the entities and keeps them in step with the bus.
type Manager struct {
	opts Options
	log  *slog.Logger

	mu       sync.RWMutex
	entities map[string]*Entity
	byNode   map[openlcb.NodeID][]string
	byEvent  map[openlcb.EventID][]binding
	ranges   []rangeBinding
	online   map[openlcb.NodeID]bool
	subs     map[int]Subscriber
	nextSub  int

	identify chan openlcb.NodeID
	queued   map[openlcb.NodeID]bool
}

// New builds a manager. Call Run to start it.
func New(opts Options) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	m := &Manager{
		opts:     opts,
		log:      opts.Logger,
		entities: make(map[string]*Entity),
		byNode:   make(map[openlcb.NodeID][]string),
		byEvent:  make(map[openlcb.EventID][]binding),
		online:   make(map[openlcb.NodeID]bool),
		subs:     make(map[int]Subscriber),
		identify: make(chan openlcb.NodeID, 256),
		queued:   make(map[openlcb.NodeID]bool),
	}
	m.loadManual()
	return m
}

// loadManual builds the entities from the file.
func (m *Manager) loadManual() {
	for _, man := range m.opts.File.Manuals() {
		spec, err := man.Spec()
		if err != nil {
			m.log.Warn("skipping manual entity", "id", man.ID, "error", err)
			continue
		}
		m.mu.Lock()
		m.place(spec, time.Now())
		m.reindexLocked()
		m.mu.Unlock()
	}
}

// Run sends identification requests until ctx ends. Entities for nodes
// already known are built first.
func (m *Manager) Run(ctx context.Context) error {
	for _, info := range m.opts.Nodes.List() {
		if info.Local {
			continue
		}
		m.HandleNode(info)
		m.Rebuild(info.ID)
	}
	m.requestIdentify(0)
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-m.identify:
			m.mu.Lock()
			delete(m.queued, id)
			m.mu.Unlock()
			m.sendIdentify(ctx, id)
			select {
			case <-time.After(identifyPause):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// HandleNode follows the registry: availability, names and removal.
func (m *Manager) HandleNode(info registry.Info) {
	if info.Local {
		return
	}
	if info.Forgotten {
		m.removeNode(info.ID)
		return
	}
	m.mu.Lock()
	wasOnline, known := m.online[info.ID]
	m.online[info.ID] = info.Online
	var changed []Entity
	renamed := false
	for _, id := range m.byNode[info.ID] {
		ent := m.entities[id]
		if ent.NodeName != info.Name() {
			renamed = true
		}
		if ent.Available != info.Online {
			ent.Available = info.Online
			ent.Updated = time.Now()
			changed = append(changed, *ent)
		}
	}
	m.mu.Unlock()
	for _, ent := range changed {
		m.publish(ent)
	}
	if renamed {
		m.Rebuild(info.ID)
	}
	if info.Online && (!known || !wasOnline) {
		m.requestIdentify(info.ID)
	}
}

// Rebuild builds a node's entities again from its current configuration.
// The event table calls it when a node's values change.
func (m *Manager) Rebuild(node openlcb.NodeID) {
	info, ok := m.opts.Nodes.Get(node)
	if !ok {
		m.removeNode(node)
		return
	}
	doc, _, ok := m.opts.Layouts.CDI(node)
	if !ok {
		return // keep what exists until the layout is back
	}
	values, ok := m.opts.Values.Values(node)
	if !ok {
		return
	}
	specs := m.opts.Builder.Build(NodeInfo{ID: node, Name: info.Name(), SNIP: info.SNIP}, doc, values.Values)
	now := time.Now()
	m.mu.Lock()
	m.online[node] = info.Online
	keep := make(map[string]bool, len(specs))
	var changed []Entity
	for _, spec := range specs {
		if spec.Node != node {
			continue
		}
		keep[spec.ID] = true
		if ent := m.place(spec, now); ent != nil {
			changed = append(changed, *ent)
		}
	}
	for _, id := range m.byNode[node] {
		if keep[id] {
			continue
		}
		ent := m.entities[id]
		delete(m.entities, id)
		ent.Removed = true
		ent.Updated = now
		changed = append(changed, *ent)
	}
	m.reindexLocked()
	m.mu.Unlock()
	for _, ent := range changed {
		m.publish(ent)
	}
	if len(specs) > 0 && info.Online {
		m.requestIdentify(node)
	}
}

// place creates or updates the entity for a spec, keeping its state. It
// returns the entity when something about it changed, else nil. The
// caller holds m.mu.
func (m *Manager) place(spec Spec, now time.Time) *Entity {
	override := m.opts.File.Override(spec.ID)
	name := spec.Name
	if override.Name != "" {
		name = override.Name
	}
	def, _ := Lookup(spec.Kind)
	bound := make(map[string]bool, len(spec.Events))
	for role := range spec.Events {
		bound[role] = true
	}
	next := Entity{
		ID:          spec.ID,
		Kind:        spec.Kind,
		Name:        name,
		DefaultName: spec.Name,
		Node:        spec.Node,
		Group:       spec.Group,
		Source:      spec.Source,
		Profile:     spec.Profile,
		Suggested:   spec.Suggested && !override.Confirmed,
		Hidden:      override.Hidden,
		Available:   spec.Node == 0 || m.online[spec.Node],
		Commands:    def.Commands(bound),
		Events:      spec.Events,
		Keys:        spec.Keys,
	}
	if spec.Node != 0 {
		if info, ok := m.opts.Nodes.Get(spec.Node); ok {
			next.NodeName = info.Name()
		}
	}
	ent, exists := m.entities[spec.ID]
	if !exists {
		next.State = initialState(spec.Kind)
		next.Updated = now
		m.entities[spec.ID] = &next
		return &next
	}
	same := ent.Name == next.Name && ent.DefaultName == next.DefaultName && ent.NodeName == next.NodeName &&
		ent.Group == next.Group && ent.Suggested == next.Suggested && ent.Hidden == next.Hidden &&
		ent.Available == next.Available && ent.Source == next.Source && ent.Profile == next.Profile &&
		sameEvents(ent.Events, next.Events) && sameStrings(ent.Commands, next.Commands)
	if same {
		return nil
	}
	next.State, next.Commanded, next.Locomotives, next.LastEvent = ent.State, ent.Commanded, ent.Locomotives, ent.LastEvent
	if !sameEvents(ent.Events, next.Events) {
		// Different events may mean a different device; start over.
		next.State = initialState(spec.Kind)
		next.Commanded = ""
		next.Locomotives = nil
	}
	next.Updated = now
	*ent = next
	return ent
}

func initialState(kind Kind) string {
	if kind == KindButton {
		return StateIdle
	}
	return StateUnknown
}

func sameEvents(a, b map[string]openlcb.EventID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reindexLocked rebuilds the lookup tables; the caller holds m.mu.
func (m *Manager) reindexLocked() {
	m.byNode = make(map[openlcb.NodeID][]string)
	m.byEvent = make(map[openlcb.EventID][]binding)
	m.ranges = m.ranges[:0]
	for id, ent := range m.entities {
		if ent.Node != 0 {
			m.byNode[ent.Node] = append(m.byNode[ent.Node], id)
		}
		def, _ := Lookup(ent.Kind)
		for role, ev := range ent.Events {
			if r, ok := def.Role(role); ok && r.Range {
				m.ranges = append(m.ranges, rangeBinding{id: id, rng: detector.Range(ev)})
				continue
			}
			m.byEvent[ev] = append(m.byEvent[ev], binding{id: id, role: role})
		}
	}
	for _, ids := range m.byNode {
		sort.Strings(ids)
	}
}

func (m *Manager) removeNode(node openlcb.NodeID) {
	now := time.Now()
	m.mu.Lock()
	var removed []Entity
	for _, id := range m.byNode[node] {
		ent := m.entities[id]
		delete(m.entities, id)
		ent.Removed = true
		ent.Updated = now
		removed = append(removed, *ent)
	}
	delete(m.online, node)
	m.reindexLocked()
	m.mu.Unlock()
	for _, ent := range removed {
		m.publish(ent)
	}
}

// --- following the bus ---------------------------------------------------

// HandleMessage applies Event Reports and Identified replies to the
// entities bound to their events. It runs on the node goroutine.
func (m *Manager) HandleMessage(msg *openlcb.Message) {
	var valid, report bool
	switch msg.MTI {
	case openlcb.MTIEventReport:
		valid, report = true, true
	case openlcb.MTIProducerIdentifiedValid, openlcb.MTIConsumerIdentifiedValid:
		valid = true
	case openlcb.MTIProducerIdentifiedInvalid, openlcb.MTIConsumerIdentifiedInvalid:
	default:
		return
	}
	ev, ok := msg.EventID()
	if !ok {
		return
	}
	m.handleEvent(ev, valid, report, time.Now())
}

func (m *Manager) handleEvent(ev openlcb.EventID, valid, report bool, now time.Time) {
	m.mu.Lock()
	var changed []Entity
	for _, b := range m.byEvent[ev] {
		ent := m.entities[b.id]
		if ent != nil && m.apply(ent, b.role, valid, report, now) {
			changed = append(changed, *ent)
		}
	}
	for _, rb := range m.ranges {
		rep, ok := detector.Decode(ev, rb.rng)
		if !ok {
			continue
		}
		ent := m.entities[rb.id]
		if ent != nil && applyReport(ent, rep, valid, now) {
			changed = append(changed, *ent)
		}
	}
	m.mu.Unlock()
	for _, ent := range changed {
		m.publish(ent)
	}
}

// apply changes an entity for an event in one of its roles. valid is false
// for an Identified Invalid reply, which says the event's state does not
// hold. report is true for an Event Report, as opposed to an answer to
// Identify. It reports whether the entity changed.
func (m *Manager) apply(ent *Entity, role string, valid, report bool, now time.Time) bool {
	def, _ := Lookup(ent.Kind)
	r, ok := def.Role(role)
	if !ok {
		return false
	}
	before := *ent
	switch ent.Kind {
	case KindTurnout:
		m.applyTurnout(ent, r, valid, report)
	case KindButton:
		if report {
			ent.LastEvent = now
			ent.Updated = now
			return true
		}
		return false
	default:
		// Two-state kinds: an invalid reply for one state means the other.
		state := r.State
		if !valid {
			state = otherState(def, r)
			if state == "" {
				return false
			}
		}
		ent.State = state
	}
	if ent.State == before.State && ent.Commanded == before.Commanded {
		return false
	}
	if report {
		ent.LastEvent = now
	}
	ent.Updated = now
	return true
}

func (m *Manager) applyTurnout(ent *Entity, r Role, valid, report bool) {
	bound := make(map[string]bool, len(ent.Events))
	for role := range ent.Events {
		bound[role] = true
	}
	def, _ := Lookup(KindTurnout)
	feedback := def.hasFeedback(bound)
	switch {
	case r.Name == "center":
		if valid && report {
			ent.State = StateUnknown
			ent.Commanded = "center"
		}
	case r.Command:
		// A command seen on the bus, or a consumer saying it is in that
		// state.
		if !valid {
			return
		}
		if report {
			ent.Commanded = r.State
			if feedback {
				ent.State = StateMoving
			} else {
				ent.State = r.State
			}
		} else {
			ent.State = r.State
		}
	default:
		// Feedback: the node says where the points are.
		if valid {
			ent.State = r.State
			ent.Commanded = ""
		}
	}
}

// otherState returns the state of the other non-range role of a two-state
// kind, or "".
func otherState(def Definition, r Role) string {
	for _, o := range def.Roles {
		if o.Name != r.Name && o.State != "" && o.State != StateUnknown {
			return o.State
		}
	}
	return ""
}

// applyReport changes a reporter for a detector report.
func applyReport(ent *Entity, rep detector.Report, valid bool, now time.Time) bool {
	present := rep.Kind.Entered() == valid
	index := -1
	for i, loco := range ent.Locomotives {
		if loco.Address == rep.Address {
			index = i
			break
		}
	}
	changed := false
	switch {
	case present && index < 0:
		ent.Locomotives = append(ent.Locomotives, Locomotive{Address: rep.Address, Name: rep.Address.String(), Facing: rep.Kind.Orientation(), Since: now})
		changed = true
	case present && rep.Kind.Entered() && ent.Locomotives[index].Facing != rep.Kind.Orientation():
		// Turned around, or the orientation became known.
		ent.Locomotives[index].Facing = rep.Kind.Orientation()
		changed = true
	case !present && index >= 0:
		ent.Locomotives = append(ent.Locomotives[:index], ent.Locomotives[index+1:]...)
		changed = true
	}
	state := StateClear
	if len(ent.Locomotives) > 0 {
		state = StateOccupied
	}
	if ent.State != state {
		ent.State = state
		changed = true
	}
	if changed {
		ent.LastEvent = now
		ent.Updated = now
	}
	return changed
}

// --- identification ------------------------------------------------------

func (m *Manager) requestIdentify(node openlcb.NodeID) {
	m.mu.Lock()
	if m.queued[node] {
		m.mu.Unlock()
		return
	}
	m.queued[node] = true
	m.mu.Unlock()
	select {
	case m.identify <- node:
	default:
		m.mu.Lock()
		delete(m.queued, node)
		m.mu.Unlock()
	}
}

// sendIdentify asks a node for the state of all its events, or, for node
// zero, asks the bus about the events of manual entities.
func (m *Manager) sendIdentify(ctx context.Context, node openlcb.NodeID) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if node != 0 {
		err := m.opts.Bus.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyEventsAddressed, Dest: openlcb.NodeHandle{ID: node}})
		if err != nil {
			m.log.Debug("identify events failed", "node", node, "error", err)
		}
		return
	}
	m.mu.RLock()
	var events []openlcb.EventID
	for _, ent := range m.entities {
		if ent.Node != 0 {
			continue
		}
		def, _ := Lookup(ent.Kind)
		for role, ev := range ent.Events {
			if r, ok := def.Role(role); ok && !r.Command {
				events = append(events, ev)
			}
		}
	}
	m.mu.RUnlock()
	for _, ev := range events {
		if err := m.opts.Bus.Send(ctx, openlcb.Message{MTI: openlcb.MTIIdentifyProducer, Payload: ev.AsSlice()}); err != nil {
			m.log.Debug("identify producer failed", "event", ev, "error", err)
			return
		}
	}
}

// IdentifyAll asks every node with entities for its states again.
func (m *Manager) IdentifyAll() {
	m.mu.RLock()
	nodes := make([]openlcb.NodeID, 0, len(m.byNode))
	for node := range m.byNode {
		if m.online[node] {
			nodes = append(nodes, node)
		}
	}
	m.mu.RUnlock()
	for _, node := range nodes {
		m.requestIdentify(node)
	}
	m.requestIdentify(0)
}

// --- the API's view ------------------------------------------------------

// Command sends an entity's command and returns the entity as it stands
// after it.
func (m *Manager) Command(ctx context.Context, id, command string) (Entity, error) {
	m.mu.RLock()
	ent, ok := m.entities[id]
	if !ok {
		m.mu.RUnlock()
		return Entity{}, ErrNotFound
	}
	snapshot := *ent
	m.mu.RUnlock()
	if !snapshot.Available {
		return snapshot, ErrUnavailable
	}
	def, _ := Lookup(snapshot.Kind)
	r, ok := def.Role(command)
	ev, bound := snapshot.Events[command]
	if !ok || !r.Command || !bound {
		return snapshot, fmt.Errorf("%w: %q", ErrUnknownCommand, command)
	}
	if err := m.opts.Bus.SendEvent(ctx, ev); err != nil {
		return snapshot, err
	}
	// Our own reports are not observed, so apply the command here.
	m.handleEvent(ev, true, true, time.Now())
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ent, ok := m.entities[id]; ok {
		return *ent, nil
	}
	return snapshot, nil
}

// Update applies a change to an entity and saves it.
func (m *Manager) Update(id string, change Change) (Entity, error) {
	m.mu.RLock()
	ent, ok := m.entities[id]
	if !ok {
		m.mu.RUnlock()
		return Entity{}, ErrNotFound
	}
	snapshot := *ent
	m.mu.RUnlock()

	if snapshot.Source == SourceManual {
		return m.updateManual(snapshot, change)
	}
	override := m.opts.File.Override(id)
	if change.Name != nil {
		override.Name = *change.Name
		if override.Name == snapshot.DefaultName {
			override.Name = ""
		}
	}
	if change.Hidden != nil {
		override.Hidden = *change.Hidden
	}
	if change.Confirmed != nil {
		override.Confirmed = *change.Confirmed
	}
	if change.Events != nil {
		return snapshot, &InvalidError{Reason: "the events of an entity built from a node's configuration come from that configuration; change them on the node"}
	}
	if err := m.opts.File.SetOverride(id, override); err != nil {
		return snapshot, err
	}
	now := time.Now()
	m.mu.Lock()
	ent, ok = m.entities[id]
	if !ok {
		m.mu.Unlock()
		return Entity{}, ErrNotFound
	}
	ent.Name = snapshot.DefaultName
	if override.Name != "" {
		ent.Name = override.Name
	}
	ent.Hidden = override.Hidden
	ent.Suggested = ent.Source == SourceGuess && !override.Confirmed
	ent.Updated = now
	result := *ent
	m.mu.Unlock()
	m.publish(result)
	return result, nil
}

func (m *Manager) updateManual(current Entity, change Change) (Entity, error) {
	man := Manual{ID: current.ID, Kind: current.Kind, Name: current.Name, Events: current.Events}
	if change.Name != nil && *change.Name != "" {
		man.Name = *change.Name
	}
	if change.Events != nil {
		man.Events = change.Events
	}
	spec, err := man.Spec()
	if err != nil {
		return current, err
	}
	if change.Hidden != nil {
		if err := m.opts.File.SetOverride(current.ID, Override{Hidden: *change.Hidden}); err != nil {
			return current, err
		}
	}
	if err := m.opts.File.PutManual(man); err != nil {
		return current, err
	}
	m.mu.Lock()
	ent := m.place(spec, time.Now())
	if ent == nil {
		ent = m.entities[spec.ID]
	}
	m.reindexLocked()
	result := *ent
	m.mu.Unlock()
	m.publish(result)
	m.requestIdentify(0)
	return result, nil
}

// Create adds an entity built by hand.
func (m *Manager) Create(man Manual) (Entity, error) {
	spec, err := man.Spec()
	if err != nil {
		return Entity{}, err
	}
	man.ID = spec.ID
	m.mu.RLock()
	_, exists := m.entities[spec.ID]
	m.mu.RUnlock()
	if exists {
		return Entity{}, fmt.Errorf("%w: %s", ErrExists, spec.ID)
	}
	if err := m.opts.File.PutManual(man); err != nil {
		return Entity{}, err
	}
	m.mu.Lock()
	ent := m.place(spec, time.Now())
	m.reindexLocked()
	result := *ent
	m.mu.Unlock()
	m.publish(result)
	m.requestIdentify(0)
	return result, nil
}

// Delete removes an entity built by hand.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	ent, ok := m.entities[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if ent.Source != SourceManual {
		m.mu.Unlock()
		return ErrNotManual
	}
	delete(m.entities, id)
	m.reindexLocked()
	removed := *ent
	m.mu.Unlock()
	if err := m.opts.File.RemoveManual(id); err != nil {
		return err
	}
	removed.Removed = true
	removed.Updated = time.Now()
	m.publish(removed)
	return nil
}

// List returns every entity, by kind then name.
func (m *Manager) List() []Entity {
	m.mu.RLock()
	out := make([]Entity, 0, len(m.entities))
	for _, ent := range m.entities {
		out = append(out, *ent)
	}
	m.mu.RUnlock()
	order := make(map[Kind]int, len(kindOrder))
	for i, k := range kindOrder {
		order[k] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one entity.
func (m *Manager) Get(id string) (Entity, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ent, ok := m.entities[id]
	if !ok {
		return Entity{}, false
	}
	return *ent, true
}

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

func (m *Manager) publish(ent Entity) {
	m.mu.RLock()
	subs := make([]Subscriber, 0, len(m.subs))
	for _, sub := range m.subs {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()
	for _, sub := range subs {
		sub(ent)
	}
}
