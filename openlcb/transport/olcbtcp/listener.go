package olcbtcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// Listener accepts OpenLCB TCP clients, such as OpenMRN nodes configured
// to reach a hub over TCP. Each connection becomes a Link with its own
// gateway, handed to Accept.
type Listener struct {
	Address string
	// ID is the gateway node ID written into frames sent to clients.
	ID openlcb.NodeID
	// Accept receives each new link and should run it (the hub does).
	Accept func(link transport.Link)
	Logger *slog.Logger

	listener net.Listener
	count    atomic.Uint64
}

// Addr returns the bound address once Listen has run.
func (l *Listener) Addr() net.Addr {
	if l.listener == nil {
		return nil
	}
	return l.listener.Addr()
}

// Listen binds the address without accepting yet.
func (l *Listener) Listen() error {
	if l.listener != nil {
		return nil
	}
	ln, err := net.Listen("tcp", l.Address)
	if err != nil {
		return fmt.Errorf("olcbtcp: listen %s: %w", l.Address, err)
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
	logger.Info("listening for OpenLCB TCP connections", "address", l.listener.Addr().String())
	go func() {
		<-ctx.Done()
		l.listener.Close()
	}()
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("olcbtcp: accept: %w", err)
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		name := fmt.Sprintf("olcbtcp-client-%d", l.count.Add(1))
		logger.Info("OpenLCB TCP client connected", "link", name, "remote", conn.RemoteAddr().String())
		l.Accept(NewConnLink(name, l.ID, conn, conn.RemoteAddr().String(), logger))
	}
}
