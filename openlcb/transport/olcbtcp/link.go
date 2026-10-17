package olcbtcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// Kind is the link kind reported in Status.
const Kind = "olcbtcp"

// Link is an OpenLCB TCP connection joined to the hub through a Gateway.
// With a Dialer it connects and reconnects with backoff; wrapped around an
// accepted connection it serves that one connection and ends.
type Link struct {
	name   string
	id     openlcb.NodeID
	dialer transport.Dialer
	conn   io.ReadWriteCloser
	target string
	logger *slog.Logger
	out    chan can.Frame

	mu          sync.Mutex
	connected   bool
	since       time.Time
	lastError   string
	framesIn    uint64 // messages from TCP
	framesOut   uint64 // messages to TCP
	dropped     uint64
	parseErrors uint64
	reconnects  uint64

	// Backoff bounds the delay between reconnection attempts.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// CIDWait is passed to the gateway.
	CIDWait time.Duration
}

const (
	sendQueue = 512
	tickEvery = 50 * time.Millisecond
)

// NewLink returns a reconnecting link to an OpenLCB TCP hub. id is this
// gateway's node ID.
func NewLink(name string, id openlcb.NodeID, dialer transport.Dialer, logger *slog.Logger) *Link {
	l := newLink(name, id, logger)
	l.dialer = dialer
	l.target = dialer.Target()
	return l
}

// NewConnLink returns a link over an accepted connection; its Run ends
// when the connection does.
func NewConnLink(name string, id openlcb.NodeID, conn io.ReadWriteCloser, target string, logger *slog.Logger) *Link {
	l := newLink(name, id, logger)
	l.conn = conn
	l.target = target
	return l
}

func newLink(name string, id openlcb.NodeID, logger *slog.Logger) *Link {
	if logger == nil {
		logger = slog.Default()
	}
	return &Link{
		name: name, id: id, logger: logger.With("link", name),
		out:        make(chan can.Frame, sendQueue),
		MinBackoff: time.Second, MaxBackoff: 30 * time.Second,
	}
}

// Name implements transport.Link.
func (l *Link) Name() string { return l.name }

// Status implements transport.Link.
func (l *Link) Status() transport.Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return transport.Status{
		Name: l.name, Kind: Kind, Target: l.target, Connected: l.connected, Since: l.since, LastError: l.lastError,
		FramesIn: l.framesIn, FramesOut: l.framesOut, Dropped: l.dropped, ParseErrors: l.parseErrors, Reconnects: l.reconnects,
	}
}

// Send implements transport.Link: the frame goes to the gateway, which
// turns it into a message for TCP.
func (l *Link) Send(f can.Frame) error {
	select {
	case l.out <- f:
		return nil
	default:
		l.mu.Lock()
		l.dropped++
		l.mu.Unlock()
		return errors.New("olcbtcp: send queue full")
	}
}

func (l *Link) setConnected(connected bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if connected && !l.connected {
		l.since = time.Now()
	}
	l.connected = connected
	if err != nil {
		l.lastError = err.Error()
	}
}

// Run implements transport.Link.
func (l *Link) Run(ctx context.Context, recv func(can.Frame)) error {
	if l.conn != nil {
		err := l.serve(ctx, l.conn, recv)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	backoff := l.MinBackoff
	for {
		conn, err := l.dialer.Dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			l.setConnected(false, err)
			l.logger.Warn("connect failed", "target", l.target, "error", err, "retry_in", backoff)
		} else {
			l.logger.Info("connected", "target", l.target)
			err = l.serve(ctx, conn, recv)
			if ctx.Err() != nil {
				return nil
			}
			l.logger.Warn("connection lost", "target", l.target, "error", err, "retry_in", backoff)
			l.mu.Lock()
			l.reconnects++
			l.mu.Unlock()
			backoff = l.MinBackoff
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, l.MaxBackoff)
	}
}

// serve runs one connection until it fails or ctx ends. It always returns
// a non-nil error describing why the connection ended.
func (l *Link) serve(ctx context.Context, conn io.ReadWriteCloser, recv func(can.Frame)) error {
	l.setConnected(true, nil)
	defer l.setConnected(false, nil)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Writes go through their own goroutine so a slow peer never stalls
	// the gateway.
	tx := make(chan []byte, 256)
	writeErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-tx:
				if _, err := conn.Write(b); err != nil {
					writeErr <- err
					return
				}
			}
		}
	}()
	in := make(chan *openlcb.Message, 256)
	readErr := make(chan error, 1)
	reader := NewReader(conn)
	go func() {
		for {
			msg, _, err := reader.Next()
			if err != nil {
				readErr <- err
				return
			}
			select {
			case in <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	gw := NewGateway(Options{
		ID:      l.id,
		CIDWait: l.CIDWait,
		Logger:  l.logger,
		ToCAN:   recv,
		ToTCP: func(msg *openlcb.Message) {
			b, err := Encode(nil, msg, l.id, uint64(time.Now().UnixMilli())&0xFFFFFFFFFFFF)
			if err != nil {
				l.logger.Debug("message not sent", "error", err)
				return
			}
			select {
			case tx <- b:
				l.mu.Lock()
				l.framesOut++
				l.mu.Unlock()
			default:
				l.mu.Lock()
				l.dropped++
				l.mu.Unlock()
			}
		},
	})
	defer gw.Close()
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close()
			return ctx.Err()
		case err := <-writeErr:
			conn.Close()
			return fmt.Errorf("write: %w", err)
		case err := <-readErr:
			conn.Close()
			l.mu.Lock()
			l.parseErrors += reader.Errors
			l.mu.Unlock()
			if errors.Is(err, io.EOF) {
				return errors.New("connection closed by the other end")
			}
			return fmt.Errorf("read: %w", err)
		case msg := <-in:
			l.mu.Lock()
			l.framesIn++
			l.mu.Unlock()
			gw.HandleMessage(msg, time.Now())
		case f := <-l.out:
			gw.HandleFrame(f, time.Now())
		case now := <-tick.C:
			gw.Tick(now)
		}
	}
}
