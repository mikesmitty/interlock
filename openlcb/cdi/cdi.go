// Package cdi parses Configuration Description Information, the XML a
// node serves from address space 0xFF to describe its configuration
// memory, and resolves every described variable to an address.
//
// The parser follows the CDI 1.4 schema and tolerates what real nodes
// send: unknown elements and attributes are skipped, text encodings other
// than UTF-8 are converted, and trailing bytes after the document end are
// ignored.
package cdi

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind is the type of a CDI element.
type Kind string

// Element kinds.
const (
	KindGroup   Kind = "group"
	KindInt     Kind = "int"
	KindFloat   Kind = "float"
	KindString  Kind = "string"
	KindEventID Kind = "eventid"
	KindAction  Kind = "action"
	KindBlob    Kind = "blob"
)

// Relation is one entry of a map: the stored property and the label shown
// for it.
type Relation struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// Hints are the rendering hints an element may carry.
type Hints struct {
	// Hideable groups may be collapsed; Hidden ones start collapsed.
	Hideable bool `json:"hideable,omitempty"`
	Hidden   bool `json:"hidden,omitempty"`
	// ReadOnly groups are shown but not edited.
	ReadOnly bool `json:"read_only,omitempty"`
	// Slider, RadioButton and Checkbox suggest a control for an int.
	Slider      *Slider `json:"slider,omitempty"`
	RadioButton bool    `json:"radio_button,omitempty"`
	Checkbox    bool    `json:"checkbox,omitempty"`
}

// Slider describes the slider hint of an int.
type Slider struct {
	TickSpacing int  `json:"tick_spacing,omitempty"`
	Immediate   bool `json:"immediate,omitempty"`
	ShowValue   bool `json:"show_value,omitempty"`
}

// Element is one node of the parsed tree: a group or a variable.
type Element struct {
	Kind        Kind
	Name        string
	Description string
	// Offset is the gap from the end of the previous element, from the XML.
	Offset int
	// Size in bytes of one value; for a group, of one replica, computed.
	Size int
	// Replication is how many copies a group has (1 if not replicated).
	Replication int
	// RepNames label the replicas; missing ones are numbered.
	RepNames []string
	// Min, Max and Default are kept as written; int and float parse them.
	Min, Max, Default string
	Map               []Relation
	Hints             *Hints
	// Formatting is a float's printf style format, such as %3.1f.
	Formatting string
	// Mode is a blob's access: read, write or readwrite.
	Mode string
	// Action elements: the button, its confirmation and the value written.
	ButtonText, DialogText, Value string
	// Children of a group.
	Children []*Element

	// Address is the absolute address of the element's first byte in its
	// segment's space; for a replicated group, of the first replica.
	Address uint32
	// Space is the address space, copied from the segment.
	Space uint8
}

// Segment is a contiguous region of one address space.
type Segment struct {
	Space       uint8
	Origin      uint32
	Name        string
	Description string
	Items       []*Element
	// Size is the number of bytes from Origin to the end of the last item.
	Size uint32
}

// Identification mirrors the identification block.
type Identification struct {
	Manufacturer    string     `json:"manufacturer"`
	Model           string     `json:"model"`
	HardwareVersion string     `json:"hardware_version"`
	SoftwareVersion string     `json:"software_version"`
	Map             []Relation `json:"map,omitempty"`
}

// ACDI records that the node also supports Abbreviated CDI, with the
// format versions of its fixed and variable blocks.
type ACDI struct {
	Fixed int `json:"fixed"`
	Var   int `json:"var"`
}

// CDI is a parsed document with every element's address resolved.
type CDI struct {
	Identification *Identification
	ACDI           *ACDI
	Segments       []*Segment
	// Truncated is set when the document ended early; what was read is
	// kept, since every element before the cut is still laid out correctly.
	Truncated bool
}

// Sizes of the fixed-size kinds.
const (
	EventIDSize = 8
	BlobSize    = 10
)

// ErrNotCDI is returned when the document's root is not a cdi element.
var ErrNotCDI = errors.New("cdi: document is not a CDI")

