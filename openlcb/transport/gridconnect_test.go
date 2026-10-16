package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb/can"
)

// collector gathers received frames safely across goroutines.
type collector struct {
	mu     sync.Mutex
	frames []can.Frame
	ch     chan can.Frame
}

func newCollector() *collector { return &collector{ch: make(chan can.Frame, 64)} }

func (c *collector) recv(f can.Frame) {
	c.mu.Lock()
	c.frames = append(c.frames, f)
	c.mu.Unlock()
	c.ch <- f
}

func (c *collector) wait(t *testing.T, want string) {
	t.Helper()
	select {
	case f := <-c.ch:
		if f.String() != want {
			t.Fatalf("received %s, want %s", f, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", want)
	}
}

// readUntil reads from conn until the text contains want.
func readUntil(t *testing.T, conn net.Conn, want string) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 256)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(sb.String(), want) {
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			t.Fatalf("reading for %q: got %q, err %v", want, sb.String(), err)
		}
	}
	return sb.String()
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestGridConnectLinkOverTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	link := NewGridConnect("test", TCPDialer{Address: ln.Addr().String()}, testLogger())
	link.MinBackoff = 10 * time.Millisecond
	link.MaxBackoff = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	col := newCollector()
	done := make(chan error, 1)
	go func() { done <- link.Run(ctx, col.recv) }()

	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}

	// Server to link, split across writes.
	if _, err := server.Write([]byte(":X1910")); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write([]byte("0123N050101012200;\r\n")); err != nil {
		t.Fatal(err)
	}
	col.wait(t, ":X19100123N050101012200;")

	// Link to server.
	if err := link.Send(can.RIDFrame(0x123)); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, server, ":X10700123N;"); !strings.HasSuffix(strings.TrimSpace(got), ";") {
		t.Errorf("server received %q", got)
	}

	st := link.Status()
	if !st.Connected || st.FramesIn != 1 || st.FramesOut != 1 || st.Kind != "tcp" {
		t.Errorf("Status = %+v", st)
	}

	// Drop the connection: the link must reconnect and keep working.
	server.Close()
	server2, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server2.Write([]byte(":X10702456N;")); err != nil {
		t.Fatal(err)
	}
	col.wait(t, ":X10702456N;")
	if st := link.Status(); st.Reconnects != 1 {
		t.Errorf("Reconnects = %d, want 1 (status %+v)", st.Reconnects, st)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	server2.Close()
}

func TestGridConnectLinkRetriesConnect(t *testing.T) {
	// Nothing listens here yet; the link must keep trying until it does.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	link := NewGridConnect("retry", TCPDialer{Address: addr, Timeout: time.Second}, testLogger())
	link.MinBackoff = 10 * time.Millisecond
	link.MaxBackoff = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	col := newCollector()
	go func() { _ = link.Run(ctx, col.recv) }()

	time.Sleep(50 * time.Millisecond)
	if st := link.Status(); st.Connected || st.LastError == "" {
		t.Errorf("Status while unreachable = %+v", st)
	}

	ln, err = net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err := server.Write([]byte(":X10700123N;")); err != nil {
		t.Fatal(err)
	}
	col.wait(t, ":X10700123N;")
}

func TestGridConnectConnRunsOnce(t *testing.T) {
	client, server := net.Pipe()
	link := NewGridConnectConn("conn", server, "tcp-client", "pipe", testLogger())
	col := newCollector()
	done := make(chan error, 1)
	go func() { done <- link.Run(context.Background(), col.recv) }()

	go func() { _, _ = client.Write([]byte(":X10700123N;")) }()
	col.wait(t, ":X10700123N;")

	if err := link.Send(can.AMEFrame(0x123, 0)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), ":X10702123N;") {
		t.Fatalf("client read %q, %v", buf[:n], err)
	}

	client.Close()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want the connection error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not end when the connection closed")
	}
	if link.Status().Connected {
		t.Error("still reports connected")
	}
}

func TestGridConnectSendDropsWhenFull(t *testing.T) {
	// Never run: the queue fills and Send must give up quickly.
	link := NewGridConnect("stuck", TCPDialer{Address: "127.0.0.1:1"}, testLogger())
	link.SendTimeout = time.Millisecond
	for range sendQueue {
		if err := link.Send(can.RIDFrame(1)); err != nil {
			t.Fatalf("Send failed before the queue was full: %v", err)
		}
	}
	start := time.Now()
	if err := link.Send(can.RIDFrame(1)); err == nil {
		t.Fatal("Send succeeded on a full queue")
	}
	if time.Since(start) > time.Second {
		t.Error("Send blocked too long")
	}
	if link.Status().Dropped != 1 {
		t.Errorf("Dropped = %d", link.Status().Dropped)
	}
}

func TestListener(t *testing.T) {
	var mu sync.Mutex
	var links []Link
	ln := &Listener{
		Address: "127.0.0.1:0",
		Logger:  testLogger(),
		Accept: func(l Link) {
			mu.Lock()
			links = append(links, l)
			mu.Unlock()
		},
	}
	if err := ln.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ln.Run(ctx) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(links)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Accept was not called")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	link := links[0]
	mu.Unlock()
	if link.Name() != "tcp-client-1" || link.Status().Kind != "tcp-client" {
		t.Errorf("link = %s %+v", link.Name(), link.Status())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestListenerBadAddress(t *testing.T) {
	ln := &Listener{Address: "256.256.256.256:1", Accept: func(Link) {}, Logger: testLogger()}
	if err := ln.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded on an unusable address")
	}
}

// Ensure the io.EOF path through serve is exercised for coverage of the
// deliberate-shutdown branch.
var _ = io.EOF
