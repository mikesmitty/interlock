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

// Codec turns frames into the bytes a stream carries and back.
// GridConnect is the usual one; SocketCAN uses the kernel's frame layout.
type Codec interface {
	// Append encodes f onto dst, including any frame terminator.
	Append(dst []byte, f can.Frame) []byte
	// Push feeds received bytes and returns the frames completed by them,
	// and how many bytes or frames could not be parsed.
	Push(b []byte) (frames []can.Frame, bad uint64)
	// MaxWrite is how many bytes one write may carry; frames are coalesced
	// up to it. Return the frame size to write one frame at a time.
	MaxWrite() int
}

// gridConnectCodec is the GridConnect text form.
type gridConnectCodec struct{ parser can.GridConnectParser }

func (c *gridConnectCodec) Append(dst []byte, f can.Frame) []byte {
	return append(can.AppendGridConnect(dst, f), '\n')
}

func (c *gridConnectCodec) Push(b []byte) ([]can.Frame, uint64) {
	before := c.parser.Errors
	frames := c.parser.Push(b)
	return frames, uint64(c.parser.Errors - before)
}

func (c *gridConnectCodec) MaxWrite() int { return 1024 }

// GridConnectLink carries frames over a byte stream, GridConnect by
// default or whatever Codec it was given. With a Dialer it reconnects
// with backoff whenever the stream fails; wrapped around an accepted
// connection it runs that one connection and then ends.
type GridConnectLink struct {
	name   string
	dialer Dialer
	conn   io.ReadWriteCloser // set when wrapping one connection
	codec  Codec
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

// NewGridConnect returns a reconnecting GridConnect link over dialer.
func NewGridConnect(name string, dialer Dialer, logger *slog.Logger) *GridConnectLink {
	return newGridConnect(name, dialer, nil, nil, logger)
}

// NewFramed returns a reconnecting link over dialer that speaks codec
// rather than GridConnect.
func NewFramed(name string, dialer Dialer, codec Codec, logger *slog.Logger) *GridConnectLink {
	return newGridConnect(name, dialer, nil, codec, logger)
}

// NewGridConnectConn returns a link over an already open connection, such
// as one accepted by a Listener. Its Run ends when the connection does.
func NewGridConnectConn(name string, conn io.ReadWriteCloser, kind, target string, logger *slog.Logger) *GridConnectLink {
	return newGridConnect(name, staticDialer{kind: kind, target: target}, conn, nil, logger)
}

func newGridConnect(name string, dialer Dialer, conn io.ReadWriteCloser, codec Codec, logger *slog.Logger) *GridConnectLink {
	if logger == nil {
		logger = slog.Default()
	}
	if codec == nil {
		codec = &gridConnectCodec{}
	}
	return &GridConnectLink{
		name:        name,
		dialer:      dialer,
		conn:        conn,
		codec:       codec,
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
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			frames, bad := l.codec.Push(buf[:n])
			l.stats.mu.Lock()
			l.stats.framesIn += uint64(len(frames))
			l.stats.parseErrors += bad
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
			buf = l.codec.Append(buf[:0], f)
			sent := uint64(1)
			// Coalesce whatever else is queued into one write.
			limit := l.codec.MaxWrite()
			for len(buf) < limit {
				select {
				case next := <-l.out:
					buf = l.codec.Append(buf, next)
					sent++
					continue
				default:
				}
				break
			}
			if _, err := conn.Write(buf); err != nil {
				return err
			}
			l.stats.mu.Lock()
			l.stats.framesOut += sent
			l.stats.mu.Unlock()
		}
	}
}
