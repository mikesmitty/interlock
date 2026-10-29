package withrottle

import (
	"strconv"
	"strings"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/train"
)

// The protocol's delimiters: between list items, between an item's
// fields, and between a throttle command's key and its action.
const (
	listSep  = "]\\["
	fieldSep = "}|{"
	keySep   = "<;>"
)

// Turnout states as the protocol numbers them.
const (
	turnoutUnknown      = 1
	turnoutClosed       = 2
	turnoutThrown       = 4
	turnoutInconsistent = 8
)

// Power states.
const (
	powerOff     = 0
	powerOn      = 1
	powerUnknown = 2
)

// clean takes the protocol's delimiters and line breaks out of a name, so
// a roster or turnout name cannot split a message.
func clean(s string) string {
	for _, d := range []string{listSep, fieldSep, keySep, "\r", "\n"} {
		s = strings.ReplaceAll(s, d, " ")
	}
	return strings.TrimSpace(s)
}

// locoKey is how the protocol names an address: L341, S3.
func locoKey(address uint16, long bool) string {
	if long {
		return "L" + strconv.Itoa(int(address))
	}
	return "S" + strconv.Itoa(int(address))
}

// parseKey reads a locomotive key back into an address.
func parseKey(key string) (address uint16, long, ok bool) {
	if len(key) < 2 || (key[0] != 'L' && key[0] != 'S') {
		return 0, false, false
	}
	n, err := strconv.Atoi(key[1:])
	if err != nil || n < 1 || n > train.MaxAddress {
		return 0, false, false
	}
	long = key[0] == 'L'
	if !long && n > 127 {
		return 0, false, false
	}
	return uint16(n), long, true
}

// rosterList is the RL message: the roster entries by name, address and
// address kind.
func rosterList(trains []train.Train) string {
	var b strings.Builder
	n := 0
	for _, t := range trains {
		if !t.Roster || t.Removed {
			continue
		}
		long := "S"
		if t.Long || t.Address > 127 {
			long = "L"
		}
		b.WriteString(listSep + clean(t.Name) + fieldSep + strconv.Itoa(int(t.Address)) + fieldSep + long)
		n++
	}
	return "RL" + strconv.Itoa(n) + b.String()
}

// consistList is the consist list: RCL with how many there are, then an
// RCD line for each DCC consist made up, by its consist address and name,
// with its locomotives and the way each faces. Apps list them beside the
// roster, and choosing one drives the consist address. Consists the
// command station keeps are left out: their lead is in the roster.
func consistList(consists []train.ConsistState, trains []train.Train) []string {
	byID := make(map[string]train.Train, len(trains))
	for _, t := range trains {
		byID[t.ID] = t
	}
	var lines []string
	for _, c := range consists {
		if !c.MadeUp || c.Kind != train.ConsistDCC {
			continue
		}
		var b strings.Builder
		b.WriteString("RCD" + fieldSep + dccAddress(c.Address, false) + fieldSep + clean(c.Name))
		for _, mb := range c.Members {
			if t, ok := byID[mb.Train]; ok {
				b.WriteString(listSep + dccAddress(t.Address, t.Long) + fieldSep + strconv.FormatBool(!mb.Reverse))
			}
		}
		lines = append(lines, b.String())
	}
	return append([]string{"RCL" + strconv.Itoa(len(lines))}, lines...)
}

// dccAddress is an address as consist lists write one: 10(S), 1234(L).
func dccAddress(address uint16, long bool) string {
	if long || address > 127 {
		return strconv.Itoa(int(address)) + "(L)"
	}
	return strconv.Itoa(int(address)) + "(S)"
}

// functionLabels is the labels part of an MTL message: one field per
// function from F0, empty for those without a label.
func functionLabels(labels map[int]string, count int) string {
	var b strings.Builder
	for fn := range count {
		b.WriteString(listSep + clean(labels[fn]))
	}
	return b.String()
}

// maxFunctions is how many functions an app is told about at most, F0 to
// F31: apps keep no more, and one handheld overruns its table past F31.
const maxFunctions = 32

// functionCount is how many functions to show for a train: F0 to F28, or
// up to the highest one labeled.
func functionCount(t train.Train) int {
	n := 29
	for fn := range t.Functions {
		if fn >= n && fn < maxFunctions {
			n = fn + 1
		}
	}
	return n
}

// momentary is which functions act only while held: the roster entry's,
// or F2 (the horn by convention) for a locomotive the roster says nothing
// about, as other WiThrottle servers do.
func momentary(t train.Train) map[int]bool {
	out := make(map[int]bool)
	if len(t.Functions) == 0 && len(t.Momentary) == 0 {
		out[2] = true
	}
	for _, fn := range t.Momentary {
		out[fn] = true
	}
	return out
}

// turnoutTitles is the PTT message naming the turnout states.
const turnoutTitles = "PTT" + listSep + "Turnouts" + fieldSep + "Turnout" +
	listSep + "Closed" + fieldSep + "2" + listSep + "Thrown" + fieldSep + "4" +
	listSep + "Unknown" + fieldSep + "1" + listSep + "Inconsistent" + fieldSep + "8"

// listed is whether a turnout belongs in the apps' list: confirmed and
// not hidden.
func listed(e entity.Entity) bool {
	return e.Kind == entity.KindTurnout && !e.Hidden && !e.Suggested && !e.Removed
}

// turnoutList is the PTL message: each turnout by ID, name and state.
// With no turnouts there is no message, since apps expect at least one.
func turnoutList(ents []entity.Entity) string {
	var b strings.Builder
	for _, e := range ents {
		if !listed(e) {
			continue
		}
		b.WriteString(listSep + clean(e.ID) + fieldSep + clean(e.Name) + fieldSep + strconv.Itoa(turnoutState(e)))
	}
	if b.Len() == 0 {
		return ""
	}
	return "PTL" + b.String()
}

// turnoutState numbers a turnout's state; one on its way shows where it
// was told to go.
func turnoutState(e entity.Entity) int {
	state := e.State
	if state == entity.StateMoving {
		state = e.Commanded
	}
	switch state {
	case entity.StateClosed:
		return turnoutClosed
	case entity.StateThrown:
		return turnoutThrown
	}
	return turnoutUnknown
}

// powerState numbers the layout's track power.
func powerState(l train.Layout) int {
	switch {
	case !l.PowerKnown:
		return powerUnknown
	case l.PowerOff:
		return powerOff
	}
	return powerOn
}
