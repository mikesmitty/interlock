package train

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

// CVDirName is the folder in the data directory that decoder CV values
// are kept in, one file per decoder.
const CVDirName = "cvs"

// cvSaveDelay gathers the CVs of a page being read into one save.
const cvSaveDelay = time.Second

// SessionIdle ends a decoder's session after this long without a read,
// a write or a change on its settings page.
const SessionIdle = 2 * time.Hour

const cvHeader = `# CV values of one decoder, as Interlock last read them from it or wrote
# them to it; values imported from another roster that the decoder has not
# been read for yet; changes made on its settings page that are not
# written yet; and decoder profiles, named copies of its values to load
# again.
# Interlock rewrites this file; trains.yaml says which locomotive the
# decoder belongs to.
`

type cvDoc struct {
	Updated  time.Time           `yaml:"updated"`
	CVs      map[string]int      `yaml:"cvs"`
	Imported map[string]int      `yaml:"imported,omitempty"`
	Changes  map[string]CVChange `yaml:"changes,omitempty"`
	Profiles []DecoderProfile    `yaml:"profiles,omitempty"`
}

// DecoderProfile is a copy of a decoder's values kept under a name, such
// as how it came or how it ran best, to load again as changes to write.
type DecoderProfile struct {
	Name string         `yaml:"name" json:"name"`
	Kept time.Time      `yaml:"kept" json:"kept"`
	CVs  map[string]int `yaml:"cvs" json:"cvs"`
}

// maxProfileName bounds a decoder profile's name.
const maxProfileName = 64

// CVValues are a decoder's saved CV values by the names its definition
// uses for them ("29", "16.0.259"), and the changes not written to it
// yet. CVs were read from the decoder or written to it; Imported came
// from another roster and wait for a read to confirm them.
type CVValues struct {
	CVs      map[string]int      `json:"cvs"`
	Imported map[string]int      `json:"imported,omitempty"`
	Updated  time.Time           `json:"updated,omitzero"`
	Changes  map[string]CVChange `json:"changes,omitempty"`
	// Session is the work on the decoder going on now, if any.
	Session *CVSession `json:"session,omitempty"`
	// Profiles are the decoder profiles kept for it, oldest first.
	Profiles []DecoderProfile `json:"profiles,omitempty"`
}

// CVSession is a spell of work on one decoder: the CVs read from it and
// written to it since it began, which can be trusted to hold those
// values more than ones saved earlier. Work on the programming track
// begins with a check that the decoder there is this one (Check says how
// it went). On the main the address picks the decoder, so reads and
// writes there begin a session unchecked. It ends after
// SessionIdle without work, when a decoder is identified on the
// programming track (the locomotive there may have been swapped), when
// another decoder is checked there, when it is ended by hand, or when
// Interlock stops. A CV read or written last is listed only there.
type CVSession struct {
	Started time.Time `json:"started"`
	Last    time.Time `json:"last"`
	Check   string    `json:"check,omitempty"`
	Read    []string  `json:"read"`
	Written []string  `json:"written"`
}

// How a session's decoder was checked on the programming track.
const (
	// CheckMatched means the decoder there was checked and is this one.
	CheckMatched = "matched"
	// CheckAccepted means it did not match and work went on anyway.
	CheckAccepted = "accepted"
)

type cvSession struct {
	started, last time.Time
	check         string
	// written is true for a CV written last, false for one read last.
	cvs map[string]bool
}

// CVChange is a change to a CV that is not written yet: the bits it
// sets and their new values. The CV's other bits are whatever the
// decoder holds.
type CVChange struct {
	Value int `yaml:"value" json:"value"`
	Bits  int `yaml:"bits" json:"bits"`
}

// heldBy reports whether a CV value already has the change.
func (c CVChange) heldBy(value int) bool { return value&c.Bits == c.Value&c.Bits }

// MarshalYAML writes a change on one line.
func (c CVChange) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	for _, kv := range [][2]string{{"value", strconv.Itoa(c.Value)}, {"bits", strconv.Itoa(c.Bits)}} {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[0]}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: kv[1]})
	}
	return n, nil
}

// CVStore keeps decoders' CV values in a folder, a file per decoder named
// by its ID. Changes are saved a moment after they stop coming, so a page
// of reads is one write; Close saves what is left. A nil *CVStore keeps
// nothing.
type CVStore struct {
	dir string
	log *slog.Logger

	mu       sync.Mutex
	docs     map[string]*cvDoc
	dirty    map[string]bool
	timer    *time.Timer
	sessions map[string]*cvSession
	now      func() time.Time
}

