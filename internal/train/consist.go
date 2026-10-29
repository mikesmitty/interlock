package train

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// Consist kinds.
const (
	// ConsistLCC is kept by the command station: the lead's train node
	// passes its speed, and the functions each member follows, on to the
	// members' train nodes.
	ConsistLCC = "lcc"
	// ConsistDCC is an advanced consist kept in the decoders: each one
	// answers a consist address as well as its own.
	ConsistDCC = "dcc"
)

// ErrConsistRefused means the command station would not take a
// locomotive into a consist.
var ErrConsistRefused = errors.New("train: the command station refused the consist")

// consistErrNotFound is what a train answers a detach of a node that is
// not in its consist.
const consistErrNotFound openlcb.ErrorCode = 0x1030

// Consist is locomotives in the roster that run together, driven as one
// through the lead, the first member.
type Consist struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	// Kind is ConsistLCC or ConsistDCC.
	Kind string `yaml:"kind" json:"kind"`
	// Address is a DCC consist's address, 1 to 127.
	Address uint16 `yaml:"address,omitempty" json:"address,omitempty"`
	// Members are the locomotives, the lead first.
	Members []Member `yaml:"members" json:"members"`
	// MadeUp is whether the consist is put together on the layout. It is
	// kept so the consist is put together again after a restart.
	MadeUp bool `yaml:"made_up,omitempty" json:"made_up"`
}

// Member is a locomotive in a consist. The lead's flags mean nothing:
// the lead is the one driven.
type Member struct {
	// Train is the roster entry's ID.
	Train string `yaml:"train" json:"train"`
	// Reverse is set for a locomotive facing the other way from the lead.
	Reverse bool `yaml:"reverse,omitempty" json:"reverse"`
	// F0 and Functions say whether the member follows the lead's F0 and
	// its other functions.
	F0        bool `yaml:"f0,omitempty" json:"f0"`
	Functions bool `yaml:"functions,omitempty" json:"functions"`
}

// flags returns the member's flags for a consist attach.
func (mb Member) flags() byte {
	var f byte
	if mb.Reverse {
		f |= traction.ConsistFlagReverse
	}
	if mb.F0 {
		f |= traction.ConsistFlagLinkF0
	}
	if mb.Functions {
		f |= traction.ConsistFlagLinkFn
	}
	return f
}

// follows reports whether the member follows the lead's function fn.
func (mb Member) follows(fn int) bool {
	if fn == 0 {
		return mb.F0
	}
	return mb.Functions
}

// The CVs a DCC consist sets in each decoder (S-9.2.2): the consist
// address with the way the decoder faces in bit 7, and the functions it
// takes at the consist address, F1 to F8 in CV 21 and F0 (both ways) and
// F9 to F12 in CV 22.
const (
	cvConsistAddress    = 19
	cvConsistFunctions1 = 21
	cvConsistFunctions2 = 22
)

// consistFunctionMax is the highest function decoders take at a consist
// address.
const consistFunctionMax = 12

// dccCVs returns the member's CVs 19, 21 and 22 in a DCC consist at
// address. The lead takes every function the consist address carries.
func (mb Member) dccCVs(address uint16, lead bool) (cv19, cv21, cv22 byte) {
	cv19 = byte(address)
	if mb.Reverse {
		cv19 |= 0x80
	}
	if lead || mb.F0 {
		cv22 |= 0x03
	}
	if lead || mb.Functions {
		cv21 = 0xFF
		cv22 |= 0x3C
	}
	return cv19, cv21, cv22
}

// ConsistState is a consist as the API shows it.
type ConsistState struct {
	Consist
	// Attached is whether Interlock put the consist together on the
	// command station since the lead's train node last came onto the bus.
	Attached bool `json:"attached"`
	// Problem says why the consist could not be put together last time.
	Problem string `json:"problem,omitempty"`
	// Removed is set on the last update about a deleted consist.
	Removed bool `json:"removed,omitempty"`
}

// ConsistSubscriber is told whenever a consist changes.
type ConsistSubscriber func(ConsistState)