// Parse reads a CDI document and resolves addresses.
func Parse(data []byte) (*CDI, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if i := bytes.IndexByte(data, 0); i >= 0 {
		data = data[:i]
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = charsetReader
	p := &parser{dec: dec}
	doc, err := p.document()
	if err != nil {
		return nil, err
	}
	doc.Truncated = p.truncated
	for _, seg := range doc.Segments {
		resolveSegment(seg)
	}
	return doc, nil
}

func charsetReader(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "latin1", "latin-1", "windows-1252":
		return &latin1Reader{r: input}, nil
	}
	return nil, fmt.Errorf("cdi: unsupported encoding %q", label)
}

// latin1Reader converts single-byte text to UTF-8.
type latin1Reader struct {
	r   io.Reader
	buf []byte
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	if len(l.buf) == 0 {
		raw := make([]byte, 512)
		n, err := l.r.Read(raw)
		if n == 0 {
			return 0, err
		}
		out := make([]byte, 0, 2*n)
		for _, b := range raw[:n] {
			out = utf8AppendRune(out, rune(b))
		}
		l.buf = out
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}

func utf8AppendRune(b []byte, r rune) []byte {
	if r < 0x80 {
		return append(b, byte(r))
	}
	return append(b, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
}

type parser struct {
	dec       *xml.Decoder
	truncated bool
}

// eof reports whether err is the document ending early, which the parser
// tolerates and records.
func (p *parser) eof(err error) bool {
	var syntax *xml.SyntaxError
	if errors.Is(err, io.EOF) || (errors.As(err, &syntax) && strings.Contains(syntax.Msg, "unexpected EOF")) {
		p.truncated = true
		return true
	}
	return false
}

// document finds the root element and parses it.
func (p *parser) document() (*CDI, error) {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ErrNotCDI
			}
			return nil, fmt.Errorf("cdi: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "cdi" {
			return nil, fmt.Errorf("%w: root element is %s", ErrNotCDI, start.Name.Local)
		}
		return p.cdi()
	}
}

