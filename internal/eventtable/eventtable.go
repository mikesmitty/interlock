// Package eventtable keeps the named event table: every event ID a node's
// configuration names, found through the eventid variables in its CDI
// and read from its configuration space. "Yard / Switch Machines / Servo 2
// / Throw Command Event" is an entry; the entity layer and the automation
// editor work from these names instead of raw event IDs.
//
// The table reads whole segments that hold event IDs, so the values next
// to the events (a description, an enabled flag) are available to the
// profiles that turn events into entities. Values are cached in the store
// so entities exist for offline nodes, and re-read when a node comes
// online, when its software version changes and after its configuration
// is written.
package eventtable

import (
	"context"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/nodeconfig"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
)

// Reader reads decoded configuration values from a node.
type Reader interface {
	Read(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, size int) (*nodeconfig.ReadResult, error)
}

// Layouts supplies parsed CDIs.
type Layouts interface {
	CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool)
}

// Store caches values between runs. *store.Store implements it.
type Store interface {
	NodeValues(ctx context.Context, id openlcb.NodeID) (*store.ValuesRecord, error)
	SaveNodeValues(ctx context.Context, rec store.ValuesRecord) error
	DeleteNodeValues(ctx context.Context, id openlcb.NodeID) error
}

// Entry is one named event.
type Entry struct {
	Node     openlcb.NodeID `json:"node"`
	NodeName string         `json:"node_name"`
	// Key is the variable's "space:address".
	Key string `json:"key"`
	// Path is the CDI names leading to the variable: segment, groups
	// (replica names where replicated), variable.
	Path []string `json:"path"`
	// Name is Path joined with " / ".
	Name  string          `json:"name"`
	Event openlcb.EventID `json:"event"`
	// Produced and Consumed come from the node's Identify Events replies.
	Produced bool `json:"produced,omitempty"`
	Consumed bool `json:"consumed,omitempty"`
}

// Values is what the table holds for one node.
type Values struct {
	Node    openlcb.NodeID
	Version string
	// Values maps "space:address" to the decoded value of every variable
	// in the segments that hold event IDs.
	Values map[string]string
	ReadAt time.Time
	// Cached means the values came from the store and have not been read
	// from the node this run.
	Cached bool
}

// Subscriber is told when a node's entries change.
type Subscriber func(node openlcb.NodeID)

type direction uint8

const (
	produced direction = 1 << iota
	consumed
)

type nodeState struct {
	name    string
	online  bool
	version string
	values  map[string]string
	entries []Entry
	readAt  time.Time
	cached  bool
	// stale means a write was seen since the last read.
	stale  bool
	queued bool
}

// maxSegment is the largest segment the table reads; the Read request is
// bounded the same way.
const maxSegment = nodeconfig.MaxRead

// writeSettle is how long to wait after a configuration write before
// reading the node again, so a batch of writes causes one read.
var writeSettle = 1500 * time.Millisecond

// Table is the named event table.
type Table struct {
	reader  Reader
	layouts Layouts
	store   Store
	log     *slog.Logger

	mu      sync.RWMutex
	nodes   map[openlcb.NodeID]*nodeState
	dirs    map[openlcb.NodeID]map[openlcb.EventID]direction
	timers  map[openlcb.NodeID]*time.Timer
	subs    map[int]Subscriber
	nextSub int

	reads chan openlcb.NodeID
}

// New returns a table. store may be nil.
func New(reader Reader, layouts Layouts, st Store, logger *slog.Logger) *Table {
	if logger == nil {
		logger = slog.Default()
	}
	return &Table{
		reader:  reader,
		layouts: layouts,
		store:   st,
		log:     logger,
		nodes:   make(map[openlcb.NodeID]*nodeState),
		dirs:    make(map[openlcb.NodeID]map[openlcb.EventID]direction),
		timers:  make(map[openlcb.NodeID]*time.Timer),
		subs:    make(map[int]Subscriber),
		reads:   make(chan openlcb.NodeID, 256),
	}
}

// Restore loads cached values for the nodes the registry restored.
func (t *Table) Restore(ctx context.Context, nodes []registry.Info) {
	if t.store == nil {
		return
	}
	for _, info := range nodes {
		if info.Local {
			continue
		}
		doc, _, ok := t.layouts.CDI(info.ID)
		if !ok {
			continue
		}
		rec, err := t.store.NodeValues(ctx, info.ID)
		if err != nil {
			t.log.Warn("could not load cached values", "node", info.ID, "error", err)
			continue
		}
		if rec == nil {
			continue
		}
		t.mu.Lock()
		st := t.state(info)
		st.values = rec.Values
		st.version = rec.SoftwareVersion
		st.readAt = rec.ReadAt
		st.cached = true
		st.entries = buildEntries(info.ID, st.name, doc, rec.Values)
		t.mu.Unlock()
		t.notify(info.ID)
	}
}

