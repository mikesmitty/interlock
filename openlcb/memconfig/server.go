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
	// Hooks, called on their own goroutine after the acknowledgment.
	OnUpdateComplete func()
	OnReboot         func()
	OnFactoryReset   func()
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
	if _, err := s.dg.SendDatagram(ctx, to.ID, payload); err != nil {
		s.log.Debug("memconfig reply not delivered", "to", to, "error", err)
	}
}

// String describes the server for logs.
func (s *Server) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("memconfig server with %d spaces", len(s.spaces))
}