// OpenCVStore keeps values in dir, which is made when the first value is
// saved.
func OpenCVStore(dir string, log *slog.Logger) *CVStore {
	if log == nil {
		log = slog.Default()
	}
	return &CVStore{dir: dir, log: log, docs: make(map[string]*cvDoc), dirty: make(map[string]bool), sessions: make(map[string]*cvSession), now: time.Now}
}

func (s *CVStore) path(decoder string) string { return filepath.Join(s.dir, decoder+".yaml") }

// loadLocked reads a decoder's file the first time it is needed.
func (s *CVStore) loadLocked(decoder string) (*cvDoc, error) {
	if d, ok := s.docs[decoder]; ok {
		return d, nil
	}
	d := &cvDoc{CVs: make(map[string]int)}
	data, err := os.ReadFile(s.path(decoder))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read CV values: %w", err)
	default:
		if err := yaml.Unmarshal(data, d); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path(decoder), err)
		}
		if d.CVs == nil {
			d.CVs = make(map[string]int)
		}
	}
	s.docs[decoder] = d
	return d, nil
}

// Get returns a decoder's values; none is not an error.
func (s *CVStore) Get(decoder string) (CVValues, error) {
	if s == nil {
		return CVValues{CVs: map[string]int{}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadLocked(decoder)
	if err != nil {
		return CVValues{}, err
	}
	v := CVValues{CVs: maps.Clone(d.CVs), Imported: maps.Clone(d.Imported), Updated: d.Updated, Changes: maps.Clone(d.Changes)}
	for _, st := range d.Profiles {
		v.Profiles = append(v.Profiles, DecoderProfile{Name: st.Name, Kept: st.Kept, CVs: maps.Clone(st.CVs)})
	}
	if ss := s.sessionLocked(decoder, false); ss != nil {
		v.Session = &CVSession{Started: ss.started, Last: ss.last, Check: ss.check, Read: []string{}, Written: []string{}}
		for _, cv := range slices.Sorted(maps.Keys(ss.cvs)) {
			if ss.cvs[cv] {
				v.Session.Written = append(v.Session.Written, cv)
			} else {
				v.Session.Read = append(v.Session.Read, cv)
			}
		}
	}
	return v, nil
}

// sessionLocked returns the decoder's session, ending it if it has been
// idle too long; with start it begins one if there is none, and either
// way marks it busy now.
func (s *CVStore) sessionLocked(decoder string, start bool) *cvSession {
	now := s.now()
	ss := s.sessions[decoder]
	if ss != nil && now.Sub(ss.last) > SessionIdle {
		delete(s.sessions, decoder)
		ss = nil
	}
	if ss == nil && start {
		ss = &cvSession{started: now, cvs: make(map[string]bool)}
		s.sessions[decoder] = ss
	}
	if ss != nil && start {
		ss.last = now
	}
	return ss
}

// Programmed records a value read from the decoder or written to it in
// the session, which begins if there is none, and saves it.
func (s *CVStore) Programmed(decoder, cv string, value int, written bool) error {
	if s == nil {
		return nil
	}
	if err := s.Set(decoder, map[string]int{cv: value}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionLocked(decoder, true).cvs[cv] = written
	return nil
}

// StartSession begins the decoder's session with a check of it on the
// programming track, or records the check in the session going on. The
// other decoders' sessions on the programming track end, since only one
// decoder can be there; a matched check is not undone by an accepted one.
func (s *CVStore) StartSession(decoder, check string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for other, ss := range s.sessions {
		if other != decoder && ss.check != "" {
			delete(s.sessions, other)
		}
	}
	ss := s.sessionLocked(decoder, true)
	if ss.check != CheckMatched {
		ss.check = check
	}
}

// Checked reports whether the decoder's session began with a check of it
// on the programming track.
func (s *CVStore) Checked(decoder string) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessionLocked(decoder, false)
	return ss != nil && ss.check != ""
}

// EndSession ends the decoder's session; its values stay saved.
func (s *CVStore) EndSession(decoder string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, decoder)
}

// EndSessions ends every decoder's session.
func (s *CVStore) EndSessions() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.sessions)
}

// Set records values read from or written to a decoder, keeping the
// others it has.
func (s *CVStore) Set(decoder string, values map[string]int) error {
	return s.change(decoder, values, false)
}

// Replace makes values, read from or written to the decoder, all it has.
func (s *CVStore) Replace(decoder string, values map[string]int) error {
	return s.change(decoder, values, true)
}

