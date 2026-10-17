package traction

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mikesmitty/interlock/openlcb"
)

// The Train Search protocol: a throttle produces an event that spells out
// a locomotive's address or number, and every train node that matches
// answers Producer Identified with the same event. A command station may
// create the train node on the spot when the allocate flag is set.

// SearchPrefix is the upper half of every train search event; the lower
// half holds six decimal digits as nibbles, padded with 0xF, and a flags
// byte.
const SearchPrefix openlcb.EventID = 0x090099FF00000000

// SearchFlags is the low byte of a search event.
type SearchFlags byte

// Search flags. The low nibble selects the protocol: SearchDCC with
// SearchLong for a long address and a speed step choice in the low two
// bits, or zero for any protocol.
const (
	// SearchAllocate asks the command station to create the train if no
	// node matches.
	SearchAllocate SearchFlags = 0x80
	// SearchExact matches the whole address rather than a prefix.
	SearchExact SearchFlags = 0x40
	// SearchAddressOnly matches the address, not the train's name.
	SearchAddressOnly SearchFlags = 0x20
	SearchDCC         SearchFlags = 0x08
	SearchLong        SearchFlags = 0x04
	SearchSteps14     SearchFlags = 0x01
	SearchSteps28     SearchFlags = 0x02
	SearchSteps128    SearchFlags = 0x03
	searchReserved    SearchFlags = 0x10
)

// SearchEvent builds the event for a query of up to six decimal digits.
func SearchEvent(query string, flags SearchFlags) (openlcb.EventID, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 6 {
		return 0, fmt.Errorf("traction: search query %q must be one to six digits", query)
	}
	var lower uint32 = 0xFFFFFF00
	shift := 28 - 4*(6-len(query))
	for _, r := range query {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("traction: search query %q must be digits only", query)
		}
		lower &^= 0xF << shift
		lower |= uint32(r-'0') << shift
		shift -= 4
	}
	return SearchPrefix | openlcb.EventID(lower) | openlcb.EventID(flags&^searchReserved), nil
}

// AddressSearch builds the event that finds, and with allocate creates,
// the DCC locomotive at an address.
func AddressSearch(address uint16, long, allocate bool) openlcb.EventID {
	flags := SearchExact | SearchAddressOnly | SearchDCC
	if long {
		flags |= SearchLong
	}
	if allocate {
		flags |= SearchAllocate
	}
	ev, _ := SearchEvent(strconv.Itoa(int(address)), flags)
	return ev
}

// IsSearchEvent reports whether ev is a train search.
func IsSearchEvent(ev openlcb.EventID) bool {
	return ev&0xFFFFFFFF00000000 == SearchPrefix
}

// ParseSearch reads the digits and flags out of a search event. Nibbles
// that are not digits or padding make it invalid.
func ParseSearch(ev openlcb.EventID) (query string, flags SearchFlags, ok bool) {
	if !IsSearchEvent(ev) {
		return "", 0, false
	}
	lower := uint32(ev)
	var b strings.Builder
	for shift := 28; shift >= 8; shift -= 4 {
		d := (lower >> shift) & 0xF
		switch {
		case d <= 9:
			b.WriteByte('0' + byte(d))
		case d == 0xF:
		default:
			return "", 0, false
		}
	}
	flags = SearchFlags(lower)
	if flags&searchReserved != 0 || (flags&SearchDCC == 0 && flags&0x07 != 0) {
		return "", 0, false
	}
	return b.String(), flags, true
}

// SearchMatches reports whether a DCC locomotive at address answers a
// search: the digits match as a prefix, or exactly with SearchExact, and
// the long and short address ranges are honored as the flags ask.
func SearchMatches(query string, flags SearchFlags, address uint16, long bool) bool {
	if query == "" {
		return false
	}
	if flags&SearchDCC != 0 {
		switch {
		case flags&SearchLong != 0:
			if !long {
				return false
			}
		case flags&SearchAllocate == 0:
			// Without a preference the address range decides: a number
			// that fits a short address wants a short one.
			n, _ := strconv.Atoi(query)
			if (n <= ShortAddressMax) == long {
				return false
			}
		}
	}
	digits := strconv.Itoa(int(address))
	if flags&SearchExact != 0 {
		return digits == query
	}
	return strings.HasPrefix(digits, query)
}
