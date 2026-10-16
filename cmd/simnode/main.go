// Command simnode is a simulated LCC node for trying Interlock without
// hardware. It connects to Interlock's hub port over GridConnect TCP,
// announces itself like a canbus-outpost, answers identification and
// produces an event every second.
//
//	go run ./cmd/simnode                 # connects to 127.0.0.1:12021
//	go run ./cmd/simnode -hub host:port
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
	"github.com/mikesmitty/interlock/openlcb/transport"
)

func main() {
	hubAddr := flag.String("hub", "127.0.0.1:12021", "Interlock hub address (GridConnect TCP)")
	idText := flag.String("id", "0A.00.00.00.00.42", "node ID to simulate")
	name := flag.String("name", "Sim Outpost", "user name reported in SNIP")
	interval := flag.Duration("events", time.Second, "how often to produce an event; 0 for never")
	verbose := flag.Bool("v", false, "log every frame")
	flag.Parse()

	id, err := openlcb.ParseNodeID(*idText)
	if err != nil {
		fmt.Fprintln(os.Stderr, "simnode:", err)
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
	peer := node.New(node.Config{
		ID: id,
		SNIP: openlcb.SNIP{
			Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", HardwareVersion: "Rev A",
			SoftwareVersion: "0.2.1", UserName: *name, UserDescription: "Simulated node from cmd/simnode",
		},
		Protocols: openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolMemoryConfiguration |
			openlcb.ProtocolEventExchange | openlcb.ProtocolSimpleNodeInformation | openlcb.ProtocolCDI |
			openlcb.ProtocolFirmwareUpgrade,
		Send: func(f can.Frame) {
			if err := link.Send(f); err != nil {
				logger.Debug("send", "error", err)
			}
		},
		Logger: logger,
	})
	peer.HandleDatagrams(0x20, func(*openlcb.Message) (node.DatagramFlags, error) {
		return node.DatagramFlags{ReplyPending: true, TimeoutExp: 2}, nil
	})
	go func() {
		if err := link.Run(ctx, peer.HandleFrame); err != nil {
			logger.Error("link", "error", err)
		}
	}()
	go func() {
		if err := peer.Run(ctx); err != nil {
			logger.Error("node", "error", err)
		}
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
