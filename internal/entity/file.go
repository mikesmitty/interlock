package entity

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"

	"github.com/mikesmitty/interlock/internal/atomicfile"

	"gopkg.in/yaml.v3"
)

// FileName is the entities file inside the data directory.
const FileName = "entities.yaml"

// Override is what the user changed about an entity a profile built.
type Override struct {
	Name      string `yaml:"name,omitempty"`
	Hidden    bool   `yaml:"hidden,omitempty"`
	Confirmed bool   `yaml:"confirmed,omitempty"`
}

func (o Override) empty() bool { return o == Override{} }

// fileDoc is the layout of entities.yaml.
type fileDoc struct {
	Overrides map[string]Override `yaml:"overrides,omitempty"`
	Manual    []Manual            `yaml:"manual,omitempty"`
}

const fileHeader = `# Entities. Interlock builds entities from node profiles; this file keeps
# what you changed about them (a name, hidden, a confirmed guess) under
# "overrides", and the entities you built by hand under "manual". The web
# interface edits this file; restart Interlock after editing it yourself.
`

// File is entities.yaml: overrides for built entities and manual ones.
// A nil *File keeps nothing.
type File struct {
	path string
	mu   sync.Mutex
	doc  fileDoc
}

// OpenFile loads path, which need not exist yet.
func OpenFile(path string) (*File, error) {
	f := &File{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &f.doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
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

// Override returns what is stored for an entity.
func (f *File) Override(id string) Override {
	if f == nil {
		return Override{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.doc.Overrides[id]
}

// Overrides returns every stored override by entity ID.
func (f *File) Overrides() map[string]Override {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.doc.Overrides)
}

// SetOverride stores an entity's override and saves the file. An empty
// override removes the entry.
func (f *File) SetOverride(id string, o Override) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if o.empty() {
		delete(f.doc.Overrides, id)
	} else {
		if f.doc.Overrides == nil {
			f.doc.Overrides = make(map[string]Override)
		}
		f.doc.Overrides[id] = o
	}
	return f.save()
}

// Manuals returns the entities built by hand.
func (f *File) Manuals() []Manual {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Manual, len(f.doc.Manual))
	copy(out, f.doc.Manual)
	return out
}

// PutManual adds or replaces a manual entity by ID and saves the file.
func (f *File) PutManual(m Manual) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	replaced := false
	for i := range f.doc.Manual {
		if f.doc.Manual[i].ID == m.ID {
			f.doc.Manual[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		f.doc.Manual = append(f.doc.Manual, m)
	}
	return f.save()
}

// RemoveManual deletes a manual entity and its override and saves the
// file.
func (f *File) RemoveManual(id string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.doc.Manual[:0]
	for _, m := range f.doc.Manual {
		if m.ID != id {
			kept = append(kept, m)
		}
	}
	f.doc.Manual = kept
	delete(f.doc.Overrides, id)
	return f.save()
}

// save writes the file atomically; the caller holds f.mu.
func (f *File) save() error {
	body, err := yaml.Marshal(f.doc)
	if err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := atomicfile.Write(f.path, append([]byte(fileHeader), body...), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	return nil
}
