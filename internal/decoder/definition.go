package decoder

import (
	"errors"
	"fmt"
	"math/bits"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Definition is one decoder model's settings and how to lay them out,
// resolved from its family's file. The browser turns CV values into
// setting values and back; the server reads and writes the CVs.
type Definition struct {
	File         string   `json:"file"`
	Family       string   `json:"family"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	ProductIDs   []string `json:"product_ids,omitempty"`
	// NumOuts and NumFns are the model's outputs and functions; -1 when
	// the file does not say.
	NumOuts  int `json:"num_outs"`
	NumFns   int `json:"num_fns"`
	MaxFnNum int `json:"max_fn_num"`
	// ESUFunctionMap is V4 or V5 for ESU decoders, whose function mapping
	// has a layout of its own.
	ESUFunctionMap string         `json:"esu_function_map,omitempty"`
	Programming    Programming    `json:"programming"`
	Variables      []Variable     `json:"variables"`
	CVs            []CV           `json:"cvs"`
	Panes          []Pane         `json:"panes"`
	FunctionLabels []FunctionText `json:"function_labels,omitempty"`
	Resets         []Reset        `json:"resets,omitempty"`
	// Missing lists files the definition includes that the source lacks.
	Missing []string `json:"missing,omitempty"`
	// Problems lists parts of the file Interlock could not use.
	Problems []string `json:"problems,omitempty"`
}

// CV is one CV name the definition uses, with what may be done to it.
// Several variables can share a CV; the flags of all of them apply.
type CV struct {
	Name      string `json:"name"`
	ReadOnly  bool   `json:"read_only,omitempty"`
	WriteOnly bool   `json:"write_only,omitempty"`
	InfoOnly  bool   `json:"info_only,omitempty"`
}

// Variable types.
const (
	TypeDecimal       = "decimal"
	TypeHex           = "hex"
	TypeEnum          = "enum"
	TypeSplit         = "split"
	TypeSplitHex      = "split_hex"
	TypeSplitEnum     = "split_enum"
	TypeSplitText     = "split_text"
	TypeSplitDateTime = "split_date_time"
	TypeSplitHundreds = "split_hundreds"
	TypeSpeedTable    = "speed_table"
	TypeShortAddress  = "short_address"
	TypeLongAddress   = "long_address"
	TypeComposite     = "composite"
	TypeConstant      = "constant"
)

// Variable is one named setting.
type Variable struct {
	// Item is the setting's stable name; panes, qualifiers and the roster
	// refer to it.
	Item    string `json:"item"`
	Label   string `json:"label"`
	Tooltip string `json:"tooltip,omitempty"`
	Comment string `json:"comment,omitempty"`
	Type    string `json:"type"`
	// Parts are the CVs holding the value, least significant first.
	Parts     []Part `json:"parts,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`
	WriteOnly bool   `json:"write_only,omitempty"`
	InfoOnly  bool   `json:"info_only,omitempty"`
	// OpsOnly settings can only be used on the main.
	OpsOnly bool `json:"ops_only,omitempty"`
	// Default is the file's default, in the type's own terms (see the
	// browser's value code); empty when the file gives none.
	Default    string      `json:"default,omitempty"`
	Qualifiers []Qualifier `json:"qualifiers,omitempty"`

	// Numbers: the displayed value is raw * Factor + Offset.
	Min    int64  `json:"min"`
	Max    *int64 `json:"max,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Factor int64  `json:"factor,omitempty"`
	// Modulus is the number of values a radix mask field holds.
	Modulus int `json:"modulus,omitempty"`
	// Case is upper or lower for hex values.
	Case       string            `json:"case,omitempty"`
	Choices    []Choice          `json:"choices,omitempty"`
	Text       *TextOptions      `json:"text,omitempty"`
	DateTime   *DateTimeOptions  `json:"date_time,omitempty"`
	SpeedTable *SpeedTable       `json:"speed_table,omitempty"`
	Composite  []CompositeChoice `json:"composite,omitempty"`
	// AddressChanges lists CVs a decoder may change when its short
	// address is written; they are written again after it.
	AddressChanges []string `json:"address_changes,omitempty"`
}

// Part is one CV's share of a value. A bit mask field is
// (cv & Mask) >> Shift and is Width bits wide; a radix field (Radix > 0)
// is (cv / Radix) % Modulus.
type Part struct {
	CV    string `json:"cv"`
	Mask  uint32 `json:"mask,omitempty"`
	Shift int    `json:"shift,omitempty"`
	Width int    `json:"width,omitempty"`
	Radix int    `json:"radix,omitempty"`
}

// Choice is one value of an enumerated setting.
type Choice struct {
	Label string `json:"label"`
	Value int64  `json:"value"`
	// Group names the group the choice sits in, for long lists.
	Group string `json:"group,omitempty"`
}

// TextOptions configures a text setting stored a byte per CV.
type TextOptions struct {
	// TermByte ends the text when set; PadByte fills the rest.
	TermByte *int   `json:"term_byte,omitempty"`
	PadByte  *int   `json:"pad_byte,omitempty"`
	Match    string `json:"match,omitempty"`
}

// DateTimeOptions configures a date stored as a count of units since a
// base time.
type DateTimeOptions struct {
	Base    string `json:"base"`
	Unit    string `json:"unit"`
	Factor  int64  `json:"factor"`
	Display string `json:"display"`
}

// SpeedTable is a table of speed steps in consecutive CVs.
type SpeedTable struct {
	Entries int `json:"entries"`
	Min     int `json:"min"`
	Max     int `json:"max"`
	// Mfx pins the first and last points to Min and Max.
	Mfx bool `json:"mfx,omitempty"`
}

// CompositeChoice is a preset that sets several other settings.
type CompositeChoice struct {
	Label    string             `json:"label"`
	Settings []CompositeSetting `json:"settings"`
}

// CompositeSetting is one setting a preset changes.
type CompositeSetting struct {
	Item  string `json:"item"`
	Value int64  `json:"value"`
}

// Qualifier relations.
const (
	RelEq    = "eq"
	RelNe    = "ne"
	RelGt    = "gt"
	RelGe    = "ge"
	RelLt    = "lt"
	RelLe    = "le"
	RelNever = "never"
)

// Qualifier makes an element available only while another setting has a
// certain value. Checks on settings the decoder lacks are settled when the
// definition is read: they are dropped, or become "never".
type Qualifier struct {
	Item     string `json:"item,omitempty"`
	Relation string `json:"relation"`
	Value    int64  `json:"value,omitempty"`
}

// FunctionText is a default label for a function key.
type FunctionText struct {
	Num      int    `json:"num"`
	Label    string `json:"label"`
	Lockable bool   `json:"lockable"`
}

// Reset is a factory reset: one write that restores the decoder's
// defaults.
type Reset struct {
	Label string `json:"label"`
	CV    string `json:"cv"`
	Value int    `json:"value"`
}

// modelInfo is what include and exclude lists and the count filters test.
type modelInfo struct {
	family, model, productID string
	numOuts, numFns          int
}

// keys returns the names a model is known by in include and exclude lists.
func (m modelInfo) keys() []string {
	id := m.productID
	if id == "" {
		id = "-1"
	}
	return []string{id, m.model, m.family}
}

// included reports whether an element is kept for the model: its include
// list (joined with inherited ones) must name the model and its exclude
// list must not.
func (m modelInfo) included(include, exclude []string) bool {
	if inc := strings.Join(include, ","); len(include) > 0 && !m.inList(inc) {
		return false
	}
	if exc := strings.Join(exclude, ","); len(exclude) > 0 && m.inList(exc) {
		return false
	}
	return true
}

func (m modelInfo) inList(list string) bool {
	wrapped := "," + list + ","
	for _, key := range m.keys() {
		if strings.Contains(wrapped, ","+key+",") {
			return true
		}
		if strings.Contains(key, ",") {
			for part := range strings.SplitSeq(key, ",") {
				if strings.Contains(wrapped, ","+part+",") {
					return true
				}
			}
		}
	}
	return false
}

// filterLists returns an element's own include and exclude lists, as
// lists to join with inherited ones.
func filterLists(e *element) (include, exclude []string) {
	if v, ok := e.Attr["include"]; ok {
		include = []string{v}
	}
	if v, ok := e.Attr["exclude"]; ok {
		exclude = []string{v}
	}
	return include, exclude
}

// text returns an element's unlocalized text for a name: the first child
// of that name without xml:lang, else the attribute. Interlock's
// interface is English.
func text(e *element, name string) string {
	for _, c := range e.Children {
		if c.Name == name && c.Attr["xml:lang"] == "" {
			return c.Text
		}
	}
	return e.Attr[name]
}

var tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// plain turns the occasional HTML in labels into text.
func plain(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<p>", "\n", "<P>", "\n", "<BR>", "\n").Replace(s)
	s = tagPattern.ReplaceAllString(s, "")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(s)
	return strings.TrimSpace(s)
}

func yes(e *element, name string) bool { return e.Attr[name] == "yes" }

// loadDefinition reads the definition of one model from its family file.
// programmer is the layout file whose panes come first (Comprehensive).
func loadDefinition(t *treeLoader, file, model, programmer string) (*Definition, error) {
	root, err := t.load(path.Join("decoders", file))
	if err != nil {
		return nil, err
	}
	if root.Name != "decoder-config" {
		return nil, fmt.Errorf("%s is not a decoder definition", file)
	}
	dec := root.child("decoder")
	if dec == nil {
		return nil, fmt.Errorf("%s has no decoder", file)
	}
	fam := dec.child("family")
	if fam == nil {
		return nil, fmt.Errorf("%s has no family", file)
	}
	var mod *element
	for _, m := range fam.all("model") {
		if m.attr("model") == model {
			mod = m
			break
		}
	}
	if mod == nil {
		return nil, fmt.Errorf("%s has no model %q", file, model)
	}
	d := &Definition{
		File: file, Family: fam.attr("name"), Manufacturer: fam.attr("mfg"), Model: model,
		NumOuts: number(mod.attr("numOuts"), -1), NumFns: number(mod.attr("numFns"), -1), MaxFnNum: number(mod.attr("maxFnNum"), 28),
	}
	if ids := mod.attr("productID"); ids != "" {
		d.ProductIDs = strings.Split(ids, ",")
	}
	if esu := mod.attr("extFnsESU"); esu != "" && esu != "no" {
		d.ESUFunctionMap = strings.ToUpper(esu)
		if d.ESUFunctionMap == "YES" {
			d.ESUFunctionMap = "V4"
		}
	}
	info := modelInfo{family: d.Family, model: model, productID: mod.attr("productID"), numOuts: d.NumOuts, numFns: d.NumFns}
	d.Programming = readProgramming(dec.child("programming"))

	b := &builder{def: d, info: info, cvIndex: make(map[string]int)}
	if vars := dec.child("variables"); vars != nil {
		b.walkVariables(vars, nil, nil, nil)
	}
	b.resolve()
	d.FunctionLabels = functionLabels(fam, mod)
	d.Resets = b.resets(dec.child("resets"))
	b.outputs = outputs(fam, mod)
	if err := b.panes(t, root, programmer); err != nil {
		d.Problems = append(d.Problems, err.Error())
	}
	d.Missing = t.Missing
	return d, nil
}

// readProgramming reads the programming element: modes and capabilities.
// Capability parameters are positional, as JMRI reads them.
func readProgramming(e *element) Programming {
	p := Programming{Direct: "yes", Paged: true, Register: true}
	if e == nil {
		return p
	}
	if v := e.attr("direct"); v != "" {
		p.Direct = v
	}
	p.Paged = e.attr("paged") != "no"
	p.Register = e.attr("register") != "no"
	p.NoDecoder = yes(e, "nodecoder")
	for _, m := range e.all("mode") {
		p.Modes = append(p.Modes, m.Text)
	}
	for _, c := range e.all("capability") {
		var params []string
		for _, prm := range c.all("parameter") {
			params = append(params, prm.Text)
		}
		param := func(i int) string {
			if i < len(params) {
				return strings.TrimSpace(params[i])
			}
			return ""
		}
		switch text(c, "name") {
		case "Indexed CV access":
			p.Index = &Indexing{
				PI:      number(param(0), 0),
				SI:      number(param(1), 0),
				CVFirst: param(2) != "false",
				Cache:   param(3) != "false",
			}
		case "TCS 4 CV access":
			p.TCS = true
		case "Ops Mode Delayed Programming":
			p.OpsWriteDelay = number(param(0), 500)
		}
	}
	return p
}

// builder collects a definition's variables and CVs.
type builder struct {
	def     *Definition
	info    modelInfo
	cvIndex map[string]int
	outputs []output
}

// walkVariables reads a variables element as JMRI does: its variables,
// then its constants, then nested variables elements, with include and
// exclude lists and qualifiers inherited from the enclosing ones.
func (b *builder) walkVariables(e *element, include, exclude []string, quals []*element) {
	inc, exc := filterLists(e)
	include = append(append([]string(nil), include...), inc...)
	exclude = append(append([]string(nil), exclude...), exc...)
	quals = append(append([]*element(nil), quals...), e.all("qualifier")...)
	for _, v := range e.all("variable") {
		b.variable(v, include, exclude, quals)
	}
	for _, c := range e.all("constant") {
		b.constant(c, include, exclude)
	}
	for _, nested := range e.all("variables") {
		b.walkVariables(nested, include, exclude, quals)
	}
}

// keep applies the count filters and the include and exclude lists.
func (b *builder) keep(e *element, include, exclude []string) bool {
	if n := number(e.attr("minFn"), -1); n >= 0 && b.info.numFns >= 0 && b.info.numFns < n {
		return false
	}
	if n := number(e.attr("minOut"), -1); n >= 0 && b.info.numOuts >= 0 && b.info.numOuts < n {
		return false
	}
	inc, exc := filterLists(e)
	return b.info.included(append(inc, include...), append(exc, exclude...))
}

func (b *builder) problem(format string, args ...any) {
	b.def.Problems = append(b.def.Problems, fmt.Sprintf(format, args...))
}

// valueTypes are the value elements in the order JMRI looks for them.
var valueTypes = []string{"decVal", "hexVal", "enumVal", "compositeVal", "speedTableVal", "longAddressVal", "shortAddressVal", "splitVal", "splitHexVal", "splitHundredsVal", "splitTextVal", "splitDateTimeVal", "splitEnumVal"}

func (b *builder) variable(e *element, include, exclude []string, quals []*element) {
	if !b.keep(e, include, exclude) {
		return
	}
	v := Variable{
		Item: e.attr("item"), Label: plain(text(e, "label")), Tooltip: plain(text(e, "tooltip")), Comment: plain(text(e, "comment")),
		ReadOnly: yes(e, "readOnly"), WriteOnly: yes(e, "writeOnly"), InfoOnly: yes(e, "infoOnly"), OpsOnly: yes(e, "opsOnly"),
		Default: e.attr("default"),
	}
	if v.Item == "" {
		v.Item = v.Label
	}
	if v.Item == "" {
		b.problem("a variable on CV %s has no name", e.attr("CV"))
		return
	}
	if v.Label == "" {
		v.Label = v.Item
	}
	for _, di := range e.all("defaultItem") {
		inc, exc := filterLists(di)
		if b.info.included(inc, exc) {
			v.Default = di.attr("default")
			break
		}
	}
	v.Qualifiers = b.qualifiers(append(append([]*element(nil), quals...), e.all("qualifier")...))
	var val *element
	for _, name := range valueTypes {
		if val = e.child(name); val != nil {
			break
		}
	}
	if val == nil {
		b.problem("%s has no value type", v.Item)
		return
	}
	var cvs []string
	if attr := e.attr("CV"); attr != "" {
		var err error
		if cvs, err = expandCVs(attr); err != nil {
			b.problem("%s: %v", v.Item, err)
			return
		}
	}
	masks := strings.Fields(e.attr("mask"))
	if err := b.valueType(&v, val, cvs, masks); err != nil {
		b.problem("%s: %v", v.Item, err)
		return
	}
	for _, p := range v.Parts {
		b.cv(p.CV, v)
	}
	for _, cv := range v.AddressChanges {
		b.cv(cv, Variable{})
	}
	b.def.Variables = append(b.def.Variables, v)
}

func (b *builder) constant(e *element, include, exclude []string) {
	if !b.keep(e, include, exclude) {
		return
	}
	v := Variable{Item: e.attr("item"), Label: plain(text(e, "label")), Comment: plain(text(e, "comment")), Type: TypeConstant, InfoOnly: true}
	if v.Item == "" {
		return
	}
	if v.Label == "" {
		v.Label = v.Item
	}
	v.Default = strconv.Itoa(number(e.attr("default"), 0))
	b.def.Variables = append(b.def.Variables, v)
}

// cv registers a CV name and merges a variable's access flags into it in
// load order: read-only and write-only stick; info-only clears both.
func (b *builder) cv(name string, v Variable) {
	i, ok := b.cvIndex[name]
	if !ok {
		i = len(b.def.CVs)
		b.cvIndex[name] = i
		b.def.CVs = append(b.def.CVs, CV{Name: name})
	}
	c := &b.def.CVs[i]
	if v.ReadOnly {
		c.ReadOnly = true
	}
	if v.WriteOnly {
		c.WriteOnly = true
	}
	if v.InfoOnly {
		c.InfoOnly, c.ReadOnly, c.WriteOnly = true, false, false
	}
}

func (b *builder) valueType(v *Variable, val *element, cvs, masks []string) error {
	single := func() error {
		if len(cvs) != 1 {
			return fmt.Errorf("needs exactly one CV, has %d", len(cvs))
		}
		return nil
	}
	maskAt := func(i int) string {
		switch {
		case len(masks) == 0:
			return ""
		case i < len(masks):
			return masks[i]
		}
		return masks[len(masks)-1]
	}
	switch val.Name {
	case "decVal", "shortAddressVal", "hexVal":
		if err := single(); err != nil {
			return err
		}
		v.Type, v.Min, v.Factor = TypeDecimal, 0, 1
		maxV := int64(255)
		switch val.Name {
		case "shortAddressVal":
			v.Type, maxV = TypeShortAddress, 127
			for _, c := range val.all("shortAddressChanges") {
				v.AddressChanges = append(v.AddressChanges, c.attr("cv"))
			}
		case "hexVal":
			v.Type = TypeHex
			v.Min = hexNumber(val.attr("min"), 0)
			maxV = hexNumber(val.attr("max"), 255)
		default:
			v.Min = int64(number(val.attr("min"), 0))
			maxV = int64(number(val.attr("max"), 255))
			v.Offset = int64(number(val.attr("offset"), 0))
			v.Factor = int64(number(val.attr("factor"), 1))
		}
		v.Max = &maxV
		mask := maskAt(0)
		if mask == "" && maxV > 255 {
			mask = strings.Repeat("V", bits.Len64(uint64(maxV)))
		}
		part, err := makePart(cvs[0], mask)
		if err != nil {
			return err
		}
		if part.Radix > 0 {
			v.Modulus = int(maxV) + 1
		}
		v.Parts = []Part{part}
	case "enumVal":
		if err := single(); err != nil {
			return err
		}
		v.Type = TypeEnum
		v.Choices = b.choices(val, "")
		v.Modulus = number(val.attr("max"), countChoices(val))
		part, err := makePart(cvs[0], maskAt(0))
		if err != nil {
			return err
		}
		v.Parts = []Part{part}
	case "splitVal", "splitHexVal", "splitEnumVal", "splitTextVal", "splitDateTimeVal", "splitHundredsVal":
		v.Type = map[string]string{
			"splitVal": TypeSplit, "splitHexVal": TypeSplitHex, "splitEnumVal": TypeSplitEnum,
			"splitTextVal": TypeSplitText, "splitDateTimeVal": TypeSplitDateTime, "splitHundredsVal": TypeSplitHundreds,
		}[val.Name]
		names := cvs
		partMasks := make([]string, len(cvs))
		for i := range cvs {
			partMasks[i] = maskAt(i)
		}
		if len(cvs) == 1 {
			if high := val.attr("highCV"); high != "" {
				names = append(names, high)
				partMasks = append(partMasks, val.attr("upperMask"))
			}
		}
		if len(names) == 0 {
			return errors.New("no CVs")
		}
		width := 0
		for i, name := range names {
			part, err := makePart(name, partMasks[i])
			if err != nil {
				return err
			}
			if part.Radix > 0 {
				return errors.New("split values need bit masks")
			}
			width += part.Width
			v.Parts = append(v.Parts, part)
		}
		if width > 64 && v.Type != TypeSplitText {
			return fmt.Errorf("%d bits is more than 64", width)
		}
		v.Factor = 1
		switch v.Type {
		case TypeSplit, TypeSplitHundreds:
			v.Min = int64(number(val.attr("min"), 0))
			if m := val.attr("max"); m != "" {
				maxV := int64(number(m, 0))
				v.Max = &maxV
			}
			v.Offset = int64(number(val.attr("offset"), 0))
			v.Factor = int64(number(val.attr("factor"), 1))
		case TypeSplitHex:
			v.Min = hexNumber(val.attr("min"), 0)
			if m := val.attr("max"); m != "" {
				maxV := hexNumber(m, 0)
				v.Max = &maxV
			}
			v.Case = val.attr("case")
		case TypeSplitEnum:
			v.Choices = b.choices(val, "")
		case TypeSplitText:
			v.Text = &TextOptions{Match: val.attr("match"), TermByte: optionalByte(val, "termByte"), PadByte: optionalByte(val, "padByte")}
		case TypeSplitDateTime:
			v.ReadOnly = true
			v.DateTime = &DateTimeOptions{
				Base: orDefault(val.attr("base"), "2000-01-01T00:00:00"), Unit: orDefault(val.attr("unit"), "Seconds"),
				Factor: int64(number(val.attr("factor"), 1)), Display: orDefault(val.attr("display"), "default"),
			}
		}
	case "speedTableVal":
		if err := single(); err != nil {
			return err
		}
		start, err := cvNumber(cvs[0])
		if err != nil {
			return err
		}
		st := &SpeedTable{Entries: number(val.attr("entries"), 28), Min: number(val.attr("min"), 0), Max: number(val.attr("max"), 255), Mfx: val.attr("mfx") == "true"}
		if st.Entries < 1 || start+st.Entries-1 > MaxCV {
			return fmt.Errorf("a speed table of %d entries from CV %d does not fit", st.Entries, start)
		}
		v.Type, v.SpeedTable = TypeSpeedTable, st
		for i := range st.Entries {
			v.Parts = append(v.Parts, Part{CV: strconv.Itoa(start + i), Mask: 0xFF, Width: 8})
		}
	case "longAddressVal":
		if err := single(); err != nil {
			return err
		}
		v.Type = TypeLongAddress
		v.Parts = []Part{{CV: cvs[0], Mask: 0xFF, Width: 8}, {CV: "18", Mask: 0xFF, Width: 8}}
	case "compositeVal":
		v.Type = TypeComposite
		v.Composite = b.compositeChoices(val)
	default:
		return fmt.Errorf("unknown value type %s", val.Name)
	}
	return nil
}

// makePart reads a mask for one CV: a V/X string (rightmost character is
// bit 0) or a decimal radix.
func makePart(cv, mask string) (Part, error) {
	if mask == "" {
		mask = "VVVVVVVV"
	}
	if c := mask[0]; c != 'V' && c != 'X' {
		r, err := strconv.Atoi(mask)
		if err != nil || r < 1 {
			return Part{}, fmt.Errorf("mask %q is neither bits nor a radix", mask)
		}
		return Part{CV: cv, Radix: r}, nil
	}
	if len(mask) > 32 {
		return Part{}, fmt.Errorf("mask %q is longer than 32 bits", mask)
	}
	p := Part{CV: cv}
	first, last := strings.IndexByte(mask, 'V'), strings.LastIndexByte(mask, 'V')
	for i := range len(mask) {
		if mask[i] == 'V' {
			p.Mask |= 1 << (len(mask) - 1 - i)
		}
	}
	if last >= 0 {
		p.Shift = len(mask) - 1 - last
		p.Width = last - first + 1
	}
	return p, nil
}

// choices builds an enum's choice list: groups are walked in order,
// choices excluded for the model are skipped without using up a value,
// and a choice without a value takes the one after the last kept.
func (b *builder) choices(e *element, group string) []Choice {
	var out []Choice
	next := int64(0)
	var walk func(e *element, group string)
	walk = func(e *element, group string) {
		for _, c := range e.Children {
			if c.Name != "enumChoice" && c.Name != "enumChoiceGroup" {
				continue
			}
			inc, exc := filterLists(c)
			if !b.info.included(inc, exc) {
				continue
			}
			if c.Name == "enumChoiceGroup" {
				name := text(c, "name")
				g := group
				if name != "" {
					g = strings.TrimPrefix(group+" › "+name, " › ")
				}
				walk(c, g)
				continue
			}
			value := next
			if s, ok := c.Attr["value"]; ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
					value = n
				}
			}
			out = append(out, Choice{Label: plain(text(c, "choice")), Value: value, Group: group})
			next = value + 1
		}
	}
	walk(e, group)
	return out
}

// countChoices counts an enum's choices before filtering, the modulus of a
// radix mask when the enum gives none.
func countChoices(e *element) int {
	n := 0
	for _, c := range e.Children {
		switch c.Name {
		case "enumChoice":
			n++
		case "enumChoiceGroup":
			n += countChoices(c)
		}
	}
	return n
}

func (b *builder) compositeChoices(e *element) []CompositeChoice {
	var out []CompositeChoice
	var walk func(e *element)
	walk = func(e *element) {
		for _, c := range e.Children {
			inc, exc := filterLists(c)
			if (c.Name == "compositeChoice" || c.Name == "compositeChoiceGroup") && !b.info.included(inc, exc) {
				continue
			}
			switch c.Name {
			case "compositeChoiceGroup":
				walk(c)
			case "compositeChoice":
				cc := CompositeChoice{Label: plain(text(c, "choice"))}
				for _, s := range c.all("compositeSetting") {
					v, err := strconv.ParseInt(strings.TrimSpace(s.attr("value")), 10, 64)
					if err != nil {
						continue
					}
					cc.Settings = append(cc.Settings, CompositeSetting{Item: s.attr("label"), Value: v})
				}
				out = append(out, cc)
			}
		}
	}
	walk(e)
	return out
}

// qualifiers reads qualifier elements; the settings they watch are
// resolved once every variable is known.
func (b *builder) qualifiers(els []*element) []Qualifier {
	var out []Qualifier
	for _, q := range els {
		ref, rel, val := q.child("variableref"), q.child("relation"), q.child("value")
		if ref == nil || rel == nil || val == nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(val.Text), 10, 64)
		if err != nil {
			b.problem("qualifier on %s: value %q is not a number", ref.Text, val.Text)
			continue
		}
		out = append(out, Qualifier{Item: strings.TrimSpace(ref.Text), Relation: strings.TrimSpace(rel.Text), Value: n})
	}
	return out
}

// find returns the variable a name refers to: by item, else by label, the
// first in load order.
func (b *builder) find(name string) (*Variable, bool) {
	for i := range b.def.Variables {
		if b.def.Variables[i].Item == name {
			return &b.def.Variables[i], true
		}
	}
	for i := range b.def.Variables {
		if b.def.Variables[i].Label == name {
			return &b.def.Variables[i], true
		}
	}
	return nil, false
}

// resolveQualifiers points qualifiers at the variables they watch and
// settles the ones that need no value: "exists" checks, and checks on
// settings the decoder lacks, which hold.
func (b *builder) resolveQualifiers(qs []Qualifier) []Qualifier {
	var out []Qualifier
	for _, q := range qs {
		v, found := b.find(q.Item)
		if q.Relation == "exists" {
			if found != (q.Value != 0) {
				out = append(out, Qualifier{Relation: RelNever})
			}
			continue
		}
		if !found {
			continue
		}
		switch q.Relation {
		case RelEq, RelNe, RelGt, RelGe, RelLt, RelLe:
			out = append(out, Qualifier{Item: v.Item, Relation: q.Relation, Value: q.Value})
		default:
			b.problem("qualifier on %s: unknown relation %q", q.Item, q.Relation)
		}
	}
	return out
}

// resolve finishes the variables once all are loaded: qualifier targets
// and composite settings by item.
func (b *builder) resolve() {
	for i := range b.def.Variables {
		v := &b.def.Variables[i]
		v.Qualifiers = b.resolveQualifiers(v.Qualifiers)
		for j := range v.Composite {
			kept := v.Composite[j].Settings[:0]
			for _, s := range v.Composite[j].Settings {
				if t, ok := b.find(s.Item); ok {
					s.Item = t.Item
					kept = append(kept, s)
				}
			}
			v.Composite[j].Settings = kept
		}
	}
}

func (b *builder) resets(e *element) []Reset {
	if e == nil {
		return nil
	}
	var out []Reset
	for _, r := range e.all("factReset") {
		label := plain(text(r, "label"))
		if _, err := b.def.Programming.Resolve(r.attr("CV")); err != nil {
			b.problem("reset %q: %v", label, err)
			continue
		}
		out = append(out, Reset{Label: label, CV: r.attr("CV"), Value: number(r.attr("default"), 0)})
	}
	return out
}

// functionLabels returns the default function labels, the model's over
// the family's.
func functionLabels(fam, mod *element) []FunctionText {
	byNum := map[int]FunctionText{}
	for _, src := range []*element{fam, mod} {
		for _, fl := range src.all("functionlabels") {
			for _, l := range fl.all("functionlabel") {
				n := number(l.attr("num"), -1)
				if n < 0 {
					continue
				}
				label := text(l, "text")
				if label == "" {
					label = l.Text
				}
				byNum[n] = FunctionText{Num: n, Label: label, Lockable: l.attr("lockable") != "false"}
			}
		}
	}
	out := make([]FunctionText, 0, len(byNum))
	for n := range 69 {
		if fl, ok := byNum[n]; ok {
			out = append(out, fl)
		}
	}
	return out
}

func hexNumber(s string, def int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 16, 64)
	if err != nil {
		return def
	}
	return n
}

// optionalByte reads termByte or padByte: absent means 0, empty means
// none.
func optionalByte(e *element, name string) *int {
	s, ok := e.Attr[name]
	if !ok {
		zero := 0
		return &zero
	}
	if s == "" {
		return nil
	}
	n := number(s, 0) & 0xFF
	return &n
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