func (s *CVStore) change(decoder string, values map[string]int, replace bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadLocked(decoder)
	if err != nil {
		return err
	}
	if replace {
		d.CVs = make(map[string]int, len(values))
		d.Imported = nil
	}
	maps.Copy(d.CVs, values)
	for cv, v := range values {
		// The decoder has confirmed or replaced an imported value.
		delete(d.Imported, cv)
		// A change the decoder now holds is no longer waiting to be written.
		if c, ok := d.Changes[cv]; ok && c.heldBy(v) {
			delete(d.Changes, cv)
		}
	}
	if len(d.Imported) == 0 {
		d.Imported = nil
	}
	d.Updated = time.Now().UTC().Truncate(time.Second)
	s.markLocked(decoder)
	return nil
}

// Import makes values from another roster all the decoder has, as of
// when that roster saved them. They count as not read until the decoder
// is.
func (s *CVStore) Import(decoder string, values map[string]int, at time.Time) error {
	if s == nil {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadLocked(decoder)
	if err != nil {
		return err
	}
	delete(s.sessions, decoder)
	d.CVs = make(map[string]int)
	d.Imported = maps.Clone(values)
	if len(d.Imported) == 0 {
		d.Imported = nil
	}
	d.Updated = at.UTC().Truncate(time.Second)
	s.markLocked(decoder)
	return nil
}

// SetChanges makes changes the decoder's changes not written yet; none
// clears them.
func (s *CVStore) SetChanges(decoder string, changes map[string]CVChange) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadLocked(decoder)
	if err != nil {
		return err
	}
	// Changing settings is work on the decoder: it keeps a session going.
	if ss := s.sessionLocked(decoder, false); ss != nil {
		ss.last = s.now()
	}
	if maps.Equal(d.Changes, changes) || (len(d.Changes) == 0 && len(changes) == 0) {
		return nil
	}
	d.Changes = maps.Clone(changes)
	if len(d.Changes) == 0 {
		d.Changes = nil
	}
	s.markLocked(decoder)
	return nil
}

// KeepProfile keeps a copy of the decoder's values under a name: those
// read from it or written to it, and imported ones not read since.
// Changes not written yet are left out. A profile by the same name is
// replaced only with replace.
func (s *CVStore) KeepProfile(decoder, name string, replace bool) (DecoderProfile, error) {
	if s == nil {
		return DecoderProfile{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadLocked(decoder)
	if err != nil {
		return DecoderProfile{}, err
	}
	st := DecoderProfile{Name: name, Kept: time.Now().UTC().Truncate(time.Second), CVs: maps.Clone(d.Imported)}
	if st.CVs == nil {
		st.CVs = make(map[string]int, len(d.CVs))
	}
	maps.Copy(st.CVs, d.CVs)
	if len(st.CVs) == 0 {
		return DecoderProfile{}, invalid("there are no values to keep yet; read the decoder first")
	}
	i := slices.IndexFunc(d.Profiles, func(x DecoderProfile) bool { return x.Name == name })
	switch {
	case i >= 0 && !replace:
		return DecoderProfile{}, fmt.Errorf("%w: %s", ErrProfileExists, name)
	case i >= 0:
		d.Profiles[i] = st
	default:
		d.Profiles = append(d.Profiles, st)
	}
	s.markLocked(decoder)
	return st, nil
}

// DeleteProfile forgets one of the decoder's profiles.
func (s *CVStore) DeleteProfile(decoder, name string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.loadLocked(decoder)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(d.Profiles, func(x DecoderProfile) bool { return x.Name == name })
	if i < 0 {
		return fmt.Errorf("%w: no decoder profile %s", ErrNotFound, name)
	}
	d.Profiles = slices.Delete(d.Profiles, i, i+1)
	s.markLocked(decoder)
	return nil
}

// markLocked saves a decoder's file a moment from now.
func (s *CVStore) markLocked(decoder string) {
	s.dirty[decoder] = true
	if s.timer == nil {
		s.timer = time.AfterFunc(cvSaveDelay, func() {
			if err := s.Flush(); err != nil {
				s.log.Warn("saving CV values failed", "error", err)
			}
		})
	}
}

// Delete forgets a decoder's values and removes its file.
func (s *CVStore) Delete(decoder string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, decoder)
	delete(s.dirty, decoder)
	delete(s.sessions, decoder)
	if err := os.Remove(s.path(decoder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove CV values: %w", err)
	}
	return nil
}

// Flush saves every changed decoder now.
func (s *CVStore) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	var errs []error
	for decoder := range s.dirty {
		if err := s.saveLocked(decoder); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(s.dirty, decoder)
	}
	return errors.Join(errs...)
}

// Close saves what is left.
func (s *CVStore) Close() error { return s.Flush() }

func (s *CVStore) saveLocked(decoder string) error {
	d := s.docs[decoder]
	if d == nil {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString(cvHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return fmt.Errorf("encode CV values: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode CV values: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("save CV values: %w", err)
	}
	if err := atomicfile.Write(s.path(decoder), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("save CV values: %w", err)
	}
	return nil
}
