package memconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// Space is an address space the server exposes.
type Space interface {
	// Size is the number of addressable bytes, starting at address 0.
	Size() uint32
	// ReadOnly reports whether writes are refused.
	ReadOnly() bool
	// Read copies bytes starting at address into buf and returns how many,
	// fewer than len(buf) at the end of the space.
	Read(address uint32, buf []byte) (int, error)
	// Write stores data at address.
	Write(address uint32, data []byte) error
}

// Deferred marks a Space whose reads and writes take a while, such as a
// DCC programming track. The server acknowledges requests to it with
// reply pending and the timeout it names, and answers from a goroutine, so
// its Read and Write may block; a failed write becomes a write failed
// reply rather than a rejected datagram.
type Deferred interface {
	Space
	// TimeoutExp is how long the requester should wait for the reply, as
	// a power of two seconds.
	TimeoutExp() uint8
}

// Firmware receives a new firmware image through space 0xEF, following
// the Firmware Upgrade standard: Freeze starts the upgrade, writes arrive
// in order from address 0, and Unfreeze ends it. Methods return nil to
// acknowledge or an *openlcb.Error whose code goes in the rejection; the
// standard's codes are ErrCodeFirmwareIncompatible,
// ErrCodeFirmwareCorrupted and ErrCodeFirmwareChecksum (temporary, so the
// sender retries the write). Methods run on the node goroutine and must
// not block.
type Firmware interface {
	// Freeze enters the upgrade state.
	Freeze() error
	// WriteFirmware stores the next piece of the image.
	WriteFirmware(address uint32, data []byte) error
	// Unfreeze leaves the upgrade state. nil means the image is complete
	// and valid; the node is expected to restart into it.
	Unfreeze() error
}

// Memory is a Space backed by a byte slice.
type Memory struct {
	mu       sync.RWMutex
	data     []byte
	readOnly bool
	// OnWrite, if set, is called after each write with the range changed.
	OnWrite func(address uint32, n int)
}

// NewMemory returns a space over data. The slice is used in place.
func NewMemory(data []byte, readOnly bool) *Memory {
	return &Memory{data: data, readOnly: readOnly}
}

// Size implements Space.
func (m *Memory) Size() uint32 { return uint32(len(m.data)) }

// ReadOnly implements Space.
func (m *Memory) ReadOnly() bool { return m.readOnly }

// Read implements Space.
func (m *Memory) Read(address uint32, buf []byte) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if address >= uint32(len(m.data)) {
		return 0, &openlcb.Error{Code: ErrCodeOutOfBounds}
	}
	return copy(buf, m.data[address:]), nil
}

// Write implements Space.
func (m *Memory) Write(address uint32, data []byte) error {
	if m.readOnly {
		return &openlcb.Error{Code: ErrCodeWriteReadOnly}
	}
	m.mu.Lock()
	if address >= uint32(len(m.data)) || uint32(len(data)) > uint32(len(m.data))-address {
		m.mu.Unlock()
		return &openlcb.Error{Code: ErrCodeOutOfBounds}
	}
	copy(m.data[address:], data)
	m.mu.Unlock()
	if m.OnWrite != nil {
		m.OnWrite(address, len(data))
	}
	return nil
}

// Bytes returns a copy of the contents.
func (m *Memory) Bytes() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]byte, len(m.data))
	copy(out, m.data)
	return out
}

// Server answers memory configuration requests for the local node. Attach
// it to a Client with WithServer.
type Server struct {
	mu     sync.RWMutex
	spaces map[uint8]Space
	dg     Datagrams
	log    *slog.Logger
	lock   openlcb.NodeID

	// Name is reported in the options reply.
	Name string
	// Firmware, when set, accepts firmware upgrades through space 0xEF.
	Firmware Firmware
	frozen   bool
	// Hooks, called on their own goroutine after the acknowledgment.
	OnUpdateComplete func()
	OnReboot         func()
	OnFactoryReset   func()
	// OnFrozen runs after a Freeze is acknowledged; the standard has the
	// node announce itself with Initialization Complete then. OnUnfrozen
	// runs after Unfreeze with whether the image was accepted.
	OnFrozen   func()
	OnUnfrozen func(accepted bool)
}

// NewServer returns an empty server.
func NewServer() *Server {
	return &Server{spaces: make(map[uint8]Space), log: slog.Default()}
}

