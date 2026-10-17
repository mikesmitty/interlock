// Package registry keeps track of the nodes on the bus: which are present,
// what they call themselves, which protocols they speak and how their
// configuration is laid out. It learns from traffic the local node
// observes, asks nodes for their identification as they appear, reads
// their CDI, and remembers all of it in the store so a restart shows the
// layout at once and offline nodes keep their names.
package registry

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// Client is what the registry needs from the local node.
type Client interface {
	ID() openlcb.NodeID
	Alias() openlcb.Alias
	State() node.State
	Observe(node.Observer) func()
	VerifyNodeIDGlobal(ctx context.Context, target openlcb.NodeID) error
	AliasMapEnquiry(ctx context.Context) error
	SNIP(ctx context.Context, id openlcb.NodeID) (openlcb.SNIP, error)
	Protocols(ctx context.Context, id openlcb.NodeID) (openlcb.Protocols, error)
}

// CDIReader fetches a node's Configuration Description Information.
type CDIReader interface {
	ReadCDI(ctx context.Context, id openlcb.NodeID, progress memconfig.Progress) ([]byte, error)
}

// Store persists nodes between runs. *store.Store implements it.
type Store interface {
	Nodes(ctx context.Context) ([]store.NodeRecord, error)
	SaveNode(ctx context.Context, rec store.NodeRecord) error
	DeleteNode(ctx context.Context, id openlcb.NodeID) error
	CDI(ctx context.Context, id openlcb.NodeID) (*store.CDIRecord, error)
	SaveCDI(ctx context.Context, rec store.CDIRecord) error
	DeleteCDI(ctx context.Context, id openlcb.NodeID) error
}

// CDI states.
const (
	// CDIPending: the node supports CDI and a read is queued.
	CDIPending = "pending"
	// CDIReading: the read is in progress; Read and Total say how far.
	CDIReading = "reading"
	// CDIReady: the layout is available.
	CDIReady = "ready"
	// CDIError: the last read failed; Error says why.
	CDIError = "error"
)

// CDIStatus is what Info says about a node's CDI. It is nil for nodes
// that do not support CDI or have not been identified yet.
type CDIStatus struct {
	State string `json:"state"`
	// Read and Total are byte counts while reading; Total is 0 if the node
	// did not say how big the document is.
	Read  int    `json:"read,omitempty"`
	Total int    `json:"total,omitempty"`
	Error string `json:"error,omitempty"`
	// FetchedAt is when the document was read from the node.
	FetchedAt time.Time `json:"fetched_at,omitempty"`
	// Cached means the document came from the store this run.
	Cached bool `json:"cached,omitempty"`
	// Truncated means the document ended early; the layout is partial.
	Truncated bool `json:"truncated,omitempty"`
	Segments  int  `json:"segments,omitempty"`
}

// Info is what is known about one node.
type Info struct {
	ID        openlcb.NodeID `json:"id"`
	Alias     openlcb.Alias  `json:"alias"`
	Online    bool           `json:"online"`
	Local     bool           `json:"local"`
	FirstSeen time.Time      `json:"first_seen"`
	LastSeen  time.Time      `json:"last_seen"`
	// SNIP is nil until the node has answered an identification request.
	SNIP *openlcb.SNIP `json:"snip,omitempty"`
	// Protocols is nil until the node has answered a protocol inquiry.
	Protocols []string `json:"protocols,omitempty"`
	// ProtocolBits is the raw Protocol Support bitmap, for tools.
	ProtocolBits openlcb.Protocols `json:"protocol_bits,omitempty"`
	// Error records why the last identification attempt failed, if it did.
	Error string `json:"error,omitempty"`
	// CDI reports on the node's configuration description.
	CDI *CDIStatus `json:"cdi,omitempty"`
	// Forgotten is set on the last update about a node removed with Forget.
	Forgotten bool `json:"forgotten,omitempty"`
}

// Name returns the friendliest name available: the user name, else the
// manufacturer and model, else the node ID.
func (i Info) Name() string {
	if i.SNIP != nil {
		if i.SNIP.UserName != "" {
			return i.SNIP.UserName
		}
		if i.SNIP.Model != "" {
			return i.SNIP.Manufacturer + " " + i.SNIP.Model
		}
	}
	return i.ID.String()
}

// Subscriber is told whenever a node's Info changes.
type Subscriber func(Info)

