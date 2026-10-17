package train

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"

	"github.com/mikesmitty/interlock/internal/atomicfile"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/internal/yamlstrict"
)

// FileName is the roster file inside the data directory.
const FileName = "trains.yaml"

type fileDoc struct {
	Trains []Entry `yaml:"trains"`
}

const fileHeader = `# The locomotive roster. The web interface edits this file; restart
# Interlock after editing it yourself. Each train has a DCC address, a name,
# optional function labels and the decoders it has; see docs/trains.md.
# Decoder CV values are kept in the cvs folder beside this file.
`

// File is trains.yaml. A nil *File keeps the roster in memory only.
type File struct {
	path string
	mu   sync.Mutex
	doc  fileDoc
}

// OpenFile loads path, which need not exist yet. Every entry must be
// valid; a broken one stops the load so nothing is lost by a later save.
func OpenFile(path string) (*File, error) {
	f := &File{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if root.Kind == 0 {
		return f, nil
	}
	if err := yamlstrict.Check(&root, reflect.TypeOf(fileDoc{})); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := root.Decode(&f.doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := make(map[string]bool, len(f.doc.Trains))
	decoders := make(map[string]string)
	for i := range f.doc.Trains {
		e := f.doc.Trains[i].Normalize()
		if e.ID == "" {
			// Entries written before locomotives had IDs of their own
			// are known by their address.
			e.ID = e.key()
		}
		if err := e.Validate(); err != nil {
			return nil, fmt.Errorf("%s: train %d (%s): %w", path, i+1, e.Name, err)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("%s: two trains have the ID %s", path, e.ID)
		}
		seen[e.ID] = true
		for _, d := range e.Decoders {
			if other, ok := decoders[d.ID]; ok {
				return nil, fmt.Errorf("%s: %s and %s both have a decoder with the ID %s", path, other, e.Name, d.ID)
			}
			decoders[d.ID] = e.Name
		}
		f.doc.Trains[i] = e
	}
	return f, nil
}

// Path returns the file's location.
func (f *File) Path() string {
	if f == nil {
		return ""
	}
	return f.path
}

// List returns the entries in file order.
func (f *File) List() []Entry {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Entry, len(f.doc.Trains))
	copy(out, f.doc.Trains)
	return out
}

// Put adds or replaces an entry by ID and saves the file.
func (f *File) Put(e Entry) error { return f.Replace(e.ID, e) }

// Replace puts e where the entry with ID old was, or at the end, and
// saves the file; e may have a new ID.
func (f *File) Replace(old string, e Entry) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	replaced := false
	for i := range f.doc.Trains {
		if f.doc.Trains[i].ID == old {
			f.doc.Trains[i] = e
			replaced = true
			break
		}
	}
	if !replaced {
		f.doc.Trains = append(f.doc.Trains, e)
	}
	return f.save()
}

// Remove deletes an entry and saves the file.
func (f *File) Remove(id string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.doc.Trains[:0]
	for _, e := range f.doc.Trains {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	f.doc.Trains = kept
	return f.save()
}

// save writes the file atomically; the caller holds f.mu.
func (f *File) save() error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f.doc); err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := atomicfile.Write(f.path, append([]byte(fileHeader), buf.Bytes()...), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	return nil
}
