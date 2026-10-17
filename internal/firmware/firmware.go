// Package firmware upgrades nodes over the bus by the Firmware Upgrade
// standard: Freeze, the image written to space 0xEF in order, Unfreeze,
// then a watch for the node to come back with its new version and stay
// up. One upgrade runs at a time, and its progress is published as it
// goes.
package firmware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// Bus is what the upgrader needs from the local node.
type Bus interface {
	Protocols(ctx context.Context, id openlcb.NodeID) (openlcb.Protocols, error)
	SNIP(ctx context.Context, id openlcb.NodeID) (openlcb.SNIP, error)
	Observe(node.Observer) (unobserve func())
}

// Memory is the memory configuration client.
type Memory interface {
	Freeze(ctx context.Context, id openlcb.NodeID, space uint8) error
	Unfreeze(ctx context.Context, id openlcb.NodeID, space uint8) error
	Write(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, data []byte) error
}

// Nodes is the registry.
type Nodes interface {
	Get(id openlcb.NodeID) (registry.Info, bool)
	RefreshNode(ctx context.Context, id openlcb.NodeID) error
}

// Options configure an Upgrader.
type Options struct {
	Bus    Bus
	Memory Memory
	Nodes  Nodes
	Logger *slog.Logger
	// RestartTimeout is how long to wait for the node to announce itself
	// after Unfreeze (default one minute).
	RestartTimeout time.Duration
	// Settle is how long to watch the restarted node before calling the
	// upgrade done, long enough for a board that tries new firmware before
	// keeping it to go back (default 75 seconds). Zero skips the watch.
	Settle time.Duration
	// Retries is how many times a write that failed with a temporary error
	// is sent again (default 3).
	Retries int
}

// MaxImage bounds the size of an image.
const MaxImage = 16 << 20

// Phase is where an upgrade is.
type Phase string

// Phases, in order.
const (
	PhaseChecking   Phase = "checking"
	PhaseFreezing   Phase = "freezing"
	PhaseWriting    Phase = "writing"
	PhaseUnfreezing Phase = "unfreezing"
	PhaseRestarting Phase = "restarting"
	PhaseVerifying  Phase = "verifying"
	PhaseDone       Phase = "done"
	PhaseFailed     Phase = "failed"
)

// Active reports whether the upgrade is still going.
func (p Phase) Active() bool { return p != PhaseDone && p != PhaseFailed }

