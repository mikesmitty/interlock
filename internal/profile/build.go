package profile

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
)

// instance is one place a rule applies: a replica of a group, a segment,
// or the whole node.
type instance struct {
	// label is the replica name ("Servo 1"), the segment name, or "".
	label string
	// slug identifies the instance within the node.
	slug string
	// index is the one-based replica number, 0 outside groups.
	index int
	// vars are the variables directly inside the instance.
	vars []cdi.Variable
}

// Build makes the entities the profile describes for a node.
func (p *Profile) Build(node entity.NodeInfo, doc *cdi.CDI, values map[string]string) []entity.Spec {
	vars := doc.Variables()
	var specs []entity.Spec
	for _, rule := range p.Entities {
		for _, inst := range instances(doc, vars, rule.Group) {
			if spec, ok := p.build(rule, node, inst, vars, values); ok {
				specs = append(specs, spec)
			}
		}
	}
	return specs
}

// instances finds where a rule applies.
func instances(doc *cdi.CDI, vars []cdi.Variable, group string) []instance {
	if group == "" {
		return []instance{{vars: vars}}
	}
	var out []instance
	for _, seg := range doc.Segments {
		prefix := []string{seg.Name}
		if group == "*" && hasDirectEvents(seg.Items) {
			out = append(out, instance{label: seg.Name, slug: entity.Slug(seg.Name), vars: directChildren(vars, prefix)})
		}
		out = appendGroups(out, seg.Items, prefix, vars, group)
	}
	return out
}

func appendGroups(out []instance, items []*cdi.Element, prefix []string, vars []cdi.Variable, group string) []instance {
	for _, el := range items {
		if el.Kind != cdi.KindGroup {
			continue
		}
		wanted := group == "*" && hasDirectEvents(el.Children) || group != "*" && strings.EqualFold(el.Name, group)
		for i := range el.Replication {
			path := append(append([]string(nil), prefix...), el.RepName(i))
			if wanted {
				out = append(out, instance{
					label: el.RepName(i),
					slug:  entity.Slug(strings.Join(path[1:], " ")),
					index: i + 1,
					vars:  directChildren(vars, path),
				})
			}
			out = appendGroups(out, el.Children, path, vars, group)
		}
	}
	return out
}

func hasDirectEvents(items []*cdi.Element) bool {
	for _, el := range items {
		if el.Kind == cdi.KindEventID {
			return true
		}
	}
	return false
}

// directChildren returns the variables whose path is prefix plus a name.
func directChildren(vars []cdi.Variable, prefix []string) []cdi.Variable {
	var out []cdi.Variable
	for _, v := range vars {
		if len(v.Path) != len(prefix)+1 {
			continue
		}
		match := true
		for i := range prefix {
			if v.Path[i] != prefix[i] {
				match = false
				break
			}
		}
		if match {
			out = append(out, v)
		}
	}
	return out
}

// build makes one entity, or reports that the rule does not apply here.
func (p *Profile) build(rule Rule, node entity.NodeInfo, inst instance, all []cdi.Variable, values map[string]string) (entity.Spec, bool) {
	def, _ := entity.Lookup(rule.Kind)
	events := make(map[string]openlcb.EventID)
	keys := make(map[string]string)
	used := make(map[string]bool)
	for _, b := range rule.Events {
		if b.literal != 0 {
			events[b.Role] = b.literal
			continue
		}
		v, ok := findVariable(inst.vars, b, used, cdi.KindEventID)
		if !ok {
			continue
		}
		used[v.Key()] = true
		ev, err := openlcb.ParseEventID(values[v.Key()])
		if err != nil || ev == 0 {
			continue
		}
		events[b.Role] = ev
		keys[b.Role] = v.Key()
	}
	bound := make(map[string]bool, len(events))
	for role := range events {
		bound[role] = true
	}
	if len(def.Missing(bound)) > 0 {
		return entity.Spec{}, false
	}
	if rule.Enabled != "" {
		if v, ok := findVariable(inst.vars, Binding{Match: rule.Enabled}, nil, ""); ok && !truthy(values[v.Key()]) {
			return entity.Spec{}, false
		}
	}

	slug := inst.slug
	if slug == "" {
		slug = rule.ID
		if slug == "" {
			slug = string(rule.Kind)
		}
	}
	spec := entity.Spec{
		ID:        entity.ProfileID(rule.Kind, node.ID, slug),
		Kind:      rule.Kind,
		Name:      p.name(rule, node, inst, all, values),
		Node:      node.ID,
		Group:     inst.label,
		Events:    events,
		Keys:      keys,
		Source:    entity.SourceProfile,
		Profile:   p.Name,
		Suggested: p.Suggest,
	}
	if p.Suggest {
		spec.Source = entity.SourceGuess
	}
	return spec, true
}

// findVariable picks the first unused variable of the kind (any kind when
// empty) whose name the binding matches.
func findVariable(vars []cdi.Variable, b Binding, used map[string]bool, kind cdi.Kind) (cdi.Variable, bool) {
	for _, v := range vars {
		if kind != "" && v.Element.Kind != kind || used[v.Key()] {
			continue
		}
		name := v.Element.Name
		if b.re != nil && b.re.MatchString(name) || b.re == nil && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(b.Match)) {
			return v, true
		}
	}
	return cdi.Variable{}, false
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off", "disabled":
		return false
	}
	return true
}

var placeholder = regexp.MustCompile(`\{([^{}]+)\}`)

// name expands the rule's templates in turn and returns the first
// non-empty result, falling back to the node and group names.
func (p *Profile) name(rule Rule, node entity.NodeInfo, inst instance, all []cdi.Variable, values map[string]string) string {
	templates := append(append(Templates(nil), rule.Name...), "{node} {group}", "{node} "+string(rule.Kind))
	for _, tpl := range templates {
		unresolved := false
		out := placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
			key := m[1 : len(m)-1]
			switch key {
			case "node":
				return node.Name
			case "group":
				return inst.label
			case "n":
				if inst.index == 0 {
					return ""
				}
				return strconv.Itoa(inst.index)
			}
			if v, ok := findVariable(inst.vars, Binding{Match: key}, nil, ""); ok {
				return values[v.Key()]
			}
			if v, ok := findVariable(all, Binding{Match: key}, nil, ""); ok {
				return values[v.Key()]
			}
			unresolved = true
			return ""
		})
		out = strings.Join(strings.Fields(out), " ")
		if !unresolved && out != "" {
			return out
		}
	}
	return string(rule.Kind)
}