func (p *parser) cdi() (*CDI, error) {
	doc := &CDI{}
	err := p.children(func(start xml.StartElement) error {
		switch start.Name.Local {
		case "identification":
			ident, err := p.identification()
			if err != nil {
				return err
			}
			doc.Identification = ident
		case "acdi":
			acdi := &ACDI{Fixed: 4, Var: 2}
			for _, a := range start.Attr {
				switch a.Name.Local {
				case "fixed":
					acdi.Fixed = atoi(a.Value, 4)
				case "var":
					acdi.Var = atoi(a.Value, 2)
				}
			}
			doc.ACDI = acdi
			return p.dec.Skip()
		case "segment":
			seg, err := p.segment(start)
			if err != nil {
				return err
			}
			doc.Segments = append(doc.Segments, seg)
		default:
			return p.dec.Skip()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// children calls fn for each child start element until the current
// element ends. fn must consume the child completely.
func (p *parser) children(fn func(start xml.StartElement) error) error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			if p.eof(err) {
				return nil
			}
			return fmt.Errorf("cdi: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := fn(t); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

// text reads the character data of the current element up to its end.
func (p *parser) text() (string, error) {
	var sb strings.Builder
	depth := 0
	for {
		tok, err := p.dec.Token()
		if err != nil {
			if p.eof(err) {
				return strings.TrimSpace(sb.String()), nil
			}
			return "", fmt.Errorf("cdi: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth == 0 {
				return strings.TrimSpace(sb.String()), nil
			}
			depth--
		}
	}
}

func (p *parser) identification() (*Identification, error) {
	ident := &Identification{}
	err := p.children(func(start xml.StartElement) error {
		var dst *string
		switch start.Name.Local {
		case "manufacturer":
			dst = &ident.Manufacturer
		case "model":
			dst = &ident.Model
		case "hardwareVersion":
			dst = &ident.HardwareVersion
		case "softwareVersion":
			dst = &ident.SoftwareVersion
		case "map":
			m, err := p.relations()
			ident.Map = m
			return err
		default:
			return p.dec.Skip()
		}
		s, err := p.text()
		*dst = s
		return err
	})
	return ident, err
}

func (p *parser) relations() ([]Relation, error) {
	var out []Relation
	err := p.children(func(start xml.StartElement) error {
		if start.Name.Local != "relation" {
			return p.dec.Skip()
		}
		var rel Relation
		err := p.children(func(start xml.StartElement) error {
			switch start.Name.Local {
			case "property":
				s, err := p.text()
				rel.Property = s
				return err
			case "value":
				s, err := p.text()
				rel.Value = s
				return err
			default:
				return p.dec.Skip()
			}
		})
		out = append(out, rel)
		return err
	})
	return out, err
}

func (p *parser) segment(start xml.StartElement) (*Segment, error) {
	seg := &Segment{Space: 0xFD}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "space":
			seg.Space = uint8(atoi(a.Value, 0xFD))
		case "origin":
			seg.Origin = uint32(atoi(a.Value, 0))
		}
	}
	err := p.children(func(start xml.StartElement) error {
		switch start.Name.Local {
		case "name":
			s, err := p.text()
			seg.Name = s
			return err
		case "description":
			s, err := p.text()
			seg.Description = s
			return err
		default:
			el, err := p.element(start)
			if err != nil {
				return err
			}
			if el != nil {
				seg.Items = append(seg.Items, el)
			}
			return nil
		}
	})
	return seg, err
}

// element parses a group or variable; it returns nil for elements that
// are not part of the layout, after skipping them.
func (p *parser) element(start xml.StartElement) (*Element, error) {
	el := &Element{Kind: Kind(start.Name.Local), Replication: 1}
	switch el.Kind {
	case KindGroup:
	case KindInt:
		el.Size = 1
	case KindFloat, KindString, KindAction:
		el.Size = 0 // required attribute
	case KindEventID:
		el.Size = EventIDSize
	case KindBlob:
		el.Size = BlobSize
	default:
		return nil, p.dec.Skip()
	}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "offset":
			el.Offset = atoi(a.Value, 0)
		case "size":
			el.Size = atoi(a.Value, el.Size)
		case "replication":
			el.Replication = max(atoi(a.Value, 1), 1)
		case "formatting":
			el.Formatting = a.Value
		case "mode":
			el.Mode = a.Value
		}
	}
	if el.Kind == KindString && el.Size <= 0 {
		return nil, fmt.Errorf("cdi: string element %q without a size", el.Name)
	}
	err := p.children(func(start xml.StartElement) error {
		var dst *string
		switch start.Name.Local {
		case "name":
			dst = &el.Name
		case "description":
			dst = &el.Description
		case "min":
			dst = &el.Min
		case "max":
			dst = &el.Max
		case "default":
			dst = &el.Default
		case "buttonText":
			dst = &el.ButtonText
		case "dialogText":
			dst = &el.DialogText
		case "value":
			dst = &el.Value
		case "repname":
			s, err := p.text()
			el.RepNames = append(el.RepNames, s)
			return err
		case "map":
			m, err := p.relations()
			el.Map = m
			return err
		case "hints":
			h, err := p.hints()
			el.Hints = h
			return err
		case "link":
			return p.dec.Skip()
		default:
			if el.Kind != KindGroup {
				return p.dec.Skip()
			}
			child, err := p.element(start)
			if err != nil {
				return err
			}
			if child != nil {
				el.Children = append(el.Children, child)
			}
			return nil
		}
		s, err := p.text()
		*dst = s
		return err
	})
	if err != nil {
		return nil, err
	}
	if el.Kind == KindFloat && el.Size == 0 {
		el.Size = 4
	}
	if el.Kind == KindAction && el.Size == 0 {
		el.Size = 1
	}
	return el, nil
}

func (p *parser) hints() (*Hints, error) {
	h := &Hints{}
	err := p.children(func(start xml.StartElement) error {
		switch start.Name.Local {
		case "visibility":
			for _, a := range start.Attr {
				switch a.Name.Local {
				case "hideable":
					h.Hideable = yes(a.Value)
				case "hidden":
					h.Hidden = yes(a.Value)
				}
			}
		case "readOnly":
			h.ReadOnly = true
		case "slider":
			s := &Slider{}
			for _, a := range start.Attr {
				switch a.Name.Local {
				case "tickSpacing":
					s.TickSpacing = atoi(a.Value, 0)
				case "immediate":
					s.Immediate = yes(a.Value)
				case "showValue":
					s.ShowValue = yes(a.Value)
				}
			}
			h.Slider = s
		case "radiobutton":
			h.RadioButton = true
		case "checkbox":
			h.Checkbox = true
		}
		return p.dec.Skip()
	})
	return h, err
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func yes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "1":
		return true
	}
	return false
}

