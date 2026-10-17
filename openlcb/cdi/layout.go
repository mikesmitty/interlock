package cdi

// Node is an element of the expanded layout, the shape the API serves:
// every replicated group is unrolled so each item has a concrete address,
// and every value has a key for value maps.
type Node struct {
	Kind        Kind   `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Space       uint8  `json:"space"`
	Address     uint32 `json:"address"`
	Size        int    `json:"size"`
	// Key is "space:address" for values; empty for groups.
	Key         string     `json:"key,omitempty"`
	Min         string     `json:"min,omitempty"`
	Max         string     `json:"max,omitempty"`
	Default     string     `json:"default,omitempty"`
	Signed      bool       `json:"signed,omitempty"`
	Map         []Relation `json:"map,omitempty"`
	Hints       *Hints     `json:"hints,omitempty"`
	Formatting  string     `json:"formatting,omitempty"`
	Mode        string     `json:"mode,omitempty"`
	ButtonText  string     `json:"button_text,omitempty"`
	DialogText  string     `json:"dialog_text,omitempty"`
	Value       string     `json:"value,omitempty"`
	Replication int        `json:"replication,omitempty"`
	// Replicas holds a group's contents, one entry per replica.
	Replicas []Replica `json:"replicas,omitempty"`
}

// Replica is one copy of a group.
type Replica struct {
	Name  string  `json:"name"`
	Items []*Node `json:"items"`
}

// LayoutSegment is a segment of the expanded layout.
type LayoutSegment struct {
	Space       uint8   `json:"space"`
	Origin      uint32  `json:"origin"`
	Size        uint32  `json:"size"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Items       []*Node `json:"items"`
}

// Layout is the expanded document.
type Layout struct {
	Identification *Identification  `json:"identification,omitempty"`
	ACDI           *ACDI            `json:"acdi,omitempty"`
	Segments       []*LayoutSegment `json:"segments"`
}

// Layout expands the document.
func (c *CDI) Layout() *Layout {
	l := &Layout{Identification: c.Identification, ACDI: c.ACDI, Segments: make([]*LayoutSegment, 0, len(c.Segments))}
	for _, seg := range c.Segments {
		l.Segments = append(l.Segments, &LayoutSegment{
			Space:       seg.Space,
			Origin:      seg.Origin,
			Size:        seg.Size,
			Name:        seg.Name,
			Description: seg.Description,
			Items:       expandItems(seg.Items, 0),
		})
	}
	return l
}

func expandItems(items []*Element, shift uint32) []*Node {
	out := make([]*Node, 0, len(items))
	for _, el := range items {
		n := &Node{
			Kind:        el.Kind,
			Name:        el.Name,
			Description: el.Description,
			Space:       el.Space,
			Address:     el.Address + shift,
			Size:        el.Size,
			Min:         el.Min,
			Max:         el.Max,
			Default:     el.Default,
			Map:         el.Map,
			Hints:       el.Hints,
			Formatting:  el.Formatting,
			Mode:        el.Mode,
			ButtonText:  el.ButtonText,
			DialogText:  el.DialogText,
			Value:       el.Value,
		}
		if el.Kind == KindGroup {
			n.Replication = el.Replication
			n.Replicas = make([]Replica, 0, el.Replication)
			for i := range el.Replication {
				n.Replicas = append(n.Replicas, Replica{
					Name:  el.RepName(i),
					Items: expandItems(el.Children, shift+uint32(i*el.Size)),
				})
			}
		} else {
			n.Key = Key(el.Space, n.Address)
			n.Signed = el.Signed()
		}
		out = append(out, n)
	}
	return out
}
