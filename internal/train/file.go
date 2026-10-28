package train

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"sync"

	"github.com/mikesmitty/interlock/internal/atomicfile"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/internal/yamlstrict"
)

// FileName is the roster file inside the data directory.
const FileName = "trains.yaml"

type fileDoc struct {
	Trains     []Entry     `yaml:"trains"`
	Consists   []Consist   `yaml:"consists,omitempty"`
	Cars       []Car       `yaml:"cars,omitempty"`
	Formations []Formation `yaml:"formations,omitempty"`
}

const fileHeader = `# The roster. The web interface edits this file; restart Interlock after
# editing it yourself. Under trains, each locomotive has a DCC address, a
# name, optional function labels and the decoders it has; consists list
# the locomotives that run together, the lead first. Cars are the rest of
# the rolling stock, and formations are the trains made up of locomotives
# and cars, from the front. See docs/roster.md.
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
	consists := make(map[string]bool, len(f.doc.Consists))
	for i := range f.doc.Consists {
		c := f.doc.Consists[i].Normalize()
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: consist %d (%s): %w", path, i+1, c.Name, err)
		}
		if consists[c.ID] {
			return nil, fmt.Errorf("%s: two consists have the ID %s", path, c.ID)
		}
		consists[c.ID] = true
		f.doc.Consists[i] = c
	}
	cars := make(map[string]bool, len(f.doc.Cars))
	for i := range f.doc.Cars {
		c := f.doc.Cars[i].Normalize()
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: car %d (%s): %w", path, i+1, c.Name, err)
		}
		if cars[c.ID] {
			return nil, fmt.Errorf("%s: two cars have the ID %s", path, c.ID)
		}
		cars[c.ID] = true
		f.doc.Cars[i] = c
	}
	formations := make(map[string]bool, len(f.doc.Formations))
	for i := range f.doc.Formations {
		fm := f.doc.Formations[i].Normalize()
		if err := fm.Validate(); err != nil {
			return nil, fmt.Errorf("%s: formation %d (%s): %w", path, i+1, fm.Name, err)
		}
		if formations[fm.ID] {
			return nil, fmt.Errorf("%s: two formations have the ID %s", path, fm.ID)
		}
		formations[fm.ID] = true
		f.doc.Formations[i] = fm
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

// Consists returns the consists in file order.
func (f *File) Consists() []Consist {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Consist, len(f.doc.Consists))
	for i, c := range f.doc.Consists {
		out[i] = c.clone()
	}
	return out
}

// PutConsist adds or replaces a consist by ID and saves the file.
func (f *File) PutConsist(c Consist) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c = c.clone()
	for i := range f.doc.Consists {
		if f.doc.Consists[i].ID == c.ID {
			f.doc.Consists[i] = c
			return f.save()
		}
	}
	f.doc.Consists = append(f.doc.Consists, c)
	return f.save()
}

// RemoveConsist deletes a consist and saves the file.
func (f *File) RemoveConsist(id string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.doc.Consists[:0]
	for _, c := range f.doc.Consists {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	f.doc.Consists = kept
	return f.save()
}

// Cars returns the cars in file order.
func (f *File) Cars() []Car {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.doc.Cars)
}

// PutCar adds or replaces a car by ID and saves the file.
func (f *File) PutCar(c Car) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc.Cars = put(f.doc.Cars, c, func(c Car) string { return c.ID })
	return f.save()
}

// RemoveCar deletes a car and saves the file.
func (f *File) RemoveCar(id string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc.Cars = slices.DeleteFunc(f.doc.Cars, func(c Car) bool { return c.ID == id })
	return f.save()
}

// Formations returns the formations in file order.
func (f *File) Formations() []Formation {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Formation, len(f.doc.Formations))
	for i, fm := range f.doc.Formations {
		out[i] = fm.clone()
	}
	return out
}

// PutFormation adds or replaces a formation by ID and saves the file.
func (f *File) PutFormation(fm Formation) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc.Formations = put(f.doc.Formations, fm.clone(), func(fm Formation) string { return fm.ID })
	return f.save()
}

// RemoveFormation deletes a formation and saves the file.
func (f *File) RemoveFormation(id string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc.Formations = slices.DeleteFunc(f.doc.Formations, func(fm Formation) bool { return fm.ID == id })
	return f.save()
}

// put replaces the item in list with v's ID, or adds v at the end.
func put[T any](list []T, v T, id func(T) string) []T {
	for i := range list {
		if id(list[i]) == id(v) {
			list[i] = v
			return list
		}
	}
	return append(list, v)
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