// Normalize tidies a consist and fills in its kind.
func (c Consist) Normalize() Consist {
	c.Name = strings.TrimSpace(c.Name)
	if c.Kind == "" {
		c.Kind = ConsistLCC
	}
	if c.Kind == ConsistLCC {
		c.Address = 0
	}
	members := make([]Member, len(c.Members))
	copy(members, c.Members)
	if len(members) > 0 {
		members[0] = Member{Train: members[0].Train}
	}
	c.Members = members
	return c
}

// Validate checks a consist on its own; the manager checks its
// locomotives against the roster.
func (c Consist) Validate() error {
	if !validID(c.ID) {
		return invalid("%q cannot be a consist ID; use letters, digits and dashes", c.ID)
	}
	if c.Name == "" {
		return invalid("a consist needs a name")
	}
	if len(c.Name) > 64 {
		return invalid("the name is too long; keep it under 64 characters")
	}
	switch c.Kind {
	case ConsistLCC:
	case ConsistDCC:
		if c.Address < 1 || c.Address > traction.ShortAddressMax {
			return invalid("a DCC consist's address must be between 1 and %d", traction.ShortAddressMax)
		}
	default:
		return invalid("%q is not a kind of consist; use lcc or dcc", c.Kind)
	}
	seen := make(map[string]bool, len(c.Members))
	for _, mb := range c.Members {
		if !validID(mb.Train) {
			return invalid("%q cannot be a train ID", mb.Train)
		}
		if seen[mb.Train] {
			return invalid("%s is in the consist twice", mb.Train)
		}
		seen[mb.Train] = true
	}
	return nil
}

func (c Consist) clone() Consist {
	c.Members = append([]Member(nil), c.Members...)
	return c
}

// lead returns the lead's roster ID.
func (c Consist) lead() string {
	if len(c.Members) == 0 {
		return ""
	}
	return c.Members[0].Train
}

// --- the manager's consists --------------------------------------------------

// loadConsists takes the consists from the file, leaving out locomotives
// no longer in the roster. The caller holds m.mu or is New.
func (m *Manager) loadConsists() {
	for _, c := range m.opts.File.Consists() {
		kept := c.Members[:0]
		for _, mb := range c.Members {
			if _, ok := m.roster[mb.Train]; ok {
				kept = append(kept, mb)
			} else {
				m.log.Warn("consist names a locomotive not in the roster", "consist", c.ID, "train", mb.Train)
			}
		}
		c.Members = kept
		m.consists[c.ID] = c.Normalize()
	}
}

// consistStateLocked is a consist as the API shows it; the caller holds
// m.mu.
func (m *Manager) consistStateLocked(c Consist) ConsistState {
	_, attached := m.consistNode[c.ID]
	// A DCC consist is kept in the decoders, which do not forget it.
	attached = attached || c.Kind == ConsistDCC
	return ConsistState{Consist: c.clone(), Attached: attached && c.MadeUp, Problem: m.consistProblem[c.ID]}
}

