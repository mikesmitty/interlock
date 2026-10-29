// Package withrottle serves the WiThrottle protocol that throttle apps
// such as Engine Driver and WiThrottle speak. They list the roster, take
// locomotives and drive them, throw turnouts, switch track power and show
// the fast clock. The server turns their commands into the train
// manager's, so on the LCC bus every app drives through Interlock's node.
package withrottle

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/mikesmitty/interlock/internal/clock"
	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/train"
)

// Trains is what the server needs from the train manager.
type Trains interface {
	List() []train.Train
	Get(id string) (train.Train, bool)
	Acquire(ctx context.Context, id string) (train.Train, error)
	Release(ctx context.Context, id string) (train.Train, error)
	SetSpeed(ctx context.Context, id string, speed float32, direction string) (train.Train, error)
	EmergencyStop(ctx context.Context, id string) (train.Train, error)
	SetFunction(ctx context.Context, id string, fn int, on bool) (train.Train, error)
	Consists() []train.ConsistState
	Layout() train.Layout
	SetPower(ctx context.Context, on bool) (train.Layout, error)
	Subscribe(train.Subscriber) func()
	SubscribeLayout(train.LayoutSubscriber) func()
	SubscribeConsists(train.ConsistSubscriber) func()
}

// Turnouts is what the server needs from the entity manager.
type Turnouts interface {
	List() []entity.Entity
	Command(ctx context.Context, id, command string) (entity.Entity, error)
	Subscribe(entity.Subscriber) func()
}

// Clock is the fast clock.
type Clock interface {
	State() clock.State
	Subscribe(func(clock.State)) func()
}

// Options configure the server.
type Options struct {
	// Listen is the address to listen on, such as ":12090".
	Listen   string
	Trains   Trains
	Turnouts Turnouts // nil leaves turnouts out
	Clock    Clock    // nil leaves the clock out
	// Name and Version describe the server to the apps.
	Name    string
	Version string
	// WebPort is the web interface's port, which apps can open; zero
	// leaves it out.
	WebPort int
	// Heartbeat is how long an app that asked to be watched may stay
	// silent before its trains are stopped; 10 seconds when zero.
	Heartbeat time.Duration
	// Advertise announces the server on the local network by mDNS, under
	// Name, so apps list it without being given an address.
	Advertise bool
	Logger    *slog.Logger
}

// serviceType is what apps browse for.
const serviceType = "_withrottle._tcp"

// commandTimeout bounds each train or turnout command.
const commandTimeout = 10 * time.Second

// Server accepts throttle apps.
type Server struct {
	opts Options
	log  *slog.Logger
	ln   net.Listener

	mu      sync.Mutex
	clients map[*client]struct{}
	// held counts the apps' throttles holding each address, so Interlock
	// gives a train up only when the last one lets it go.
	held map[string]int
}

// New makes a server; Listen binds it and Run serves.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 10 * time.Second
	}
	if opts.Name == "" {
		opts.Name = "Interlock"
	}
	return &Server{
		opts:    opts,
		log:     opts.Logger.With("component", "withrottle"),
		clients: make(map[*client]struct{}),
		held:    make(map[string]int),
	}
}

// Listen binds the server's address, so a port in use shows at start.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Addr is the address the server listens on, once Listen has bound it.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Run serves apps until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	unsubscribe := s.subscribe()
	defer unsubscribe()
	if s.opts.Advertise {
		if stop := s.advertise(); stop != nil {
			defer stop()
		}
	}
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()
	s.log.Info("WiThrottle server listening", "address", s.ln.Addr().String())
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.log.Warn("accept failed", "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		c := newClient(s, conn)
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.run(ctx)
			s.mu.Lock()
			delete(s.clients, c)
			s.mu.Unlock()
		}()
	}
}

// advertise announces the server by mDNS and returns what ends it, or nil
// when it could not be announced; apps can still be given the address.
func (s *Server) advertise() (stop func()) {
	port := 0
	if addr, ok := s.ln.Addr().(*net.TCPAddr); ok {
		port = addr.Port
	}
	// The host's own name, without the .local some systems already give
	// it, and no fixed addresses: each network is told the address
	// Interlock has on it.
	host, _ := os.Hostname()
	host = strings.TrimSuffix(strings.TrimSuffix(host, "."), ".local")
	if host == "" {
		host = "interlock"
	}
	text := []string{"version=" + s.opts.Version}
	mdns, err := zeroconf.RegisterProxy(clean(s.opts.Name), serviceType, "local.", port, host, nil, text, nil)
	if err != nil {
		s.log.Warn("could not announce the WiThrottle server; apps need its address", "error", err)
		return nil
	}
	s.log.Info("announced the WiThrottle server", "name", s.opts.Name)
	return mdns.Shutdown
}

// subscribe follows the trains, the consists, the layout, the turnouts
// and the clock, passing each change to every app.
func (s *Server) subscribe() (unsubscribe func()) {
	var subs []func()
	subs = append(subs, s.opts.Trains.Subscribe(func(t train.Train) {
		s.each(func(c *client) { c.trainChanged(t) })
	}))
	subs = append(subs, s.opts.Trains.SubscribeLayout(func(l train.Layout) {
		s.each(func(c *client) { c.layoutChanged(l) })
	}))
	subs = append(subs, s.opts.Trains.SubscribeConsists(func(train.ConsistState) {
		s.each(func(c *client) { c.consistsChanged() })
	}))
	if s.opts.Turnouts != nil {
		subs = append(subs, s.opts.Turnouts.Subscribe(func(e entity.Entity) {
			if e.Kind == entity.KindTurnout {
				s.each(func(c *client) { c.turnoutChanged(e) })
			}
		}))
	}
	if s.opts.Clock != nil {
		subs = append(subs, s.opts.Clock.Subscribe(func(st clock.State) {
			s.each(func(c *client) { c.clockChanged(st) })
		}))
	}
	return func() {
		for _, unsubscribe := range subs {
			unsubscribe()
		}
	}
}

// each queues fn on every app's goroutine. The subscribers call it from
// the managers, so it must not block.
func (s *Server) each(fn func(*client)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.queue(fn)
	}
}

// replace closes the other connections of an app that connected again,
// which it shows by sending the same device ID.
func (s *Server) replace(c *client, device string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.device = device
	for other := range s.clients {
		if other != c && other.device == device {
			other.replaced.Store(true)
			other.close()
		}
	}
}

// hold counts another throttle holding an address.
func (s *Server) hold(address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[address]++
}

// unhold counts a throttle letting an address go and reports whether it
// was the last.
func (s *Server) unhold(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[address]--
	if s.held[address] > 0 {
		return false
	}
	delete(s.held, address)
	return true
}
