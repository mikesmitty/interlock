package train

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Import is a locomotive from another program's roster, ready to come in.
type Import struct {
	// Key names it within one import, such as the file it came from.
	Key string `json:"key"`
	// Entry is the locomotive: name, address, labels and what it is. Its
	// ID and decoders are left empty.
	Entry Entry `json:"entry"`
	// Decoder is its decoder, when the roster names one; its ID and the
	// address and labels are left empty.
	Decoder *Decoder `json:"decoder,omitempty"`
	// CVs are the decoder's CV values.
	CVs     map[string]int `json:"-"`
	Updated time.Time      `json:"updated,omitzero"`
}

// Import actions.
const (
	// ImportNew makes a locomotive of its own, with the decoder fitted.
	ImportNew = "new"
	// ImportDecoder adds the decoder to another locomotive: one in the
	// roster, or one this import makes.
	ImportDecoder = "decoder"
	// ImportSkip leaves it out.
	ImportSkip = "skip"
)

// ImportStep is what to do with one import.
type ImportStep struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	// Into is the locomotive that gets the decoder: a roster ID, or the
	// key of an import whose action is new.
	Into string `json:"into,omitempty"`
	// Install fits the decoder in Into.
	Install bool `json:"install,omitempty"`
	// Name is the new locomotive's name, or the name of the decoder
	// joining another.
	Name string `json:"name,omitempty"`
	// DecoderName is the new locomotive's decoder's name. The plan fills
	// it in for every import with a decoder, so a step can be changed to
	// new.
	DecoderName string `json:"decoder_name,omitempty"`
	// Note says why the plan suggests this.
	Note string `json:"note,omitempty"`
}

// ImportResult is what an import did.
type ImportResult struct {
	// Trains are the locomotives made or given a decoder, as they stand.
	Trains   []Train `json:"trains"`
	Decoders int     `json:"decoders"`
	CVs      int     `json:"cvs"`
	Skipped  int     `json:"skipped"`
}

// PlanImport suggests what to do with each import. One already in the
// roster under its name is skipped. Imports with the same address, road
// name and road number are taken as one locomotive with several
// decoders: the newest is fitted and the rest join it, named by what
// their names add to the name they share. Everything else is new.
func (m *Manager) PlanImport(items []Import) []ImportStep {
	m.mu.RLock()
	byName := make(map[string]string, len(m.roster))
	for id, e := range m.roster {
		byName[strings.ToLower(e.Name)] = id
	}
	m.mu.RUnlock()

	steps := make(map[string]*ImportStep, len(items))
	groups := make(map[string][]Import)
	var order []string
	for _, it := range items {
		step := &ImportStep{Key: it.Key, Action: ImportNew, Name: it.Entry.Name}
		if it.Decoder != nil {
			step.DecoderName = it.Decoder.Title()
		}
		steps[it.Key] = step
		if id, ok := byName[strings.ToLower(it.Entry.Name)]; ok {
			step.Action, step.Into, step.Note = ImportSkip, id, "Already in the roster."
			continue
		}
		if it.Decoder == nil || it.Entry.RoadNumber == "" {
			continue
		}
		e := it.Entry.Normalize()
		g := fmt.Sprintf("%s|%s|%s", e.key(), strings.ToLower(e.RoadName), strings.ToLower(e.RoadNumber))
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], it)
	}
	for _, g := range order {
		members := groups[g]
		if len(members) < 2 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].Updated.After(members[j].Updated) })
		names := make([]string, len(members))
		for i, it := range members {
			names[i] = it.Entry.Name
		}
		shared := sharedName(names)
		newest := steps[members[0].Key]
		if shared != "" {
			newest.Name = shared
			if rest := strings.TrimSpace(strings.TrimPrefix(members[0].Entry.Name, shared)); rest != "" {
				newest.DecoderName = rest
			}
		}
		for _, it := range members[1:] {
			s := steps[it.Key]
			s.Action, s.Into, s.Install = ImportDecoder, members[0].Key, false
			if rest := strings.TrimSpace(strings.TrimPrefix(it.Entry.Name, shared)); shared != "" && rest != "" {
				s.Name = rest
			} else {
				s.Name = it.Decoder.Title()
			}
			s.Note = fmt.Sprintf("Same address and road number as %s, so its decoder joins that locomotive.", members[0].Entry.Name)
		}
	}
	out := make([]ImportStep, len(items))
	for i, it := range items {
		out[i] = *steps[it.Key]
	}
	return out
}

