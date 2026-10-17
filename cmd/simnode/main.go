// Command simnode is a simulated LCC node for trying Interlock without
// hardware. It connects to Interlock's hub port over GridConnect TCP and
// behaves like a canbus-outpost: it answers identification, serves the
// outpost's real CDI with a configuration space behind it that can be
// read and written, and produces an event every second.
//
//	go run ./cmd/simnode                 # connects to 127.0.0.1:12021
//	go run ./cmd/simnode -hub host:port
//	go run ./cmd/simnode -cdi cs         # simulate the command station instead
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

func main() {
	hubAddr := flag.String("hub", "127.0.0.1:12021", "Interlock hub address (GridConnect TCP)")
	idText := flag.String("id", "0A.00.00.00.00.42", "node ID to simulate")
	name := flag.String("name", "Sim Outpost", "user name reported in SNIP and the configuration")
	which := flag.String("cdi", "outpost", "which device to simulate: outpost or cs (command station)")
	interval := flag.Duration("events", time.Second, "how often to produce an event; 0 for never")
	verbose := flag.Bool("v", false, "log every frame")
	flag.Parse()

	id, err := openlcb.ParseNodeID(*idText)
	if err != nil {
		fmt.Fprintln(os.Stderr, "simnode:", err)
		os.Exit(2)
	}
	var cdiXML []byte
	switch *which {
	case "outpost":
		cdiXML = sim.OutpostCDI
	case "cs":
		cdiXML = sim.CommandStationCDI
	default:
		fmt.Fprintln(os.Stderr, "simnode: -cdi must be outpost or cs")
		os.Exit(2)
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	link := transport.NewGridConnect("hub", transport.TCPDialer{Address: *hubAddr}, logger)
	peer, err := sim.New(node.Config{
		ID:   id,
		SNIP: openlcb.SNIP{UserName: *name, UserDescription: "Simulated node from cmd/simnode"},
		Send: func(f can.Frame) {
			if err := link.Send(f); err != nil {
				logger.Debug("send", "error", err)
			}
		},
		Logger: logger,
	}, cdiXML)
	if err != nil {
		fmt.Fprintln(os.Stderr, "simnode:", err)
		stop()
		os.Exit(1) //nolint:gocritic // stop() was just called by hand
	}
	// The link outlives the node by a moment so the alias withdrawal the
	// node sends on exit reaches the bus.
	linkCtx, stopLink := context.WithCancel(context.Background())
	defer stopLink()
	go func() {
		if err := link.Run(linkCtx, peer.HandleFrame); err != nil {
			logger.Error("link", "error", err)
		}
	}()
	nodeDone := make(chan struct{})
	go func() {
		defer close(nodeDone)
		if err := peer.Run(ctx); err != nil {
			logger.Error("node", "error", err)
		}
	}()
	defer func() {
		<-nodeDone
		time.Sleep(200 * time.Millisecond)
	}()

	if *interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if peer.State() != node.StateInitialized {
				continue
			}
			n++
			if err := peer.SendEvent(ctx, id.Event(uint16(n%2))); err != nil {
				logger.Debug("event", "error", err)
			}
		}
	}
}
