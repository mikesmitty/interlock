package decoder

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A CV name is how a definition addresses one value on a decoder. Most are
// plain CV numbers ("29"); others reach a CV behind index CVs ("16.0.259":
// write CV 31 = 16 and CV 32 = 0, then use CV 259), and TCS WOW decoders
// use their own four-CV protocol ("T2CV.1.05"). A variable's CV attribute
// can also list several names at once for values spread over CVs.

// expandCVs turns a CV attribute into the CV names it lists, in order:
// "1,9", "16.4.(258,257)", "0.255.261:4" (four names from 261 up),
// "16.3.25(1-7)". Anything else is one name.
func expandCVs(attr string) ([]string, error) {
	if !strings.ContainsAny(attr, "(),-:") {
		return []string{attr}, nil
	}
	prefix, body, suffix := "", attr, ""
	if open := strings.IndexByte(attr, '('); open >= 0 {
		end := strings.IndexByte(attr, ')')
		if end < open {
			return nil, fmt.Errorf("CV %q: unbalanced parentheses", attr)
		}
		prefix, body, suffix = attr[:open], attr[open+1:end], attr[end+1:]
		if strings.ContainsAny(prefix+suffix, "(),-:") {
			return nil, fmt.Errorf("CV %q: more than one list", attr)
		}
	}
	var items []string
	switch {
	case strings.Contains(body, ","):
		items = strings.Split(body, ",")
	case strings.Contains(body, ":"):
		before, after, _ := strings.Cut(body, ":")
		pre, start, err := trailingNumber(before)
		if err != nil {
			return nil, fmt.Errorf("CV %q: %w", attr, err)
		}
		down := strings.HasPrefix(after, "-")
		count, post, err := leadingNumber(strings.TrimPrefix(after, "-"))
		if err != nil {
			return nil, fmt.Errorf("CV %q: %w", attr, err)
		}
		step := 1
		if down {
			step = -1
		}
		for i := range max(count, 1) {
			items = append(items, pre+strconv.Itoa(start+i*step)+post)
		}
	case strings.Contains(body, "-"):
		before, after, _ := strings.Cut(body, "-")
		pre, from, err := trailingNumber(before)
		if err != nil {
			return nil, fmt.Errorf("CV %q: %w", attr, err)
		}
		to, post, err := leadingNumber(after)
		if err != nil {
			return nil, fmt.Errorf("CV %q: %w", attr, err)
		}
		step := 1
		if to < from {
			step = -1
		}
		for n := from; ; n += step {
			items = append(items, pre+strconv.Itoa(n)+post)
			if n == to {
				break
			}
		}
	default:
		items = []string{body}
	}
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = prefix + item + suffix
	}
	return names, nil
}

// trailingNumber splits "0.255.285" into "0.255." and 285.
func trailingNumber(s string) (string, int, error) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return "", 0, errors.New("no number before the list")
	}
	return s[:i], n, nil
}

// leadingNumber splits "4.31=0.32=255" into 4 and ".31=0.32=255".
func leadingNumber(s string) (int, string, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, "", errors.New("no number after the list")
	}
	return n, s[i:], nil
}

// Programming is how a decoder's CVs are reached: the programming modes
// its file allows and the index schemes it declares.
type Programming struct {
	// Direct is yes, no, bitOnly or byteOnly; Paged and Register are yes
	// or no.
	Direct   string `json:"direct"`
	Paged    bool   `json:"paged"`
	Register bool   `json:"register"`
	// NoDecoder marks a definition with nothing to read or write.
	NoDecoder bool `json:"no_decoder,omitempty"`
	// Modes lists programming modes the file names; LocoNet modes mean the
	// device is not programmed through DCC.
	Modes []string `json:"modes,omitempty"`
	// Index is the decoder's indexed CV access, when it has one.
	Index *Indexing `json:"index,omitempty"`
	// TCS is set for TCS WOW decoders' four-CV access (T2CV and T3CV).
	TCS bool `json:"tcs,omitempty"`
	// OpsWriteDelay is how long to wait after each write on the main, in
	// milliseconds.
	OpsWriteDelay int `json:"ops_write_delay,omitempty"`
}

// Indexing is the "Indexed CV access" capability: index values are
// written to PI (and SI) before the CV itself is used.
type Indexing struct {
	PI int `json:"pi"`
	SI int `json:"si"`
	// CVFirst says names are written CV first ("56.36.1"); otherwise index
	// values come first ("16.0.259").
	CVFirst bool `json:"cv_first"`
	// Cache allows skipping index writes that repeat the previous ones.
	Cache bool `json:"cache"`
}