// Run reads nodes as they are queued until ctx ends.
func (t *Table) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			t.mu.Lock()
			for _, timer := range t.timers {
				timer.Stop()
			}
			t.mu.Unlock()
			return nil
		case id := <-t.reads:
			t.read(ctx, id)
		}
	}
}

// state returns the node's state, creating it; the caller holds t.mu.
func (t *Table) state(info registry.Info) *nodeState {
	st, ok := t.nodes[info.ID]
	if !ok {
		st = &nodeState{}
		t.nodes[info.ID] = st
	}
	st.name = info.Name()
	return st
}

// HandleNode follows the registry: it reads a node's values when the
// node is online with a known layout and nothing current is held, and
// drops a forgotten node. It is safe to call from any goroutine and does
// not block.
func (t *Table) HandleNode(info registry.Info) {
	if info.Local {
		return
	}
	if info.Forgotten {
		t.forget(info.ID)
		return
	}
	version := ""
	if info.SNIP != nil {
		version = info.SNIP.SoftwareVersion
	}
	ready := info.CDI != nil && info.CDI.State == registry.CDIReady

	t.mu.Lock()
	st := t.nodes[info.ID]
	renamed := st != nil && st.name != info.Name() && len(st.entries) > 0
	wasOnline := st != nil && st.online
	st = t.state(info)
	st.online = info.Online
	if renamed {
		for i := range st.entries {
			st.entries[i].NodeName = st.name
		}
	}
	want := info.Online && ready && (st.values == nil || st.version != version || !wasOnline || st.stale)
	if want {
		st.version = version
	}
	t.mu.Unlock()

	if renamed {
		t.notify(info.ID)
	}
	if want {
		t.request(info.ID)
	}
}

// HandleMessage watches the bus for two things: configuration writes to a
// node, which make its values stale, and Identify Events replies, which
// say whether a node produces or consumes an event. It runs on the node
// goroutine and must stay quick.
func (t *Table) HandleMessage(msg *openlcb.Message) {
	switch msg.MTI {
	case openlcb.MTIDatagram:
		if msg.Dest.ID == 0 || len(msg.Payload) < 2 || msg.Payload[0] != memconfig.ContentType {
			return
		}
		cmd := msg.Payload[1]
		// Writes and writes under mask are below the write reply code.
		if cmd < memconfig.CmdWriteReply || cmd == memconfig.CmdUpdateComplete || cmd == memconfig.CmdFactoryReset {
			t.Invalidate(msg.Dest.ID)
		}
	case openlcb.MTIProducerIdentifiedValid, openlcb.MTIProducerIdentifiedInvalid, openlcb.MTIProducerIdentifiedUnknown:
		t.direction(msg, produced)
	case openlcb.MTIConsumerIdentifiedValid, openlcb.MTIConsumerIdentifiedInvalid, openlcb.MTIConsumerIdentifiedUnknown:
		t.direction(msg, consumed)
	}
}

func (t *Table) direction(msg *openlcb.Message, dir direction) {
	ev, ok := msg.EventID()
	if !ok || msg.Source.ID == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.dirs[msg.Source.ID]
	if m == nil {
		m = make(map[openlcb.EventID]direction)
		t.dirs[msg.Source.ID] = m
	}
	m[ev] |= dir
}

// Invalidate notes that a node's configuration changed and reads it again
// once the writes settle.
func (t *Table) Invalidate(id openlcb.NodeID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.nodes[id]
	if !ok {
		return
	}
	st.stale = true
	if timer, ok := t.timers[id]; ok {
		timer.Reset(writeSettle)
		return
	}
	t.timers[id] = time.AfterFunc(writeSettle, func() {
		t.mu.Lock()
		delete(t.timers, id)
		online := t.nodes[id] != nil && t.nodes[id].online
		t.mu.Unlock()
		if online {
			t.request(id)
		}
	})
}

// Refresh reads a node's values again now.
func (t *Table) Refresh(id openlcb.NodeID) {
	t.request(id)
}

func (t *Table) request(id openlcb.NodeID) {
	t.mu.Lock()
	st, ok := t.nodes[id]
	if !ok || st.queued {
		t.mu.Unlock()
		return
	}
	st.queued = true
	t.mu.Unlock()
	select {
	case t.reads <- id:
	default:
		t.mu.Lock()
		st.queued = false
		t.mu.Unlock()
	}
}

func (t *Table) forget(id openlcb.NodeID) {
	t.mu.Lock()
	delete(t.nodes, id)
	delete(t.dirs, id)
	if timer, ok := t.timers[id]; ok {
		timer.Stop()
		delete(t.timers, id)
	}
	t.mu.Unlock()
	if t.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := t.store.DeleteNodeValues(ctx, id); err != nil {
			t.log.Warn("could not delete cached values", "node", id, "error", err)
		}
	}
	t.notify(id)
}