// Options tune the registry.
type Options struct {
	// Interval between check-in rounds; zero disables them.
	Interval time.Duration
	// LocalSNIP is what the local node reports about itself.
	LocalSNIP openlcb.SNIP
	// LocalProtocols is what the local node advertises.
	LocalProtocols openlcb.Protocols
	Logger         *slog.Logger
	// Timeout bounds each identification request.
	Timeout time.Duration
	// Store keeps nodes between runs; nil keeps them in memory only.
	Store Store
	// CDI reads configuration descriptions; nil disables that.
	CDI CDIReader
}

// cdiEntry is a parsed CDI held in memory.
type cdiEntry struct {
	xml       []byte
	doc       *cdi.CDI
	version   string
	fetchedAt time.Time
}

// Registry is the node table.
type Registry struct {
	client Client
	opts   Options
	log    *slog.Logger

	mu      sync.RWMutex
	nodes   map[openlcb.NodeID]*Info
	cdis    map[openlcb.NodeID]*cdiEntry
	subs    map[int]Subscriber
	nextSub int

	fetch    chan openlcb.NodeID
	fetching map[openlcb.NodeID]bool
	cdiFetch chan openlcb.NodeID
	cdiBusy  map[openlcb.NodeID]bool
	kick     chan struct{}
}

// New builds a registry over client. Call Restore to load stored nodes
// and Run to start discovery.
func New(client Client, opts Options) *Registry {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	r := &Registry{
		client:   client,
		opts:     opts,
		log:      opts.Logger,
		nodes:    make(map[openlcb.NodeID]*Info),
		cdis:     make(map[openlcb.NodeID]*cdiEntry),
		subs:     make(map[int]Subscriber),
		fetch:    make(chan openlcb.NodeID, 256),
		fetching: make(map[openlcb.NodeID]bool),
		cdiFetch: make(chan openlcb.NodeID, 256),
		cdiBusy:  make(map[openlcb.NodeID]bool),
		kick:     make(chan struct{}, 1),
	}
	now := time.Now()
	local := &Info{ID: client.ID(), Local: true, FirstSeen: now, LastSeen: now}
	snip := opts.LocalSNIP
	local.SNIP = &snip
	local.ProtocolBits = opts.LocalProtocols
	local.Protocols = opts.LocalProtocols.Names()
	r.nodes[client.ID()] = local
	return r
}

// Restore loads nodes and CDIs from the store as offline entries.
func (r *Registry) Restore(ctx context.Context) error {
	if r.opts.Store == nil {
		return nil
	}
	records, err := r.opts.Store.Nodes(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		if rec.ID == r.client.ID() {
			continue
		}
		info := &Info{ID: rec.ID, FirstSeen: rec.FirstSeen, LastSeen: rec.LastSeen, SNIP: rec.SNIP}
		if rec.Protocols != 0 {
			info.ProtocolBits = rec.Protocols
			info.Protocols = rec.Protocols.Names()
		}
		r.nodes[rec.ID] = info
		cached, err := r.opts.Store.CDI(ctx, rec.ID)
		if err != nil {
			r.log.Warn("could not load cached CDI", "node", rec.ID, "error", err)
			continue
		}
		if cached == nil {
			continue
		}
		doc, err := cdi.Parse(cached.XML)
		if err != nil {
			r.log.Warn("cached CDI does not parse; it will be read again", "node", rec.ID, "error", err)
			continue
		}
		r.cdis[rec.ID] = &cdiEntry{xml: cached.XML, doc: doc, version: cached.SoftwareVersion, fetchedAt: cached.FetchedAt}
		info.CDI = &CDIStatus{State: CDIReady, FetchedAt: cached.FetchedAt, Cached: true, Truncated: doc.Truncated, Segments: len(doc.Segments)}
	}
	r.log.Info("restored nodes from store", "count", len(records))
	return nil
}

// Run observes the bus, identifies nodes as they appear and runs check-in
// rounds until ctx ends.
func (r *Registry) Run(ctx context.Context) error {
	unobserve := r.client.Observe(r.handleEvent)
	defer unobserve()

	go r.fetchLoop(ctx)
	go r.cdiLoop(ctx)

	// Wait for the local node to be able to talk before the first round.
	for r.client.State() != node.StateInitialized {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(20 * time.Millisecond):
		}
	}
	r.round(ctx)

	var tick <-chan time.Time
	if r.opts.Interval > 0 {
		ticker := time.NewTicker(r.opts.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			r.persistAll()
			return nil
		case <-r.kick:
			r.round(ctx)
		case <-tick:
			r.round(ctx)
			r.expire()
			r.persistAll()
		}
	}
}