// DCC reports whether the decoder is programmed through DCC at all.
func (p Programming) DCC() bool {
	if p.NoDecoder {
		return false
	}
	for _, m := range p.Modes {
		if strings.HasPrefix(m, "LOCONET") {
			return false
		}
	}
	return true
}

// AccessKind says how a CV name reaches the decoder.
type AccessKind int

const (
	// AccessByte is one CV, possibly behind index writes.
	AccessByte AccessKind = iota
	// AccessTCS16 is a 16-bit TCS value (T2CV).
	AccessTCS16
	// AccessTCS8 is an 8-bit TCS value (T3CV).
	AccessTCS8
)

// IndexWrite is one index CV set before an indexed CV is used.
type IndexWrite struct {
	CV    int `json:"cv"`
	Value int `json:"value"`
}

// Access is the plan for reading or writing one CV name.
type Access struct {
	Name string
	Kind AccessKind
	// Index lists the index writes, in order, before CV is used.
	Index []IndexWrite
	CV    int
	// TCS holds the T2CV or T3CV parameters.
	TCS [3]int
}

// The fixed CVs of the TCS four-CV protocol.
const (
	tcsIndex1 = 201
	tcsIndex2 = 202
	tcsMSB    = 203
	tcsLSB    = 204
)

// Resolve plans how a CV name reaches the decoder.
func (p Programming) Resolve(name string) (Access, error) {
	a := Access{Name: name}
	if rest, ok := strings.CutPrefix(name, "T2CV."); ok {
		if !p.TCS {
			return a, fmt.Errorf("CV %q needs TCS access, which the definition does not declare", name)
		}
		nums, err := numbers(rest, 2)
		if err != nil {
			return a, fmt.Errorf("CV %q: %w", name, err)
		}
		a.Kind, a.TCS = AccessTCS16, [3]int{nums[0], nums[1]}
		return a, nil
	}
	if rest, ok := strings.CutPrefix(name, "T3CV."); ok {
		if !p.TCS {
			return a, fmt.Errorf("CV %q needs TCS access, which the definition does not declare", name)
		}
		nums, err := numbers(rest, 3)
		if err != nil {
			return a, fmt.Errorf("CV %q: %w", name, err)
		}
		a.Kind, a.TCS = AccessTCS8, [3]int{nums[0], nums[1], nums[2]}
		return a, nil
	}
	if !strings.Contains(name, ".") {
		cv, err := cvNumber(name)
		if err != nil {
			return a, err
		}
		a.CV = cv
		return a, nil
	}
	if p.Index == nil {
		return a, fmt.Errorf("CV %q is indexed, but the definition declares no indexed access", name)
	}
	parts := strings.Split(name, ".")
	if len(parts) > 3 {
		return a, fmt.Errorf("CV %q has too many parts", name)
	}
	var cvPart string
	var idxParts []string
	if p.Index.CVFirst {
		cvPart, idxParts = parts[0], parts[1:]
	} else {
		cvPart, idxParts = parts[len(parts)-1], parts[:len(parts)-1]
	}
	cv, err := cvNumber(cvPart)
	if err != nil {
		return a, fmt.Errorf("CV %q: %w", name, err)
	}
	a.CV = cv
	defaults := []int{p.Index.PI, p.Index.SI}
	for i, part := range idxParts {
		target := defaults[i]
		if addr, val, ok := strings.Cut(part, "="); ok {
			n, err := strconv.Atoi(addr)
			if err != nil {
				return a, fmt.Errorf("CV %q: index CV %q is not a number", name, addr)
			}
			target, part = n, val
		}
		v, err := strconv.Atoi(part)
		if err != nil || v < 0 || v > 255 {
			return a, fmt.Errorf("CV %q: index value %q is not a byte", name, part)
		}
		if target < 1 || target > MaxCV {
			return a, fmt.Errorf("CV %q: index CV %d is out of range", name, target)
		}
		a.Index = append(a.Index, IndexWrite{CV: target, Value: v})
	}
	return a, nil
}

// MaxCV is the highest CV a DCC decoder has.
const MaxCV = 1024

func cvNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxCV {
		return 0, fmt.Errorf("%q is not a CV number from 1 to %d", s, MaxCV)
	}
	return n, nil
}

// numbers parses n dot-separated numbers.
func numbers(s string, n int) ([]int, error) {
	parts := strings.Split(s, ".")
	if len(parts) != n {
		return nil, fmt.Errorf("want %d numbers", n)
	}
	out := make([]int, n)
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", p)
		}
		out[i] = v
	}
	return out, nil
}
