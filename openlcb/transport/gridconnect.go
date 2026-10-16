package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/mikesmitty/interlock/openlcb/can"
)

// Dialer opens the byte stream a GridConnect link runs over.
type Dialer interface {
	Dial(ctx context.Context) (io.ReadWriteCloser, error)
	// Kind and Target describe the link for Status.
	Kind() string
	Target() string
}

// GridConnectLink speaks GridConnect over a byte stream. With a Dialer it
// reconnects with backoff whenever the stream fails; wrapped around an
// accepted connection it runs that one connection and then ends.
type GridConnectLink struct {
	name   string
	dialer Dialer
	conn   io.ReadWriteCloser // set when wrapping one connection
	logger *slog.Logger
	out    chan can.Frame
	stats  stats

	// Backoff bounds the delay between reconnection attempts.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// SendTimeout is how long Send waits for room before dropping.
	SendTimeout time.Duration
}

// sendQueue is how many outbound frames a link buffers.
const sendQueue = 512

// NewGridConnect returns a reconnecting link over dialer.
func NewGridConnect(name string, dialer Dialer, logger *slog.Logger) *GridConnectLink {
	return newGridConnect(name, dialer, nil, logger)
}

// NewGridConnectConn returns a link over an already open connection, such
// as one accepted by a Listener. Its Run ends when the connection does.
func NewGridConnectConn(name string, conn io.ReadWriteCloser, kind, target string, logger *slog.Logger) *GridConnectLink {
	return newGridConnect(name, staticDialer{kind: kind, target: target}, conn, logger)
}

func newGridConnect(name string, dialer Dialer, conn io.ReadWriteCloser, logger *slog.Logger) *GridConnectLink {
	if logger == nil {
		logger = slog.Default()
	}
	return &GridConnectLink{
		name:        name,
		dialer:      dialer,
		conn:        conn,
		logger:      logger.With("link", name),
		out:         make(chan can.Frame, sendQueue),
		MinBackoff:  time.Second,
		MaxBackoff:  30 * time.Second,
		SendTimeout: 100 * time.Millisecond,
	}
}

type staticDialer struct{ kind, target string }

func (d staticDialer) Dial(context.Context) (io.ReadWriteCloser, error) {
	return nil, errors.New("transport: connection cannot be reopened")
}
func (d staticDialer) Kind() string   { return d.kind }
func (d staticDialer) Target() string { return d.target }

// Name implements Link.
func (l *GridConnectLink) Name() string { return l.name }

// Status implements Link.
func (l *GridConnectLink) Status() Status {
	st := Status{Name: l.name, Kind: l.dialer.Kind(), Target: l.dialer.Target()}
	l.stats.fill(&st)
	return st
}

// Send implements Link. Frames queue for the writer; when the queue stays
// full past SendTimeout the frame is dropped and counted.
func (l *GridConnectLink) Send(f can.Frame) error {
	select {
	case l.out <- f:
		return nil
	default:
	}
	timer := time.NewTimer(l.SendTimeout)
	defer timer.Stop()
	select {
	case l.out <- f:
		return nil
	case <-timer.C:
		l.stats.mu.Lock()
		l.stats.dropped++
		dropped := l.stats.dropped
		l.stats.mu.Unlock()
		if dropped == 1 || dropped%100 == 0 {
			l.logger.Warn("send queue full, dropping frames", "dropped", dropped)
		}
		return fmt.Errorf("transport: %s: send queue full", l.name)
	}
}

// Run implements Link.
func (l *GridConnectLink) Run(ctx context.Context, recv func(can.Frame)) error {
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
			l.stats.setConnected(false, err)
			l.logger.Warn("connect failed", "target", l.dialer.Target(), "error", err, "retry_in", backoff)
		} else {
			l.logger.Info("connected", "target", l.dialer.Target())
			err = l.serve(ctx, conn, recv)
			if ctx.Err() != nil {
				return nil
			}
			l.logger.Warn("connection lost", "target", l.dialer.Target(), "error", err, "retry_in", backoff)
			l.stats.mu.Lock()
			l.stats.reconnects++
			l.stats.mu.Unlock()
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
func (l *GridConnectLink) serve(ctx context.Context, conn io.ReadWriteCloser, recv func(can.Frame)) error {
	l.stats.setConnected(true, nil)
	defer l.stats.setConnected(false, nil)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Closing the connection is what unblocks the reader, so one goroutine
	// waits for ctx (or the writer's failure) and closes.
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- l.writeLoop(ctx, conn)
	}()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	readErr := l.readLoop(conn, recv)
	cancel()
	werr := <-writeErr

	if werr != nil && !errors.Is(werr, context.Canceled) {
		l.stats.setConnected(false, werr)
		return werr
	}
	l.stats.setConnected(false, readErr)
	return readErr
}

func (l *GridConnectLink) readLoop(conn io.Reader, recv func(can.Frame)) error {
	var parser can.GridConnectParser
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			before := parser.Errors
			frames := parser.Push(buf[:n])
			l.stats.mu.Lock()
			l.stats.framesIn += uint64(len(frames))
			l.stats.parseErrors += uint64(parser.Errors - before)
			l.stats.mu.Unlock()
			for _, f := range frames {
				recv(f)
			}
		}
		if err != nil {
			return err
		}
	}
}

func (l *GridConnectLink) writeLoop(ctx context.Context, conn io.Writer) error {
	buf := make([]byte, 0, 64)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-l.out:
			buf = can.AppendGridConnect(buf[:0], f)
			buf = append(buf, '\n')
			// Coalesce whatever else is queued into one write.
			for len(buf) < 1024 {
				select {
				case next := <-l.out:
					buf = can.AppendGridConnect(buf, next)
					buf = append(buf, '\n')
					continue
				default:
				}
				break
			}
			if _, err := conn.Write(buf); err != nil {
				return err
			}
			l.stats.mu.Lock()
			l.stats.framesOut++
			l.stats.mu.Unlock()
		}
	}
}
