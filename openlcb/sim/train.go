package sim

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Train is a simulated train node for one DCC locomotive, as simple-dcc
// creates them: it speaks Train Control, answers the search that created
// it, and takes POM writes in space 0xF8.
type Train struct {
	*node.Node
	station *Station
	log     *slog.Logger
	Address uint16
	Long    bool
	Decoder *Decoder

	mu         sync.Mutex
	ctx        context.Context
	controller openlcb.NodeID
	consist    []consistMember
	pending    openlcb.EventID
	cvIndex    uint32
}

type consistMember struct {
	node  openlcb.NodeID
	flags byte
}

func newTrain(s *Station, address uint16, long bool, dec *Decoder, pending openlcb.EventID) *Train {
	cfg := s.cfg
	cfg.ID = traction.DCCNodeID(address, long)
	cfg.SNIP = openlcb.SNIP{
		Manufacturer:    s.SNIPInfo().Manufacturer,
		Model:           "DCC Train Proxy",
		HardwareVersion: s.SNIPInfo().HardwareVersion,
		SoftwareVersion: s.SNIPInfo().SoftwareVersion,
		UserDescription: "DCC address " + traction.AddressName(address, long),
	}
	cfg.Protocols = openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolMemoryConfiguration |
		openlcb.ProtocolEventExchange | openlcb.ProtocolSimpleNodeInformation | openlcb.ProtocolTractionControl
	t := &Train{Node: node.New(cfg), station: s, log: s.log, Address: address, Long: long, Decoder: dec, ctx: context.Background(), pending: pending}
	server := memconfig.NewServer()
	server.Name = "DCC Train Proxy"
	server.AddSpace(traction.SpaceCV, &pomSpace{t: t})
	memconfig.NewClient(t.Node, memconfig.WithServer(server), memconfig.WithLogger(s.log))
	t.HandleTraction(t.command)
	t.Observe(t.observe)
	return t
}

// Run operates the train node until ctx ends.
func (t *Train) Run(ctx context.Context) error {
	t.mu.Lock()
	t.ctx = ctx
	t.mu.Unlock()
	return t.Node.Run(ctx)
}

// Controller returns the node controlling the train, or zero.
func (t *Train) Controller() openlcb.NodeID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.controller
}

func (t *Train) observe(ev node.Event) {
	switch ev.Kind {
	case node.EventStateChanged:
		if ev.State == node.StateInitialized {
			t.mu.Lock()
			pending := t.pending
			t.pending = 0
			t.mu.Unlock()
			t.station.schedule(func() {
				t.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: traction.EventIsTrain.AsSlice()})
				if pending != 0 {
					t.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: pending.AsSlice()})
				}
			})
		}
	case node.EventMessage:
		msg := ev.Message
		if msg.MTI.IsAddressed() && msg.Dest.Alias != t.Alias() {
			return
		}
		switch msg.MTI {
		case openlcb.MTIIdentifyEventsGlobal, openlcb.MTIIdentifyEventsAddressed:
			t.station.schedule(func() {
				t.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: traction.EventIsTrain.AsSlice()})
			})
		case openlcb.MTIIdentifyProducer:
			if id, ok := msg.EventID(); ok && id == traction.EventIsTrain {
				t.station.schedule(func() {
					t.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: id.AsSlice()})
				})
			}
		}
	}
}

func (t *Train) send(msg openlcb.Message) {
	t.mu.Lock()
	ctx := t.ctx
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := t.Send(ctx, msg); err != nil {
		t.log.Debug("sim: train send failed", "error", err)
	}
}

// answerSearch replies to a search the train matches, once it is on the
// bus.
func (t *Train) answerSearch(ev openlcb.EventID) {
	if t.State() != node.StateInitialized {
		t.mu.Lock()
		t.pending = ev
		t.mu.Unlock()
		return
	}
	t.station.schedule(func() {
		t.send(openlcb.Message{MTI: openlcb.MTIProducerIdentifiedValid, Payload: ev.AsSlice()})
	})
}

// command answers a Traction Control Command, on the node goroutine.
func (t *Train) command(src openlcb.NodeHandle, payload []byte) []byte {
	c, err := traction.ParseCommand(payload)
	if err != nil {
		return nil
	}
	s := t.station
	s.mu.Lock()
	defer s.mu.Unlock()
	d := t.Decoder
	switch c.Kind {
	case traction.CmdSetSpeed:
		d.Speed, d.EStop = c.Speed, false
		t.log.Info("sim: train speed", "address", t.Address, "speed", c.Speed.String())
	case traction.CmdSetFunction:
		if c.Function <= traction.MaxFunction {
			d.Functions[c.Function] = c.Value != 0
		}
		t.log.Info("sim: train function", "address", t.Address, "function", c.Function, "on", c.Value != 0)
	case traction.CmdEmergencyStop:
		d.EStop = true
		d.Speed.Value = 0
		t.log.Info("sim: train emergency stop", "address", t.Address)
	case traction.CmdQuerySpeeds:
		b := d.Speed.Encode()
		if d.EStop {
			b = [2]byte{0x7E, 0x00} // NaN, as OpenMRN reports after an estop
			if d.Speed.Reverse {
				b[0] |= 0x80
			}
		}
		status := byte(0)
		if d.EStop {
			status = 0x01
		}
		return []byte{traction.CmdQuerySpeeds, b[0], b[1], status, b[0], b[1], b[0], b[1]}
	case traction.CmdQueryFunction:
		v := uint16(0)
		if d.Functions[c.Function] {
			v = 1
		}
		return []byte{traction.CmdQueryFunction, byte(c.Function >> 16), byte(c.Function >> 8), byte(c.Function), byte(v >> 8), byte(v)}
	case traction.CmdController:
		return t.controllerCommand(c)
	case traction.CmdConsist:
		return t.consistCommand(c)
	case traction.CmdManagement:
		switch c.Sub {
		case traction.ManagementReserve:
			return []byte{traction.CmdManagement, traction.ManagementReserve, 0}
		case traction.ManagementNoop:
			return []byte{traction.CmdManagement, traction.ManagementHeartbeat, 0}
		}
	}
	_ = src
	return nil
}