// sharedName is the words the names start with in common, if that leaves
// a name worth keeping.
func sharedName(names []string) string {
	prefix := names[0]
	for _, n := range names[1:] {
		i := 0
		for i < len(prefix) && i < len(n) && prefix[i] == n[i] {
			i++
		}
		prefix = prefix[:i]
	}
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	// Whole words only: "Kato P42 E" from "Kato P42 ESU" and "Kato P42
	// EMD" becomes "Kato P42".
	for _, n := range names {
		if len(n) > len(prefix) && !unicode.IsSpace(rune(n[len(prefix)])) && !strings.HasSuffix(prefix, " ") {
			if i := strings.LastIndexFunc(prefix, unicode.IsSpace); i >= 0 {
				prefix = prefix[:i]
			} else {
				prefix = ""
			}
			break
		}
	}
	prefix = strings.TrimRightFunc(prefix, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) })
	if len(prefix) < 3 {
		return ""
	}
	return prefix
}

// ApplyImport carries out a plan. Every step is checked before anything
// changes; a failure part way stops there and returns what was done.
func (m *Manager) ApplyImport(ctx context.Context, items []Import, plan []ImportStep) (ImportResult, error) {
	res := ImportResult{Trains: []Train{}}
	byKey := make(map[string]Import, len(items))
	for _, it := range items {
		byKey[it.Key] = it
	}
	steps := make(map[string]ImportStep, len(plan))
	for _, s := range plan {
		if _, ok := byKey[s.Key]; !ok {
			return res, invalid("the plan names %q, which is not in the import", s.Key)
		}
		if _, dup := steps[s.Key]; dup {
			return res, invalid("the plan names %q twice", s.Key)
		}
		steps[s.Key] = s
	}
	for _, s := range plan {
		switch s.Action {
		case ImportNew, ImportSkip:
		case ImportDecoder:
			if byKey[s.Key].Decoder == nil {
				return res, invalid("%s has no decoder to add to another locomotive", byKey[s.Key].Entry.Name)
			}
			if into, ok := steps[s.Into]; ok {
				if into.Action != ImportNew {
					return res, invalid("%s's decoder can only join a locomotive this import makes or one in the roster", byKey[s.Key].Entry.Name)
				}
			} else if _, ok := m.Entry(s.Into); !ok {
				return res, invalid("there is no locomotive %q for %s's decoder", s.Into, byKey[s.Key].Entry.Name)
			}
		default:
			return res, invalid("%q is not an import action; use new, decoder or skip", s.Action)
		}
	}

	made := make(map[string]string)
	changed := make(map[string]bool)
	var touched []string
	touch := func(id string) {
		if !changed[id] {
			changed[id] = true
			touched = append(touched, id)
		}
	}
	finish := func() ImportResult {
		for _, id := range touched {
			if tr, ok := m.Get(id); ok {
				res.Trains = append(res.Trains, tr)
			}
		}
		return res
	}
	addDecoder := func(id string, it Import, d Decoder, install bool) error {
		_, added, err := m.AddDecoder(ctx, id, d, install)
		if err != nil {
			return fmt.Errorf("%s: %w", it.Entry.Name, err)
		}
		res.Decoders++
		if len(it.CVs) > 0 {
			// The values are as old as the roster that kept them.
			err := m.checkCVs(id, added.ID, it.CVs)
			if err == nil {
				err = m.opts.CVs.ReplaceAt(added.ID, it.CVs, it.Updated)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", it.Entry.Name, err)
			}
			res.CVs += len(it.CVs)
		}
		return nil
	}
	for _, s := range plan {
		it := byKey[s.Key]
		switch s.Action {
		case ImportSkip:
			res.Skipped++
		case ImportNew:
			e := it.Entry.clone()
			e.ID, e.Decoders, e.Installed = "", nil, ""
			if s.Name != "" {
				e.Name = s.Name
			}
			tr, err := m.Create(e)
			if err != nil {
				return finish(), fmt.Errorf("%s: %w", it.Entry.Name, err)
			}
			made[s.Key] = tr.ID
			touch(tr.ID)
			if it.Decoder != nil {
				d := *it.Decoder
				d.ID = ""
				if s.DecoderName != "" {
					d.Name = s.DecoderName
				}
				if err := addDecoder(tr.ID, it, d, true); err != nil {
					return finish(), err
				}
			}
		}
	}
	for _, s := range plan {
		if s.Action != ImportDecoder {
			continue
		}
		it := byKey[s.Key]
		id := s.Into
		if made, ok := made[s.Into]; ok {
			id = made
		}
		d := *it.Decoder
		d.ID = ""
		if s.Name != "" {
			d.Name = s.Name
		}
		e := it.Entry.Normalize()
		d.Address, d.Long, d.Functions, d.Momentary = e.Address, e.Long, cloneLabels(e.Functions), append([]int(nil), e.Momentary...)
		if err := addDecoder(id, it, d, s.Install); err != nil {
			return finish(), err
		}
		touch(id)
	}
	return finish(), nil
}
