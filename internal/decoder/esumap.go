package decoder

import (
	"fmt"
	"strconv"
	"strings"
)

// ESUMap is an ESU decoder's function mapping: a table of rows, each of
// which switches its outputs, logical functions and sounds on while all
// of its conditions hold. Every row has the same four blocks of items
// (conditions, physical outputs, logical functions, sounds), packed into
// CVs behind the index CVs 31 and 32. The browser works out each item's
// CV and bits from the blocks.
type ESUMap struct {
	Rows   int        `json:"rows"`
	Blocks []ESUBlock `json:"blocks"`
}

// ESUBlock is one block of every row. Row r's share of it starts at CV
// Start + 16 * (r % 16) on index page Page + r / 16 (CV 31 = 16, CV 32 =
// the page), with Bits bits per item, packed from bit 0 up.
type ESUBlock struct {
	Name  string    `json:"name"`
	Bits  int       `json:"bits"`
	Page  int       `json:"page"`
	Start int       `json:"start"`
	Items []ESUItem `json:"items"`
}

// ESUItem is one condition or output. A hidden item still takes its bits.
type ESUItem struct {
	Name string `json:"name"`
	// On and Off name a condition's two states ("Moving", "Stopped");
	// empty for plain on and off.
	On     string `json:"on,omitempty"`
	Off    string `json:"off,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// esuIndexValue is CV 31's value for the mapping pages.
const esuIndexValue = 16

// CV returns the CV name ("16.3.257") and mask of one item of a row.
func (m *ESUMap) CV(row, block, item int) (string, uint32) {
	b := m.Blocks[block]
	bit := item * b.Bits
	page := b.Page + row/16
	cv := b.Start + (16*row+bit/8)%256
	mask := uint32(1<<b.Bits-1) << (bit % 8)
	return fmt.Sprintf("%d.%d.%d", esuIndexValue, page, cv), mask
}

// CVs returns every CV of the map that holds an item shown, row by row,
// each once.
func (m *ESUMap) CVs() []string {
	var out []string
	seen := map[string]bool{}
	for r := range m.Rows {
		for bi, b := range m.Blocks {
			for i, it := range b.Items {
				if it.Hidden {
					continue
				}
				if cv, _ := m.CV(r, bi, i); !seen[cv] {
					seen[cv] = true
					out = append(out, cv)
				}
			}
		}
	}
	return out
}

// esuMap lays out the mapping of a V4 or V5 decoder. numFns, when set,
// is the number of rows the model has; numOuts the physical outputs it
// has, so the AUX outputs past them are left out. outs are the family's
// and the model's output elements, which name and hide items.
func esuMap(version string, numFns, numOuts int, outs []output) (*ESUMap, error) {
	var (
		fns, auxes, logic, sounds, rows int
		reserved                        bool
		pages, starts                   [4]int
	)
	switch version {
	case "V4":
		fns, auxes, logic, sounds, rows = 29, 10, 16, 24, 40
		pages, starts = [4]int{2, 2, 2, 2}, [4]int{257, 266, 268, 270}
	case "V5":
		fns, auxes, logic, sounds, rows = 32, 18, 24, 32, 72
		reserved = true
		pages, starts = [4]int{3, 8, 8, 8}, [4]int{257, 257, 260, 263}
	default:
		return nil, fmt.Errorf("unknown ESU function map %q", version)
	}
	if numFns > 0 {
		rows = numFns
	}

	conditions := []ESUItem{{Name: "Motion", On: "Moving", Off: "Stopped"}, {Name: "Direction", On: "Forward", Off: "Reverse"}}
	for i := range fns {
		conditions = append(conditions, ESUItem{Name: "F" + strconv.Itoa(i)})
	}
	conditions = append(conditions, ESUItem{Name: "Wheel sensor"})
	if reserved {
		conditions = append(conditions, ESUItem{Name: "Reserved"})
	}
	for i := 1; i <= 4; i++ {
		conditions = append(conditions, ESUItem{Name: "Sensor " + strconv.Itoa(i)})
	}

	// Each light output has two configurations, [1] and [2]; only the
	// headlight, rear light, AUX1 and AUX2 have a second.
	physical := []ESUItem{{Name: "Headlight [1]"}, {Name: "Rear light [1]"}}
	for i := 1; i <= auxes; i++ {
		name := "AUX" + strconv.Itoa(i)
		if i <= 2 {
			name += " [1]"
		}
		physical = append(physical, ESUItem{Name: name})
	}
	physical = append(physical, ESUItem{Name: "Headlight [2]"}, ESUItem{Name: "Rear light [2]"}, ESUItem{Name: "AUX1 [2]"}, ESUItem{Name: "AUX2 [2]"})

	logical := make([]ESUItem, logic)
	for i := range logical {
		logical[i] = ESUItem{Name: "Logical function " + strconv.Itoa(i+1)}
	}
	sound := make([]ESUItem, sounds)
	for i := range sound {
		sound[i] = ESUItem{Name: "Sound slot " + strconv.Itoa(i+1)}
	}

	m := &ESUMap{Rows: rows}
	for i, items := range [][]ESUItem{conditions, physical, logical, sound} {
		bits := 1
		if i == 0 {
			bits = 2
		}
		name := [...]string{"Conditions", "Physical outputs", "Logical functions", "Sounds"}[i]
		m.Blocks = append(m.Blocks, ESUBlock{Name: name, Bits: bits, Page: pages[i], Start: starts[i], Items: items})
	}
	m.label(outs)
	// The model lacks the outputs past its count; their second
	// configurations stay.
	if numOuts >= 0 {
		for i := numOuts; i < 2+auxes; i++ {
			m.Blocks[1].Items[i].Hidden = true
		}
	}
	return m, nil
}

// label applies the output elements: name "n" is the nth item counting
// through every block, "b,i" item i of block b, and any other name
// renames the item at that output's position in the list. A label is
// "name|on|off"; "|" hides the item.
func (m *ESUMap) label(outs []output) {
	var items []*ESUItem
	var starts []int
	for bi := range m.Blocks {
		starts = append(starts, len(items))
		for i := range m.Blocks[bi].Items {
			items = append(items, &m.Blocks[bi].Items[i])
		}
	}
	at := func(i int) *ESUItem {
		if i < 0 || i >= len(items) {
			return nil
		}
		return items[i]
	}
	for pos, o := range outs {
		if o.label == "" {
			continue
		}
		if n, err := strconv.Atoi(o.name); err == nil {
			setESULabel(at(n-1), o.label)
			continue
		}
		if b, i, ok := strings.Cut(o.name, ","); ok {
			bn, err1 := strconv.Atoi(b)
			in, err2 := strconv.Atoi(i)
			if err1 == nil && err2 == nil {
				if bn >= 1 && bn <= len(starts) {
					setESULabel(at(starts[bn-1]+in-1), o.label)
				}
				continue
			}
		}
		if it := at(pos); it != nil {
			it.On, it.Off = "", ""
			setESULabel(it, o.name+"|"+o.label)
		}
	}
}

// setESULabel reads a "name|on|off" label as JMRI does: empty trailing
// parts are dropped, so "name|" keeps the item's off text.
func setESULabel(it *ESUItem, label string) {
	if it == nil {
		return
	}
	if label == "|" {
		*it = ESUItem{Hidden: true}
		return
	}
	parts := strings.Split(label, "|")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	switch {
	case len(parts) == 0:
		it.Name, it.On = "", ""
	case len(parts) == 1:
		it.Name, it.On = parts[0], ""
	case len(parts) == 2:
		it.Name, it.On, it.Off = parts[0], parts[1], ""
	default:
		it.Name, it.On, it.Off = parts[0], parts[1], parts[2]
	}
	it.Hidden = it.Name == ""
}
