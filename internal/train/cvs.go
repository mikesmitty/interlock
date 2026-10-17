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

const cvHeader = `# CV values of one decoder, as Interlock last read them from it or wrote
# them to it, and changes made on its settings page that are not written
# yet. Interlock rewrites this file; trains.yaml says which locomotive the
# decoder belongs to.
`

type cvDoc struct {
	Updated time.Time           `yaml:"updated"`
	CVs     map[string]int      `yaml:"cvs"`
	Changes map[string]CVChange `yaml:"changes,omitempty"`
}

// CVValues are a decoder's saved CV values by the names its definition
// uses for them ("29", "16.0.259"), and the changes not written to it
// yet.
type CVValues struct {
	CVs     map[string]int      `json:"cvs"`
	Updated time.Time           `json:"updated,omitzero"`
	Changes map[string]CVChange `json:"changes,omitempty"`
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

	mu    sync.Mutex
	docs  map[string]*cvDoc
	dirty map[string]bool
	timer *time.Timer
}

// OpenCVStore keeps values in dir, which is made when the first value is
// saved.
func OpenCVStore(dir string, log *slog.Logger) *CVStore {
	if log == nil {
		log = slog.Default()
	}
	return &CVStore{dir: dir, log: log, docs: make(map[string]*cvDoc), dirty: make(map[string]bool)}
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
	return CVValues{CVs: maps.Clone(d.CVs), Updated: d.Updated, Changes: maps.Clone(d.Changes)}, nil
}

// Set records values for a decoder, keeping the others it has.
func (s *CVStore) Set(decoder string, values map[string]int) error {
	return s.change(decoder, values, false, time.Now())
}

// Replace makes values all the decoder has.
func (s *CVStore) Replace(decoder string, values map[string]int) error {
	return s.change(decoder, values, true, time.Now())
}

// ReplaceAt makes values all the decoder has, as of when they were last
// read or written: an imported roster's date, say.
func (s *CVStore) ReplaceAt(decoder string, values map[string]int, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	return s.change(decoder, values, true, at)
}

func (s *CVStore) change(decoder string, values map[string]int, replace bool, at time.Time) error {
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
	}
	maps.Copy(d.CVs, values)
	// A change the decoder now holds is no longer waiting to be written.
	for cv, v := range values {
		if c, ok := d.Changes[cv]; ok && c.heldBy(v) {
			delete(d.Changes, cv)
		}
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
