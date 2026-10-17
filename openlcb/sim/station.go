package sim

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Decoder is a simulated DCC decoder: a locomotive's configuration
// variables and the state the command station drives it to.
type Decoder struct {
	Address uint16
	Long    bool
	// CVs holds the configuration variables by number (1-based); unset
	// ones read as zero.
	CVs map[uint16]byte

	Speed     traction.Speed
	EStop     bool
	Functions map[uint32]bool
}

// NewDecoder returns a decoder at address with the usual CVs filled in:
// the address in CV 1 or 17 and 18, CV 29 to match, version 1 and the
// DIY manufacturer ID 13.
func NewDecoder(address uint16, long bool) *Decoder {
	d := &Decoder{Address: address, Long: long, CVs: map[uint16]byte{7: 1, 8: 13, 3: 5, 4: 5, 5: 255, 29: 0x02}, Functions: make(map[uint32]bool)}
	if long {
		d.CVs[1] = 3
		d.CVs[17] = byte(address>>8) | 0xC0
		d.CVs[18] = byte(address)
		d.CVs[29] = 0x22
	} else {
		d.CVs[1] = byte(address)
	}
	return d
}

// StationOptions configure a simulated command station.
type StationOptions struct {
	// Decoders are the locomotives on the layout, by DCC address. A train
	// node is created for one when a throttle searches for it.
	Decoders []*Decoder
	// ProgrammingTrack is the address of the decoder sitting on the
	// programming track; zero for an empty track.
	ProgrammingTrack uint16
	// ServiceModeDelay is how long a CV read or write takes; 300 ms when
	// zero, as a real decoder's acknowledgment pulses take.
	ServiceModeDelay time.Duration
}

// Station adds a simple-dcc command station's behavior to a simulated
// node built from its CDI: it answers Train Search events by pointing at
// the matching train nodes, creating them on demand as the firmware does;
// each train node speaks Train Control and takes POM writes in space 0xF8;
// the station owns a programming track whose CV reads and writes go
// through space 0xF8 with a delayed reply and the DCC error codes; and the
// well-known emergency events cut and restore track power.
type Station struct {
	*Node
	log *slog.Logger
	cfg node.Config

	mu       sync.Mutex
	ctx      context.Context
	decoders map[uint16]*Decoder
	trains   map[openlcb.NodeID]*Train
	prog     *Decoder
	delay    time.Duration
	power    bool
	estop    bool
	work     chan func()
}

// NewStation builds a command station. cfg.Send must be set; cfg.ID
// should not be in the DCC train range.
func NewStation(cfg node.Config, opts StationOptions) (*Station, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	cfg.Protocols = openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolMemoryConfiguration |
		openlcb.ProtocolEventExchange | openlcb.ProtocolIdentification | openlcb.ProtocolAbbreviatedDefaultCDI |
		openlcb.ProtocolSimpleNodeInformation | openlcb.ProtocolCDI
	n, err := New(cfg, CommandStationCDI)
	if err != nil {
		return nil, err
	}
	s := &Station{
		Node:     n,
		log:      cfg.Logger,
		cfg:      cfg,
		ctx:      context.Background(),
		decoders: make(map[uint16]*Decoder),
		trains:   make(map[openlcb.NodeID]*Train),
		delay:    opts.ServiceModeDelay,
		power:    true,
		work:     make(chan func(), 256),
	}
	if s.delay == 0 {
		s.delay = 300 * time.Millisecond
	}
	for _, d := range opts.Decoders {
		if d.Functions == nil {
			d.Functions = make(map[uint32]bool)
		}
		if d.CVs == nil {
			d.CVs = make(map[uint16]byte)
		}
		s.decoders[d.Address] = d
	}
	if opts.ProgrammingTrack != 0 {
		s.prog = s.decoders[opts.ProgrammingTrack]
	}
	n.Server.AddSpace(traction.SpaceCV, &programmingTrack{s: s})
	n.Observe(s.observe)
	return s, nil
}

// Run operates the station until ctx ends. Train nodes created while it
// runs stop with it.
func (s *Station) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case fn := <-s.work:
				fn()
			}
		}
	}()
	return s.Node.Run(ctx)
}

// HandleFrame feeds a frame to the station and every train node.
func (s *Station) HandleFrame(f can.Frame) {
	s.Node.HandleFrame(f)
	s.mu.Lock()
	trains := make([]*Train, 0, len(s.trains))
	for _, t := range s.trains {
		trains = append(trains, t)
	}
	s.mu.Unlock()
	for _, t := range trains {
		t.HandleFrame(f)
	}
}

