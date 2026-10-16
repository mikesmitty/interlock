// Command interlock runs the Interlock LCC layout controller: the bus hub,
// the local node, the automation engine and the web interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"github.com/mikesmitty/interlock/internal/api"
	"github.com/mikesmitty/interlock/internal/config"
	"github.com/mikesmitty/interlock/internal/identity"
	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/server"
	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/internal/version"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/hub"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/transport"
	"github.com/mikesmitty/interlock/web"
)

// monitorCapacity is how many bus entries the monitor keeps.
const monitorCapacity = 5000

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "interlock:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("interlock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", defaultDataDir(),
		"directory holding interlock.yaml and the database (env INTERLOCK_DATA)")
	listen := flags.String("listen", os.Getenv("INTERLOCK_LISTEN"),
		"listen address, overriding the config file (env INTERLOCK_LISTEN)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintf(stdout, "interlock %s %s\n", version.Version, version.Commit())
		return err
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	logger, err := newLogger(cfg.Log, stderr)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, config.DBPath(*dataDir))
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("closing store", "error", err)
		}
	}()

	nodeID, err := identity.Resolve(ctx, cfg.NodeID, st)
	if err != nil {
		return err
	}

	logger.Info("starting interlock",
		"version", version.Version, "commit", version.Commit(),
		"node_id", nodeID, "data", *dataDir, "listen", cfg.Listen)

	// The bus outlives the request context by a moment so the node can
	// withdraw its alias on the way out.
	busCtx, stopBus := context.WithCancel(context.Background())
	defer stopBus()
	h := hub.New(logger)
	defer h.Close()

	snip := openlcb.SNIP{
		Manufacturer:    "Interlock",
		Model:           "Layout Controller",
		HardwareVersion: runtime.GOOS + "/" + runtime.GOARCH,
		SoftwareVersion: version.Version,
		UserName:        cfg.Node.Name,
		UserDescription: cfg.Node.Description,
	}
	protocols := openlcb.ProtocolSimple | openlcb.ProtocolDatagram |
		openlcb.ProtocolEventExchange | openlcb.ProtocolSimpleNodeInformation
	local := node.New(node.Config{ID: nodeID, SNIP: snip, Protocols: protocols, Send: h.Send, Logger: logger})
	mon := monitor.New(monitorCapacity, local.Cache())
	h.Subscribe(func(ev hub.Event) {
		if ev.Direction == hub.In {
			local.HandleFrame(ev.Frame)
		}
		mon.HandleHubEvent(ev)
	})
	local.Observe(mon.HandleNodeEvent)
	reg := registry.New(local, registry.Options{
		Interval:       cfg.Discovery.Interval,
		LocalSNIP:      snip,
		LocalProtocols: protocols,
		Logger:         logger,
	})

	for _, lc := range cfg.Links {
		var dialer transport.Dialer
		switch lc.Type {
		case "serial":
			dialer = transport.SerialDialer{Device: lc.Device, Baud: lc.Baud}
		case "tcp":
			dialer = transport.TCPDialer{Address: lc.Address}
		default:
			continue
		}
		h.AddLink(busCtx, transport.NewGridConnect(lc.Name, dialer, logger))
	}
	if cfg.Hub.Listen != "" {
		listener := &transport.Listener{
			Address: cfg.Hub.Listen,
			Logger:  logger,
			Accept:  func(link transport.Link) { h.AddLink(busCtx, link) },
		}
		if err := listener.Listen(); err != nil {
			logger.Error("hub port unavailable; other tools cannot connect", "error", err)
		} else {
			go func() {
				if err := listener.Run(busCtx); err != nil {
					logger.Error("hub listener stopped", "error", err)
				}
			}()
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := local.Run(ctx); err != nil {
			logger.Error("node stopped", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := reg.Run(ctx); err != nil {
			logger.Error("registry stopped", "error", err)
		}
	}()

	srv := server.New(server.Options{
		Listen: cfg.Listen,
		Logger: logger,
		API: api.New(api.Deps{
			Node:        local,
			Registry:    reg,
			Monitor:     mon,
			Bus:         h,
			SerialPorts: transport.SerialPorts,
			Logger:      logger,
		}),
		UI: web.Dist(),
	})
	err = srv.Run(ctx)
	stop()
	wg.Wait() // the node has sent its AMR by now; the hub can close
	return err
}

// defaultDataDir is INTERLOCK_DATA, else an "interlock" directory under the
// user's configuration directory.
func defaultDataDir() string {
	if dir := os.Getenv("INTERLOCK_DATA"); dir != "" {
		return dir
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "interlock-data"
	}
	return filepath.Join(base, "interlock")
}

func newLogger(cfg config.LogConfig, w io.Writer) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", cfg.Level, err)
	}
	opts := &slog.HandlerOptions{Level: level}
	switch cfg.Format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text", "":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, errors.New("log format must be text or json")
	}
}
