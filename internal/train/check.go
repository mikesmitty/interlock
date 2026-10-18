package train

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// maxTellReads bounds the CVs read to tell a decoder from others like it;
// each takes a couple of seconds on the programming track.
const maxTellReads = 3

// OnTrack is what the decoder on the programming track said about
// itself: its identity, the address it answers to, and the plain CVs read
// to learn them (CVs 1, 7, 8, 17, 18 and 29).
type OnTrack struct {
	Identity Identity
	Address  uint16
	Long     bool
	CVs      map[string]int
}

// CheckOptions are what a check needs from outside the roster.
type CheckOptions struct {
	// Manufacturer and ProductIDs are what the decoder's definition
	// allows, for a decoder with no identity saved: the manufacturer's ID
	// (zero when unknown) and the model's product IDs (none when it lists
	// none).
	Manufacturer int
	ProductIDs   []uint32
	// Read reads a CV of the decoder on the track by the name its
	// definition uses, to tell it from other roster decoders of the same
	// model at the same address. Nil reads nothing more.
	Read func(cv string) (int, error)
}

// DecoderCheck says whether the decoder on the programming track is one
// of the roster's.
type DecoderCheck struct {
	// Match is set when it is: nothing differs, and no other roster
	// decoder could be it.
	Match       bool         `json:"match"`
	Differences []Difference `json:"differences"`
	// Told lists the CVs read to tell it from other roster decoders.
	Told []string `json:"told,omitempty"`
	// Alike lists the roster decoders of the same model at the same
	// address that nothing saved tells it from.
	Alike []DecoderMatch `json:"alike"`
	// Others lists the roster decoders it could be instead, when
	// something differs.
	Others []DecoderMatch `json:"others"`
	// Read holds the values read for the check, by CV.
	Read map[string]int `json:"read"`
}

// Difference is something the decoder on the programming track and the
// roster disagree on.
type Difference struct {
	// What is "manufacturer", "product", "address" or "cv".
	What string `json:"what"`
	// CV names the CV when What is "cv".
	CV string `json:"cv,omitempty"`
	// Roster is the roster's value and Track the decoder's: a
	// manufacturer or product ID, an address or a CV value. Roster is
	// zero for a product when the definition lists several.
	Roster int64 `json:"roster"`
	Track  int64 `json:"track"`
	// RosterLong and TrackLong say which addresses are long ones.
	RosterLong bool `json:"roster_long,omitempty"`
	TrackLong  bool `json:"track_long,omitempty"`
	// Imported is set when the roster's value was imported and has not
	// been read since.
	Imported bool `json:"imported,omitempty"`
	// RosterName and TrackName name manufacturers, for the API to fill.
	RosterName string `json:"roster_name,omitempty"`
	TrackName  string `json:"track_name,omitempty"`
}