// round asks everyone to announce themselves.
func (r *Registry) round(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	if err := r.client.AliasMapEnquiry(ctx); err != nil {
		r.log.Debug("alias map enquiry failed", "error", err)
	}
	if err := r.client.VerifyNodeIDGlobal(ctx, 0); err != nil {
		r.log.Debug("verify node id global failed", "error", err)
	}
	// Nodes we know but have not identified get another try.
	r.mu.RLock()
	var retry []openlcb.NodeID
	for id, info := range r.nodes {
		if !info.Local && info.Online && (info.SNIP == nil || info.Protocols == nil) {
			retry = append(retry, id)
		}
	}
	r.mu.RUnlock()
	for _, id := range retry {
		r.requestFetch(id)
	}
}

// expire marks nodes offline that have not been heard from for more than
// two check-in rounds.
func (r *Registry) expire() {
	cutoff := time.Now().Add(-(2*r.opts.Interval + 5*time.Second))
	var changed []Info
	r.mu.Lock()
	for _, info := range r.nodes {
		if !info.Local && info.Online && info.LastSeen.Before(cutoff) {
			info.Online = false
			changed = append(changed, *info)
		}
	}
	r.mu.Unlock()
	for _, info := range changed {
		r.log.Info("node went offline", "node", info.ID, "name", info.Name())
		r.publish(info)
	}
}

// Refresh starts a check-in round now.
func (r *Registry) Refresh(_ context.Context) error {
	select {
	case r.kick <- struct{}{}:
	default:
	}
	return nil
}

// RefreshNode re-reads a node's identification.
func (r *Registry) RefreshNode(_ context.Context, id openlcb.NodeID) error {
	r.mu.RLock()
	_, known := r.nodes[id]
	r.mu.RUnlock()
	if !known {
		return node.ErrNotFound
	}
	r.requestFetch(id)
	return nil
}

// Forget removes an offline node and what was stored about it.
func (r *Registry) Forget(ctx context.Context, id openlcb.NodeID) error {
	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return node.ErrNotFound
	}
	if info.Local || info.Online {
		r.mu.Unlock()
		return ErrNodeOnline
	}
	delete(r.nodes, id)
	delete(r.cdis, id)
	snapshot := *info
	r.mu.Unlock()
	if r.opts.Store != nil {
		if err := r.opts.Store.DeleteNode(ctx, id); err != nil {
			return err
		}
	}
	snapshot.Forgotten = true
	r.publish(snapshot)
	return nil
}

func (r *Registry) handleEvent(ev node.Event) {
	switch ev.Kind {
	case node.EventAliasMapped:
		r.seen(ev.NodeID, ev.Alias)
	case node.EventAliasReset:
		if ev.NodeID != 0 {
			r.offline(ev.NodeID)
		}
	case node.EventMessage:
		if ev.Message.Source.ID != 0 {
			r.seen(ev.Message.Source.ID, ev.Message.Source.Alias)
		}
	case node.EventStateChanged:
		r.mu.Lock()
		local := r.nodes[r.client.ID()]
		local.Alias = ev.Alias
		local.Online = ev.State == node.StateInitialized
		info := *local
		r.mu.Unlock()
		r.publish(info)
	}
}

// seen records that a node is present, creating it on first sight.
func (r *Registry) seen(id openlcb.NodeID, alias openlcb.Alias) {
	if id == r.client.ID() {
		return
	}
	now := time.Now()
	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok {
		info = &Info{ID: id, FirstSeen: now}
		r.nodes[id] = info
	}
	changed := !ok || !info.Online || (alias != 0 && info.Alias != alias)
	cameOnline := !info.Online
	info.LastSeen = now
	info.Online = true
	if alias != 0 {
		info.Alias = alias
	}
	needFetch := info.SNIP == nil || info.Protocols == nil
	snapshot := *info
	r.mu.Unlock()

	if !ok {
		r.log.Info("node found", "node", id, "alias", alias)
	}
	if changed {
		r.publish(snapshot)
	}
	if cameOnline {
		r.persist(snapshot)
	}
	if needFetch {
		r.requestFetch(id)
	} else if cameOnline {
		// Known node back on the bus: its software may have changed.
		r.requestFetch(id)
	}
}

func (r *Registry) offline(id openlcb.NodeID) {
	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok || !info.Online {
		r.mu.Unlock()
		return
	}
	info.Online = false
	snapshot := *info
	r.mu.Unlock()
	r.log.Info("node left the bus", "node", id, "name", snapshot.Name())
	r.publish(snapshot)
	r.persist(snapshot)
}

