// Package monitor keeps a rolling log of bus traffic with each frame
// decoded for display, and streams new entries to subscribers.
package monitor

import (
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/hub"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// Entry is one line of the monitor.
type Entry struct {
	Seq       uint64    `json:"seq"`
	Time      time.Time `json:"time"`
	Direction string    `json:"dir"`
	Link      string    `json:"link,omitempty"`
	// Frame is the GridConnect text; empty for assembled messages.
	Frame string `json:"frame,omitempty"`
	// Kind is control, message, fragment, datagram, assembled or other.
	Kind    string   `json:"kind"`
	Decoded *Decoded `json:"decoded,omitempty"`
}

// Decoded is the human readable view of a frame or message.
type Decoded struct {
	// Type names the frame or message: "AMD", "Event Report", ...
	Type string `json:"type"`
	// MTI is the numeric MTI for message frames.
	MTI uint16 `json:"mti,omitempty"`
	// Source and Dest describe the nodes involved.
	Source *Endpoint `json:"source,omitempty"`
	Dest   *Endpoint `json:"dest,omitempty"`
	// Summary is a one line explanation of the content.
	Summary string `json:"summary,omitempty"`
	// Payload is the message payload in hex.
	Payload string `json:"payload,omitempty"`
}

// Endpoint is a node as a message names it.
type Endpoint struct {
	Alias uint16         `json:"alias"`
	ID    openlcb.NodeID `json:"id,omitempty"`
}

// Subscriber receives entries as they are added. It must not block.
type Subscriber func(Entry)

// Monitor is the rolling log.
type Monitor struct {
	cache *can.AliasCache

	mu      sync.RWMutex
	ring    []Entry
	start   int // index of the oldest entry
	count   int
	seq     uint64
	subs    map[int]Subscriber
	nextSub int
}

// New returns a monitor keeping the last capacity entries. cache supplies
// node IDs for aliases; it may be nil.
func New(capacity int, cache *can.AliasCache) *Monitor {
	if capacity < 1 {
		capacity = 1
	}
	return &Monitor{cache: cache, ring: make([]Entry, capacity), subs: make(map[int]Subscriber)}
}

// HandleHubEvent records a frame passing through the hub.
func (m *Monitor) HandleHubEvent(ev hub.Event) {
	entry := Entry{
		Time:      ev.Time,
		Direction: ev.Direction.String(),
		Link:      ev.Link,
		Frame:     ev.Frame.String(),
	}
	entry.Kind, entry.Decoded = DecodeFrame(ev.Frame, m.lookup)
	m.add(entry)
}

// HandleNodeEvent records messages that spanned several frames once the
// node has put them back together, so the monitor shows the whole thing.
func (m *Monitor) HandleNodeEvent(ev node.Event) {
	if ev.Kind != node.EventMessage || ev.Message == nil {
		return
	}
	msg := ev.Message
	if !multiFrame(msg) {
		return
	}
	entry := Entry{Time: time.Now(), Direction: "in", Kind: "assembled"}
	entry.Decoded = DecodeMessage(msg)
	m.add(entry)
}

func multiFrame(msg *openlcb.Message) bool {
	if msg.MTI == openlcb.MTIDatagram {
		return len(msg.Payload) > 8
	}
	return msg.MTI.IsAddressed() && len(msg.Payload) > 6
}

func (m *Monitor) lookup(alias openlcb.Alias) openlcb.NodeID {
	if m.cache == nil {
		return 0
	}
	id, _ := m.cache.Lookup(alias)
	return id
}

func (m *Monitor) add(entry Entry) {
	m.mu.Lock()
	m.seq++
	entry.Seq = m.seq
	idx := (m.start + m.count) % len(m.ring)
	if m.count == len(m.ring) {
		m.start = (m.start + 1) % len(m.ring)
	} else {
		m.count++
	}
	m.ring[idx] = entry
	subs := make([]Subscriber, 0, len(m.subs))
	for _, sub := range m.subs {
		subs = append(subs, sub)
	}
	m.mu.Unlock()
	for _, sub := range subs {
		sub(entry)
	}
}

// Since returns up to limit entries with Seq greater than seq, oldest
// first. A limit of 0 means all.
func (m *Monitor) Since(seq uint64, limit int) []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Entry
	for i := 0; i < m.count; i++ {
		e := m.ring[(m.start+i)%len(m.ring)]
		if e.Seq > seq {
			out = append(out, e)
			if limit > 0 && len(out) == limit {
				break
			}
		}
	}
	return out
}

// Recent returns the newest limit entries, oldest first.
func (m *Monitor) Recent(limit int) []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > m.count {
		limit = m.count
	}
	out := make([]Entry, 0, limit)
	for i := m.count - limit; i < m.count; i++ {
		out = append(out, m.ring[(m.start+i)%len(m.ring)])
	}
	return out
}

// LastSeq returns the newest sequence number, 0 when empty.
func (m *Monitor) LastSeq() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.seq
}

// Subscribe registers a subscriber and returns a function that removes it.
func (m *Monitor) Subscribe(sub Subscriber) (unsubscribe func()) {
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
