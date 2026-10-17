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

// Datagrams is what the client and server need from the local node.
type Datagrams interface {
	ID() openlcb.NodeID
	SendDatagram(ctx context.Context, id openlcb.NodeID, payload []byte) (node.DatagramResult, error)
	HandleDatagrams(contentType byte, handler node.DatagramHandler)
}

// Errors returned by the client.
var (
	// ErrNoReply: the node acknowledged the request but its reply did not
	// arrive in time.
	ErrNoReply = errors.New("memconfig: no reply from node")
	// ErrUnexpectedReply: the node answered with something other than the
	// reply to our request.
	ErrUnexpectedReply = errors.New("memconfig: unexpected reply")
)

// Client reads and writes address spaces on other nodes. One request to
// a given node is in flight at a time; requests to different nodes may
// overlap. All methods may be called from any goroutine.
type Client struct {
	dg     Datagrams
	log    *slog.Logger
	server *Server

	// ReplyTimeout is how long to wait for a reply datagram after the
	// acknowledgment when the node did not say how long it needs.
	ReplyTimeout time.Duration

	mu      sync.Mutex
	slots   map[openlcb.NodeID]chan struct{} // one token per node
	waiting map[openlcb.NodeID]chan []byte
}

// Option configures a Client.
type Option func(*Client)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// WithServer serves requests other nodes send to the local node.
func WithServer(s *Server) Option { return func(c *Client) { c.server = s } }

// WithReplyTimeout sets the default reply wait.
func WithReplyTimeout(d time.Duration) Option { return func(c *Client) { c.ReplyTimeout = d } }

// NewClient registers for memory configuration datagrams on dg.
func NewClient(dg Datagrams, opts ...Option) *Client {
	c := &Client{
		dg:           dg,
		log:          slog.Default(),
		ReplyTimeout: 3 * time.Second,
		slots:        make(map[openlcb.NodeID]chan struct{}),
		waiting:      make(map[openlcb.NodeID]chan []byte),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.server != nil {
		c.server.bind(dg, c.log)
	}
	dg.HandleDatagrams(ContentType, c.handle)
	return c
}

// handle runs on the node goroutine for every memory configuration
// datagram addressed to us: replies go to the waiting request, requests to
// the server.
func (c *Client) handle(msg *openlcb.Message) (node.DatagramFlags, error) {
	if len(msg.Payload) < 2 {
		return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments}
	}
	if !IsReply(msg.Payload[1]) {
		if c.server == nil {
			return node.DatagramFlags{}, &openlcb.Error{Code: openlcb.ErrCodeNotImplementedSubcommand}
		}
		return c.server.handle(msg)
	}
	c.mu.Lock()
	ch := c.waiting[msg.Source.ID]
	c.mu.Unlock()
	if ch == nil {
		c.log.Debug("memconfig reply with no request outstanding", "from", msg.Source, "command", fmt.Sprintf("0x%02X", msg.Payload[1]))
		return node.DatagramFlags{}, nil
	}
	payload := make([]byte, len(msg.Payload))
	copy(payload, msg.Payload)
	select {
	case ch <- payload:
	default:
		c.log.Debug("memconfig reply dropped, request already answered", "from", msg.Source)
	}
	return node.DatagramFlags{}, nil
}