// read fetches every segment of the node that holds event IDs.
func (t *Table) read(ctx context.Context, id openlcb.NodeID) {
	defer func() {
		t.mu.Lock()
		if st, ok := t.nodes[id]; ok {
			st.queued = false
		}
		t.mu.Unlock()
	}()
	doc, _, ok := t.layouts.CDI(id)
	if !ok {
		return
	}
	t.mu.RLock()
	st, known := t.nodes[id]
	var version string
	if known {
		st.stale = false
		version = st.version
	}
	t.mu.RUnlock()
	if !known {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	values := make(map[string]string)
	for _, seg := range doc.Segments {
		if !hasEvents(seg.Items) {
			continue
		}
		if seg.Size > maxSegment {
			t.log.Warn("segment too large to index", "node", id, "segment", seg.Name, "bytes", seg.Size)
			continue
		}
		res, err := t.reader.Read(ctx, id, seg.Space, seg.Origin, int(seg.Size))
		if err != nil {
			t.log.Warn("could not read node values", "node", id, "segment", seg.Name, "error", err)
			t.mu.Lock()
			st.stale = true
			t.mu.Unlock()
			return
		}
		maps.Copy(values, res.Values)
	}

	now := time.Now()
	t.mu.Lock()
	st.values = values
	st.readAt = now
	st.cached = false
	st.entries = buildEntries(id, st.name, doc, values)
	count := len(st.entries)
	name := st.name
	t.mu.Unlock()
	t.log.Info("indexed node events", "node", id, "name", name, "events", count)
	if t.store != nil {
		rec := store.ValuesRecord{NodeID: id, SoftwareVersion: version, Values: values, ReadAt: now}
		saveCtx, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelSave()
		if err := t.store.SaveNodeValues(saveCtx, rec); err != nil {
			t.log.Warn("could not cache node values", "node", id, "error", err)
		}
	}
	t.notify(id)
}

// hasEvents reports whether any element in the tree is an event ID.
func hasEvents(items []*cdi.Element) bool {
	for _, el := range items {
		if el.Kind == cdi.KindEventID || hasEvents(el.Children) {
			return true
		}
	}
	return false
}

// buildEntries lists the event variables with a non-zero value.
func buildEntries(id openlcb.NodeID, name string, doc *cdi.CDI, values map[string]string) []Entry {
	var out []Entry
	for _, v := range doc.Variables() {
		if v.Element.Kind != cdi.KindEventID {
			continue
		}
		raw, ok := values[v.Key()]
		if !ok {
			continue
		}
		ev, err := openlcb.ParseEventID(raw)
		if err != nil || ev == 0 {
			continue
		}
		out = append(out, Entry{
			Node:     id,
			NodeName: name,
			Key:      v.Key(),
			Path:     v.Path,
			Name:     strings.Join(v.Path, " / "),
			Event:    ev,
		})
	}
	return out
}

// Subscribe registers a subscriber and returns a function that removes it.
func (t *Table) Subscribe(sub Subscriber) (unsubscribe func()) {
	t.mu.Lock()
	id := t.nextSub
	t.nextSub++
	t.subs[id] = sub
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		delete(t.subs, id)
		t.mu.Unlock()
	}
}

func (t *Table) notify(id openlcb.NodeID) {
	t.mu.RLock()
	subs := make([]Subscriber, 0, len(t.subs))
	for _, sub := range t.subs {
		subs = append(subs, sub)
	}
	t.mu.RUnlock()
	for _, sub := range subs {
		sub(id)
	}
}

// withDirections copies entries, filling in what Identify Events replies
// said; the caller holds t.mu.
func (t *Table) withDirections(id openlcb.NodeID, entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	dirs := t.dirs[id]
	for i := range out {
		d := dirs[out[i].Event]
		out[i].Produced = d&produced != 0
		out[i].Consumed = d&consumed != 0
	}
	return out
}

// Entries returns every named event, by node name then path.
func (t *Table) Entries() []Entry {
	t.mu.RLock()
	var out []Entry
	for id, st := range t.nodes {
		out = append(out, t.withDirections(id, st.entries)...)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeName != out[j].NodeName {
			return out[i].NodeName < out[j].NodeName
		}
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// NodeEntries returns one node's named events in address order.
func (t *Table) NodeEntries(id openlcb.NodeID) []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.nodes[id]
	if !ok {
		return nil
	}
	return t.withDirections(id, st.entries)
}

// Values returns what is held for a node.
func (t *Table) Values(id openlcb.NodeID) (Values, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.nodes[id]
	if !ok || st.values == nil {
		return Values{}, false
	}
	return Values{Node: id, Version: st.version, Values: st.values, ReadAt: st.readAt, Cached: st.cached}, true
}

// Lookup returns the entries naming an event ID, across nodes.
func (t *Table) Lookup(ev openlcb.EventID) []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []Entry
	for id, st := range t.nodes {
		for _, e := range st.entries {
			if e.Event == ev {
				out = append(out, t.withDirections(id, []Entry{e})...)
			}
		}
	}
	return out
}
