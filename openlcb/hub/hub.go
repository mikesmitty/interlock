// Package hub joins links into one logical CAN bus: every frame received
// on a link is repeated to every other link, and the local node and the
// monitor see all traffic in both directions.
//
// Each physical CAN segment must reach the hub through exactly one link.
// Two links onto the same segment would echo frames back and forth.
package hub

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// Direction says whether a frame arrived from a link or was sent by the
// local node.
type Direction uint8

// Directions.
const (
	In Direction = iota
	Out
)

// String implements fmt.Stringer.
func (d Direction) String() string {
	if d == Out {
		return "out"
	}
	return "in"
}

// Event is one frame passing through the hub.
type Event struct {
	Time      time.Time
	Frame     can.Frame
	Direction Direction
	// Link is the link the frame arrived on, or "" for frames the local
	// node sent.
	Link string
}

// Subscriber receives every event. It must return quickly: hand the event
// to a channel or buffer rather than doing work in place.
type Subscriber func(Event)

type member struct {
	link   transport.Link
	cancel context.CancelFunc
	done   chan struct{}
}

// Hub routes frames between links and local subscribers.
type Hub struct {
	logger *slog.Logger

	mu      sync.RWMutex
	links   map[string]*member
	subs    map[int]Subscriber
	nextSub int
	closed  bool
}

// New returns an empty hub.
func New(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{logger: logger, links: make(map[string]*member), subs: make(map[int]Subscriber)}
}

// AddLink starts running link until the hub is closed, RemoveLink is
// called, or the link's Run returns. A link with the same name as an
// existing one replaces it.
func (h *Hub) AddLink(ctx context.Context, link transport.Link) {
	ctx, cancel := context.WithCancel(ctx)
	m := &member{link: link, cancel: cancel, done: make(chan struct{})}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancel()
		close(m.done)
		return
	}
	if old, ok := h.links[link.Name()]; ok {
		old.cancel()
	}
	h.links[link.Name()] = m
	h.mu.Unlock()

	go func() {
		defer close(m.done)
		err := link.Run(ctx, func(f can.Frame) { h.received(f, link) })
		if err != nil {
			h.logger.Warn("link stopped", "link", link.Name(), "error", err)
		} else {
			h.logger.Debug("link stopped", "link", link.Name())
		}
		h.mu.Lock()
		if h.links[link.Name()] == m {
			delete(h.links, link.Name())
		}
		h.mu.Unlock()
	}()
}

// RemoveLink stops a link and waits for it to finish.
func (h *Hub) RemoveLink(name string) {
	h.mu.Lock()
	m, ok := h.links[name]
	if ok {
		delete(h.links, name)
	}
	h.mu.Unlock()
	if ok {
		m.cancel()
		<-m.done
	}
}

// Close stops every link and waits for them.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	members := make([]*member, 0, len(h.links))
	for name, m := range h.links {
		members = append(members, m)
		delete(h.links, name)
	}
	h.mu.Unlock()
	for _, m := range members {
		m.cancel()
		<-m.done
	}
}

// Subscribe registers a subscriber and returns a function that removes it.
func (h *Hub) Subscribe(sub Subscriber) (unsubscribe func()) {
	h.mu.Lock()
	id := h.nextSub
	h.nextSub++
	h.subs[id] = sub
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

// Send transmits a frame from the local node on every link.
func (h *Hub) Send(f can.Frame) {
	ev := Event{Time: time.Now(), Frame: f, Direction: Out}
	h.mu.RLock()
	links := make([]transport.Link, 0, len(h.links))
	for _, m := range h.links {
		links = append(links, m.link)
	}
	subs := h.subscribers()
	h.mu.RUnlock()

	for _, link := range links {
		if err := link.Send(f); err != nil {
			h.logger.Debug("send failed", "link", link.Name(), "error", err)
		}
	}
	for _, sub := range subs {
		sub(ev)
	}
}

// received repeats a frame from one link to the others and to subscribers.
func (h *Hub) received(f can.Frame, from transport.Link) {
	ev := Event{Time: time.Now(), Frame: f, Direction: In, Link: from.Name()}
	h.mu.RLock()
	links := make([]transport.Link, 0, len(h.links))
	for _, m := range h.links {
		if m.link != from {
			links = append(links, m.link)
		}
	}
	subs := h.subscribers()
	h.mu.RUnlock()

	for _, link := range links {
		if err := link.Send(f); err != nil {
			h.logger.Debug("forward failed", "from", from.Name(), "to", link.Name(), "error", err)
		}
	}
	for _, sub := range subs {
		sub(ev)
	}
}

// subscribers snapshots the subscriber list; the caller holds h.mu.
func (h *Hub) subscribers() []Subscriber {
	subs := make([]Subscriber, 0, len(h.subs))
	for _, sub := range h.subs {
		subs = append(subs, sub)
	}
	return subs
}

// Links reports every link's status, sorted by name.
func (h *Hub) Links() []transport.Status {
	h.mu.RLock()
	statuses := make([]transport.Status, 0, len(h.links))
	for _, m := range h.links {
		statuses = append(statuses, m.link.Status())
	}
	h.mu.RUnlock()
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}
