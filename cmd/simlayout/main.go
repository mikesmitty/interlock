// Command simlayout runs a simulated layout for an Interlock to drive:
// a command station like simple-dcc and outposts like canbus-outpost on
// Interlock's hub port, and trains that move along Interlock's track plan
// at the speeds their decoders are set to. The outposts' servos set the
// plan's turnouts and their RailCom detectors see the trains' decoders,
// with the real detectors' timing (see internal/layoutsim).
//
// With an empty plan it saves its test layout, a double-track loop with
// two stations, a siding and a crossover whose blocks and turnouts are
// linked to the simulated outposts, and it adds its trains to the roster
// with their lengths and their decoders' CVs, for train tracking.
// Run it against an Interlock of its own, never one with a real layout:
//
//	bin/interlock -data ./simdata &
//	go run ./cmd/simlayout -api http://127.0.0.1:8080 -state ./simdata/simlayout.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/internal/atomicfile"
	"github.com/mikesmitty/interlock/internal/layoutsim"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/transport"
)

type options struct {
	hub, api   string
	outposts   int
	trainsFile string
	stateFile  string
	seed       bool
	tick       time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.hub, "hub", "127.0.0.1:12021", "Interlock hub address (GridConnect TCP)")
	flag.StringVar(&o.api, "api", "http://127.0.0.1:8080", "Interlock's web address, for the track plan and the roster")
	flag.IntVar(&o.outposts, "outposts", layoutsim.TestOutposts+1, "how many outposts to simulate, from node ID "+layoutsim.OutpostID(1).String()+" every 0x10")
	flag.StringVar(&o.trainsFile, "trains", "", "YAML list of trains (name, address, cars, motor, top_speed, block, reverse); the test layout's when empty")
	flag.StringVar(&o.stateFile, "state", "", "file that keeps where the trains are over a restart; none when empty")
	flag.BoolVar(&o.seed, "seed", true, "save the test layout when Interlock's plan is empty, and add the trains to the roster")
	flag.DurationVar(&o.tick, "tick", 20*time.Millisecond, "how often the trains move")
	verbose := flag.Bool("v", false, "log every frame")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if err := run(o, logger); err != nil {
		fmt.Fprintln(os.Stderr, "simlayout:", err)
		os.Exit(1)
	}
}

// board is a simulated node with its own connection to the hub, as
// separate boards on the bus would have.
type board struct {
	node interface {
		Run(ctx context.Context) error
		HandleFrame(f can.Frame)
	}
	link *transport.GridConnectLink
}

// hardware is the boards as the layout sees them.
type hardware struct {
	station  *sim.Station
	outposts map[openlcb.NodeID]*sim.Outpost
}

func (h hardware) DecoderState(address uint16, long bool) sim.Decoder {
	return h.station.DecoderState(address, long)
}

func (h hardware) Power() bool { return h.station.Power() }

func (h hardware) Turnout(n openlcb.NodeID, servo int) string {
	o, ok := h.outposts[n]
	if !ok || servo < 0 || servo >= sim.OutpostServos {
		return "unknown"
	}
	return o.Turnout(servo)
}

func (h hardware) SetTrack(n openlcb.NodeID, track int, loco *sim.Locomotive) {
	if o, ok := h.outposts[n]; ok {
		o.SetTrack(track, loco)
	}
}

