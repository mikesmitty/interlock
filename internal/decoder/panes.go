package decoder

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// DefaultProgrammer is the layout whose panes a definition starts from;
// a decoder's own panes replace the ones with the same name and follow
// the rest.
const DefaultProgrammer = "Comprehensive.xml"

// Pane is one page of settings.
type Pane struct {
	Name       string      `json:"name"`
	Tooltip    string      `json:"tooltip,omitempty"`
	Qualifiers []Qualifier `json:"qualifiers,omitempty"`
	Nodes      []Node      `json:"nodes"`
	// Items lists the settings the pane reads and writes, in order.
	Items []string `json:"items"`
	// AllCVs is set on the CV table pane, which reads and writes every CV.
	AllCVs bool `json:"all_cvs,omitempty"`
}

// Node kinds.
const (
	NodeColumn    = "column"
	NodeRow       = "row"
	NodeGroup     = "group"
	NodeGrid      = "grid"
	NodeGridItem  = "griditem"
	NodeDisplay   = "display"
	NodeLabel     = "label"
	NodeSeparator = "separator"
	NodeAddress   = "dccaddress"
	NodeFnMap     = "fnmap"
	NodeESUFnMap  = "esu_fnmap"
	NodeCVTable   = "cvtable"
)

// Node is one element of a pane's layout.
type Node struct {
	Kind     string `json:"kind"`
	Children []Node `json:"children,omitempty"`
	// Item is the setting a display shows.
	Item string `json:"item,omitempty"`
	// Label replaces the setting's label when set, even to "".
	Label   *string `json:"label,omitempty"`
	Tooltip string  `json:"tooltip,omitempty"`
	// Format is how a setting is shown: checkbox, radiobuttons,
	// onradiobutton, offradiobutton, hslider, vslider, hslider-percent,
	// tree, or empty for the type's usual control.
	Format string `json:"format,omitempty"`
	// Layout places the label: left (empty), right, above or below.
	Layout   string `json:"layout,omitempty"`
	ViewOnly bool   `json:"view_only,omitempty"`
	// Text is a label's text.
	Text       string      `json:"text,omitempty"`
	Qualifiers []Qualifier `json:"qualifiers,omitempty"`
	Grid       *GridPos    `json:"grid,omitempty"`
	FnMap      *FnMap      `json:"fnmap,omitempty"`
	ESUMap     *ESUMap     `json:"esu_map,omitempty"`
	// Items are the address settings a dccaddress node edits.
	Items []string `json:"items,omitempty"`
}

// GridPos places a grid item; a width or height of 0 means the rest of
// the row or column.
type GridPos struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// FnMap is a function mapping matrix: which functions switch which
// outputs. Each cell lists the settings that live there (usually one).
type FnMap struct {
	Columns []FnColumn `json:"columns"`
	Rows    []FnRow    `json:"rows"`
}

// FnColumn is an output.
type FnColumn struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// FnRow is a function input; Cells line up with the map's columns.
type FnRow struct {
	Label string     `json:"label"`
	Cells [][]string `json:"cells"`
}

// output is a family's or model's output label.
type output struct{ name, label string }

func outputs(fam, mod *element) []output {
	var out []output
	var walk func(e *element)
	walk = func(e *element) {
		for _, c := range e.Children {
			switch c.Name {
			case "output":
				out = append(out, output{name: c.attr("name"), label: text(c, "label")})
			case "outputs":
				walk(c)
			}
		}
	}
	walk(fam)
	walk(mod)
	return out
}

// paneBuild collects one pane's read and write list as its nodes are
// built.
type paneBuild struct {
	b      *builder
	items  []string
	seen   map[string]bool
	allCVs bool
	// nameFmtItem labels displays with the item text.
	nameFmtItem bool
}

func (pb *paneBuild) use(item string) {
	if !pb.seen[item] {
		pb.seen[item] = true
		pb.items = append(pb.items, item)
	}
}

func firstName(p *element) string {
	for _, c := range p.Children {
		if c.Name == "name" {
			return c.Text
		}
	}
	return ""
}

