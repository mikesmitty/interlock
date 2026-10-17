// Package profile turns a node's configuration into entities. A profile
// is data: it names the node it applies to by manufacturer and model, and
// for each kind of entity the CDI group whose replicas become one and the
// variables that play each role ("each Servo group with a Throw Command
// Event and a Close Command Event is a turnout"). Profiles for the devices
// Interlock was built against ship inside the binary; users add their own
// as YAML files in the profiles directory, and a generic profile guesses
// from event names on nodes nothing else matches.
package profile

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
)

// DirName is the directory of user profiles inside the data directory.
const DirName = "profiles"

// Built-in profiles.
//
//go:embed profiles/*.yaml
var builtin embed.FS

// Profile describes how one kind of node becomes entities.
type Profile struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Match       Match  `yaml:"match" json:"match"`
	// Suggest marks the profile's entities as guesses to confirm.
	Suggest  bool   `yaml:"suggest,omitempty" json:"suggest,omitempty"`
	Entities []Rule `yaml:"entities" json:"entities"`
	// Source is "built-in" or the file the profile came from.
	Source string `yaml:"-" json:"source"`
}

// Match says which nodes a profile applies to. Manufacturer and model
// compare case-insensitively; software_version is a prefix.
type Match struct {
	Manufacturer    string `yaml:"manufacturer,omitempty" json:"manufacturer,omitempty"`
	Model           string `yaml:"model,omitempty" json:"model,omitempty"`
	SoftwareVersion string `yaml:"software_version,omitempty" json:"software_version,omitempty"`
	// Any matches every node; such a profile is tried after the others.
	Any bool `yaml:"any,omitempty" json:"any,omitempty"`
}

// Matches reports whether the node's identification fits.
func (m Match) Matches(snip *openlcb.SNIP) bool {
	if m.Any {
		return true
	}
	if snip == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(snip.Manufacturer), m.Manufacturer) ||
		!strings.EqualFold(strings.TrimSpace(snip.Model), m.Model) {
		return false
	}
	return m.SoftwareVersion == "" || strings.HasPrefix(snip.SoftwareVersion, m.SoftwareVersion)
}

