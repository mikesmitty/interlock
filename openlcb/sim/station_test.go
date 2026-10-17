package sim_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/sim"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

const stationID = openlcb.NodeID(0x0A0000000077)

// simStation wires Interlock's node to a simulated command station with
// one locomotive on the programming track.
func simStation(t *testing.T) (*node.Node, *memconfig.Client, *sim.Station, context.Context) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	var mu sync.Mutex
	var local *node.Node
	var station *sim.Station
	local = node.New(node.Config{ID: localID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); station.HandleFrame(f) }})
	var err error
	station, err = sim.NewStation(node.Config{ID: stationID, Logger: logger, CIDWait: 10 * time.Millisecond, ReplyTimeout: time.Second,
		SNIP: openlcb.SNIP{UserName: "Bench"},
		Send: func(f can.Frame) { mu.Lock(); defer mu.Unlock(); local.HandleFrame(f) }},
		sim.StationOptions{Decoders: []*sim.Decoder{sim.NewDecoder(1234, true)}, ProgrammingTrack: 1234, ServiceModeDelay: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	client := memconfig.NewClient(local, memconfig.WithLogger(logger), memconfig.WithReplyTimeout(500*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	go func() { _ = local.Run(ctx) }()
	go func() { _ = station.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for local.State() != node.StateInitialized || station.State() != node.StateInitialized {
		if time.Now().After(deadline) {
			t.Fatal("nodes did not initialize")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return local, client, station, ctx
}

func TestStationTrains(t *testing.T) {
	local, client, station, ctx := simStation(t)

	// A search with allocate creates the train node, which answers once
	// it is on the bus.
	ev := traction.AddressSearch(1234, true, true)
	found, err := local.Producers(ctx, ev, 400*time.Millisecond, true)
	if err != nil {
		t.Fatal(err)
	}
	want := traction.DCCNodeID(1234, true)
	if len(found) != 1 || found[0].Node != want || found[0].State != "valid" {
		t.Fatalf("producers = %+v, want %v", found, want)
	}
	train, ok := station.Train(1234, true)
	if !ok {
		t.Fatal("no train node")
	}

	// Searching again finds the same node without creating another.
	found, err = local.Producers(ctx, traction.AddressSearch(1234, true, false), 200*time.Millisecond, true)
	if err != nil || len(found) != 1 || found[0].Node != want {
		t.Fatalf("second search = %+v, %v", found, err)
	}
	if len(station.Trains()) != 1 {
		t.Fatalf("trains = %d", len(station.Trains()))
	}

	// Take control, drive, and read back.
	reply, err := local.TractionCommand(ctx, want, traction.AssignController(localID), true)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := traction.ParseAssignReply(reply); err != nil || r != traction.AssignOK {
		t.Fatalf("assign reply = %X (%d, %v)", reply, r, err)
	}
	if train.Controller() != localID {
		t.Errorf("controller = %v", train.Controller())
	}
	if _, err := local.TractionCommand(ctx, want, traction.SetSpeed(traction.Speed{Value: 30, Reverse: true}), false); err != nil {
		t.Fatal(err)
	}
	if _, err := local.TractionCommand(ctx, want, traction.SetFunction(2, 1), false); err != nil {
		t.Fatal(err)
	}
	reply, err = local.TractionCommand(ctx, want, traction.QuerySpeeds(), true)
	if err != nil {
		t.Fatal(err)
	}
	speeds, err := traction.ParseSpeeds(reply)
	if err != nil || speeds.Set != (traction.Speed{Value: 30, Reverse: true}) || speeds.EStop {
		t.Fatalf("speeds = %+v, %v", speeds, err)
	}
	reply, err = local.TractionCommand(ctx, want, traction.QueryFunction(2), true)
	if err != nil {
		t.Fatal(err)
	}
	if fn, v, err := traction.ParseFunction(reply); err != nil || fn != 2 || v != 1 {
		t.Fatalf("function = %d %d %v", fn, v, err)
	}
	if _, err := local.TractionCommand(ctx, want, traction.EmergencyStop(), false); err != nil {
		t.Fatal(err)
	}
	reply, err = local.TractionCommand(ctx, want, traction.QuerySpeeds(), true)
	if err != nil {
		t.Fatal(err)
	}
	if speeds, err = traction.ParseSpeeds(reply); err != nil || !speeds.EStop {
		t.Fatalf("speeds after estop = %+v, %v", speeds, err)
	}
	reply, err = local.TractionCommand(ctx, want, traction.QueryController(), true)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := traction.ParseControllerReply(reply); err != nil || id != localID {
		t.Fatalf("controller reply = %v %v", id, err)
	}
	if _, err := local.TractionCommand(ctx, want, traction.ReleaseController(localID), false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if train.Controller() != 0 {
		t.Errorf("controller after release = %v", train.Controller())
	}

	// POM: a write to space 0xF8 changes the decoder's CV.
	if err := client.Write(ctx, want, traction.SpaceCV, 2, []byte{7}); err != nil {
		t.Fatalf("POM write: %v", err)
	}
	if got := station.DecoderState(1234, true).CVs[3]; got != 7 {
		t.Errorf("CV 3 after POM = %d", got)
	}
	if _, err := client.Read(ctx, want, traction.SpaceCV, 2, 1); err == nil {
		t.Error("POM read should fail without RailCom")
	}
}

func TestStationProgrammingTrack(t *testing.T) {
	local, client, station, ctx := simStation(t)

	// The station says it owns the programming track.
	found, err := local.Producers(ctx, traction.EventProgrammingTrack, 200*time.Millisecond, false)
	if err != nil || len(found) != 1 || found[0].Node != stationID {
		t.Fatalf("programming track producers = %+v, %v", found, err)
	}

	// Reads come back after the delay, through reply pending.
	v, err := client.Read(ctx, stationID, traction.SpaceCV, 7, 1) // CV 8
	if err != nil || len(v) != 1 || v[0] != 13 {
		t.Fatalf("CV 8 = %v, %v", v, err)
	}
	if err := client.Write(ctx, stationID, traction.SpaceCV, 2, []byte{9}); err != nil { // CV 3
		t.Fatalf("write CV 3: %v", err)
	}
	if v, err = client.Read(ctx, stationID, traction.SpaceCV, 2, 1); err != nil || v[0] != 9 {
		t.Fatalf("CV 3 after write = %v, %v", v, err)
	}

	// An empty programming track answers with the DCC no-locomotive code.
	station.SetProgrammingTrack(0)
	_, err = client.Read(ctx, stationID, traction.SpaceCV, 0, 1)
	var oerr *openlcb.Error
	if !errors.As(err, &oerr) || oerr.Code != 0x2031 {
		t.Fatalf("read with no locomotive = %v", err)
	}
	err = client.Write(ctx, stationID, traction.SpaceCV, 0, []byte{3})
	if !errors.As(err, &oerr) || oerr.Code != 0x2031 {
		t.Fatalf("write with no locomotive = %v", err)
	}

	// Emergency off cuts track power; the station reports it on request.
	if err := local.SendEvent(ctx, openlcb.EventEmergencyOff); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for station.Power() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if station.Power() {
		t.Fatal("power still on after Emergency Off")
	}
	if err := local.SendEvent(ctx, openlcb.EventClearEmergencyOff); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for !station.Power() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !station.Power() {
		t.Fatal("power still off after Clear Emergency Off")
	}
}

func TestESUDecoderPages(t *testing.T) {
	d := sim.NewESUDecoder(6641, true)
	read := func(cv uint16) byte { return d.CV(cv) }
	if read(8) != 151 || read(7) != 255 {
		t.Fatalf("identity = %d, %d", read(8), read(7))
	}
	d.SetCV(31, 0)
	d.SetCV(32, 255)
	id := uint32(read(261)) | uint32(read(262))<<8 | uint32(read(263))<<16 | uint32(read(264))<<24
	if id != sim.ESUProductID {
		t.Errorf("product ID = %d", id)
	}
	d.SetCV(32, 0)
	d.SetCV(31, 1)
	if read(261) != 'S' {
		t.Errorf("name page CV 261 = %q", read(261))
	}
	// Each page keeps its own values; CVs outside 257 to 512 have one.
	d.SetCV(300, 9)
	d.SetCV(31, 16)
	if read(300) != 0 {
		t.Errorf("CV 300 in another page = %d", read(300))
	}
	d.SetCV(2, 7)
	if read(2) != 7 || d.CVs[2] != 7 {
		t.Errorf("CV 2 = %d", read(2))
	}
	if plain := sim.NewDecoder(3, false); plain.Pages != nil {
		t.Error("a plain decoder has pages")
	}
}