func (r *Registry) requestFetch(id openlcb.NodeID) {
	r.mu.Lock()
	if r.fetching[id] {
		r.mu.Unlock()
		return
	}
	r.fetching[id] = true
	r.mu.Unlock()
	select {
	case r.fetch <- id:
	default:
		r.mu.Lock()
		delete(r.fetching, id)
		r.mu.Unlock()
	}
}

// fetchLoop identifies nodes one at a time so a busy bus is not flooded.
func (r *Registry) fetchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.fetch:
			r.identify(ctx, id)
			r.mu.Lock()
			delete(r.fetching, id)
			r.mu.Unlock()
		}
	}
}

func (r *Registry) identify(ctx context.Context, id openlcb.NodeID) {
	ctx, cancel := context.WithTimeout(ctx, 2*r.opts.Timeout)
	defer cancel()

	var fetchErr error
	protocols, err := r.client.Protocols(ctx, id)
	if err != nil {
		fetchErr = err
	}
	var snip *openlcb.SNIP
	if err == nil && !protocols.Has(openlcb.ProtocolSimpleNodeInformation) {
		snip = &openlcb.SNIP{}
	} else {
		s, err := r.client.SNIP(ctx, id)
		if err != nil {
			fetchErr = err
		} else {
			snip = &s
		}
	}

	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return
	}
	if protocols != 0 || fetchErr == nil {
		info.ProtocolBits = protocols
		info.Protocols = protocols.Names()
		if info.Protocols == nil {
			info.Protocols = []string{}
		}
	}
	if snip != nil {
		info.SNIP = snip
	}
	if fetchErr != nil {
		info.Error = fetchErr.Error()
	} else {
		info.Error = ""
	}
	wantCDI := fetchErr == nil && r.opts.CDI != nil &&
		protocols.Has(openlcb.ProtocolCDI|openlcb.ProtocolMemoryConfiguration|openlcb.ProtocolDatagram)
	if wantCDI {
		r.planCDILocked(info)
	}
	snapshot := *info
	r.mu.Unlock()

	if fetchErr != nil {
		r.log.Warn("could not identify node", "node", id, "error", fetchErr)
	} else {
		r.log.Info("node identified", "node", id, "name", snapshot.Name(), "model", snapshot.SNIP.Model)
	}
	r.publish(snapshot)
	r.persist(snapshot)
	if snapshot.CDI != nil && snapshot.CDI.State == CDIPending {
		r.requestCDI(id)
	}
}

// Subscribe registers a subscriber and returns a function that removes it.
func (r *Registry) Subscribe(sub Subscriber) (unsubscribe func()) {
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = sub
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

func (r *Registry) publish(info Info) {
	r.mu.RLock()
	subs := make([]Subscriber, 0, len(r.subs))
	for _, sub := range r.subs {
		subs = append(subs, sub)
	}
	r.mu.RUnlock()
	for _, sub := range subs {
		sub(info)
	}
}

// persist writes one node to the store.
func (r *Registry) persist(info Info) {
	if r.opts.Store == nil || info.Local {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rec := store.NodeRecord{ID: info.ID, FirstSeen: info.FirstSeen, LastSeen: info.LastSeen, SNIP: info.SNIP, Protocols: info.ProtocolBits}
	if err := r.opts.Store.SaveNode(ctx, rec); err != nil {
		r.log.Warn("could not save node", "node", info.ID, "error", err)
	}
}

// persistAll refreshes last-seen times of online nodes.
func (r *Registry) persistAll() {
	if r.opts.Store == nil {
		return
	}
	r.mu.RLock()
	online := make([]Info, 0, len(r.nodes))
	for _, info := range r.nodes {
		if info.Online && !info.Local {
			online = append(online, *info)
		}
	}
	r.mu.RUnlock()
	for _, info := range online {
		r.persist(info)
	}
}

// List returns every known node, the local one first, then by name.
func (r *Registry) List() []Info {
	r.mu.RLock()
	list := make([]Info, 0, len(r.nodes))
	for _, info := range r.nodes {
		list = append(list, *info)
	}
	r.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Local != list[j].Local {
			return list[i].Local
		}
		if list[i].Online != list[j].Online {
			return list[i].Online
		}
		ni, nj := list[i].Name(), list[j].Name()
		if ni != nj {
			return ni < nj
		}
		return list[i].ID < list[j].ID
	})
	return list
}

// Get returns one node.
func (r *Registry) Get(id openlcb.NodeID) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.nodes[id]
	if !ok {
		return Info{}, false
	}
	return *info, true
}
