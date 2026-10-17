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
	"github.com/mikesmitty/interlock/internal/automation"
	"github.com/mikesmitty/interlock/internal/backup"
	"github.com/mikesmitty/interlock/internal/clock"
	"github.com/mikesmitty/interlock/internal/config"
	"github.com/mikesmitty/interlock/internal/decoder"
	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/firmware"
	"github.com/mikesmitty/interlock/internal/identity"
	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/nodeconfig"
	"github.com/mikesmitty/interlock/internal/profile"
	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/server"
	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/internal/version"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/hub"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/transport"
	"github.com/mikesmitty/interlock/openlcb/transport/olcbtcp"
	"github.com/mikesmitty/interlock/openlcb/transport/socketcan"
	"github.com/mikesmitty/interlock/web"
)

// monitorCapacity is how many bus entries the monitor keeps.
const monitorCapacity = 5000

// errRestart ends a run that should start over: after a restore, or when
// asked to reload its configuration.
var errRestart = errors.New("restart requested")

func main() {
	for {
		err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
		if errors.Is(err, errRestart) {
			continue
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "interlock:", err)
			os.Exit(1)
		}
		return
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
	// A restore staged by the previous run goes in place before anything
	// is opened.
	kept, err := backup.Apply(*dataDir)
	if err != nil {
		return err
	}
	if kept != "" {
		slog.Info("restored a backup", "previous_files", filepath.Join(*dataDir, kept))
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
	ctx, end := context.WithCancelCause(ctx)
	defer end(nil)
	restart := func() {
		logger.Info("restarting")
		end(errRestart)
	}

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
	mem := memconfig.NewClient(local, memconfig.WithLogger(logger))
	reg := registry.New(local, registry.Options{
		Interval:       cfg.Discovery.Interval,
		LocalSNIP:      snip,
		LocalProtocols: protocols,
		Logger:         logger,
		Store:          st,
		CDI:            mem,
	})
	if err := reg.Restore(ctx); err != nil {
		logger.Error("could not restore nodes from the store", "error", err)
	}
	nodeCfg := nodeconfig.New(mem, reg, logger)

	// The named event table and the entities built over it.
	table := eventtable.New(nodeCfg, reg, st, logger)
	nodeCfg.OnWritten(table.Invalidate)
	table.Restore(ctx, reg.List())
	profiles, err := profile.Load(filepath.Join(*dataDir, profile.DirName))
	if err != nil {
		return err
	}
	logger.Info("loaded node profiles", "count", len(profiles.List()))
	entFile, err := entity.OpenFile(filepath.Join(*dataDir, entity.FileName))
	if err != nil {
		return err
	}
	entities := entity.New(entity.Options{
		Bus: local, Nodes: reg, Layouts: reg, Values: table, Builder: profiles, File: entFile, Logger: logger,
	})
	reg.Subscribe(table.HandleNode)
	reg.Subscribe(entities.HandleNode)
	table.Subscribe(entities.Rebuild)

	// The trains and the CV programmer.
	trainFile, err := train.OpenFile(filepath.Join(*dataDir, train.FileName))
	if err != nil {
		return err
	}
	cvStore := train.OpenCVStore(filepath.Join(*dataDir, train.CVDirName), logger)
	defer func() {
		if err := cvStore.Close(); err != nil {
			logger.Warn("saving CV values failed", "error", err)
		}
	}()
	trains := train.New(train.Options{Bus: local, Nodes: reg, File: trainFile, Logger: logger, CVs: cvStore})
	logger.Info("loaded the roster", "count", len(trainFile.List()))
	reg.Subscribe(trains.HandleNode)
	prog := programmer.New(programmer.Options{Bus: local, Memory: mem, Nodes: reg, Trains: trains, Logger: logger})
	reg.Subscribe(prog.HandleNode)
	decoders, err := decoder.Open(decoder.Options{
		Source: cfg.Decoders.Source, Ref: cfg.Decoders.Ref, Path: cfg.Decoders.Path,
		CacheDir:  filepath.Join(*dataDir, decoder.CacheDirName),
		UserAgent: "interlock/" + version.Version,
		Logger:    logger,
	})
	if err != nil {
		return err
	}
	defer func() { _ = decoders.Close() }()
	upgrader := firmware.New(firmware.Options{Bus: local, Memory: mem, Nodes: reg, Logger: logger})

	// The fast clock, broadcast on the bus.
	fast := clock.New(ctx, clock.Options{Bus: local, Store: st, Logger: logger})

	// The automations over the entities, trains and clock.
	autoFile, err := automation.OpenFile(filepath.Join(*dataDir, automation.FileName))
	if err != nil {
		return err
	}
	engine := automation.New(automation.Options{
		Entities: entities, Bus: local, Nodes: reg, Trains: trains, Clock: fast, Store: st, File: autoFile, Logger: logger,
	})
	logger.Info("loaded automations", "count", len(engine.List()))
	entities.Subscribe(engine.HandleEntity)
	reg.Subscribe(engine.HandleNode)
	fast.OnMinute(engine.HandleClock)
	local.Observe(func(ev node.Event) {
		if ev.Kind == node.EventMessage {
			table.HandleMessage(ev.Message)
			entities.HandleMessage(ev.Message)
			trains.HandleMessage(ev.Message)
			fast.HandleMessage(ev.Message)
			engine.HandleMessage(ev.Message)
		}
	})

	for _, lc := range cfg.Links {
		switch lc.Type {
		case config.LinkSerial:
			h.AddLink(busCtx, transport.NewGridConnect(lc.Name, transport.SerialDialer{Device: lc.Device, Baud: lc.Baud}, logger))
		case config.LinkTCP:
			h.AddLink(busCtx, transport.NewGridConnect(lc.Name, transport.TCPDialer{Address: lc.Address}, logger))
		case config.LinkOpenLCB:
			h.AddLink(busCtx, olcbtcp.NewLink(lc.Name, nodeID, transport.TCPDialer{Address: lc.Address}, logger))
		case config.LinkSocketCAN:
			link, err := socketcan.New(lc.Name, lc.Interface, logger)
			if err != nil {
				logger.Error("socketcan link not started", "link", lc.Name, "error", err)
				continue
			}
			h.AddLink(busCtx, link)
		}
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
	if cfg.Hub.OpenLCBTCP != "" {
		listener := &olcbtcp.Listener{
			Address: cfg.Hub.OpenLCBTCP,
			ID:      nodeID,
			Logger:  logger,
			Accept:  func(link transport.Link) { h.AddLink(busCtx, link) },
		}
		if err := listener.Listen(); err != nil {
			logger.Error("OpenLCB TCP port unavailable", "error", err)
		} else {
			go func() {
				if err := listener.Run(busCtx); err != nil {
					logger.Error("OpenLCB TCP listener stopped", "error", err)
				}
			}()
		}
	}

	var wg sync.WaitGroup
	wg.Add(8)
	go func() {
		defer wg.Done()
		if err := decoders.Download(ctx); err != nil && ctx.Err() == nil {
			logger.Error("decoder definitions not downloaded", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := fast.Run(ctx); err != nil {
			logger.Error("clock stopped", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := local.Run(ctx); err != nil {
			logger.Error("node stopped", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		_ = upgrader.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		if err := reg.Run(ctx); err != nil {
			logger.Error("registry stopped", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := table.Run(ctx); err != nil {
			logger.Error("event table stopped", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := entities.Run(ctx); err != nil {
			logger.Error("entities stopped", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := engine.Run(ctx); err != nil {
			logger.Error("automations stopped", "error", err)
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
			Config:      nodeCfg,
			Entities:    entities,
			EventNames:  table,
			Profiles:    profiles,
			Automations: engine,
			Trains:      trains,
			Programmer:  prog,
			Decoders:    decoders,
			Firmware:    upgrader,
			Clock:       fast,
			Backups:     &backup.Manager{Dir: *dataDir, DB: st, Version: version.Version},
			Restart:     restart,
			SerialPorts: transport.SerialPorts,
			Logger:      logger,
		}),
		UI: web.Dist(),
	})
	err = srv.Run(ctx)
	stop()
	wg.Wait() // the node has sent its AMR by now; the hub can close
	if errors.Is(context.Cause(ctx), errRestart) {
		return errRestart
	}
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
