package train

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// AddDecoder gives a locomotive another decoder, with an ID made from its
// name when it has none. Installing it makes it the fitted one.
func (m *Manager) AddDecoder(ctx context.Context, id string, d Decoder, install bool) (Train, Decoder, error) {
	var added Decoder
	tr, err := m.edit(ctx, id, func(e *Entry) error {
		if d.ID == "" {
			// edit holds m.mu while it changes the entry.
			d.ID = m.newDecoderIDLocked(d.Title(), e.Decoders)
		}
		if _, ok := e.Decoder(d.ID); ok {
			return invalid("%s already has a decoder with the ID %s", e.Name, d.ID)
		}
		e.Decoders = append(e.Decoders, d)
		if install || len(e.Decoders) == 1 && e.Installed == "" {
			// The locomotive's own address and labels are a newly fitted
			// decoder's, unless it brings its own.
			swap(e, d.ID)
		}
		added, _ = e.Decoder(d.ID)
		return nil
	})
	return tr, added, err
}

// UpdateDecoder changes one of a locomotive's decoders: its name,
// definition and identity. change edits a copy.
func (m *Manager) UpdateDecoder(ctx context.Context, id, decoder string, change func(*Decoder)) (Train, error) {
	return m.edit(ctx, id, func(e *Entry) error {
		for i := range e.Decoders {
			old := e.Decoders[i]
			if old.ID != decoder {
				continue
			}
			d := old
			change(&d)
			d.ID = decoder
			d.Address, d.Long, d.Functions, d.Momentary = old.Address, old.Long, old.Functions, old.Momentary
			e.Decoders[i] = d
			return nil
		}
		return fmt.Errorf("%w: %s has no decoder %s", ErrNotFound, e.Name, decoder)
	})
}

// RemoveDecoder takes a decoder off a locomotive and forgets its CV
// values. The locomotive keeps its address and labels.
func (m *Manager) RemoveDecoder(ctx context.Context, id, decoder string) (Train, error) {
	tr, err := m.edit(ctx, id, func(e *Entry) error {
		kept := e.Decoders[:0]
		found := false
		for _, d := range e.Decoders {
			if d.ID == decoder {
				found = true
				continue
			}
			kept = append(kept, d)
		}
		if !found {
			return fmt.Errorf("%w: %s has no decoder %s", ErrNotFound, e.Name, decoder)
		}
		e.Decoders = kept
		if e.Installed == decoder {
			e.Installed = ""
		}
		return nil
	})
	if err != nil {
		return tr, err
	}
	if err := m.opts.CVs.Delete(decoder); err != nil {
		m.log.Warn("removing CV values failed", "decoder", decoder, "error", err)
	}
	return tr, nil
}

// Install makes one of a locomotive's decoders the fitted one, or none
// when decoder is empty. The decoder taken out keeps the locomotive's
// address and labels; the one put in brings back its own.
func (m *Manager) Install(ctx context.Context, id, decoder string) (Train, error) {
	return m.edit(ctx, id, func(e *Entry) error {
		if decoder != "" {
			if _, ok := e.Decoder(decoder); !ok {
				return fmt.Errorf("%w: %s has no decoder %s", ErrNotFound, e.Name, decoder)
			}
		}
		swap(e, decoder)
		return nil
	})
}

// swap fits decoder in e, moving the address and labels between the
// locomotive and the decoders.
func swap(e *Entry, decoder string) {
	if e.Installed == decoder {
		return
	}
	for i := range e.Decoders {
		if d := &e.Decoders[i]; d.ID == e.Installed {
			d.Address, d.Long = e.Address, e.Long
			d.Functions, d.Momentary = cloneLabels(e.Functions), append([]int(nil), e.Momentary...)
		}
	}
	for i := range e.Decoders {
		if d := &e.Decoders[i]; d.ID == decoder {
			if d.Address != 0 {
				e.Address, e.Long = d.Address, d.Long
			}
			if d.Functions != nil || d.Momentary != nil {
				e.Functions, e.Momentary = d.Functions, d.Momentary
			}
			d.Address, d.Long, d.Functions, d.Momentary = 0, false, nil, nil
		}
	}
	e.Installed = decoder
}

// hasDecoder checks that a locomotive has a decoder.
func (m *Manager) hasDecoder(id, decoder string) error {
	e, ok := m.Entry(id)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if _, ok := e.Decoder(decoder); !ok {
		return fmt.Errorf("%w: %s has no decoder %s", ErrNotFound, e.Name, decoder)
	}
	return nil
}

// CVs returns the CV values saved for one of a locomotive's decoders.
func (m *Manager) CVs(id, decoder string) (CVValues, error) {
	if err := m.hasDecoder(id, decoder); err != nil {
		return CVValues{}, err
	}
	return m.opts.CVs.Get(decoder)
}

// SetCVs saves CV values for one of a locomotive's decoders, keeping the
// others it has unless replace is set.
func (m *Manager) SetCVs(id, decoder string, values map[string]int, replace bool) error {
	if err := m.checkCVs(id, decoder, values); err != nil {
		return err
	}
	if replace {
		return m.opts.CVs.Replace(decoder, values)
	}
	return m.opts.CVs.Set(decoder, values)
}

// Programmed saves a value read from one of a locomotive's decoders or
// written to it, as part of the decoder's session.
func (m *Manager) Programmed(id, decoder, cv string, value int, written bool) error {
	if err := m.checkCVs(id, decoder, map[string]int{cv: value}); err != nil {
		return err
	}
	return m.opts.CVs.Programmed(decoder, cv, value, written)
}