// AddSpace exposes space under number.
func (s *Server) AddSpace(number uint8, space Space) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spaces[number] = space
}

// Space returns an exposed space.
func (s *Server) Space(number uint8) (Space, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sp, ok := s.spaces[number]
	return sp, ok
}

// Frozen reports whether a firmware upgrade is in progress.
func (s *Server) Frozen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.frozen
}

func (s *Server) bind(dg Datagrams, log *slog.Logger) {
	s.dg = dg
	s.log = log
}

// handle runs on the node goroutine. Requests that need a reply datagram
// are acknowledged with reply pending and answered from a goroutine.
func (s *Server) handle(msg *openlcb.Message) (node.DatagramFlags, error) {
	p := msg.Payload
	cmd := p[1]
	switch {
	case IsReadWrite(cmd) && cmd&rwCommandMask == CmdRead:
		rw, err := ParseReadWrite(p)
		if err != nil || len(rw.Data) < 1 || rw.Data[0] == 0 || int(rw.Data[0]) > MaxTransfer {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		exp := uint8(1)
		if sp, ok := s.Space(rw.Space); ok {
			if d, ok := sp.(Deferred); ok {
				exp = d.TimeoutExp()
			}
		}
		go s.reply(msg.Source, s.read(rw))
		return node.DatagramFlags{ReplyPending: true, TimeoutExp: exp}, nil
	case IsReadWrite(cmd) && cmd&rwCommandMask == CmdWrite:
		rw, err := ParseReadWrite(p)
		if err != nil || len(rw.Data) == 0 {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		if rw.Space == SpaceFirmware {
			return node.DatagramFlags{}, s.writeFirmware(rw)
		}
		sp, ok := s.Space(rw.Space)
		if !ok {
			return node.DatagramFlags{}, &openlcb.Error{Code: ErrCodeSpaceUnknown}
		}
		if sp.ReadOnly() {
			return node.DatagramFlags{}, &openlcb.Error{Code: ErrCodeWriteReadOnly}
		}
		if d, ok := sp.(Deferred); ok {
			go s.reply(msg.Source, s.write(d, rw))
			return node.DatagramFlags{ReplyPending: true, TimeoutExp: d.TimeoutExp()}, nil
		}
		if err := sp.Write(rw.Address, rw.Data); err != nil {
			return node.DatagramFlags{}, err
		}
		return node.DatagramFlags{}, nil
	case cmd == CmdOptions:
		go s.reply(msg.Source, s.options().Encode())
		return node.DatagramFlags{ReplyPending: true, TimeoutExp: 1}, nil
	case cmd == CmdSpaceInfo:
		if len(p) < 3 {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		go s.reply(msg.Source, s.spaceInfo(p[2]).Encode())
		return node.DatagramFlags{ReplyPending: true, TimeoutExp: 1}, nil
	case cmd == CmdLock:
		if len(p) < 8 {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		requester := openlcb.NodeIDFromBytes([6]byte(p[2:8]))
		s.mu.Lock()
		if requester == 0 || s.lock == 0 {
			s.lock = requester
		}
		holder := s.lock
		s.mu.Unlock()
		go s.reply(msg.Source, append([]byte{ContentType, CmdLockReply}, holder.AsSlice()...))
		return node.DatagramFlags{ReplyPending: true, TimeoutExp: 1}, nil
	case cmd == CmdUpdateComplete:
		if s.OnUpdateComplete != nil {
			go s.OnUpdateComplete()
		}
		return node.DatagramFlags{}, nil
	case cmd == CmdReboot:
		if s.OnReboot != nil {
			go s.OnReboot()
		}
		return node.DatagramFlags{}, nil
	case cmd == CmdFreeze, cmd == CmdUnfreeze:
		if len(p) < 3 {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		return node.DatagramFlags{}, s.freeze(cmd == CmdFreeze, p[2])
	case cmd == CmdFactoryReset:
		if len(p) < 8 || openlcb.NodeIDFromBytes([6]byte(p[2:8])) != s.dg.ID() {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
		}
		if s.OnFactoryReset != nil {
			go s.OnFactoryReset()
		}
		return node.DatagramFlags{}, nil
	default:
		return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeNotImplementedSubcommand}
	}
}

func (s *Server) read(rw ReadWrite) []byte {
	sp, ok := s.Space(rw.Space)
	if !ok {
		return ReadFailedReply(rw.Space, rw.Address, ErrCodeSpaceUnknown)
	}
	buf := make([]byte, rw.Data[0])
	n, err := sp.Read(rw.Address, buf)
	if err != nil {
		code := openlcb.ErrCodePermanent
		var oerr *openlcb.Error
		if errors.As(err, &oerr) {
			code = oerr.Code
		}
		return ReadFailedReply(rw.Space, rw.Address, code)
	}
	return ReadReply(rw.Space, rw.Address, buf[:n])
}

// write performs a deferred write and builds its reply.
func (s *Server) write(sp Space, rw ReadWrite) []byte {
	if err := sp.Write(rw.Address, rw.Data); err != nil {
		code := openlcb.ErrCodePermanent
		var oerr *openlcb.Error
		if errors.As(err, &oerr) {
			code = oerr.Code
		}
		return WriteFailedReply(rw.Space, rw.Address, code)
	}
	return WriteReply(rw.Space, rw.Address)
}

// freeze handles Freeze and Unfreeze, which only the firmware space
// supports.
func (s *Server) freeze(freeze bool, space uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Firmware == nil || space != SpaceFirmware {
		return &openlcb.Error{Code: ErrCodeSpaceUnknown}
	}
	if freeze {
		if s.frozen {
			return nil
		}
		if err := s.Firmware.Freeze(); err != nil {
			return err
		}
		s.frozen = true
		if s.OnFrozen != nil {
			go s.OnFrozen()
		}
		return nil
	}
	if !s.frozen {
		return nil
	}
	// Senders unfreeze after a failed transfer too, to end the upgrade.
	s.frozen = false
	err := s.Firmware.Unfreeze()
	if s.OnUnfrozen != nil {
		go s.OnUnfrozen(err == nil)
	}
	return err
}

// writeFirmware hands a piece of the image to the Firmware; the space
// exists only while frozen.
func (s *Server) writeFirmware(rw ReadWrite) error {
	s.mu.RLock()
	fw, frozen := s.Firmware, s.frozen
	s.mu.RUnlock()
	if fw == nil || !frozen {
		return &openlcb.Error{Code: ErrCodeSpaceUnknown}
	}
	return fw.WriteFirmware(rw.Address, rw.Data)
}

func (s *Server) options() Options {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o := Options{
		Available:    AvailUnalignedReads | AvailUnalignedWrites,
		WriteLengths: WriteLength1 | WriteLength2 | WriteLength4 | WriteLength64 | WriteLengthArbitrary,
		Name:         s.Name,
	}
	first := true
	for num := range s.spaces {
		if first || num > o.HighestSpace {
			o.HighestSpace = num
		}
		if first || num < o.LowestSpace {
			o.LowestSpace = num
		}
		first = false
	}
	if s.Firmware != nil && (first || SpaceFirmware < o.LowestSpace) {
		o.LowestSpace = SpaceFirmware
		if first {
			o.HighestSpace = SpaceFirmware
		}
	}
	return o
}

func (s *Server) spaceInfo(number uint8) SpaceInfo {
	sp, ok := s.Space(number)
	if !ok {
		return SpaceInfo{Space: number}
	}
	info := SpaceInfo{Space: number, Present: true, ReadOnly: sp.ReadOnly()}
	if size := sp.Size(); size > 0 {
		info.HighestAddress = size - 1
	}
	return info
}

func (s *Server) reply(to openlcb.NodeHandle, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A node that joined the bus after the requester never heard it
	// announce its ID, so it may know only the alias.
	if to.ID == 0 {
		if v, ok := s.dg.(aliasVerifier); ok {
			id, err := v.VerifyAlias(ctx, to.Alias)
			if err != nil {
				s.log.Debug("memconfig reply not delivered", "to", to, "error", err)
				return
			}
			to.ID = id
		}
	}
	if _, err := s.dg.SendDatagram(ctx, to.ID, payload); err != nil {
		s.log.Debug("memconfig reply not delivered", "to", to, "error", err)
	}
}

// aliasVerifier finds the node ID behind an alias; *node.Node is one.
type aliasVerifier interface {
	VerifyAlias(ctx context.Context, alias openlcb.Alias) (openlcb.NodeID, error)
}

// String describes the server for logs.
func (s *Server) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("memconfig server with %d spaces", len(s.spaces))
}