// acquire takes the per-node slot so requests to one node are serialized.
func (c *Client) acquire(ctx context.Context, id openlcb.NodeID) (release func(), err error) {
	c.mu.Lock()
	slot, ok := c.slots[id]
	if !ok {
		slot = make(chan struct{}, 1)
		c.slots[id] = slot
	}
	c.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// transact sends a request and, if accept is non-nil, waits for the reply
// datagram it accepts. accept returns false for replies to ignore (and
// keep waiting), true when the reply is the one wanted.
func (c *Client) transact(ctx context.Context, id openlcb.NodeID, request []byte, accept func(reply []byte) bool) ([]byte, error) {
	release, err := c.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()

	var ch chan []byte
	if accept != nil {
		ch = make(chan []byte, 4)
		c.mu.Lock()
		c.waiting[id] = ch
		c.mu.Unlock()
		defer func() {
			c.mu.Lock()
			delete(c.waiting, id)
			c.mu.Unlock()
		}()
	}

	result, err := c.dg.SendDatagram(ctx, id, request)
	if err != nil {
		return nil, err
	}
	if accept == nil || (!result.ReplyPending && !expectsReplyAnyway(request)) {
		return nil, nil
	}
	wait := c.ReplyTimeout
	if result.Timeout > wait {
		wait = result.Timeout
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case reply := <-ch:
			if accept(reply) {
				return reply, nil
			}
			c.log.Debug("memconfig: ignoring reply that does not match the request", "node", id, "command", fmt.Sprintf("0x%02X", reply[1]))
		case <-timer.C:
			return nil, fmt.Errorf("%w %v within %v", ErrNoReply, id, wait)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// expectsReplyAnyway reports whether a request is always answered with a
// reply datagram, whether or not the acknowledgment says so. Reads and
// informational requests are; writes only when the node asks us to wait.
func expectsReplyAnyway(request []byte) bool {
	cmd := request[1]
	if IsReadWrite(cmd) {
		return cmd&rwCommandMask == CmdRead
	}
	return true
}

// Read fetches count bytes (1 to 64) at address in space.
func (c *Client) Read(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, count int) ([]byte, error) {
	if count < 1 || count > MaxTransfer {
		return nil, fmt.Errorf("memconfig: read count %d out of range 1..%d", count, MaxTransfer)
	}
	var got ReadWrite
	reply, err := c.transact(ctx, id, ReadRequest(space, address, uint8(count)), func(reply []byte) bool {
		rw, err := ParseReadWrite(reply)
		if err != nil || (rw.Command != CmdReadReply && rw.Command != CmdReadFailed) {
			return false
		}
		if rw.Space != space || rw.Address != address {
			return false
		}
		got = rw
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("read %s of %v at 0x%X: %w", SpaceName(space), id, address, err)
	}
	if reply == nil {
		return nil, fmt.Errorf("read %s of %v at 0x%X: %w", SpaceName(space), id, address, ErrNoReply)
	}
	if got.Failed() {
		return nil, &openlcb.Error{Code: got.ErrorCode(), Op: fmt.Sprintf("read %s of %v at 0x%X", SpaceName(space), id, address)}
	}
	return got.Data, nil
}

// Progress reports how a long transfer is going: done bytes so far, and
// the total if known (else 0).
type Progress func(done, total int)

// ReadAll reads size bytes starting at address, in 64 byte pieces. A size
// below zero reads until the node reports the end of the space, either by
// returning fewer bytes than asked or by an out-of-bounds error after at
// least one piece arrived.
func (c *Client) ReadAll(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, size int, progress Progress) ([]byte, error) {
	var out []byte
	total := max(size, 0)
	for size < 0 || len(out) < size {
		count := MaxTransfer
		if size >= 0 && size-len(out) < count {
			count = size - len(out)
		}
		data, err := c.Read(ctx, id, space, address+uint32(len(out)), count)
		if err != nil {
			var oerr *openlcb.Error
			if size < 0 && len(out) > 0 && errors.As(err, &oerr) && oerr.Code == ErrCodeOutOfBounds {
				break
			}
			return out, err
		}
		out = append(out, data...)
		if progress != nil {
			progress(len(out), total)
		}
		if len(data) < count {
			break // end of the space
		}
		if len(data) == 0 {
			break
		}
	}
	return out, nil
}

// ReadCDI fetches the node's Configuration Description Information XML,
// stopping at its terminating NUL.
func (c *Client) ReadCDI(ctx context.Context, id openlcb.NodeID, progress Progress) ([]byte, error) {
	size := -1
	infoCtx, cancel := context.WithTimeout(ctx, c.ReplyTimeout)
	info, err := c.SpaceInfo(infoCtx, id, SpaceCDI)
	cancel()
	if err == nil && info.Present && info.Size() > 0 && info.Size() < 1<<20 {
		size = int(info.Size())
	}
	var out []byte
	for size < 0 || len(out) < size {
		count := MaxTransfer
		if size >= 0 && size-len(out) < count {
			count = size - len(out)
		}
		data, err := c.Read(ctx, id, SpaceCDI, uint32(len(out)), count)
		if err != nil {
			var oerr *openlcb.Error
			if len(out) > 0 && errors.As(err, &oerr) && oerr.Code == ErrCodeOutOfBounds {
				break
			}
			return out, err
		}
		done := false
		if i := indexByte(data, 0); i >= 0 {
			data = data[:i]
			done = true
		}
		out = append(out, data...)
		if progress != nil {
			progress(len(out), max(size, 0))
		}
		if done || len(data) < count {
			break
		}
	}
	return out, nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Write stores data at address in space, in 64 byte pieces. It returns
// after the node has acknowledged every piece, and the write reply when the
// node sends one.
func (c *Client) Write(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	for off := 0; off < len(data); off += MaxTransfer {
		end := min(off+MaxTransfer, len(data))
		addr := address + uint32(off)
		var got ReadWrite
		reply, err := c.transact(ctx, id, WriteRequest(space, addr, data[off:end]), func(reply []byte) bool {
			rw, err := ParseReadWrite(reply)
			if err != nil || (rw.Command != CmdWriteReply && rw.Command != CmdWriteFailed) {
				return false
			}
			if rw.Space != space || rw.Address != addr {
				return false
			}
			got = rw
			return true
		})
		if err != nil {
			return fmt.Errorf("write %s of %v at 0x%X: %w", SpaceName(space), id, addr, err)
		}
		if reply != nil && got.Failed() {
			return &openlcb.Error{Code: got.ErrorCode(), Op: fmt.Sprintf("write %s of %v at 0x%X", SpaceName(space), id, addr)}
		}
	}
	return nil
}

// Options asks what the node's memory configuration supports.
func (c *Client) Options(ctx context.Context, id openlcb.NodeID) (Options, error) {
	reply, err := c.transact(ctx, id, OptionsRequest(), func(reply []byte) bool { return reply[1] == CmdOptionsReply })
	if err != nil {
		return Options{}, fmt.Errorf("options of %v: %w", id, err)
	}
	return ParseOptions(reply)
}

// SpaceInfo asks about one address space.
func (c *Client) SpaceInfo(ctx context.Context, id openlcb.NodeID, space uint8) (SpaceInfo, error) {
	reply, err := c.transact(ctx, id, SpaceInfoRequest(space), func(reply []byte) bool {
		return reply[1]&0xFE == CmdSpaceInfoReply && len(reply) >= 3 && reply[2] == space
	})
	if err != nil {
		return SpaceInfo{}, fmt.Errorf("info on %s of %v: %w", SpaceName(space), id, err)
	}
	return ParseSpaceInfo(reply)
}

// Lock asks for the node's configuration lock and returns who holds it
// afterwards: us on success, another node if it got there first. A zero
// requester releases the lock.
func (c *Client) Lock(ctx context.Context, id, requester openlcb.NodeID) (openlcb.NodeID, error) {
	reply, err := c.transact(ctx, id, LockRequest(requester), func(reply []byte) bool { return reply[1] == CmdLockReply })
	if err != nil {
		return 0, fmt.Errorf("lock %v: %w", id, err)
	}
	return ParseLockReply(reply)
}

// UpdateComplete tells the node that a series of writes is finished so it
// can apply them.
func (c *Client) UpdateComplete(ctx context.Context, id openlcb.NodeID) error {
	_, err := c.transact(ctx, id, UpdateCompleteRequest(), nil)
	if err != nil {
		return fmt.Errorf("update complete to %v: %w", id, err)
	}
	return nil
}

// Reboot asks the node to restart.
func (c *Client) Reboot(ctx context.Context, id openlcb.NodeID) error {
	_, err := c.transact(ctx, id, RebootRequest(), nil)
	if err != nil {
		return fmt.Errorf("reboot %v: %w", id, err)
	}
	return nil
}

// FactoryReset asks the node to return to its factory configuration. Most
// nodes restart afterwards.
func (c *Client) FactoryReset(ctx context.Context, id openlcb.NodeID) error {
	_, err := c.transact(ctx, id, FactoryResetRequest(id), nil)
	if err != nil {
		return fmt.Errorf("factory reset %v: %w", id, err)
	}
	return nil
}

// Freeze puts the node into the frozen state for a firmware upgrade of
// space. The node answers with Initialization Complete, which the
// registry sees as the node reappearing.
func (c *Client) Freeze(ctx context.Context, id openlcb.NodeID, space uint8) error {
	_, err := c.transact(ctx, id, FreezeRequest(space), nil)
	if err != nil {
		return fmt.Errorf("freeze %v: %w", id, err)
	}
	return nil
}

// Unfreeze ends the frozen state.
func (c *Client) Unfreeze(ctx context.Context, id openlcb.NodeID, space uint8) error {
	_, err := c.transact(ctx, id, UnfreezeRequest(space), nil)
	if err != nil {
		return fmt.Errorf("unfreeze %v: %w", id, err)
	}
	return nil
}