// StartSession begins the session on one of a locomotive's decoders with
// a check of it on the programming track (CheckMatched or
// CheckAccepted), ending the sessions there of the others.
func (m *Manager) StartSession(id, decoder, check string) error {
	if err := m.hasDecoder(id, decoder); err != nil {
		return err
	}
	if check != CheckMatched && check != CheckAccepted {
		return invalid("a session's check is %q or %q", CheckMatched, CheckAccepted)
	}
	m.opts.CVs.StartSession(decoder, check)
	return nil
}

// SessionChecked reports whether the session on a decoder began with a
// check of it on the programming track, which work there needs first.
func (m *Manager) SessionChecked(decoder string) bool { return m.opts.CVs.Checked(decoder) }

// KeepProfile keeps a copy of one of a locomotive's decoders' values as a
// decoder profile, replacing one by that name only with replace.
func (m *Manager) KeepProfile(id, decoder, name string, replace bool) (DecoderProfile, error) {
	if err := m.hasDecoder(id, decoder); err != nil {
		return DecoderProfile{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxProfileName {
		return DecoderProfile{}, invalid("a decoder profile needs a name of up to %d characters", maxProfileName)
	}
	return m.opts.CVs.KeepProfile(decoder, name, replace)
}

// DeleteProfile forgets one of the decoder profiles kept for a
// locomotive's decoder.
func (m *Manager) DeleteProfile(id, decoder, name string) error {
	if err := m.hasDecoder(id, decoder); err != nil {
		return err
	}
	return m.opts.CVs.DeleteProfile(decoder, name)
}

// EndSession ends the session on one of a locomotive's decoders.
func (m *Manager) EndSession(id, decoder string) error {
	if err := m.hasDecoder(id, decoder); err != nil {
		return err
	}
	m.opts.CVs.EndSession(decoder)
	return nil
}

// EndSessions ends the session on every decoder, as when another
// locomotive may be on the programming track.
func (m *Manager) EndSessions() { m.opts.CVs.EndSessions() }

// SetChanges keeps the changes made to one of a locomotive's decoders
// that are not written to it yet, replacing those kept before. Only the
// bits each change sets are kept.
func (m *Manager) SetChanges(id, decoder string, changes map[string]CVChange) error {
	values := make(map[string]int, len(changes))
	kept := make(map[string]CVChange, len(changes))
	for cv, c := range changes {
		if c.Bits <= 0 || c.Bits > 0xFFFF {
			return invalid("the change to CV %q sets no bits", cv)
		}
		values[cv] = c.Value
		kept[cv] = CVChange{Value: c.Value & c.Bits, Bits: c.Bits}
	}
	if err := m.checkCVs(id, decoder, values); err != nil {
		return err
	}
	return m.opts.CVs.SetChanges(decoder, kept)
}

func (m *Manager) checkCVs(id, decoder string, values map[string]int) error {
	if err := m.hasDecoder(id, decoder); err != nil {
		return err
	}
	for cv, v := range values {
		if cv == "" || len(cv) > 32 || v < 0 || v > 0xFFFF {
			return invalid("CV %q cannot hold %d", cv, v)
		}
	}
	return nil
}

// DecoderMatch is a roster decoder that fits one on the programming
// track.
type DecoderMatch struct {
	Train string `json:"train"`
	// Decoder is empty for a locomotive at the address with no decoders.
	Decoder string `json:"decoder,omitempty"`
	// Installed is whether the decoder is the locomotive's fitted one.
	Installed bool `json:"installed"`
	// Identity is set when the decoder's saved identity agrees, not just
	// its address.
	Identity bool `json:"identity"`
	// Checked is set when Identify checked the decoder and began its
	// session.
	Checked bool `json:"checked,omitempty"`
}

// FindDecoder lists the roster decoders that could be the one identified
// on the programming track, best first: those at its address whose
// identity agrees, those at its address with no identity saved, and, when
// none is at the address, those with the same identity (whose address
// may have been changed since).
func (m *Manager) FindDecoder(id Identity, address uint16, long bool) []DecoderMatch {
	long = long || address > 127
	m.mu.RLock()
	defer m.mu.RUnlock()
	type ranked struct {
		DecoderMatch
		name string
		rank int
	}
	var atAddress, sameIdentity []ranked
	for _, e := range m.roster {
		if len(e.Decoders) == 0 && address != 0 && e.Address == address && e.Long == long {
			atAddress = append(atAddress, ranked{DecoderMatch{Train: e.ID}, e.Name, 2})
		}
		for _, d := range e.Decoders {
			a, l := d.Address, d.Long
			if d.ID == e.Installed || a == 0 {
				a, l = e.Address, e.Long
			}
			agrees, known := identityAgrees(d.Identity, id)
			match := ranked{DecoderMatch{Train: e.ID, Decoder: d.ID, Installed: d.ID == e.Installed, Identity: known && agrees}, e.Name, 1}
			switch {
			case !agrees:
			case address != 0 && a == address && l == long:
				if known {
					match.rank = 0
				}
				atAddress = append(atAddress, match)
			case known:
				sameIdentity = append(sameIdentity, match)
			}
		}
	}
	out := atAddress
	if len(out) == 0 {
		out = sameIdentity
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		if out[i].Installed != out[j].Installed {
			return out[i].Installed
		}
		return out[i].name < out[j].name
	})
	matches := make([]DecoderMatch, len(out))
	for i, r := range out {
		matches[i] = r.DecoderMatch
	}
	return matches
}

// identityAgrees compares a saved identity with one just read: known is
// false when nothing was saved, and then it agrees. The version is left
// out, since a firmware update changes it.
func identityAgrees(saved *Identity, read Identity) (agrees, known bool) {
	if saved == nil {
		return true, false
	}
	if saved.Manufacturer != read.Manufacturer {
		return false, true
	}
	if saved.ProductID != nil && read.ProductID != nil && *saved.ProductID != *read.ProductID {
		return false, true
	}
	return true, true
}