// panes lays out the definition's panes: the programmer's, with the
// decoder's own panes replacing those of the same name, then the decoder's
// other panes. Panes with nothing to read or write are left out, except a
// decoder's own extra panes, which often hold notes.
func (b *builder) panes(t *treeLoader, root *element, programmer string) error {
	var progPanes []*element
	decoderPanes := true
	showEmpty := false
	var err error
	if programmer != "" {
		var pr *element
		pr, err = t.load(path.Join("programmers", programmer))
		if err == nil {
			if prog := pr.child("programmer"); prog != nil {
				decoderPanes = prog.attr("decoderFilePanes") == "yes"
				showEmpty = prog.attr("showEmptyPanes") == "yes"
				progPanes = prog.all("pane")
			}
		} else {
			err = fmt.Errorf("programmer layout: %w", err)
		}
	}
	switch root.attr("showEmptyPanes") {
	case "yes":
		showEmpty = true
	case "no":
		showEmpty = false
	}
	var decPanes []*element
	if decoderPanes {
		decPanes = root.all("pane")
	}
	used := make([]bool, len(decPanes))
	for _, p := range progPanes {
		pane := p
		for i := len(decPanes) - 1; i >= 0; i-- {
			if !used[i] && firstName(decPanes[i]) == firstName(p) {
				pane, used[i] = decPanes[i], true
			}
		}
		b.addPane(pane, false, showEmpty)
	}
	for i, d := range decPanes {
		if !used[i] {
			b.addPane(d, true, showEmpty)
		}
	}
	return err
}

func (b *builder) addPane(e *element, forced, showEmpty bool) {
	inc, exc := filterLists(e)
	if !b.info.included(inc, exc) {
		return
	}
	pb := &paneBuild{b: b, items: []string{}, seen: map[string]bool{}, nameFmtItem: e.attr("nameFmt") == "item"}
	var nodes []Node
	// JMRI places a pane's columns first, then rows, grids and groups.
	for _, kind := range []string{"column", "row", "grid", "group"} {
		for _, c := range e.all(kind) {
			if n, ok := pb.node(c); ok {
				nodes = append(nodes, n)
			}
		}
	}
	empty := len(pb.items) == 0 && !pb.allCVs && !hasKind(nodes, NodeESUFnMap)
	if empty && !forced && !showEmpty {
		return
	}
	name := text(e, "name")
	if name == "" {
		name = "Settings"
	}
	b.def.Panes = append(b.def.Panes, Pane{
		Name: name, Tooltip: plain(text(e, "tooltip")), Qualifiers: b.resolveQualifiers(b.qualifiers(e.all("qualifier"))),
		Nodes: nodes, Items: pb.items, AllCVs: pb.allCVs,
	})
}

func hasKind(nodes []Node, kind string) bool {
	for _, n := range nodes {
		if n.Kind == kind || hasKind(n.Children, kind) {
			return true
		}
	}
	return false
}

// node builds one layout element; ok is false when it shows nothing for
// this model.
func (pb *paneBuild) node(e *element) (Node, bool) {
	b := pb.b
	quals := func() []Qualifier { return b.resolveQualifiers(b.qualifiers(e.all("qualifier"))) }
	switch e.Name {
	case "column", "row", "group", "griditem":
		if e.Name == "group" {
			inc, exc := filterLists(e)
			if !b.info.included(inc, exc) {
				return Node{}, false
			}
		}
		n := Node{Kind: e.Name, Qualifiers: quals()}
		n.Children = pb.content(e)
		return n, len(n.Children) > 0
	case "grid":
		n := Node{Kind: NodeGrid, Qualifiers: quals()}
		g := &gridState{lastX: -1, lastY: -1}
		n.Children = pb.gridItems(e, e, g, nil)
		return n, len(n.Children) > 0
	case "display":
		v, ok := b.findDisplay(e.attr("item"))
		if !ok {
			return Node{}, false
		}
		n := Node{Kind: NodeDisplay, Item: v.Item, Format: e.attr("format"), Layout: e.attr("layout"), ViewOnly: yes(e, "viewOnly"), Tooltip: plain(text(e, "tooltip"))}
		if n.Layout == "left" {
			n.Layout = ""
		}
		if n.Format == "default" {
			n.Format = ""
		}
		if _, ok := e.Attr["label"]; ok || e.child("label") != nil {
			l := plain(text(e, "label"))
			n.Label = &l
		} else if pb.nameFmtItem {
			l := e.attr("item")
			n.Label = &l
		}
		pb.use(v.Item)
		return n, true
	case "label":
		s := ""
		if t := e.child("text"); t != nil {
			s = text(e, "text")
		}
		if s == "" {
			s = e.attr("text")
		}
		if s == "" {
			s = text(e, "label")
		}
		return Node{Kind: NodeLabel, Text: plain(s), Qualifiers: quals()}, true
	case "separator":
		return Node{Kind: NodeSeparator}, true
	case "dccaddress":
		n := Node{Kind: NodeAddress}
		for _, item := range []string{"Short Address", "Address Format", "Long Address", "Consist Address"} {
			if v, ok := b.find(item); ok {
				n.Items = append(n.Items, v.Item)
				pb.use(v.Item)
			}
		}
		return n, len(n.Items) > 0
	case "fnmapping":
		if b.def.ESUFunctionMap != "" {
			m, err := esuMap(b.def.ESUFunctionMap, b.info.numFns, b.info.numOuts, b.outputs)
			if err != nil {
				b.def.Problems = append(b.def.Problems, err.Error())
				return Node{}, false
			}
			for _, cv := range m.CVs() {
				b.cv(cv, Variable{})
			}
			return Node{Kind: NodeESUFnMap, ESUMap: m}, true
		}
		m := b.fnMap()
		if m == nil {
			return Node{}, false
		}
		for _, r := range m.Rows {
			for _, cell := range r.Cells {
				for _, item := range cell {
					pb.use(item)
				}
			}
		}
		return Node{Kind: NodeFnMap, FnMap: m}, true
	case "cvtable":
		pb.allCVs = true
		return Node{Kind: NodeCVTable}, true
	}
	return Node{}, false
}