// CheckDecoder compares the decoder on the programming track with one of
// a locomotive's decoders: its model, its address, and the values saved
// for it. When other roster decoders have the same model and address, it
// reads CVs whose saved values tell them apart. It changes nothing.
func (m *Manager) CheckDecoder(id, decoder string, track OnTrack, opts CheckOptions) (DecoderCheck, error) {
	e, ok := m.Entry(id)
	if !ok {
		return DecoderCheck{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	d, ok := e.Decoder(decoder)
	if !ok {
		return DecoderCheck{}, fmt.Errorf("%w: %s has no decoder %s", ErrNotFound, e.Name, decoder)
	}
	mine, err := m.opts.CVs.Get(decoder)
	if err != nil {
		return DecoderCheck{}, err
	}
	track.Long = track.Long || track.Address > 127
	c := DecoderCheck{Differences: []Difference{}, Alike: []DecoderMatch{}, Others: []DecoderMatch{}, Read: maps.Clone(track.CVs)}
	if c.Read == nil {
		c.Read = map[string]int{}
	}

	// The model.
	maker, products := opts.Manufacturer, opts.ProductIDs
	if d.Identity != nil {
		maker, products = d.Identity.Manufacturer, nil
		if d.Identity.ProductID != nil {
			products = []uint32{*d.Identity.ProductID}
		}
	}
	otherMaker := maker != 0 && maker != track.Identity.Manufacturer
	switch pid := track.Identity.ProductID; {
	case otherMaker:
		c.Differences = append(c.Differences, Difference{What: "manufacturer", Roster: int64(maker), Track: int64(track.Identity.Manufacturer)})
	case pid != nil && len(products) > 0 && !slices.Contains(products, *pid):
		diff := Difference{What: "product", Track: int64(*pid)}
		if len(products) == 1 {
			diff.Roster = int64(products[0])
		}
		c.Differences = append(c.Differences, diff)
	}

	// The address.
	addr, long := decoderAddress(e, d)
	otherAddress := addr != 0 && (addr != track.Address || long != track.Long)
	if otherAddress {
		c.Differences = append(c.Differences, Difference{What: "address", Roster: int64(addr), Track: int64(track.Address), RosterLong: long, TrackLong: track.Long})
	}

	// The values read on the way, leaving out those that only repeat a
	// difference already found.
	for _, cv := range sortedCVs(slices.Collect(maps.Keys(track.CVs))) {
		if cv == "8" && otherMaker || (cv == "1" || cv == "17" || cv == "18") && otherAddress {
			continue
		}
		if v, imported, ok := savedValue(mine, cv); ok && v != track.CVs[cv] {
			c.Differences = append(c.Differences, Difference{What: "cv", CV: cv, Roster: int64(v), Track: int64(track.CVs[cv]), Imported: imported})
		}
	}

	// The other roster decoders it could be.
	type candidate struct {
		match DecoderMatch
		alike bool
		saved CVValues
	}
	var cands []candidate
	for _, f := range m.FindDecoder(track.Identity, track.Address, track.Long) {
		if f.Train == id && f.Decoder == decoder {
			continue
		}
		cand := candidate{match: f}
		if f.Decoder != "" {
			if cand.saved, err = m.opts.CVs.Get(f.Decoder); err != nil {
				return DecoderCheck{}, err
			}
			other, _ := m.Entry(f.Train)
			od, _ := other.Decoder(f.Decoder)
			a, l := decoderAddress(other, od)
			// Alike means the same model at the same address: a known
			// identity that agrees, or none and the same definition.
			cand.alike = a == track.Address && l == track.Long && (f.Identity || od.Identity == nil && od.File == d.File && od.Model == d.Model)
		}
		cands = append(cands, cand)
	}
	consistent := func(cand candidate) bool {
		for cv, v := range c.Read {
			if sv, _, ok := savedValue(cand.saved, cv); ok && sv != v {
				return false
			}
		}
		return true
	}
	if len(c.Differences) == 0 {
		var alike []candidate
		for _, cand := range cands {
			if cand.alike && consistent(cand) {
				alike = append(alike, cand)
			}
		}
		tried := map[string]bool{}
		for range maxTellReads {
			if len(alike) == 0 || opts.Read == nil {
				break
			}
			saved := make([]CVValues, len(alike))
			for i, cand := range alike {
				saved[i] = cand.saved
			}
			cv := tellingCV(mine, saved, tried)
			if cv == "" {
				break
			}
			tried[cv] = true
			v, err := opts.Read(cv)
			if err != nil {
				continue
			}
			c.Read[cv] = v
			c.Told = append(c.Told, cv)
			if sv, imported, _ := savedValue(mine, cv); sv != v {
				c.Differences = append(c.Differences, Difference{What: "cv", CV: cv, Roster: int64(sv), Track: int64(v), Imported: imported})
				break
			}
			alike = slices.DeleteFunc(alike, func(cand candidate) bool { return !consistent(cand) })
		}
		if len(c.Differences) == 0 {
			for _, cand := range alike {
				c.Alike = append(c.Alike, cand.match)
			}
		}
	}
	if len(c.Differences) > 0 {
		for _, cand := range cands {
			if consistent(cand) {
				c.Others = append(c.Others, cand.match)
			}
		}
	}
	c.Match = len(c.Differences) == 0 && len(c.Alike) == 0
	return c, nil
}

// decoderAddress is the address a locomotive's decoder answers to: the
// locomotive's for the fitted one, else its own if it keeps one.
func decoderAddress(e Entry, d Decoder) (uint16, bool) {
	a, l := d.Address, d.Long
	if d.ID == e.Installed || a == 0 {
		a, l = e.Address, e.Long
	}
	return a, l || a > 127
}

// savedValue is a CV's value as the roster has it: read or written, else
// imported.
func savedValue(v CVValues, cv string) (value int, imported, ok bool) {
	if x, ok := v.CVs[cv]; ok {
		return x, false, true
	}
	x, ok := v.Imported[cv]
	return x, ok, ok
}

// tellingCV picks the CV whose value, read, would rule out the most of
// others: one saved for mine whose saved value in them differs. Of those
// that rule out as many, plain CVs go first, since indexed ones take
// writes to reach.
func tellingCV(mine CVValues, others []CVValues, tried map[string]bool) string {
	names := slices.Collect(maps.Keys(mine.CVs))
	for cv := range mine.Imported {
		if _, ok := mine.CVs[cv]; !ok {
			names = append(names, cv)
		}
	}
	best, most := "", 0
	for _, cv := range sortedCVs(names) {
		if tried[cv] {
			continue
		}
		v, _, _ := savedValue(mine, cv)
		n := 0
		for _, o := range others {
			if ov, _, ok := savedValue(o, cv); ok && ov != v {
				n++
			}
		}
		if n > most {
			best, most = cv, n
		}
	}
	return best
}

// sortedCVs sorts CV names, plain numbers by value first.
func sortedCVs(names []string) []string {
	slices.SortFunc(names, func(a, b string) int {
		x, errA := strconv.Atoi(a)
		y, errB := strconv.Atoi(b)
		switch {
		case errA == nil && errB == nil:
			return cmp.Compare(x, y)
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		}
		return strings.Compare(a, b)
	})
	return names
}
