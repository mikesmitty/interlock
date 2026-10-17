package automation

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"

	"github.com/mikesmitty/interlock/internal/atomicfile"

	"github.com/mikesmitty/interlock/internal/yamlstrict"

	"gopkg.in/yaml.v3"
)

// FileName is the automations file inside the data directory.
const FileName = "automations.yaml"

type fileDoc struct {
	Automations []Automation `yaml:"automations"`
}

const fileHeader = `# Automations. The web interface edits this file; restart Interlock after
# editing it yourself. Each automation has triggers (when), conditions
# (only if) and actions (then); see docs/automations.md for the fields.
`

// File is automations.yaml. A nil *File keeps automations in memory only.
type File struct {
	path string
	mu   sync.Mutex
	doc  fileDoc
}

// OpenFile loads path, which need not exist yet. Every automation in it
// must be valid; a broken one stops the load so nothing is lost by a
// later save.
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
		return f, nil // an empty file
	}
	if err := yamlstrict.Check(&root, reflect.TypeOf(fileDoc{})); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := root.Decode(&f.doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := make(map[string]bool, len(f.doc.Automations))
	for i := range f.doc.Automations {
		a := f.doc.Automations[i].Normalize()
		if err := a.Validate(); err != nil {
			return nil, fmt.Errorf("%s: automation %d (%s): %w", path, i+1, a.Name, err)
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("%s: two automations have the ID %q", path, a.ID)
		}
		seen[a.ID] = true
		f.doc.Automations[i] = a
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

// List returns the automations in file order.
func (f *File) List() []Automation {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Automation, len(f.doc.Automations))
	copy(out, f.doc.Automations)
	return out
}

// Put adds or replaces an automation by ID and saves the file.
func (f *File) Put(a Automation) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	replaced := false
	for i := range f.doc.Automations {
		if f.doc.Automations[i].ID == a.ID {
			f.doc.Automations[i] = a
			replaced = true
			break
		}
	}
	if !replaced {
		f.doc.Automations = append(f.doc.Automations, a)
	}
	return f.save()
}

// Remove deletes an automation and saves the file.
func (f *File) Remove(id string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.doc.Automations[:0]
	for _, a := range f.doc.Automations {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	f.doc.Automations = kept
	return f.save()
}

// save writes the file atomically; the caller holds f.mu.
func (f *File) save() error {
	body, err := MarshalYAML(f.doc)
	if err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := atomicfile.Write(f.path, append([]byte(fileHeader), body...), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	return nil
}

// MarshalYAML encodes a document the way the file is written: two space
// indentation, no document marker.
func MarshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