func run(o options, logger *slog.Logger) error {
	trains := layoutsim.TestTrains()
	if o.trainsFile != "" {
		data, err := os.ReadFile(o.trainsFile)
		if err != nil {
			return err
		}
		trains = nil
		if err := yaml.Unmarshal(data, &trains); err != nil {
			return fmt.Errorf("%s: %w", o.trainsFile, err)
		}
	}

	var boards []board
	config := func(id openlcb.NodeID, name string) node.Config {
		link := transport.NewGridConnect(name, transport.TCPDialer{Address: o.hub}, logger)
		boards = append(boards, board{link: link})
		return node.Config{
			ID:   id,
			SNIP: openlcb.SNIP{UserName: name, UserDescription: "Simulated by cmd/simlayout"},
			Send: func(f can.Frame) {
				if err := link.Send(f); err != nil {
					logger.Debug("send", "error", err)
				}
			},
			Logger: logger,
		}
	}
	decoders := make([]*sim.Decoder, 0, len(trains))
	for _, t := range trains {
		decoders = append(decoders, sim.NewDecoder(t.Address, t.Long || t.Address > 127))
	}
	station, err := sim.NewStation(config(layoutsim.StationID, "Sim Command Station"), sim.StationOptions{Decoders: decoders})
	if err != nil {
		return err
	}
	boards[len(boards)-1].node = station
	hw := hardware{station: station, outposts: map[openlcb.NodeID]*sim.Outpost{}}
	for i := 1; i <= o.outposts; i++ {
		id := layoutsim.OutpostID(i)
		outpost, err := sim.NewOutpost(config(id, fmt.Sprintf("Sim Outpost %d", i)))
		if err != nil {
			return err
		}
		boards[len(boards)-1].node = outpost
		hw.outposts[id] = outpost
	}

	layout := layoutsim.New(hw, trains, logger)
	for id := range hw.outposts {
		for t := range sim.OutpostTracks {
			layout.AddDetector(id, t)
		}
	}
	if o.stateFile != "" {
		data, err := os.ReadFile(o.stateFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		default:
			var st layoutsim.State
			if err := json.Unmarshal(data, &st); err != nil {
				return fmt.Errorf("%s: %w", o.stateFile, err)
			}
			layout.Restore(st)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The links outlive the boards by a moment so the alias withdrawals
	// the boards send on exit reach the bus.
	linkCtx, stopLinks := context.WithCancel(context.Background())
	defer stopLinks()
	var running sync.WaitGroup
	for _, b := range boards {
		go func() {
			if err := b.link.Run(linkCtx, b.node.HandleFrame); err != nil {
				logger.Error("link", "error", err)
			}
		}()
		running.Add(1)
		go func() {
			defer running.Done()
			if err := b.node.Run(ctx); err != nil {
				logger.Error("node", "error", err)
			}
		}()
	}
	defer func() {
		running.Wait()
		time.Sleep(200 * time.Millisecond)
	}()

	client := &layoutsim.Client{Base: o.api}
	cvs := func(t layoutsim.TrainSpec) map[uint16]byte {
		return station.DecoderState(t.Address, t.Long || t.Address > 127).CVs
	}
	go followPlan(ctx, client, layout, trains, cvs, o.seed, logger)

	save := func() {
		if o.stateFile == "" {
			return
		}
		data, err := json.Marshal(layout.State())
		if err == nil {
			err = atomicfile.Write(o.stateFile, data, 0o644)
		}
		if err != nil {
			logger.Warn("saving where the trains are", "error", err)
		}
	}
	defer save()
	ticker := time.NewTicker(o.tick)
	defer ticker.Stop()
	saving := time.NewTicker(5 * time.Second)
	defer saving.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			layout.Step(now, now.Sub(last))
			last = now
		case <-saving.C:
			save()
		}
	}
}

// followPlan keeps the layout on Interlock's plan, checking for a new
// one every two seconds. With seed it first saves the test layout over
// an empty plan and gives the trains roster entries with their lengths
// and their decoders' CVs.
func followPlan(ctx context.Context, client *layoutsim.Client, layout *layoutsim.Sim, trains []layoutsim.TrainSpec, cvs func(layoutsim.TrainSpec) map[uint16]byte, seed bool, logger *slog.Logger) {
	seeded, rostered := !seed, !seed
	var lastErr string
	for {
		st, err := client.Plan(ctx)
		switch {
		case err != nil:
			if err.Error() != lastErr && ctx.Err() == nil {
				logger.Warn("reading Interlock's track plan", "error", err)
			}
			lastErr = err.Error()
		default:
			lastErr = ""
			if !seeded {
				seeded = true
				if st.Plan.Empty() {
					saved, err := client.PutPlan(ctx, layoutsim.TestLayout(), st.Revision)
					if err != nil {
						logger.Warn("saving the test layout", "error", err)
					} else {
						st = saved
						logger.Info("saved the test layout as Interlock's track plan")
					}
				}
			}
			if st.Revision != layout.Revision() {
				layout.SetPlan(st.Plan, st.Revision)
				logger.Info("running on the track plan", "revision", st.Revision, "tracks", len(st.Plan.Tracks), "blocks", len(st.Plan.Blocks))
			}
			if !rostered {
				added, err := client.AddToRoster(ctx, trains, cvs)
				if err != nil {
					logger.Warn("adding the trains to the roster", "error", err)
				} else {
					rostered = true
					if len(added) > 0 {
						logger.Info("put in the roster", "trains", added)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