// resolveSegment assigns addresses to every element of a segment.
func resolveSegment(seg *Segment) {
	end := resolveItems(seg.Items, int64(seg.Origin), seg.Space)
	if end > int64(seg.Origin) {
		seg.Size = uint32(end - int64(seg.Origin))
	}
}

// resolveItems lays items out from base and returns the address after the
// last one. Group children are laid out relative to the group's start, so
// their addresses are those of the first replica.
func resolveItems(items []*Element, base int64, space uint8) int64 {
	pos := base
	for _, el := range items {
		pos += int64(el.Offset)
		el.Space = space
		el.Address = uint32(max(pos, 0))
		if el.Kind == KindGroup {
			end := resolveItems(el.Children, pos, space)
			el.Size = int(end - pos)
			pos += int64(el.Size) * int64(el.Replication)
		} else {
			pos += int64(el.Size)
		}
	}
	return pos
}

// RepName returns the label of replica i (zero based).
func (e *Element) RepName(i int) string {
	if i < len(e.RepNames) && e.RepNames[i] != "" {
		return e.RepNames[i]
	}
	if e.Replication > 1 {
		return fmt.Sprintf("%s %d", e.Name, i+1)
	}
	return e.Name
}

// IsVariable reports whether the element holds a value (not a group).
func (e *Element) IsVariable() bool { return e.Kind != KindGroup }

// Variable is one addressable value in the layout, with the path of names
// leading to it: segment, groups (replica names for replicated groups),
// and the variable's own name.
type Variable struct {
	Path    []string
	Element *Element
	Space   uint8
	Address uint32
	Size    int
}

// Key identifies a variable by space and address, as "253:132".
func (v Variable) Key() string { return Key(v.Space, v.Address) }

// Key formats a space and address as the key used in value maps.
func Key(space uint8, address uint32) string {
	return strconv.Itoa(int(space)) + ":" + strconv.FormatUint(uint64(address), 10)
}

// ParseKey reverses Key.
func ParseKey(key string) (space uint8, address uint32, err error) {
	s, a, ok := strings.Cut(key, ":")
	if !ok {
		return 0, 0, fmt.Errorf("cdi: key %q is not space:address", key)
	}
	sp, err1 := strconv.ParseUint(s, 10, 8)
	ad, err2 := strconv.ParseUint(a, 10, 32)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("cdi: key %q is not space:address", key)
	}
	return uint8(sp), uint32(ad), nil
}

// Variables lists every value in the document, replicated groups
// expanded, in address order within each segment.
func (c *CDI) Variables() []Variable {
	var out []Variable
	for _, seg := range c.Segments {
		path := []string{seg.Name}
		out = walkVariables(out, seg.Items, path, 0, seg.Space)
	}
	return out
}

func walkVariables(out []Variable, items []*Element, path []string, shift uint32, space uint8) []Variable {
	for _, el := range items {
		if el.Kind != KindGroup {
			out = append(out, Variable{
				Path:    appendPath(path, el.Name),
				Element: el,
				Space:   space,
				Address: el.Address + shift,
				Size:    el.Size,
			})
			continue
		}
		for i := range el.Replication {
			rep := shift + uint32(i*el.Size)
			out = walkVariables(out, el.Children, appendPath(path, el.RepName(i)), rep, space)
		}
	}
	return out
}

func appendPath(path []string, name string) []string {
	out := make([]string, len(path), len(path)+1)
	copy(out, path)
	return append(out, name)
}

// Lookup builds an index of variables by key.
func (c *CDI) Lookup() map[string]Variable {
	vars := c.Variables()
	index := make(map[string]Variable, len(vars))
	for _, v := range vars {
		index[v.Key()] = v
	}
	return index
}