// Rule makes entities of one kind.
type Rule struct {
	Kind entity.Kind `yaml:"kind" json:"kind"`
	// Group names the CDI group whose replicas each become an entity. "*"
	// tries every group (and segment) that holds event IDs. Empty makes
	// one entity for the node, for roles bound to fixed event IDs.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
	// ID names the entity within the node when there is no group.
	ID string `yaml:"id,omitempty" json:"id,omitempty"`
	// Name is a template, or templates tried in order until one comes out
	// non-empty: {node}, {group}, {n} and {Variable Name} are replaced.
	Name Templates `yaml:"name,omitempty" json:"name,omitempty"`
	// Events binds roles to variables of the group by name, exact or
	// ~regexp, or to literal event IDs. Order matters: each variable binds
	// to the first role that names it.
	Events Bindings `yaml:"events" json:"events"`
	// Enabled names a variable that must be non-zero for the entity to
	// exist.
	Enabled string `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// Templates is one name template or several alternatives.
type Templates []string

// UnmarshalYAML accepts a scalar or a sequence.
func (t *Templates) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*t = Templates{value.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*t = list
		return nil
	default:
		return errors.New("name must be a string or a list of strings")
	}
}

// Binding ties a role to a variable name, a pattern or an event ID.
type Binding struct {
	Role  string `json:"role"`
	Match string `json:"match"`

	literal openlcb.EventID
	re      *regexp.Regexp
}

// Bindings keeps the order roles were written in.
type Bindings []Binding

// UnmarshalYAML reads a mapping of role to match in document order.
func (b *Bindings) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return errors.New("events must be a mapping of role to variable name or event ID")
	}
	var out Bindings
	for i := 0; i+1 < len(value.Content); i += 2 {
		out = append(out, Binding{Role: value.Content[i].Value, Match: value.Content[i+1].Value})
	}
	*b = out
	return nil
}

// MarshalYAML writes the mapping back in order.
func (b Bindings) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, bind := range b {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: bind.Role},
			&yaml.Node{Kind: yaml.ScalarNode, Value: bind.Match})
	}
	return node, nil
}

// Parse reads one profile and checks it.
func Parse(data []byte, source string) (*Profile, error) {
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	p.Source = source
	if err := p.compile(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &p, nil
}

// compile validates the profile and prepares its matchers.
func (p *Profile) compile() error {
	if p.Name == "" {
		return errors.New("profile needs a name")
	}
	if !p.Match.Any && (p.Match.Manufacturer == "" || p.Match.Model == "") {
		return fmt.Errorf("profile %s: match needs manufacturer and model, or any: true", p.Name)
	}
	if len(p.Entities) == 0 {
		return fmt.Errorf("profile %s: no entities", p.Name)
	}
	for i := range p.Entities {
		rule := &p.Entities[i]
		def, ok := entity.Lookup(rule.Kind)
		if !ok {
			return fmt.Errorf("profile %s: entities[%d]: %q is not a kind of entity", p.Name, i, rule.Kind)
		}
		bound := make(map[string]bool)
		for j := range rule.Events {
			b := &rule.Events[j]
			if _, ok := def.Role(b.Role); !ok {
				return fmt.Errorf("profile %s: entities[%d]: a %s has no %q event", p.Name, i, def.Label, b.Role)
			}
			if bound[b.Role] {
				return fmt.Errorf("profile %s: entities[%d]: %q is bound twice", p.Name, i, b.Role)
			}
			bound[b.Role] = true
			switch {
			case strings.HasPrefix(b.Match, "~"):
				re, err := regexp.Compile("(?i)" + strings.TrimPrefix(b.Match, "~"))
				if err != nil {
					return fmt.Errorf("profile %s: entities[%d]: %s: %w", p.Name, i, b.Role, err)
				}
				b.re = re
			default:
				if ev, err := openlcb.ParseEventID(b.Match); err == nil {
					if ev == 0 {
						return fmt.Errorf("profile %s: entities[%d]: %s: an event ID of zero means no event", p.Name, i, b.Role)
					}
					b.literal = ev
				} else if strings.TrimSpace(b.Match) == "" {
					return fmt.Errorf("profile %s: entities[%d]: %s has nothing to match", p.Name, i, b.Role)
				}
			}
		}
		if missing := def.Missing(bound); len(missing) > 0 {
			return fmt.Errorf("profile %s: entities[%d]: a %s needs its %s event", p.Name, i, def.Label, missing[0])
		}
	}
	return nil
}

// Set is the loaded profiles in the order they are tried: user profiles,
// then built-in ones, with catch-all profiles last.
type Set struct {
	profiles []*Profile
}

// Load reads the built-in profiles and the YAML files in dir, which may
// be empty or missing. A user profile replaces a built-in one of the same
// name.
func Load(dir string) (*Set, error) {
	byName := make(map[string]*Profile)
	var order []string
	add := func(p *Profile) {
		if _, seen := byName[p.Name]; !seen {
			order = append(order, p.Name)
		}
		byName[p.Name] = p
	}

	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("profiles: %w", err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if e.IsDir() || !isYAML(e.Name()) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("profiles: %w", err)
			}
			p, err := Parse(data, path)
			if err != nil {
				return nil, fmt.Errorf("profiles: %w", err)
			}
			add(p)
		}
	}

	built, err := fs.ReadDir(builtin, "profiles")
	if err != nil {
		return nil, fmt.Errorf("profiles: %w", err)
	}
	for _, e := range built {
		data, err := builtin.ReadFile("profiles/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("profiles: %w", err)
		}
		p, err := Parse(data, "built-in")
		if err != nil {
			return nil, fmt.Errorf("profiles: %w", err)
		}
		if _, seen := byName[p.Name]; seen {
			continue // a user file replaced it
		}
		add(p)
	}

	s := &Set{}
	for _, name := range order {
		if !byName[name].Match.Any {
			s.profiles = append(s.profiles, byName[name])
		}
	}
	for _, name := range order {
		if byName[name].Match.Any {
			s.profiles = append(s.profiles, byName[name])
		}
	}
	return s, nil
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// Match returns the first profile that fits the node, or nil.
func (s *Set) Match(snip *openlcb.SNIP) *Profile {
	for _, p := range s.profiles {
		if p.Match.Matches(snip) {
			return p
		}
	}
	return nil
}

// List returns the profiles in the order they are tried.
func (s *Set) List() []*Profile {
	return append([]*Profile(nil), s.profiles...)
}

// Build makes the entities for a node from the profile that matches it.
func (s *Set) Build(node entity.NodeInfo, doc *cdi.CDI, values map[string]string) []entity.Spec {
	p := s.Match(node.SNIP)
	if p == nil {
		return nil
	}
	return p.Build(node, doc, values)
}
