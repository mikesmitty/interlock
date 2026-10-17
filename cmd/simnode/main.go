// Command simnode is a simulated LCC node for trying Interlock without
// hardware. It connects to Interlock's hub port over GridConnect TCP and
// behaves like a canbus-outpost: it answers identification, serves the
// outpost's real CDI with a configuration space behind it that can be
// read and written, moves its servos when their throw and close events
// arrive and reports the feedback, and walks a locomotive around its
// four tracks so the blocks and reporters have something to show, and it
// takes firmware upgrades (images made with -make-firmware), running a
// new image on trial the way the board does. As a
// command station (-cdi cs) it behaves like simple-dcc instead: it
// creates train nodes when a throttle searches for them, drives them
// with Train Control, and owns a programming track with a decoder on it
// whose CVs can be read and written.
//
//	go run ./cmd/simnode                 # connects to 127.0.0.1:12021
//	go run ./cmd/simnode -hub host:port
//	go run ./cmd/simnode -pace 0         # no locomotive
//	go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43   # the command station
//	go run ./cmd/simnode -make-firmware outpost-2.0.0.bin -firmware-version 2.0.0
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
	"github.com/mikesmitty/interlock/openlcb/detector"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

// runner is what both kinds of simulated node offer main.
type runner interface {
	Run(ctx context.Context) error
	HandleFrame(f can.Frame)
}

func main() {
	hubAddr := flag.String("hub", "127.0.0.1:12021", "Interlock hub address (GridConnect TCP)")
	idText := flag.String("id", "0A.00.00.00.00.42", "node ID to simulate")
	name := flag.String("name", "Sim Outpost", "user name reported in SNIP and the configuration")
	which := flag.String("cdi", "outpost", "which device to simulate: outpost or cs (command station)")
	pace := flag.Duration("pace", 6*time.Second, "how often the locomotive moves to the next track; 0 for never")
	address := flag.Int("address", 1234, "DCC address of the locomotive (outpost: the one detected; cs: the one on the programming track)")
	decoderKind := flag.String("decoder", "diy", "cs: the decoder on the programming track, diy (a plain one) or esu (a LokSound 5 with paged CVs)")
	verbose := flag.Bool("v", false, "log every frame")
	makeFirmware := flag.String("make-firmware", "", "write a firmware image the simulated outpost accepts to this file and exit")
	firmwareVersion := flag.String("firmware-version", "1.2.3", "software version the image made with -make-firmware reports")
	firmwareFails := flag.Bool("firmware-fails", false, "make an image that does not confirm itself, so the outpost goes back")
	flag.Parse()

	if *makeFirmware != "" {
		if err := os.WriteFile(*makeFirmware, sim.FirmwareImage(*firmwareVersion, *firmwareFails), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "simnode:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s: simulated outpost firmware version %s\n", *makeFirmware, *firmwareVersion)
		return
	}

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
	cfg := node.Config{
		ID:   id,
		SNIP: openlcb.SNIP{UserName: *name, UserDescription: "Simulated node from cmd/simnode"},
		Send: func(f can.Frame) {
			if err := link.Send(f); err != nil {
				logger.Debug("send", "error", err)
			}
		},
		Logger: logger,
	}
	var peer runner
	var outpost *sim.Outpost
	switch *which {
	case "outpost":
		outpost, err = sim.NewOutpost(cfg)
		peer = outpost
	case "cs":
		if *name == "Sim Outpost" {
			cfg.SNIP.UserName = "Sim Command Station"
		}
		long := *address > 127
		dec := sim.NewDecoder(uint16(*address), long)
		switch *decoderKind {
		case "diy":
		case "esu":
			dec = sim.NewESUDecoder(uint16(*address), long)
		default:
			err = fmt.Errorf("-decoder must be diy or esu, not %q", *decoderKind)
		}
		if err == nil {
			peer, err = sim.NewStation(cfg, sim.StationOptions{
				Decoders:         []*sim.Decoder{dec},
				ProgrammingTrack: uint16(*address),
			})
		}
	default:
		err = fmt.Errorf("-cdi must be outpost or cs, not %q", *which)
	}
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

	if outpost == nil || *pace <= 0 {
		<-ctx.Done()
		return
	}
	// A locomotive runs around the four tracks, facing east, and turns
	// every lap.
	loco := sim.Locomotive{Address: detector.Address{Type: detector.Long, Number: uint16(*address)}, Facing: "east"}
	if *address <= 127 {
		loco.Address.Type = detector.Short
	}
	ticker := time.NewTicker(*pace)
	defer ticker.Stop()
	track := -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := (track + 1) % sim.OutpostTracks
			if track >= 0 {
				outpost.SetTrack(track, nil)
			}
			if next == 0 && track >= 0 {
				if loco.Facing == "east" {
					loco.Facing = "west"
				} else {
					loco.Facing = "east"
				}
			}
			outpost.SetTrack(next, &loco)
			logger.Info("locomotive moved", "address", loco.Address.String(), "track", next+1, "facing", loco.Facing)
			track = next
		}
	}
}