// content builds a container's children in document order.
func (pb *paneBuild) content(e *element) []Node {
	var out []Node
	for _, c := range e.Children {
		if c.Name == "qualifier" {
			continue
		}
		if n, ok := pb.node(c); ok {
			out = append(out, n)
		}
	}
	return out
}

// gridState tracks the previous grid item, which NEXT and CURRENT
// positions count from.
type gridState struct{ lastX, lastY int }

// gridItems builds a grid's items. A group inside a grid only filters and
// qualifies its items; they are placed in the grid itself.
func (pb *paneBuild) gridItems(grid, e *element, g *gridState, inherited []Qualifier) []Node {
	b := pb.b
	var out []Node
	for _, c := range e.Children {
		switch c.Name {
		case "group":
			inc, exc := filterLists(c)
			quals := append(append([]Qualifier(nil), inherited...), b.resolveQualifiers(b.qualifiers(c.all("qualifier")))...)
			if b.info.included(inc, exc) {
				out = append(out, pb.gridItems(grid, c, g, quals)...)
			} else {
				pb.skipGridItems(grid, c, g)
			}
		case "griditem":
			pos := g.place(grid, c)
			n, ok := pb.node(c)
			if !ok {
				continue
			}
			n.Grid = &pos
			n.Qualifiers = append(append([]Qualifier(nil), inherited...), n.Qualifiers...)
			out = append(out, n)
		}
	}
	return out
}

// skipGridItems advances the grid positions past an excluded group's
// items, which still count as placed.
func (pb *paneBuild) skipGridItems(grid, e *element, g *gridState) {
	for _, c := range e.Children {
		switch c.Name {
		case "group":
			pb.skipGridItems(grid, c, g)
		case "griditem":
			g.place(grid, c)
		}
	}
}

// place resolves a grid item's position from the grid's attributes, then
// its own.
func (g *gridState) place(grid, item *element) GridPos {
	attr := func(name string) string {
		if v, ok := item.Attr[name]; ok {
			return v
		}
		return grid.attr(name)
	}
	coord := func(name string, last int) int {
		switch v := strings.TrimSpace(attr(name)); v {
		case "", "NEXT", "RELATIVE":
			return last + 1
		case "CURRENT":
			return max(last, 0)
		default:
			n, err := strconv.Atoi(v)
			if err != nil {
				return last + 1
			}
			return n
		}
	}
	span := func(name string) int {
		switch v := strings.TrimSpace(attr(name)); v {
		case "REMAINDER":
			return 0
		case "", "RELATIVE":
			return 1
		default:
			return max(number(v, 1), 1)
		}
	}
	x := coord("gridx", g.lastX)
	y := g.lastY
	if attr("gridy") != "" || attr("gridx") != "" {
		y = coord("gridy", g.lastY)
	} else {
		y = max(y, 0)
	}
	g.lastX, g.lastY = x, y
	return GridPos{X: x, Y: y, W: span("gridwidth"), H: span("gridheight")}
}