// controllerCommand takes over, releases and reports the controller. Like
// the firmware, a new assign takes the train without asking the old
// controller.
func (t *Train) controllerCommand(c traction.Command) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch c.Sub {
	case traction.ControllerAssign:
		t.controller = c.Node
		t.log.Info("sim: train controller assigned", "address", t.Address, "controller", c.Node)
		return []byte{traction.CmdController, traction.ControllerAssign, traction.AssignOK}
	case traction.ControllerRelease:
		if t.controller == c.Node {
			t.controller = 0
		}
	case traction.ControllerQuery:
		return append([]byte{traction.CmdController, traction.ControllerQuery, 0}, t.controller.AsSlice()...)
	}
	return nil
}

func (t *Train) consistCommand(c traction.Command) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := func(sub byte, id openlcb.NodeID, code openlcb.ErrorCode) []byte {
		return append(append([]byte{traction.CmdConsist, sub}, id.AsSlice()...), byte(code>>8), byte(code))
	}
	switch c.Sub {
	case traction.ConsistAttach:
		for i := range t.consist {
			if t.consist[i].node == c.Node {
				t.consist[i].flags = c.Flags
				return result(traction.ConsistAttach, c.Node, 0)
			}
		}
		t.consist = append(t.consist, consistMember{node: c.Node, flags: c.Flags})
		return result(traction.ConsistAttach, c.Node, 0)
	case traction.ConsistDetach:
		for i := range t.consist {
			if t.consist[i].node == c.Node {
				t.consist = append(t.consist[:i], t.consist[i+1:]...)
				return result(traction.ConsistDetach, c.Node, 0)
			}
		}
		return result(traction.ConsistDetach, c.Node, 0x1030)
	case traction.ConsistQuery:
		if c.Index >= 0 && c.Index < len(t.consist) {
			m := t.consist[c.Index]
			return append([]byte{traction.CmdConsist, traction.ConsistQuery, byte(len(t.consist)), byte(c.Index), m.flags}, m.node.AsSlice()...)
		}
		return []byte{traction.CmdConsist, traction.ConsistQuery, byte(len(t.consist))}
	}
	return nil
}

// pomSpace is space 0xF8 on a train node: a write to address n-1 sends a
// POM write of CV n, as do the indirect index and value registers; reads
// of CVs need RailCom, which the firmware does not have yet.
type pomSpace struct {
	t *Train
}

const (
	cvIndexOffset = 0x7F000000
	cvValueOffset = 0x7F000004
	cvSpaceSize   = 0x7F000007
)

func (p *pomSpace) Size() uint32   { return cvSpaceSize }
func (p *pomSpace) ReadOnly() bool { return false }

func (p *pomSpace) Read(address uint32, buf []byte) (int, error) {
	if address >= cvIndexOffset && address < cvValueOffset {
		p.t.mu.Lock()
		idx := p.t.cvIndex
		p.t.mu.Unlock()
		b := []byte{byte(idx >> 24), byte(idx >> 16), byte(idx >> 8), byte(idx)}
		return copy(buf, b[address-cvIndexOffset:]), nil
	}
	if address < 1024 || address >= cvValueOffset {
		return 0, &openlcb.Error{Code: openlcb.ErrCodeNotImplementedDatagram}
	}
	return 0, &openlcb.Error{Code: openlcb.ErrCodeAddressOutOfBounds}
}

func (p *pomSpace) Write(address uint32, data []byte) error {
	if len(data) == 0 {
		return &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
	}
	write := func(cv uint32, v byte) {
		p.t.station.mu.Lock()
		p.t.Decoder.SetCV(uint16(cv), v)
		p.t.station.mu.Unlock()
		p.t.log.Info("sim: POM write", "address", p.t.Address, "cv", cv, "value", v)
	}
	switch {
	case address < 1024:
		write(address+1, data[0])
	case address >= cvIndexOffset && address < cvValueOffset:
		p.t.mu.Lock()
		b := []byte{byte(p.t.cvIndex >> 24), byte(p.t.cvIndex >> 16), byte(p.t.cvIndex >> 8), byte(p.t.cvIndex)}
		copy(b[address-cvIndexOffset:], data)
		p.t.cvIndex = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		p.t.mu.Unlock()
	case address == cvValueOffset:
		p.t.mu.Lock()
		cv := p.t.cvIndex
		p.t.mu.Unlock()
		if cv == 0 || cv > 1024 {
			return &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		write(cv, data[0])
	case address == cvValueOffset+1:
	case address == cvValueOffset+2:
		return &openlcb.Error{Code: openlcb.ErrCodeWriteToReadOnly}
	default:
		return &openlcb.Error{Code: openlcb.ErrCodeAddressOutOfBounds}
	}
	return nil
}
