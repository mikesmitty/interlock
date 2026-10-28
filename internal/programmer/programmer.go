// Package programmer reads and writes DCC configuration variables: in
// service mode on the programming track, through memory space 0xF8 of the
// node that produces the "is programming track" event, and on the main
// (POM) through the same space of a train node, where a read needs a
// command station that hears the decoder's RailCom answer. Each operation
// is one byte, answered when the decoder has, with the DCC error codes
// turned into plain words.
package programmer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Bus finds the programming track.
type Bus interface {
	Producers(ctx context.Context, ev openlcb.EventID, window time.Duration, alsoReport bool) ([]node.Producer, error)
	VerifyAlias(ctx context.Context, alias openlcb.Alias) (openlcb.NodeID, error)
}

// Memory is the memory configuration client.
type Memory interface {
	Read(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, count int) ([]byte, error)
	Write(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, data []byte) error
}

// Nodes is the registry; it may be nil.
type Nodes interface {
	Get(id openlcb.NodeID) (registry.Info, bool)
}

// Trains finds train nodes for programming on the main; it may be nil.
type Trains interface {
	NodeFor(ctx context.Context, id string) (openlcb.NodeID, train.Train, error)
}

// Options configure the programmer.
type Options struct {
	Bus    Bus
	Memory Memory
	Nodes  Nodes
	Trains Trains
	Logger *slog.Logger
	// SearchWindow is how long to wait for the programming track to
	// answer; one second when zero.
	SearchWindow time.Duration
	// History is how many operations to remember; 50 when zero.
	History int
}

// Limits.
const (
	MaxCV    = 1024
	MaxValue = 255
)

// Errors.
var (
	ErrNoTrack = errors.New("programmer: no programming track found")
)

// DCC programming error codes, from the DCC working note. On the main,
// simple-dcc answers a read no decoder answered with CodeNoLocomotive and
// OpenMRN with CodeNoAnswer.
const (
	CodeProgrammingDisabled openlcb.ErrorCode = 0x1021
	CodeNoAnswer            openlcb.ErrorCode = 0x2030
	CodeNoLocomotive        openlcb.ErrorCode = 0x2031
	CodeFailedVerify        openlcb.ErrorCode = 0x2032
	CodeShort               openlcb.ErrorCode = 0x2035
)

// Track is the node that owns the programming track.
type Track struct {
	Node    openlcb.NodeID `json:"node,omitempty"`
	Name    string         `json:"name,omitempty"`
	Online  bool           `json:"online"`
	Checked time.Time      `json:"checked,omitzero"`
}