// Job is one upgrade, as the API shows it.
type Job struct {
	ID       string         `json:"id"`
	Node     openlcb.NodeID `json:"node"`
	NodeName string         `json:"node_name"`
	File     string         `json:"file"`
	Size     int            `json:"size"`
	Written  int            `json:"written"`
	Phase    Phase          `json:"phase"`
	// Message says what is happening, or how it ended, in plain words.
	Message string `json:"message"`
	// From and To are the node's software versions before and after.
	From     string    `json:"from,omitempty"`
	To       string    `json:"to,omitempty"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	Finished time.Time `json:"finished,omitzero"`
}

// Errors from Start.
var (
	ErrBusy     = errors.New("firmware: an upgrade is already in progress")
	ErrNotFound = errors.New("firmware: unknown node")
	ErrOffline  = errors.New("firmware: the node is offline")
	ErrNoJob    = errors.New("firmware: no upgrade for that node")
)

// InvalidError says the file is not something that can be sent.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "firmware: " + e.Reason }

type job struct {
	Job
	cancel context.CancelFunc
	// announce carries the time of each Initialization Complete the node
	// sent; waits ignore ones from before the step they follow.
	announce chan time.Time
	lastPub  int
}

// Upgrader runs firmware upgrades.
type Upgrader struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	jobs    map[openlcb.NodeID]*job
	subs    map[int]func(Job)
	nextSub int
	base    context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

// New builds an upgrader. Call Run to let it cancel upgrades on shutdown.
func New(opts Options) *Upgrader {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.RestartTimeout == 0 {
		opts.RestartTimeout = time.Minute
	}
	if opts.Settle == 0 {
		opts.Settle = 75 * time.Second
	}
	if opts.Retries == 0 {
		opts.Retries = 3
	}
	base, stop := context.WithCancel(context.Background())
	u := &Upgrader{opts: opts, log: opts.Logger, jobs: make(map[openlcb.NodeID]*job), subs: make(map[int]func(Job)), base: base, stop: stop}
	opts.Bus.Observe(u.observe)
	return u
}

// Run waits for ctx to end, then cancels the upgrade in progress.
func (u *Upgrader) Run(ctx context.Context) error {
	<-ctx.Done()
	u.stop()
	u.wg.Wait()
	return nil
}

// observe watches the bus for the upgraded node announcing itself.
func (u *Upgrader) observe(ev node.Event) {
	if ev.Kind != node.EventMessage {
		return
	}
	msg := ev.Message
	if msg.MTI != openlcb.MTIInitializationComplete && msg.MTI != openlcb.MTIInitializationCompleteSimple {
		return
	}
	id, ok := msg.PayloadNodeID()
	if !ok {
		return
	}
	u.mu.Lock()
	j, ok := u.jobs[id]
	active := ok && j.Phase.Active()
	u.mu.Unlock()
	if !active {
		return
	}
	select {
	case j.announce <- time.Now():
	default:
	}
}

// Decode checks a file and turns it into the image to send: UF2 files
// are unpacked, anything else is sent as it is.
func Decode(name string, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, &InvalidError{"the file is empty"}
	}
	if len(data) > MaxImage {
		return nil, &InvalidError{fmt.Sprintf("the file is larger than %d MB", MaxImage>>20)}
	}
	if IsUF2(data) {
		image, err := UnpackUF2(data)
		if err != nil {
			return nil, &InvalidError{err.Error()}
		}
		return image, nil
	}
	_ = name
	return data, nil
}

// Start begins upgrading node id with the file. It returns at once; the
// job's progress follows through Job and Subscribe.
func (u *Upgrader) Start(_ context.Context, id openlcb.NodeID, file string, data []byte) (Job, error) {
	image, err := Decode(file, data)
	if err != nil {
		return Job{}, err
	}
	info, ok := u.opts.Nodes.Get(id)
	if !ok {
		return Job{}, ErrNotFound
	}
	if !info.Online {
		return Job{}, ErrOffline
	}
	u.mu.Lock()
	for _, other := range u.jobs {
		if other.Phase.Active() {
			u.mu.Unlock()
			return Job{}, ErrBusy
		}
	}
	now := time.Now()
	j := &job{Job: Job{
		ID: newID(), Node: id, NodeName: info.Name(), File: file, Size: len(image),
		Phase: PhaseChecking, Message: "Checking the node", Started: now, Updated: now,
	}, announce: make(chan time.Time, 8)}
	if info.SNIP != nil {
		j.From = info.SNIP.SoftwareVersion
	}
	ctx, cancel := context.WithCancel(u.base)
	j.cancel = cancel
	u.jobs[id] = j
	snapshot := j.Job
	u.mu.Unlock()
	u.publish(snapshot)
	u.wg.Add(1)
	go u.run(ctx, j, image)
	return snapshot, nil
}

// Cancel stops the upgrade of node id. The node is unfrozen and keeps the
// firmware it has.
func (u *Upgrader) Cancel(id openlcb.NodeID) error {
	u.mu.Lock()
	j, ok := u.jobs[id]
	active := ok && j.Phase.Active()
	u.mu.Unlock()
	if !active {
		return ErrNoJob
	}
	j.cancel()
	return nil
}

// Job returns the latest upgrade of node id.
func (u *Upgrader) Job(id openlcb.NodeID) (Job, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	j, ok := u.jobs[id]
	if !ok {
		return Job{}, false
	}
	return j.Job, true
}

// Jobs returns the latest upgrade of every node that had one.
func (u *Upgrader) Jobs() []Job {
	u.mu.Lock()
	defer u.mu.Unlock()
	jobs := make([]Job, 0, len(u.jobs))
	for _, j := range u.jobs {
		jobs = append(jobs, j.Job)
	}
	return jobs
}

// Subscribe registers fn for every change to a job.
func (u *Upgrader) Subscribe(fn func(Job)) (unsubscribe func()) {
	u.mu.Lock()
	id := u.nextSub
	u.nextSub++
	u.subs[id] = fn
	u.mu.Unlock()
	return func() {
		u.mu.Lock()
		delete(u.subs, id)
		u.mu.Unlock()
	}
}

func (u *Upgrader) publish(j Job) {
	u.mu.Lock()
	subs := make([]func(Job), 0, len(u.subs))
	for _, fn := range u.subs {
		subs = append(subs, fn)
	}
	u.mu.Unlock()
	for _, fn := range subs {
		fn(j)
	}
}

// set moves a job to a phase with a message and publishes it.
func (u *Upgrader) set(j *job, phase Phase, message string) {
	u.mu.Lock()
	j.Phase = phase
	j.Message = message
	j.Updated = time.Now()
	if !phase.Active() {
		j.Finished = j.Updated
	}
	snapshot := j.Job
	u.mu.Unlock()
	u.log.Info("firmware upgrade", "node", j.Node, "phase", phase, "message", message)
	u.publish(snapshot)
}

// progress records bytes written, publishing every 4 KB and at the end.
func (u *Upgrader) progress(j *job, written int) {
	u.mu.Lock()
	j.Written = written
	j.Updated = time.Now()
	publish := written == j.Size || written-j.lastPub >= 4096
	if publish {
		j.lastPub = written
	}
	snapshot := j.Job
	u.mu.Unlock()
	if publish {
		u.publish(snapshot)
	}
}

// run carries one upgrade through its phases.
func (u *Upgrader) run(ctx context.Context, j *job, image []byte) {
	defer u.wg.Done()
	frozen := false
	err := u.upgrade(ctx, j, image, &frozen)
	if err == nil {
		return
	}
	if frozen {
		// End the upgrade state so the node goes on with its old firmware.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if uerr := u.opts.Memory.Unfreeze(cleanup, j.Node, memconfig.SpaceFirmware); uerr != nil {
			u.log.Debug("unfreeze after a failed upgrade", "node", j.Node, "error", uerr)
		}
		cancel()
	}
	message := err.Error()
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		message = "Canceled before it finished. The node keeps the firmware it had."
	}
	u.set(j, PhaseFailed, message)
}

// failure is an error whose text is already written for the user.
type failure string

func (f failure) Error() string { return string(f) }

func (u *Upgrader) upgrade(ctx context.Context, j *job, image []byte, frozen *bool) error {
	id := j.Node
	space := memconfig.SpaceFirmware

	// Does the node take upgrades at all?
	protocols, err := u.opts.Bus.Protocols(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, _ := u.opts.Nodes.Get(id)
		if info.ProtocolBits == 0 {
			return failure("The node did not answer: " + err.Error())
		}
		protocols = info.ProtocolBits
	}
	if !protocols.Has(openlcb.ProtocolFirmwareUpgrade) {
		return failure("This node does not offer firmware upgrades.")
	}

	u.set(j, PhaseFreezing, "Asking the node to prepare for new firmware")
	sentAt := time.Now()
	if err := u.opts.Memory.Freeze(ctx, id, space); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var oerr *openlcb.Error
		switch {
		case errors.As(err, &oerr) && oerr.Code.Temporary():
			return failure("The node is not ready for an upgrade yet: it is still trying out the last one. Wait a minute and try again.")
		case errors.As(err, &oerr):
			return failure("The node refused to start an upgrade: " + oerr.Code.String() + ".")
		case noReply(err):
			// Nodes that restart into a bootloader answer with their new
			// self rather than an acknowledgment.
			u.set(j, PhaseFreezing, "Waiting for the node to restart into its bootloader")
			if !u.waitAnnounce(ctx, j, sentAt, u.opts.RestartTimeout/2) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return failure("The node did not answer the request to prepare for new firmware.")
			}
		default:
			return failure("Could not prepare the node: " + err.Error())
		}
	}
	*frozen = true

	u.set(j, PhaseWriting, "Sending the image")
	for off := 0; off < len(image); {
		end := min(off+memconfig.MaxTransfer, len(image))
		if err := u.write(ctx, id, uint32(off), image[off:end]); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return writeFailure(off, err)
		}
		off = end
		u.progress(j, off)
	}

	u.set(j, PhaseUnfreezing, "Asking the node to check the image")
	*frozen = false
	sentAt = time.Now()
	if err := u.opts.Memory.Unfreeze(ctx, id, space); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var oerr *openlcb.Error
		switch {
		case errors.As(err, &oerr):
			return imageFailure(oerr.Code, "The node rejected the image")
		case noReply(err):
			// A bootloader may restart without answering.
		default:
			return failure("Could not finish the upgrade: " + err.Error())
		}
	}

	u.set(j, PhaseRestarting, "Waiting for the node to restart")
	if !u.waitAnnounce(ctx, j, sentAt, u.opts.RestartTimeout) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure("The node has not come back since the upgrade. Check it, then reload this page.")
	}
	version, err := u.readVersion(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure("The node restarted but did not say which version it runs: " + err.Error())
	}
	u.refreshNode(j.Node)
	u.mu.Lock()
	j.To = version
	from := j.From
	u.mu.Unlock()
	switch {
	case u.opts.Settle <= 0:
		return u.finish(j, version, from)
	case version == from:
		u.set(j, PhaseVerifying, "The node restarted but still reports version "+version+". Watching it.")
	default:
		u.set(j, PhaseVerifying, "The node restarted with version "+version+". Making sure it stays up.")
	}
	settle := time.NewTimer(u.opts.Settle)
	defer settle.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-settle.C:
			return u.finish(j, version, from)
		case <-j.announce:
			again, err := u.readVersion(ctx, id)
			if err != nil {
				continue
			}
			u.refreshNode(j.Node)
			if again != version && again == from {
				u.mu.Lock()
				j.To = again
				u.mu.Unlock()
				return failure("The node went back to version " + from + ": the new firmware did not confirm itself.")
			}
		}
	}
}

// refreshNode has the registry identify the node again, so its page shows
// the version it runs now.
func (u *Upgrader) refreshNode(id openlcb.NodeID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := u.opts.Nodes.RefreshNode(ctx, id); err != nil {
		u.log.Debug("refresh after upgrade", "node", id, "error", err)
	}
}

// finish declares an upgrade done.
func (u *Upgrader) finish(j *job, version, from string) error {
	switch version {
	case "":
		u.set(j, PhaseDone, "The node is back, but reports no software version.")
	case from:
		u.set(j, PhaseDone, "The node is back and reports version "+version+", the same as before.")
	default:
		u.set(j, PhaseDone, "Version "+version+" is running, replacing "+orUnknown(from)+".")
	}
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown version"
	}
	return s
}

// write sends one piece, retrying temporary failures and lost
// acknowledgments; the node accepts a repeat of its last write.
func (u *Upgrader) write(ctx context.Context, id openlcb.NodeID, address uint32, data []byte) error {
	var err error
	for attempt := 0; attempt <= u.opts.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(100*attempt) * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err = u.opts.Memory.Write(ctx, id, memconfig.SpaceFirmware, address, data)
		if err == nil || ctx.Err() != nil || !retryable(err) {
			return err
		}
		u.log.Debug("firmware write retried", "node", id, "address", address, "error", err)
	}
	return err
}

// retryable says whether sending a write again may succeed.
func retryable(err error) bool {
	var oerr *openlcb.Error
	if errors.As(err, &oerr) {
		return oerr.Code.Temporary()
	}
	return noReply(err)
}

// noReply says whether the node never answered at all.
func noReply(err error) bool {
	return errors.Is(err, node.ErrTimeout) || errors.Is(err, memconfig.ErrNoReply) || errors.Is(err, node.ErrNotFound)
}

// waitAnnounce waits for an Initialization Complete the node sent after
// cutoff; a Freeze makes the node announce itself too, and that one must
// not pass for the restart.
func (u *Upgrader) waitAnnounce(ctx context.Context, j *job, cutoff time.Time, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case at := <-j.announce:
			if at.After(cutoff) {
				return true
			}
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// readVersion reads the node's software version, patiently: a node that
// just started may take a moment to answer.
func (u *Upgrader) readVersion(ctx context.Context, id openlcb.NodeID) (string, error) {
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		var snip openlcb.SNIP
		snip, err = u.opts.Bus.SNIP(ctx, id)
		if err == nil {
			return snip.SoftwareVersion, nil
		}
	}
	return "", err
}

// writeFailure explains a write that failed at offset.
func writeFailure(offset int, err error) error {
	var oerr *openlcb.Error
	if errors.As(err, &oerr) {
		switch oerr.Code {
		case openlcb.ErrCodeFirmwareIncompatible, openlcb.ErrCodeFirmwareCorrupted, openlcb.ErrCodeFirmwareChecksum:
			return imageFailure(oerr.Code, "The node rejected the image")
		case openlcb.ErrCodeAddressOutOfBounds:
			return failure("The image is larger than the space the node has for firmware.")
		}
		return failure(fmt.Sprintf("The node refused the write at byte %d: %s.", offset, oerr.Code))
	}
	if noReply(err) {
		return failure(fmt.Sprintf("The node stopped answering at byte %d of the image.", offset))
	}
	return failure(fmt.Sprintf("Writing the image failed at byte %d: %v", offset, err))
}

// imageFailure explains the firmware error codes.
func imageFailure(code openlcb.ErrorCode, lead string) error {
	switch code {
	case openlcb.ErrCodeFirmwareIncompatible:
		return failure(lead + ": it is not firmware for this hardware.")
	case openlcb.ErrCodeFirmwareCorrupted:
		return failure(lead + ": it is damaged or not a complete firmware image.")
	case openlcb.ErrCodeFirmwareChecksum:
		return failure(lead + ": it did not arrive whole. Try again.")
	}
	return failure(lead + ": " + code.String() + ".")
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