// findDisplay finds the variable a display names: by item, by label, or
// by "CV" and its CV.
func (b *builder) findDisplay(name string) (*Variable, bool) {
	if v, ok := b.find(name); ok {
		return v, true
	}
	if cv, ok := strings.CutPrefix(name, "CV"); ok {
		for i := range b.def.Variables {
			v := &b.def.Variables[i]
			if len(v.Parts) > 0 && v.Parts[0].CV == cv {
				return v, true
			}
		}
	}
	return nil, false
}

// fnMap builds the standard function map from the variables named
// "<function> controls output <output>" and the model's outputs.
func (b *builder) fnMap() *FnMap {
	const maxOut = 40
	numOut := b.info.numOuts
	if numOut < 0 {
		numOut = 20
	}
	names := make([]string, maxOut+1)
	labels := make([]string, maxOut+1)
	for i := 1; i <= maxOut; i++ {
		names[i] = strconv.Itoa(i)
	}
	copy(labels[1:], []string{"White", "Yellow", "Green", "Vlt/Brwn"})
	for _, o := range b.outputs {
		if n, err := strconv.Atoi(o.name); err == nil {
			if n < 1 || n > maxOut {
				continue
			}
			switch {
			case o.label == "|":
				names[n], labels[n] = "", ""
			case strings.Contains(o.label, "|"):
				names[n], labels[n], _ = strings.Cut(o.label, "|")
			case o.label != "":
				labels[n] = o.label
			}
			numOut = max(numOut, n)
		} else if numOut < maxOut {
			numOut++
			names[numOut], labels[numOut] = o.name, o.label
		}
	}
	numOut = min(numOut, maxOut)

	type column struct {
		index       int
		name, label string
	}
	var cols []column
	for i := 1; i <= numOut; i++ {
		if names[i] != "" || labels[i] != "" {
			cols = append(cols, column{i, names[i], labels[i]})
		}
	}
	var bases []string
	bases = append(bases, "STOP", "DRIVE", "FWD", "REV", "FL")
	for i := 0; i <= 28; i++ {
		bases = append(bases, "F"+strconv.Itoa(i))
	}
	for i := 0; i <= 28; i++ {
		bases = append(bases, "S"+strconv.Itoa(i))
	}
	m := &FnMap{}
	usedCol := make([]bool, len(cols))
	var rows [][][]string
	var rowLabels []string
	for _, base := range bases {
		if b.info.numFns >= 0 && len(rows) >= b.info.numFns {
			break
		}
		for _, suffix := range []string{"", "(f)", "(r)"} {
			fn := base + suffix
			cells := make([][]string, len(cols))
			found := false
			for ci, c := range cols {
				var candidates []string
				if c.name != strconv.Itoa(c.index) {
					candidates = append(candidates, fn+" controls output "+strconv.Itoa(c.index), fn+" controls output "+strconv.Itoa(c.index)+"(alt)")
				}
				candidates = append(candidates, fn+" controls output "+c.name, fn+" controls output "+c.name+"(alt)")
				for _, cand := range candidates {
					if v, ok := b.find(cand); ok {
						cells[ci] = append(cells[ci], v.Item)
						found, usedCol[ci] = true, true
					}
				}
			}
			if found {
				rows = append(rows, cells)
				rowLabels = append(rowLabels, fnRowLabel(base, suffix))
			}
		}
	}
	if len(rows) == 0 {
		return nil
	}
	for ci, c := range cols {
		if usedCol[ci] {
			m.Columns = append(m.Columns, FnColumn{Name: c.name, Label: c.label})
		}
	}
	for ri, cells := range rows {
		row := FnRow{Label: rowLabels[ri]}
		for ci := range cols {
			if usedCol[ci] {
				row.Cells = append(row.Cells, cells[ci])
			}
		}
		m.Rows = append(m.Rows, row)
	}
	return m
}

func fnRowLabel(base, suffix string) string {
	tail := ""
	switch suffix {
	case "(f)":
		tail = " (forward)"
	case "(r)":
		tail = " (reverse)"
	}
	switch {
	case base == "FL" && suffix == "(f)":
		return "Headlight F0 (forward)"
	case base == "FL" && suffix == "(r)":
		return "Headlight F0 (reverse)"
	case base == "STOP":
		return "Stopped" + tail
	case base == "DRIVE":
		return "Moving" + tail
	case base == "FWD":
		return "Forward" + tail
	case base == "REV":
		return "Reverse" + tail
	case strings.HasPrefix(base, "F") && base != "FL":
		return "Function " + base[1:] + tail
	case strings.HasPrefix(base, "S"):
		return "Sensor " + base[1:] + tail
	}
	return base + suffix
}