// Op is one CV operation, kept in the history.
type Op struct {
	ID int `json:"id"`
	// Kind is read, write, pom (a write on the main) or pom_read.
	Kind string `json:"kind"`
	// Train and TrainName for work on the main.
	Train     string `json:"train,omitempty"`
	TrainName string `json:"train_name,omitempty"`
	CV        int    `json:"cv"`
	// Value is what was read or written; meaningless when OK is false.
	Value    int       `json:"value"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
}

// Subscriber is told about every finished operation.
type Subscriber func(Op)

// Programmer runs CV operations one at a time.
type Programmer struct {
	opts   Options
	log    *slog.Logger
	window time.Duration
	keep   int

	busy sync.Mutex // one operation at a time

	mu      sync.RWMutex
	track   Track
	history []Op
	nextID  int
	subs    map[int]Subscriber
	nextSub int
}

// New builds a programmer.
func New(opts Options) *Programmer {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	p := &Programmer{opts: opts, log: opts.Logger, window: opts.SearchWindow, keep: opts.History, nextID: 1, subs: make(map[int]Subscriber)}
	if p.window <= 0 {
		p.window = time.Second
	}
	if p.keep <= 0 {
		p.keep = 50
	}
	return p
}

// Track returns what is known about the programming track.
func (p *Programmer) Track() Track {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.track
}

// Find asks the bus who owns the programming track.
func (p *Programmer) Find(ctx context.Context) (Track, error) {
	producers, err := p.opts.Bus.Producers(ctx, traction.EventProgrammingTrack, p.window, false)
	if err != nil {
		return p.Track(), err
	}
	var id openlcb.NodeID
	for _, pr := range producers {
		id = pr.Node
		if id == 0 {
			id, _ = p.opts.Bus.VerifyAlias(ctx, pr.Alias)
		}
		if id != 0 {
			break
		}
	}
	p.mu.Lock()
	p.track = Track{Node: id, Online: id != 0, Checked: time.Now()}
	if id != 0 {
		p.track.Name = id.String()
		if p.opts.Nodes != nil {
			if info, ok := p.opts.Nodes.Get(id); ok {
				p.track.Name = info.Name()
			}
		}
	}
	tr := p.track
	p.mu.Unlock()
	if id == 0 {
		return tr, ErrNoTrack
	}
	p.log.Info("found the programming track", "node", id, "name", tr.Name)
	return tr, nil
}

// HandleNode follows the registry for the track's node.
func (p *Programmer) HandleNode(info registry.Info) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.track.Node == 0 || info.ID != p.track.Node {
		return
	}
	p.track.Online = info.Online && !info.Forgotten
	if info.Forgotten {
		p.track = Track{}
	} else {
		p.track.Name = info.Name()
	}
}

func (p *Programmer) ensureTrack(ctx context.Context) (openlcb.NodeID, error) {
	p.mu.RLock()
	tr := p.track
	p.mu.RUnlock()
	if tr.Node != 0 && tr.Online {
		return tr.Node, nil
	}
	found, err := p.Find(ctx)
	if err != nil {
		return 0, err
	}
	return found.Node, nil
}

// Read reads a CV on the programming track.
func (p *Programmer) Read(ctx context.Context, cv int) (Op, error) {
	if err := checkCV(cv); err != nil {
		return Op{}, err
	}
	return p.run(ctx, Op{Kind: "read", CV: cv}, func(ctx context.Context, op *Op) error {
		id, err := p.ensureTrack(ctx)
		if err != nil {
			return err
		}
		data, err := p.opts.Memory.Read(ctx, id, traction.SpaceCV, uint32(cv-1), 1)
		if err != nil {
			return err
		}
		if len(data) != 1 {
			return fmt.Errorf("programmer: the command station answered with %d bytes", len(data))
		}
		op.Value = int(data[0])
		return nil
	})
}

// Write writes a CV on the programming track.
func (p *Programmer) Write(ctx context.Context, cv, value int) (Op, error) {
	if err := checkCV(cv); err != nil {
		return Op{}, err
	}
	if err := checkValue(value); err != nil {
		return Op{}, err
	}
	return p.run(ctx, Op{Kind: "write", CV: cv, Value: value}, func(ctx context.Context, _ *Op) error {
		id, err := p.ensureTrack(ctx)
		if err != nil {
			return err
		}
		return p.opts.Memory.Write(ctx, id, traction.SpaceCV, uint32(cv-1), []byte{byte(value)})
	})
}

// WritePOM writes a CV on the main, through the train's node.
func (p *Programmer) WritePOM(ctx context.Context, trainID string, cv, value int) (Op, error) {
	if err := checkCV(cv); err != nil {
		return Op{}, err
	}
	if err := checkValue(value); err != nil {
		return Op{}, err
	}
	if p.opts.Trains == nil {
		return Op{}, errors.New("programmer: trains are not available")
	}
	return p.run(ctx, Op{Kind: "pom", Train: trainID, CV: cv, Value: value}, func(ctx context.Context, op *Op) error {
		id, tr, err := p.opts.Trains.NodeFor(ctx, trainID)
		op.Train, op.TrainName = tr.ID, tr.Name
		if err != nil {
			return err
		}
		if err := p.opts.Memory.Write(ctx, id, traction.SpaceCV, uint32(cv-1), []byte{byte(value)}); err != nil {
			return mainError{err}
		}
		return nil
	})
}

// ReadPOM reads a CV on the main, through the train's node. The command
// station sends the decoder a read and waits for its RailCom answer.
func (p *Programmer) ReadPOM(ctx context.Context, trainID string, cv int) (Op, error) {
	if err := checkCV(cv); err != nil {
		return Op{}, err
	}
	if p.opts.Trains == nil {
		return Op{}, errors.New("programmer: trains are not available")
	}
	return p.run(ctx, Op{Kind: "pom_read", Train: trainID, CV: cv}, func(ctx context.Context, op *Op) error {
		id, tr, err := p.opts.Trains.NodeFor(ctx, trainID)
		op.Train, op.TrainName = tr.ID, tr.Name
		if err != nil {
			return err
		}
		data, err := p.opts.Memory.Read(ctx, id, traction.SpaceCV, uint32(cv-1), 1)
		if err != nil {
			return mainError{err}
		}
		if len(data) != 1 {
			return fmt.Errorf("programmer: the command station answered with %d bytes", len(data))
		}
		op.Value = int(data[0])
		return nil
	})
}

// mainError is the command station's answer to work on the main, which
// Explain words apart from the programming track's.
type mainError struct{ err error }

func (e mainError) Error() string { return e.err.Error() }
func (e mainError) Unwrap() error { return e.err }

// CannotReadOnMain reports whether the command station said it cannot
// read on the main at all, as one without RailCom does.
func CannotReadOnMain(err error) bool {
	var oerr *openlcb.Error
	return errors.As(err, &mainError{}) && errors.As(err, &oerr) && notImplemented(oerr.Code)
}

func notImplemented(code openlcb.ErrorCode) bool {
	return code == openlcb.ErrCodeNotImplementedDatagram || code == openlcb.ErrCodeNotImplemented || code == openlcb.ErrCodeNotImplementedSubcommand
}

// run performs one operation, records it and tells subscribers.
func (p *Programmer) run(ctx context.Context, op Op, do func(context.Context, *Op) error) (Op, error) {
	p.busy.Lock()
	defer p.busy.Unlock()
	op.Started = time.Now()
	err := do(ctx, &op)
	op.Finished = time.Now()
	op.OK = err == nil
	if err != nil {
		op.Error = Explain(err)
		p.log.Info("CV operation failed", "kind", op.Kind, "cv", op.CV, "error", err)
	} else {
		p.log.Info("CV operation done", "kind", op.Kind, "cv", op.CV, "value", op.Value)
	}
	p.mu.Lock()
	op.ID = p.nextID
	p.nextID++
	p.history = append([]Op{op}, p.history...)
	if len(p.history) > p.keep {
		p.history = p.history[:p.keep]
	}
	subs := make([]Subscriber, 0, len(p.subs))
	for _, sub := range p.subs {
		subs = append(subs, sub)
	}
	p.mu.Unlock()
	for _, sub := range subs {
		sub(op)
	}
	return op, err
}

// History returns past operations, newest first.
func (p *Programmer) History() []Op {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Op, len(p.history))
	copy(out, p.history)
	return out
}

// Subscribe registers a subscriber and returns a function that removes it.
func (p *Programmer) Subscribe(sub Subscriber) (unsubscribe func()) {
	p.mu.Lock()
	id := p.nextSub
	p.nextSub++
	p.subs[id] = sub
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.subs, id)
		p.mu.Unlock()
	}
}

func checkCV(cv int) error {
	if cv < 1 || cv > MaxCV {
		return &train.InvalidError{Reason: fmt.Sprintf("CV numbers run from 1 to %d", MaxCV)}
	}
	return nil
}

func checkValue(value int) error {
	if value < 0 || value > MaxValue {
		return &train.InvalidError{Reason: fmt.Sprintf("a CV value runs from 0 to %d", MaxValue)}
	}
	return nil
}

// Explain puts a programming error in plain words.
func Explain(err error) string {
	var oerr *openlcb.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNoTrack):
		return "No programming track found. Is a command station with one on the bus?"
	case errors.Is(err, train.ErrNoNode):
		return "No command station answered for that locomotive."
	case errors.Is(err, memconfig.ErrNoReply), errors.Is(err, node.ErrTimeout):
		return "The command station did not answer in time."
	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out waiting for the command station."
	case errors.As(err, &mainError{}) && errors.As(err, &oerr):
		switch {
		case oerr.Code == CodeNoLocomotive || oerr.Code == CodeNoAnswer:
			return "No decoder answered on the main. Is the locomotive on the track with power on, and RailCom turned on in its decoder?"
		case oerr.Code == openlcb.ErrCodeTemporary:
			return "The command station is busy with another CV on the main; try again."
		case notImplemented(oerr.Code):
			return "This command station cannot read on the main. That needs RailCom: a detector at the command station and the cutout turned on."
		}
		return "The command station reported " + oerr.Code.String() + "."
	case errors.As(err, &oerr):
		switch oerr.Code {
		case CodeNoLocomotive:
			return "No decoder answered. Is a locomotive on the programming track?"
		case CodeFailedVerify:
			return "The decoder did not confirm the value."
		case CodeShort:
			return "Short circuit on the programming track."
		case CodeProgrammingDisabled:
			return "The command station has no programming track."
		case openlcb.ErrCodeTemporary:
			return "The programming track is busy; try again."
		}
		if notImplemented(oerr.Code) {
			return "The command station cannot do that."
		}
		return "The command station reported " + oerr.Code.String() + "."
	}
	return err.Error()
}