func (s *Station) schedule(fn func()) {
	select {
	case s.work <- fn:
	default:
		s.log.Warn("sim: station work queue full")
	}
}

// DecoderState returns a copy of a decoder's state, safe to read while the
// station drives it. The decoder is created when the layout has none.
func (s *Station) DecoderState(address uint16, long bool) Decoder {
	d := s.Decoder(address, long)
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := *d
	snapshot.CVs = make(map[uint16]byte, len(d.CVs))
	for k, v := range d.CVs {
		snapshot.CVs[k] = v
	}
	snapshot.Functions = make(map[uint32]bool, len(d.Functions))
	for k, v := range d.Functions {
		snapshot.Functions[k] = v
	}
	return snapshot
}

// Decoder returns the decoder at address, creating one when the layout
// has none, as a real locomotive would answer whatever address it has.
// The station writes its fields under its own lock; read them through
// DecoderState.
func (s *Station) Decoder(address uint16, long bool) *Decoder {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.decoders[address]
	if !ok {
		d = NewDecoder(address, long)
		s.decoders[address] = d
	}
	return d
}

// SetProgrammingTrack puts the decoder at address on the programming
// track, or empties the track with zero.
func (s *Station) SetProgrammingTrack(address uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if address == 0 {
		s.prog = nil
		return
	}
	s.prog = s.decoders[address]
	if s.prog == nil {
		s.prog = NewDecoder(address, address > traction.ShortAddressMax)
		s.decoders[address] = s.prog
	}
}

// Power reports whether the main track is powered.
func (s *Station) Power() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.power
}

// Trains lists the train nodes that exist.
func (s *Station) Trains() []*Train {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Train, 0, len(s.trains))
	for _, t := range s.trains {
		out = append(out, t)
	}
	return out
}

// Train returns the train node for a DCC address, if one exists.
func (s *Station) Train(address uint16, long bool) (*Train, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trains[traction.DCCNodeID(address, long)]
	return t, ok
}

func (s *Station) observe(ev node.Event) {
	if ev.Kind != node.EventMessage {
		return
	}
	msg := ev.Message
	if msg.MTI.IsAddressed() && msg.Dest.Alias != s.Alias() {
		return
	}
	switch msg.MTI {
	case openlcb.MTIEventReport, openlcb.MTIIdentifyProducer:
		id, ok := msg.EventID()
		if !ok {
			return
		}
		identify := msg.MTI == openlcb.MTIIdentifyProducer
		s.schedule(func() { s.onEvent(id, identify) })
	case openlcb.MTIVerifyNodeIDGlobal:
		if id, ok := msg.PayloadNodeID(); ok {
			if address, long, ok := traction.DCCAddress(id); ok {
				s.schedule(func() { s.ensureTrain(address, long, 0) })
			}
		}
	case openlcb.MTIIdentifyEventsGlobal, openlcb.MTIIdentifyEventsAddressed:
		s.schedule(s.identifyAll)
	case openlcb.MTIIdentifyConsumer:
		if id, ok := msg.EventID(); ok {
			s.schedule(func() { s.identifyConsumer(id) })
		}
	}
}

func (s *Station) send(msg openlcb.Message) {
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.Send(ctx, msg); err != nil {
		s.log.Debug("sim: send failed", "error", err)
	}
}

// onEvent handles a global event: a train search, or an emergency event.
func (s *Station) onEvent(ev openlcb.EventID, identify bool) {
	if traction.IsSearchEvent(ev) {
		s.onSearch(ev)
		return
	}
	if identify {
		if ev == traction.EventProgrammingTrack {
			s.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: ev.AsSlice()})
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev {
	case openlcb.EventEmergencyOff:
		s.power = false
		s.log.Info("sim: track power off")
	case openlcb.EventClearEmergencyOff:
		s.power = true
		s.log.Info("sim: track power on")
	case openlcb.EventEmergencyStop:
		s.estop = true
		for _, d := range s.decoders {
			d.EStop = true
			d.Speed.Value = 0
		}
		s.log.Info("sim: emergency stop")
	case openlcb.EventClearEmergencyStop:
		s.estop = false
	}
}