// Consists returns the consists by name.
func (m *Manager) Consists() []ConsistState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ConsistState, 0, len(m.consists))
	for _, c := range m.consists {
		out = append(out, m.consistStateLocked(c))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Consist returns one consist.
func (m *Manager) Consist(id string) (ConsistState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.consists[id]
	if !ok {
		return ConsistState{}, false
	}
	return m.consistStateLocked(c), true
}

// checkConsistLocked checks a consist's locomotives against the roster:
// two or more, each in the roster, at addresses of their own, and a DCC
// consist's address not one a locomotive answers to. The caller holds
// m.mu.
func (m *Manager) checkConsistLocked(c Consist) error {
	if len(c.Members) < 2 {
		return invalid("a consist needs two locomotives or more")
	}
	keys := make(map[string]string, len(c.Members))
	for _, mb := range c.Members {
		e, ok := m.roster[mb.Train]
		if !ok {
			return invalid("%s is not in the roster", mb.Train)
		}
		if other, ok := keys[e.key()]; ok {
			return invalid("%s and %s share the address %s; a consist needs locomotives at addresses of their own", other, e.Name, traction.AddressName(e.Address, e.Long))
		}
		keys[e.key()] = e.Name
	}
	if c.Kind == ConsistDCC {
		key := IDFor(c.Address, false)
		for _, e := range m.roster {
			if e.key() == key {
				return invalid("%s answers to address %d already; choose a consist address no locomotive uses", e.Name, c.Address)
			}
		}
	}
	return nil
}

// madeUpAtLocked returns the made-up consist with a locomotive at a train
// key, other than skip. The caller holds m.mu.
func (m *Manager) madeUpAtLocked(key, skip string) (Consist, int, bool) {
	for _, c := range m.consists {
		if !c.MadeUp || c.ID == skip {
			continue
		}
		for i, mb := range c.Members {
			if e, ok := m.roster[mb.Train]; ok && e.key() == key {
				return c, i, true
			}
		}
	}
	return Consist{}, 0, false
}

// newConsistIDLocked makes an ID for a new consist from its name.
func (m *Manager) newConsistIDLocked(name string) string {
	base := slug(name)
	if base == "" {
		base = "consist"
	}
	return uniqueID(base, func(id string) bool {
		_, taken := m.consists[id]
		return taken
	})
}

// CreateConsist adds a consist. It starts apart.
func (m *Manager) CreateConsist(c Consist) (ConsistState, error) {
	m.mu.Lock()
	c = c.Normalize()
	c.ID = m.newConsistIDLocked(c.Name)
	c.MadeUp = false
	if err := c.Validate(); err != nil {
		m.mu.Unlock()
		return ConsistState{}, err
	}
	if err := m.checkConsistLocked(c); err != nil {
		m.mu.Unlock()
		return ConsistState{}, err
	}
	m.consists[c.ID] = c
	state := m.consistStateLocked(c)
	m.mu.Unlock()
	if err := m.opts.File.PutConsist(c); err != nil {
		return state, err
	}
	m.sendConsist(state)
	return state, nil
}

// UpdateConsist changes a consist: change edits a copy, which is checked
// and saved. A made-up consist can only be renamed.
func (m *Manager) UpdateConsist(id string, change func(*Consist) error) (ConsistState, error) {
	m.consistOp.Lock()
	defer m.consistOp.Unlock()
	m.mu.Lock()
	old, ok := m.consists[id]
	if !ok {
		m.mu.Unlock()
		return ConsistState{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next := old.clone()
	if err := change(&next); err != nil {
		m.mu.Unlock()
		return ConsistState{}, err
	}
	next.ID, next.MadeUp = old.ID, old.MadeUp
	next = next.Normalize()
	if err := next.Validate(); err != nil {
		m.mu.Unlock()
		return ConsistState{}, err
	}
	if old.MadeUp {
		renamed := old.clone()
		renamed.Name = next.Name
		if !consistsEqual(renamed, next) {
			m.mu.Unlock()
			return ConsistState{}, invalid("break %s up before changing its locomotives", old.Name)
		}
	}
	if err := m.checkConsistLocked(next); err != nil {
		m.mu.Unlock()
		return ConsistState{}, err
	}
	m.consists[id] = next
	state := m.consistStateLocked(next)
	m.mu.Unlock()
	if err := m.opts.File.PutConsist(next); err != nil {
		return state, err
	}
	m.sendConsist(state)
	return state, nil
}

func consistsEqual(a, b Consist) bool {
	if a.ID != b.ID || a.Name != b.Name || a.Kind != b.Kind || a.Address != b.Address || a.MadeUp != b.MadeUp || len(a.Members) != len(b.Members) {
		return false
	}
	for i := range a.Members {
		if a.Members[i] != b.Members[i] {
			return false
		}
	}
	return true
}

// DeleteConsist removes a consist that is apart.
func (m *Manager) DeleteConsist(id string) error {
	m.consistOp.Lock()
	defer m.consistOp.Unlock()
	m.mu.Lock()
	c, ok := m.consists[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if c.MadeUp {
		m.mu.Unlock()
		return invalid("break %s up before removing it", c.Name)
	}
	delete(m.consists, id)
	delete(m.consistProblem, id)
	state := m.consistStateLocked(c)
	m.mu.Unlock()
	if err := m.opts.File.RemoveConsist(id); err != nil {
		return err
	}
	state.Removed = true
	m.sendConsist(state)
	return nil
}

// consistTrainsLocked returns the trains of a consist's locomotives, the
// lead first. The caller holds m.mu, and has checked the consist.
func (m *Manager) consistTrainsLocked(c Consist) []*train {
	out := make([]*train, len(c.Members))
	for i, mb := range c.Members {
		e := m.roster[mb.Train]
		out[i] = m.trainAtLocked(e.Address, e.Long)
	}
	return out
}

// MakeUp puts a consist together on the layout: for an LCC consist, the
// lead's train node is told to pass its commands on to the members'; for
// a DCC consist, each decoder is given the consist address.
func (m *Manager) MakeUp(ctx context.Context, id string) (ConsistState, error) {
	m.consistOp.Lock()
	defer m.consistOp.Unlock()
	m.mu.Lock()
	c, ok := m.consists[id]
	if !ok {
		m.mu.Unlock()
		return ConsistState{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	refuse := func(err error) (ConsistState, error) {
		state := m.consistStateLocked(c)
		m.mu.Unlock()
		return state, err
	}
	if err := m.checkConsistLocked(c); err != nil {
		return refuse(err)
	}
	for _, mb := range c.Members {
		e := m.roster[mb.Train]
		if other, _, ok := m.madeUpAtLocked(e.key(), c.ID); ok {
			return refuse(invalid("%s runs in %s; break that up first", e.Name, other.Name))
		}
	}
	trains := m.consistTrainsLocked(c)
	if c.Kind == ConsistDCC {
		if m.opts.Memory == nil {
			return refuse(invalid("DCC consists need programming on the main, which is not set up"))
		}
		if other, ok := m.dccConsistAtLocked(c.Address, false); ok && other.ID != c.ID {
			return refuse(invalid("%s uses consist address %d already", other.Name, c.Address))
		}
	}
	m.mu.Unlock()
	if c.Kind == ConsistDCC {
		// A decoder answers its own address again when the consist is
		// broken up, at the speed the command station last sent there.
		// Another throttle may be driving one; the command station knows.
		for _, tr := range trains {
			moving, err := m.moving(ctx, tr, true)
			if err == nil && moving {
				err = invalid("stop %s before making up the consist", m.Name(tr.Address, tr.Long))
			} else if err != nil {
				err = fmt.Errorf("check %s is stopped: %w", m.Name(tr.Address, tr.Long), err)
			}
			if err != nil {
				m.mu.RLock()
				state := m.consistStateLocked(m.consists[id])
				m.mu.RUnlock()
				return state, err
			}
		}
	}

	var lead openlcb.NodeID
	var err error
	if c.Kind == ConsistDCC {
		err = m.makeUpDCC(ctx, c, trains)
	} else {
		lead, err = m.leadNode(ctx, trains[0])
		if err == nil {
			err = m.attach(ctx, c, lead, trains)
		}
	}
	m.mu.Lock()
	if err != nil {
		m.consistProblem[id] = problemText(err)
	} else {
		delete(m.consistProblem, id)
		c = m.consists[id]
		c.MadeUp = true
		m.consists[id] = c
		if lead != 0 {
			m.consistNode[id] = lead
		}
	}
	state := m.consistStateLocked(c)
	m.mu.Unlock()
	if err == nil {
		if ferr := m.opts.File.PutConsist(c); ferr != nil {
			err = ferr
		}
		m.log.Info("made up a consist", "consist", c.ID, "kind", c.Kind, "lead", trains[0].ID)
	}
	m.sendConsist(state)
	for _, tr := range trains {
		m.publish(tr)
	}
	return state, err
}

// leadNode finds the lead's train node.
func (m *Manager) leadNode(ctx context.Context, lead *train) (openlcb.NodeID, error) {
	lead.acquire.Lock()
	defer lead.acquire.Unlock()
	return m.ensureNode(ctx, lead)
}

// attach finds the members' train nodes and attaches them to the lead's.
// When one cannot be attached, those attached already are detached again
// so no part of the consist is left together. The caller holds
// m.consistOp.
func (m *Manager) attach(ctx context.Context, c Consist, lead openlcb.NodeID, trains []*train) error {
	var done []openlcb.NodeID
	undo := func() {
		for _, n := range done {
			if _, err := m.opts.Bus.TractionCommand(ctx, lead, traction.DetachConsist(n), true); err != nil {
				m.log.Debug("detach failed", "consist", c.ID, "member", n, "error", err)
			}
		}
	}
	for i := 1; i < len(trains); i++ {
		tr := trains[i]
		tr.acquire.Lock()
		node, err := m.ensureNode(ctx, tr)
		tr.acquire.Unlock()
		if err != nil {
			undo()
			return err
		}
		name := m.Name(tr.Address, tr.Long)
		reply, err := m.opts.Bus.TractionCommand(ctx, lead, traction.AttachConsist(node, c.Members[i].flags()), true)
		if err != nil {
			undo()
			return fmt.Errorf("add %s to the consist: %w", name, err)
		}
		result, err := traction.ParseConsistResult(reply)
		if err != nil {
			undo()
			return err
		}
		if result.Code != 0 {
			undo()
			return fmt.Errorf("%w: it would not add %s (error 0x%04X), and may hold no more locomotives", ErrConsistRefused, name, uint16(result.Code))
		}
		done = append(done, node)
	}
	return nil
}

// makeUpDCC gives each decoder of a DCC consist, on the main, the
// functions it takes at the consist address and then the consist address
// with the way it faces. With RailCom, each one's CV 19 is read back to
// check it took it. When one cannot be given it, the others are taken out
// again so no part of the consist is left together. The caller holds
// m.consistOp.
func (m *Manager) makeUpDCC(ctx context.Context, c Consist, trains []*train) error {
	var done []*train
	undo := func() {
		for _, tr := range done {
			if err := m.writeCV(ctx, tr, cvConsistAddress, 0); err != nil {
				m.log.Debug("taking a decoder out of a consist failed", "consist", c.ID, "train", tr.ID, "error", err)
			}
		}
	}
	for i, tr := range trains {
		cv19, cv21, cv22 := c.Members[i].dccCVs(c.Address, i == 0)
		for _, w := range []struct {
			cv    int
			value byte
		}{{cvConsistFunctions1, cv21}, {cvConsistFunctions2, cv22}, {cvConsistAddress, cv19}} {
			if err := m.writeCV(ctx, tr, w.cv, w.value); err != nil {
				undo()
				return fmt.Errorf("give %s the consist address: %w", m.Name(tr.Address, tr.Long), err)
			}
		}
		done = append(done, tr)
	}
	for i, tr := range trains {
		want, _, _ := c.Members[i].dccCVs(c.Address, i == 0)
		// Without RailCom, or with it off in the decoder, nothing answers.
		if got, err := m.readCV(ctx, tr, cvConsistAddress); err == nil && got != want {
			undo()
			return fmt.Errorf("%s did not take the consist address: its CV 19 reads %d, not %d", m.Name(tr.Address, tr.Long), got, want)
		}
	}
	return nil
}

// breakUpDCC takes each decoder of a DCC consist out of it. It reports
// the decoders that could not be reached, or that still answer the
// consist address when read back. The caller holds m.consistOp.
func (m *Manager) breakUpDCC(ctx context.Context, c Consist, trains []*train) error {
	var problems []string
	for _, tr := range trains {
		name := m.Name(tr.Address, tr.Long)
		if err := m.writeCV(ctx, tr, cvConsistAddress, 0); err != nil {
			problems = append(problems, fmt.Sprintf("%s could not be reached (%v)", name, err))
			continue
		}
		if got, err := m.readCV(ctx, tr, cvConsistAddress); err == nil && got != 0 {
			problems = append(problems, fmt.Sprintf("%s still has CV 19 at %d", name, got))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s is still made up: %s; break it up again", c.Name, strings.Join(problems, ", "))
	}
	return nil
}

// writeCV writes a CV on the main through a train's node, and keeps the
// value for the decoder fitted in the roster's locomotives at its
// address.
func (m *Manager) writeCV(ctx context.Context, tr *train, cv int, value byte) error {
	tr.acquire.Lock()
	node, err := m.ensureNode(ctx, tr)
	tr.acquire.Unlock()
	if err != nil {
		return err
	}
	if err := m.opts.Memory.Write(ctx, node, traction.SpaceCV, uint32(cv-1), []byte{value}); err != nil {
		return err
	}
	m.recordCV(tr, cv, value, true)
	return nil
}

// readCV reads a CV on the main through a train's node, which needs a
// command station that hears the decoder's RailCom answer.
func (m *Manager) readCV(ctx context.Context, tr *train, cv int) (byte, error) {
	tr.acquire.Lock()
	node, err := m.ensureNode(ctx, tr)
	tr.acquire.Unlock()
	if err != nil {
		return 0, err
	}
	b, err := m.opts.Memory.Read(ctx, node, traction.SpaceCV, uint32(cv-1), 1)
	if err != nil {
		return 0, err
	}
	if len(b) != 1 {
		return 0, fmt.Errorf("read %d bytes of CV %d", len(b), cv)
	}
	m.recordCV(tr, cv, b[0], false)
	return b[0], nil
}

// recordCV keeps a value read from or written to the decoder at a
// train's address for the decoder the roster says is fitted there.
func (m *Manager) recordCV(tr *train, cv int, value byte, written bool) {
	m.mu.RLock()
	es := m.entriesAtLocked(tr.ID)
	m.mu.RUnlock()
	for _, e := range es {
		if e.Installed == "" {
			continue
		}
		if err := m.opts.CVs.Programmed(e.Installed, strconv.Itoa(cv), int(value), written); err != nil {
			m.log.Warn("could not keep a consist CV", "train", e.ID, "decoder", e.Installed, "cv", cv, "error", err)
		}
	}
}

// ensureConsist puts the LCC consist a train leads together again when
// the command station may have lost it: after Interlock or the command
// station restarted.
func (m *Manager) ensureConsist(ctx context.Context, tr *train) error {
	m.mu.RLock()
	c, ok := m.ledByLocked(tr)
	needed := ok && c.Kind == ConsistLCC && m.consistNode[c.ID] == 0
	m.mu.RUnlock()
	if !needed {
		return nil
	}
	m.consistOp.Lock()
	defer m.consistOp.Unlock()
	m.mu.Lock()
	c, ok = m.ledByLocked(tr)
	if !ok || c.Kind != ConsistLCC || m.consistNode[c.ID] != 0 {
		m.mu.Unlock()
		return nil
	}
	if err := m.checkConsistLocked(c); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("%s cannot be put together: %w", c.Name, err)
	}
	trains := m.consistTrainsLocked(c)
	m.mu.Unlock()
	lead, err := m.leadNode(ctx, tr)
	if err == nil {
		err = m.attach(ctx, c, lead, trains)
	}
	m.mu.Lock()
	if err != nil {
		m.consistProblem[c.ID] = problemText(err)
	} else {
		delete(m.consistProblem, c.ID)
		m.consistNode[c.ID] = lead
	}
	state := m.consistStateLocked(m.consists[c.ID])
	m.mu.Unlock()
	m.sendConsist(state)
	if err != nil {
		return fmt.Errorf("put %s together: %w", c.Name, err)
	}
	m.log.Info("put a consist together again", "consist", c.ID, "lead", lead)
	return nil
}

// problemText is an error as a consist's problem reads.
func problemText(err error) string {
	var iv *InvalidError
	if errors.As(err, &iv) {
		return iv.Reason
	}
	return strings.ReplaceAll(err.Error(), "train: ", "")
}

// ledByLocked returns the made-up consist a train leads. The caller
// holds m.mu.
func (m *Manager) ledByLocked(tr *train) (Consist, bool) {
	c, i, ok := m.madeUpAtLocked(tr.ID, "")
	return c, ok && i == 0
}

// BreakUp takes a consist apart. The consist has to be stopped: a member
// of an LCC consist keeps the last speed the lead passed on, and the
// decoders of a DCC consist the last speed sent to the consist address.
func (m *Manager) BreakUp(ctx context.Context, id string) (ConsistState, error) {
	m.consistOp.Lock()
	defer m.consistOp.Unlock()
	m.mu.Lock()
	c, ok := m.consists[id]
	if !ok {
		m.mu.Unlock()
		return ConsistState{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if !c.MadeUp {
		state := m.consistStateLocked(c)
		m.mu.Unlock()
		return state, nil
	}
	trains := m.consistTrainsLocked(c)
	driven := trains[0]
	if c.Kind == ConsistDCC {
		driven = m.trainAtLocked(c.Address, false)
	}
	leadNode, online := driven.Node, driven.Online
	members := make([]openlcb.NodeID, 0, len(trains)-1)
	for _, tr := range trains[1:] {
		if tr.Node != 0 {
			members = append(members, tr.Node)
		}
	}
	m.mu.Unlock()
	stateNow := func() ConsistState {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.consistStateLocked(m.consists[id])
	}

	// The decoders run at the speed last sent to the consist address,
	// which only the command station knows after a restart.
	if moving, err := m.moving(ctx, driven, c.Kind == ConsistDCC); err != nil {
		return stateNow(), fmt.Errorf("check %s is stopped: %w", c.Name, err)
	} else if moving {
		return stateNow(), invalid("stop %s before breaking it up; its locomotives would keep running", c.Name)
	}
	if c.Kind == ConsistDCC {
		if err := m.breakUpDCC(ctx, c, trains); err != nil {
			m.mu.Lock()
			m.consistProblem[id] = problemText(err)
			m.mu.Unlock()
			state := stateNow()
			m.sendConsist(state)
			return state, err
		}
	} else if online && leadNode != 0 {
		var problems []error
		for _, n := range members {
			reply, err := m.opts.Bus.TractionCommand(ctx, leadNode, traction.DetachConsist(n), true)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			if result, err := traction.ParseConsistResult(reply); err != nil {
				problems = append(problems, err)
			} else if result.Code != 0 && result.Code != consistErrNotFound {
				problems = append(problems, fmt.Errorf("the command station would not let %s go (error 0x%04X)", n, uint16(result.Code)))
			}
		}
		if len(problems) > 0 {
			m.log.Warn("breaking up a consist", "consist", c.ID, "error", errors.Join(problems...))
		}
	}
	m.mu.Lock()
	c = m.consists[id]
	c.MadeUp = false
	m.consists[id] = c
	delete(m.consistNode, id)
	delete(m.consistProblem, id)
	state := m.consistStateLocked(c)
	m.mu.Unlock()
	err := m.opts.File.PutConsist(c)
	m.log.Info("broke up a consist", "consist", c.ID)
	m.sendConsist(state)
	for _, tr := range trains {
		m.publish(tr)
	}
	return state, err
}

// moving reports whether a train is moving, asking its node when it is on
// the bus, since another throttle may be driving it. With find, the node
// is found first if need be, and an error says it could not be.
func (m *Manager) moving(ctx context.Context, tr *train, find bool) (bool, error) {
	if find {
		tr.acquire.Lock()
		_, err := m.ensureNode(ctx, tr)
		tr.acquire.Unlock()
		if err != nil {
			return false, err
		}
	}
	m.mu.RLock()
	node, online := tr.Node, tr.Online
	moving := tr.Speed != 0 && !tr.EStop
	m.mu.RUnlock()
	if !online || node == 0 {
		return moving, nil
	}
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	reply, err := m.opts.Bus.TractionCommand(qctx, node, traction.QuerySpeeds(), true)
	cancel()
	if err == nil {
		if speeds, perr := traction.ParseSpeeds(reply); perr == nil {
			moving = speeds.Set.Value != 0 && !speeds.EStop
		}
	}
	return moving, nil
}

// follow updates the state Interlock keeps for the locomotives that
// follow a train: the members of the LCC consist it leads, as the lead
// passed a command on to them, or every locomotive of the DCC consist
// whose address it is, as their decoders took it there.
func (m *Manager) follow(tr *train, change func(member *train, mb Member)) {
	m.mu.Lock()
	var trains []*train
	var members []Member
	if c, ok := m.atConsistAddressLocked(tr); ok {
		trains, members = m.consistTrainsLocked(c), append([]Member(nil), c.Members...)
		// The lead's decoder takes every function the consist address
		// carries.
		members[0].F0, members[0].Functions = true, true
	} else if c, ok := m.ledByLocked(tr); ok && c.Kind == ConsistLCC && m.consistNode[c.ID] != 0 {
		trains, members = m.consistTrainsLocked(c)[1:], c.Members[1:]
	} else {
		m.mu.Unlock()
		return
	}
	for i, member := range trains {
		change(member, members[i])
		member.Updated = time.Now()
	}
	m.mu.Unlock()
	for _, member := range trains {
		m.publish(member)
	}
}

// dccTarget returns the ID of the address a command for a train goes to
// when the train leads a made-up DCC consist, or with members set runs in
// one at all.
func (m *Manager) dccTarget(tr *train, members bool) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, i, ok := m.madeUpAtLocked(tr.ID, "")
	if !ok || c.Kind != ConsistDCC || (i > 0 && !members) {
		return "", false
	}
	return IDFor(c.Address, false), true
}

// atConsistAddressLocked returns the made-up DCC consist whose address a
// train is. The caller holds m.mu.
func (m *Manager) atConsistAddressLocked(tr *train) (Consist, bool) {
	return m.dccConsistAtLocked(tr.Address, tr.Long)
}

// dccConsistAtLocked returns the made-up DCC consist at an address. The
// caller holds m.mu.
func (m *Manager) dccConsistAtLocked(address uint16, long bool) (Consist, bool) {
	if long || address > traction.ShortAddressMax {
		return Consist{}, false
	}
	for _, c := range m.consists {
		if c.MadeUp && c.Kind == ConsistDCC && c.Address == address {
			return c, true
		}
	}
	return Consist{}, false
}

// ConsistAt returns the made-up DCC consist at a consist address, which
// RailCom detectors report its decoders by.
func (m *Manager) ConsistAt(address uint16) (ConsistState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.dccConsistAtLocked(address, false)
	if !ok {
		return ConsistState{}, false
	}
	return m.consistStateLocked(c), true
}

// following returns an error when a train runs in a made-up consist
// other than as its lead: its speed comes from the lead.
func (m *Manager) following(tr *train) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, i, ok := m.madeUpAtLocked(tr.ID, "")
	if !ok || i == 0 {
		return nil
	}
	return invalid("%s runs in %s; drive %s instead", m.nameLocked(tr.Address, tr.Long), c.Name, m.roster[c.lead()].Name)
}

// forgetAttachedLocked notes that a train node left the bus: the consists it
// leads have to be put together again.
func (m *Manager) forgetAttachedLocked(tr *train) {
	for id, node := range m.consistNode {
		if node == tr.Node {
			delete(m.consistNode, id)
		}
	}
}

// renameMemberLocked follows a roster entry's new ID into the consists. The
// caller holds m.mu; it returns the consists to save.
func (m *Manager) renameMemberLocked(old, next string) []Consist {
	var changed []Consist
	for id, c := range m.consists {
		touched := false
		for i := range c.Members {
			if c.Members[i].Train == old {
				c.Members[i].Train = next
				touched = true
			}
		}
		if touched {
			m.consists[id] = c
			changed = append(changed, c.clone())
		}
	}
	return changed
}

// dropMemberLocked takes a roster entry out of the consists, which are
// apart. The caller holds m.mu; it returns the consists to save.
func (m *Manager) dropMemberLocked(train string) []Consist {
	var changed []Consist
	for id, c := range m.consists {
		kept := make([]Member, 0, len(c.Members))
		for _, mb := range c.Members {
			if mb.Train != train {
				kept = append(kept, mb)
			}
		}
		if len(kept) != len(c.Members) {
			c.Members = kept
			c = c.Normalize()
			m.consists[id] = c
			changed = append(changed, c.clone())
		}
	}
	return changed
}

// saveConsists writes changed consists and tells subscribers.
func (m *Manager) saveConsists(changed []Consist) error {
	var errs []error
	for _, c := range changed {
		if err := m.opts.File.PutConsist(c); err != nil {
			errs = append(errs, err)
		}
		m.mu.RLock()
		state := m.consistStateLocked(m.consists[c.ID])
		m.mu.RUnlock()
		m.sendConsist(state)
	}
	return errors.Join(errs...)
}

// SubscribeConsists registers a consist subscriber.
func (m *Manager) SubscribeConsists(sub ConsistSubscriber) (unsubscribe func()) {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.consistSubs[id] = sub
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.consistSubs, id)
		m.mu.Unlock()
	}
}

func (m *Manager) sendConsist(c ConsistState) {
	m.mu.RLock()
	subs := make([]ConsistSubscriber, 0, len(m.consistSubs))
	for _, sub := range m.consistSubs {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()
	for _, sub := range subs {
		sub(c)
	}
}
