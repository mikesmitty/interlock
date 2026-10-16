package hub

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// fakeLink is an in-memory link: frames pushed with inject appear as
// received, frames the hub sends are recorded.
type fakeLink struct {
	name string
	mu   sync.Mutex
	sent []can.Frame
	recv func(can.Frame)
	ran  chan struct{}
	fail error
}

func newFakeLink(name string) *fakeLink {
	return &fakeLink{name: name, ran: make(chan struct{})}
}

func (l *fakeLink) Name() string { return l.name }

func (l *fakeLink) Send(f can.Frame) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, f)
	return nil
}

func (l *fakeLink) Run(ctx context.Context, recv func(can.Frame)) error {
	l.mu.Lock()
	l.recv = recv
	l.mu.Unlock()
	close(l.ran)
	if l.fail != nil {
		return l.fail
	}
	<-ctx.Done()
	return nil
}

func (l *fakeLink) Status() transport.Status {
	return transport.Status{Name: l.name, Kind: "fake", Connected: true}
}

func (l *fakeLink) inject(f can.Frame) {
	l.mu.Lock()
	recv := l.recv
	l.mu.Unlock()
	recv(f)
}

func (l *fakeLink) sentFrames() []can.Frame {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]can.Frame(nil), l.sent...)
}

func waitRunning(t *testing.T, links ...*fakeLink) {
	t.Helper()
	for _, l := range links {
		select {
		case <-l.ran:
		case <-time.After(5 * time.Second):
			t.Fatalf("link %s never ran", l.name)
		}
	}
}

func TestHubForwardsBetweenLinks(t *testing.T) {
	h := New(slog.New(slog.DiscardHandler))
	defer h.Close()
	a, b, c := newFakeLink("a"), newFakeLink("b"), newFakeLink("c")
	ctx := context.Background()
	h.AddLink(ctx, a)
	h.AddLink(ctx, b)
	h.AddLink(ctx, c)
	waitRunning(t, a, b, c)

	var mu sync.Mutex
	var events []Event
	unsubscribe := h.Subscribe(func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	frame := can.RIDFrame(0x123)
	a.inject(frame)

	if got := a.sentFrames(); len(got) != 0 {
		t.Errorf("frame echoed to its origin: %v", got)
	}
	for _, l := range []*fakeLink{b, c} {
		if got := l.sentFrames(); len(got) != 1 || got[0] != frame {
			t.Errorf("link %s received %v, want [%v]", l.name, got, frame)
		}
	}
	mu.Lock()
	if len(events) != 1 || events[0].Direction != In || events[0].Link != "a" || events[0].Frame != frame {
		t.Errorf("events = %+v", events)
	}
	mu.Unlock()

	out := can.AMEFrame(0x456, 0)
	h.Send(out)
	for _, l := range []*fakeLink{a, b, c} {
		got := l.sentFrames()
		if got[len(got)-1] != out {
			t.Errorf("link %s did not get the local frame: %v", l.name, got)
		}
	}
	mu.Lock()
	if len(events) != 2 || events[1].Direction != Out || events[1].Link != "" {
		t.Errorf("events after Send = %+v", events)
	}
	mu.Unlock()

	unsubscribe()
	h.Send(out)
	mu.Lock()
	if len(events) != 2 {
		t.Error("unsubscribed subscriber still called")
	}
	mu.Unlock()

	statuses := h.Links()
	if len(statuses) != 3 || statuses[0].Name != "a" || statuses[2].Name != "c" {
		t.Errorf("Links = %+v", statuses)
	}
}

func TestHubRemoveAndReplace(t *testing.T) {
	h := New(slog.New(slog.DiscardHandler))
	defer h.Close()
	a := newFakeLink("a")
	h.AddLink(context.Background(), a)
	waitRunning(t, a)

	h.RemoveLink("a")
	if len(h.Links()) != 0 {
		t.Fatal("link still listed after RemoveLink")
	}
	h.Send(can.RIDFrame(1))
	if len(a.sentFrames()) != 0 {
		t.Error("removed link still receives")
	}

	a2 := newFakeLink("a")
	a3 := newFakeLink("a")
	h.AddLink(context.Background(), a2)
	waitRunning(t, a2)
	h.AddLink(context.Background(), a3)
	waitRunning(t, a3)
	time.Sleep(10 * time.Millisecond)
	if n := len(h.Links()); n != 1 {
		t.Fatalf("Links = %d, want 1 after replacement", n)
	}
	h.Send(can.RIDFrame(2))
	if len(a3.sentFrames()) != 1 {
		t.Error("replacement link did not receive")
	}
}

func TestHubLinkFailureIsForgotten(t *testing.T) {
	h := New(slog.New(slog.DiscardHandler))
	defer h.Close()
	bad := newFakeLink("bad")
	bad.fail = context.DeadlineExceeded
	h.AddLink(context.Background(), bad)
	waitRunning(t, bad)
	deadline := time.Now().Add(5 * time.Second)
	for len(h.Links()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("failed link still listed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHubCloseStopsLinks(t *testing.T) {
	h := New(slog.New(slog.DiscardHandler))
	a := newFakeLink("a")
	h.AddLink(context.Background(), a)
	waitRunning(t, a)
	h.Close()
	if len(h.Links()) != 0 {
		t.Error("links remain after Close")
	}
	b := newFakeLink("b")
	h.AddLink(context.Background(), b)
	select {
	case <-b.ran:
		t.Error("link added after Close was run")
	case <-time.After(50 * time.Millisecond):
	}
}
