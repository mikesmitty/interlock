package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
)

// Listener accepts GridConnect TCP connections, so JMRI, other tools or
// another Interlock can join the hub. Each accepted connection becomes a
// Link handed to Accept.
type Listener struct {
	Address string
	// Accept receives each new link and should run it (the hub does).
	Accept func(link Link)
	Logger *slog.Logger

	listener net.Listener
	count    atomic.Uint64
}

// Addr returns the bound address once Run has started listening.
func (l *Listener) Addr() net.Addr {
	if l.listener == nil {
		return nil
	}
	return l.listener.Addr()
}

// Listen binds the address without accepting yet, so callers can learn the
// port before Run.
func (l *Listener) Listen() error {
	if l.listener != nil {
		return nil
	}
	ln, err := net.Listen("tcp", l.Address)
	if err != nil {
		return fmt.Errorf("transport: listen %s: %w", l.Address, err)
	}
	l.listener = ln
	return nil
}

// Run accepts connections until ctx ends.
func (l *Listener) Run(ctx context.Context) error {
	if err := l.Listen(); err != nil {
		return err
	}
	logger := l.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("hub listening for GridConnect connections", "address", l.listener.Addr().String())

	go func() {
		<-ctx.Done()
		l.listener.Close()
	}()
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("transport: accept: %w", err)
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		n := l.count.Add(1)
		name := fmt.Sprintf("tcp-client-%d", n)
		logger.Info("GridConnect client connected", "link", name, "remote", conn.RemoteAddr().String())
		link := NewGridConnectConn(name, conn, "tcp-client", conn.RemoteAddr().String(), logger)
		l.Accept(link)
	}
}
