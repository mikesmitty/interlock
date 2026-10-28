package trackplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/mikesmitty/interlock/internal/atomicfile"

	"gopkg.in/yaml.v3"
)

// FileName is the track plan file inside the data directory.
const FileName = "trackplan.yaml"

const fileHeader = `# The track plan, in millimeters (x rightward, y downward). Tracks run
# between points, straight or turning by an angle in degrees (positive
# clockwise); junctions are turnouts, wyes, crossings and crossovers,
# their ends listed in the order the documentation gives. A background
# image is kept in the backgrounds folder. The web interface edits this
# file; restart Interlock after editing it yourself.
`

// ErrConflict means the plan was saved by someone else since the revision
// a change was based on.
var ErrConflict = errors.New("the track plan was saved by someone else since it was opened")

// State is the plan as saved, with its revision and topology.
type State struct {
	Plan Plan `json:"plan"`
	// Revision identifies the saved contents; a save names the revision
	// it was based on.
	Revision string   `json:"revision"`
	Topology Topology `json:"topology"`
}

// File is trackplan.yaml. Callers must not change the plans it returns.
type File struct {
	path string

	mu    sync.Mutex
	state State
	subs  map[int]func(State)
	next  int
}

// OpenFile loads path, which need not exist yet. A plan that doesn't
// validate stops the load, so nothing is lost by a later save.
func OpenFile(path string) (*File, error) {
	f := &File{path: path, subs: map[int]func(State){}}
	var plan Plan
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", path, err)
	default:
		var converted bool
		if plan, converted, err = decode(data); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if converted {
			// The next save replaces the file, so keep the grid plan as
			// it was.
			old := filepath.Join(filepath.Dir(path), LegacyFileName)
			if _, err := os.Stat(old); errors.Is(err, os.ErrNotExist) {
				if err := atomicfile.Write(old, data, 0o644); err != nil {
					return nil, fmt.Errorf("keep the grid plan: %w", err)
				}
			}
			slog.Info("converted the track plan from the grid to millimeters", "cell_mm", LegacyCell, "kept", old)
		}
	}
	plan = plan.Normalize()
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	body, err := encode(plan)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	f.state = State{Plan: plan, Revision: revision(body), Topology: plan.Topology()}
	return f, nil
}

// Path returns the file's location.
func (f *File) Path() string { return f.path }

// Get returns the saved plan.
func (f *File) Get() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// Put saves plan in place of the one at revision base. It fails with
// ErrConflict when the saved plan is no longer at base, and with an
// *InvalidError when the plan doesn't validate.
func (f *File) Put(plan Plan, base string) (State, error) {
	plan = plan.Normalize()
	if err := plan.Validate(); err != nil {
		return State{}, err
	}
	keep := ""
	if plan.Background != nil {
		keep = plan.Background.Image
		if _, err := f.BackgroundFile(keep); err != nil {
			return State{}, invalid("the background image isn't on the server any more; choose it again")
		}
	}
	body, err := encode(plan)
	if err != nil {
		return State{}, fmt.Errorf("encode %s: %w", f.path, err)
	}

	f.mu.Lock()
	if base != f.state.Revision {
		f.mu.Unlock()
		return State{}, ErrConflict
	}
	if err := atomicfile.Write(f.path, append([]byte(fileHeader), body...), 0o644); err != nil {
		f.mu.Unlock()
		return State{}, fmt.Errorf("write %s: %w", f.path, err)
	}
	f.state = State{Plan: plan, Revision: revision(body), Topology: plan.Topology()}
	state := f.state
	subs := make([]func(State), 0, len(f.subs))
	for _, fn := range f.subs {
		subs = append(subs, fn)
	}
	f.mu.Unlock()

	if err := f.pruneBackgrounds(keep); err != nil {
		slog.Warn("could not delete background images no longer used", "error", err)
	}
	for _, fn := range subs {
		fn(state)
	}
	return state, nil
}

// Subscribe calls fn with every saved plan.
func (f *File) Subscribe(fn func(State)) (unsubscribe func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.next
	f.next++
	f.subs[id] = fn
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subs, id)
	}
}

// LegacyFileName is where a plan drawn on the old grid of cells is kept
// when it is converted.
const LegacyFileName = "trackplan-grid.yaml"

// decode reads a saved plan, converting one drawn on the old grid of
// cells.
func decode(data []byte) (plan Plan, converted bool, err error) {
	var head struct {
		Tiles yaml.Node `yaml:"tiles"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return Plan{}, false, err
	}
	strict := func(v any) error {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
	if head.Tiles.Kind != 0 {
		var old legacyPlan
		if err := strict(&old); err != nil {
			return Plan{}, false, err
		}
		return old.convert(LegacyCell), true, nil
	}
	err = strict(&plan)
	return plan, false, err
}

func encode(plan Plan) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(plan); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func revision(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}