// onSearch answers a Train Search event: every matching train replies,
// and with the allocate flag a train is created when none matches.
func (s *Station) onSearch(ev openlcb.EventID) {
	query, flags, ok := traction.ParseSearch(ev)
	if !ok {
		return
	}
	s.mu.Lock()
	var matched []*Train
	for _, t := range s.trains {
		if traction.SearchMatches(query, flags, t.Address, t.Long) {
			matched = append(matched, t)
		}
	}
	s.mu.Unlock()
	for _, t := range matched {
		t.answerSearch(ev)
	}
	if len(matched) > 0 || flags&traction.SearchAllocate == 0 {
		return
	}
	var address uint16
	for _, r := range query {
		address = address*10 + uint16(r-'0')
	}
	if address == 0 || address > 10239 {
		return
	}
	long := flags&traction.SearchLong != 0 || address > traction.ShortAddressMax
	s.ensureTrain(address, long, ev)
}

// ensureTrain creates the train node for an address if it does not exist;
// pending is a search event to answer once the node is on the bus.
func (s *Station) ensureTrain(address uint16, long bool, pending openlcb.EventID) {
	id := traction.DCCNodeID(address, long)
	s.mu.Lock()
	if t, ok := s.trains[id]; ok {
		s.mu.Unlock()
		if pending != 0 {
			t.answerSearch(pending)
		}
		return
	}
	ctx := s.ctx
	dec, ok := s.decoders[address]
	if !ok {
		dec = NewDecoder(address, long)
		s.decoders[address] = dec
	}
	t := newTrain(s, address, long, dec, pending)
	s.trains[id] = t
	s.mu.Unlock()
	s.log.Info("sim: train node created", "address", traction.AddressName(address, long), "node", id)
	go func() {
		if err := t.Run(ctx); err != nil {
			s.log.Debug("sim: train node stopped", "error", err)
		}
	}()
}

func (s *Station) identifyAll() {
	s.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: traction.EventProgrammingTrack.AsSlice()})
	for _, ev := range []openlcb.EventID{openlcb.EventEmergencyOff, openlcb.EventClearEmergencyOff, openlcb.EventEmergencyStop, openlcb.EventClearEmergencyStop} {
		s.identifyConsumer(ev)
	}
}

func (s *Station) identifyConsumer(ev openlcb.EventID) {
	s.mu.Lock()
	power, estop := s.power, s.estop
	s.mu.Unlock()
	var mti openlcb.MTI
	switch ev {
	case openlcb.EventEmergencyOff:
		mti = validMTI(!power)
	case openlcb.EventClearEmergencyOff:
		mti = validMTI(power)
	case openlcb.EventEmergencyStop:
		mti = validMTI(estop)
	case openlcb.EventClearEmergencyStop:
		mti = validMTI(!estop)
	default:
		return
	}
	s.send(openlcb.Message{MTI: mti, Payload: ev.AsSlice()})
}

func validMTI(valid bool) openlcb.MTI {
	if valid {
		return openlcb.MTIConsumerIdentifiedValid
	}
	return openlcb.MTIConsumerIdentifiedInvalid
}

// programmingTrack is space 0xF8 on the station: CV n at address n-1,
// one byte per request, answered after the service mode delay with the
// DCC error codes when no decoder is on the track.
type programmingTrack struct {
	s *Station
}

// Error codes the DCC working note defines for programming.
const (
	errProgrammingDisabled openlcb.ErrorCode = 0x1021
	errNoLocomotive        openlcb.ErrorCode = 0x2031
	errFailedVerify        openlcb.ErrorCode = 0x2032
	errProgrammingShort    openlcb.ErrorCode = 0x2035
)

func (p *programmingTrack) Size() uint32      { return 1024 }
func (p *programmingTrack) ReadOnly() bool    { return false }
func (p *programmingTrack) TimeoutExp() uint8 { return 3 }
func (p *programmingTrack) decoder() *Decoder {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return p.s.prog
}

func (p *programmingTrack) Read(address uint32, buf []byte) (int, error) {
	if len(buf) != 1 || address >= 1024 {
		return 0, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
	}
	time.Sleep(p.s.delay)
	d := p.decoder()
	if d == nil {
		return 0, &openlcb.Error{Code: errNoLocomotive}
	}
	p.s.mu.Lock()
	buf[0] = d.CVs[uint16(address)+1]
	p.s.mu.Unlock()
	return 1, nil
}

func (p *programmingTrack) Write(address uint32, data []byte) error {
	if len(data) != 1 || address >= 1024 {
		return &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
	}
	time.Sleep(p.s.delay)
	d := p.decoder()
	if d == nil {
		return &openlcb.Error{Code: errNoLocomotive}
	}
	p.s.mu.Lock()
	d.CVs[uint16(address)+1] = data[0]
	p.s.mu.Unlock()
	return nil
}

var _ memconfig.Deferred = (*programmingTrack)(nil)

// String describes the station for logs.
func (s *Station) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("simulated command station %v with %d trains", s.ID(), len(s.trains))
}
