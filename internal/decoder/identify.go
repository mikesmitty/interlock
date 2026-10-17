package decoder

import (
	"context"
	"fmt"
	"slices"
	"strconv"
)

// Identity is what a decoder says about itself: its manufacturer (CV 8),
// version (CV 7) and, for makers that publish one, a product ID.
type Identity struct {
	Manufacturer int `json:"manufacturer"`
	Version      int `json:"version"`
	// ProductID is valid when HasProductID is set.
	ProductID    uint32 `json:"product_id,omitempty"`
	HasProductID bool   `json:"has_product_id"`
}

// NMRA manufacturer IDs with product ID rules.
const (
	mfgDIY          = 13
	mfgHornby       = 48
	mfgTrainOMatic  = 78
	mfgDoehlerHaass = 97
	mfgHarman       = 98
	mfgQSI          = 113
	mfgDietz        = 115
	mfgSoundTraxx   = 141
	mfgZimo         = 145
	mfgESU          = 151
	mfgTCS          = 153
	mfgPIKO         = 162
)

// identifyAttempts is how many times each identify step is tried.
const identifyAttempts = 3

// Identify reads a decoder's identity on the programming track: CV 8,
// CV 7, then the CVs its manufacturer keeps a product ID in. Some
// manufacturers' IDs sit behind index CVs, which are left pointing there.
func Identify(ctx context.Context, bus CVBus) (Identity, error) {
	read := func(cv int) (int, error) {
		var err error
		for range identifyAttempts {
			var v int
			if v, err = bus.ReadCV(ctx, cv); err == nil {
				return v, nil
			}
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
		}
		return 0, fmt.Errorf("read CV %d: %w", cv, err)
	}
	write := func(cv, value int) error {
		var err error
		for range identifyAttempts {
			if err = bus.WriteCV(ctx, cv, value); err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		return fmt.Errorf("write CV %d: %w", cv, err)
	}
	// reads reads several CVs; ok is false when one fails, which leaves
	// the decoder without a product ID rather than failing the identify.
	reads := func(cvs ...int) ([]int, bool, error) {
		out := make([]int, len(cvs))
		for i, cv := range cvs {
			v, err := read(cv)
			if err != nil {
				if ctx.Err() != nil {
					return nil, false, ctx.Err()
				}
				return nil, false, nil
			}
			out[i] = v
		}
		return out, true, nil
	}

	var id Identity
	var err error
	if id.Manufacturer, err = read(8); err != nil {
		return id, err
	}
	if id.Version, err = read(7); err != nil {
		return id, err
	}
	set := func(v uint32) { id.ProductID, id.HasProductID = v, true }
	var v []int
	var ok bool
	switch id.Manufacturer {
	case mfgDietz:
		if v, ok, err = reads(128); ok {
			set(uint32(v[0]))
		}
	case mfgDIY:
		if v, ok, err = reads(47, 48, 49, 50); ok {
			set(uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3]))
		}
	case mfgDoehlerHaass:
		if v, ok, err = reads(261); ok {
			set(uint32(v[0]))
		}
	case mfgESU:
		if id.Version != 255 {
			break
		}
		if err := write(31, 0); err != nil {
			return id, err
		}
		if err := write(32, 255); err != nil {
			return id, err
		}
		if v, ok, err = reads(261, 262, 263, 264); ok {
			set(uint32(v[0]) | uint32(v[1])<<8 | uint32(v[2])<<16 | uint32(v[3])<<24)
		}
	case mfgHarman:
		if v, ok, err = reads(112, 113); ok {
			set(uint32(v[0])<<8 | uint32(v[1]))
		}
	case mfgHornby:
		if id.Version == 254 {
			if v, ok, err = reads(200, 201); ok {
				set(uint32(v[0])*256 + uint32(v[1]))
			}
			break
		}
		if v, ok, err = reads(159); ok {
			if v[0] != 143 {
				set(uint32(v[0]))
			} else if hi, ok2, err2 := reads(158); ok2 {
				set(uint32(hi[0])<<8 | 143)
			} else {
				err = err2
			}
		}
	case mfgPIKO:
		if err := write(31, 0); err != nil {
			return id, err
		}
		if err := write(32, 255); err != nil {
			return id, err
		}
		if v, ok, err = reads(315, 316, 317); ok {
			set(uint32((v[0]*100+v[1])*100 + v[2]))
		}
	case mfgQSI:
		if err := write(49, 254); err != nil {
			return id, err
		}
		if err := write(50, 4); err != nil {
			return id, err
		}
		var hi []int
		if hi, ok, err = reads(56); !ok {
			break
		}
		if err := write(50, 5); err != nil {
			return id, err
		}
		if v, ok, err = reads(56); ok {
			set(uint32(hi[0])*256 + uint32(v[0]))
		}
	case mfgSoundTraxx:
		if id.Version < 70 || id.Version > 72 {
			break
		}
		if v, ok, err = reads(253, 256, 255); ok {
			set(uint32(v[1]) | uint32(v[2]&7)<<8 | uint32(v[0])<<11)
		}
	case mfgTCS:
		if v, ok, err = reads(249); !ok {
			break
		}
		a := uint32(v[0])
		if a < 129 {
			set(a)
			break
		}
		if v, ok, err = reads(248, 111, 110); ok {
			b, c, d := uint32(v[0]), uint32(v[1]), uint32(v[2])
			ver := id.Version
			switch {
			case (a >= 129 && a <= 135 && b == 5) || ver >= 5:
				if a == 180 && ver == 5 {
					set(a + b*256)
				} else {
					set(a + b<<8 + c<<16 + d<<24)
				}
			case ver == 4 && ((a >= 129 && a <= 135) || (a >= 170 && a <= 172) || a == 180):
				set(a + b*256)
			default:
				set(a)
			}
		}
	case mfgTrainOMatic:
		if v, ok, err = reads(510, 509, 508); ok {
			set(uint32(v[2]) + uint32(v[1])<<8 + uint32(v[0])<<16)
		}
	case mfgZimo:
		if v, ok, err = reads(250); ok {
			set(uint32(v[0]))
		}
	}
	return id, err
}

// Candidate is a decoder model an identity matches.
type Candidate struct {
	Family       string `json:"family"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	File         string `json:"file"`
	// Exact is set when the product ID matched, not just the
	// manufacturer and version.
	Exact bool `json:"exact"`
	// maybe marks models the index lists only for identification.
	maybe bool
}

// Match lists the models an identity fits, best first: models whose
// product ID matched, then the rest of the manufacturer's models for the
// version. Models listed only for old decoders are left out unless
// nothing else fits.
func (x *Index) Match(id Identity) []Candidate {
	mfgs := map[string]bool{}
	for _, m := range x.Manufacturers {
		if m.ID == id.Manufacturer {
			mfgs[m.Name] = true
		}
	}
	var exact, loose, hidden []Candidate
	pid := ""
	if id.HasProductID {
		pid = strconv.FormatUint(uint64(id.ProductID), 10)
	}
	for _, f := range x.Families {
		if !mfgs[f.Manufacturer] || !f.Programmable() {
			continue
		}
		for _, m := range f.Models {
			if !m.covers(f, id.Version) {
				continue
			}
			c := Candidate{Family: f.Name, Manufacturer: f.Manufacturer, Model: m.Name, File: f.File, maybe: m.Show == "maybe"}
			switch {
			case m.Show == "no":
				hidden = append(hidden, c)
			case pid != "" && slices.Contains(m.ProductIDs, pid):
				c.Exact = true
				exact = append(exact, c)
			default:
				loose = append(loose, c)
			}
		}
	}
	// Models shown only when identified go after the usual ones.
	shownFirst := func(a, b Candidate) int {
		switch {
		case a.maybe == b.maybe:
			return 0
		case b.maybe:
			return -1
		}
		return 1
	}
	switch {
	case len(exact) > 0:
		slices.SortStableFunc(exact, shownFirst)
		return exact
	case len(loose) > 0:
		slices.SortStableFunc(loose, shownFirst)
		return loose
	}
	return hidden
}

// covers reports whether a model's CV 7 ranges include a version.
func (m Model) covers(f Family, version int) bool {
	for _, r := range m.Versions(f) {
		if version >= r.Low && version <= r.High {
			return true
		}
	}
	return false
}

// Address is the DCC address a decoder answers to.
type Address struct {
	Address int  `json:"address"`
	Long    bool `json:"long"`
}

// ReadAddress reads the address a decoder answers to on the programming
// track: CV 29 says whether that is the long address in CVs 17 and 18 or
// the short one in CV 1.
func ReadAddress(ctx context.Context, bus CVBus) (Address, error) {
	read := func(cv int) (int, error) {
		var err error
		for range identifyAttempts {
			var v int
			if v, err = bus.ReadCV(ctx, cv); err == nil {
				return v, nil
			}
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
		}
		return 0, fmt.Errorf("read CV %d: %w", cv, err)
	}
	cv29, err := read(29)
	if err != nil {
		return Address{}, err
	}
	if cv29&0x20 == 0 {
		cv1, err := read(1)
		if err != nil {
			return Address{}, err
		}
		return Address{Address: cv1 & 0x7F}, nil
	}
	cv17, err := read(17)
	if err != nil {
		return Address{}, err
	}
	cv18, err := read(18)
	if err != nil {
		return Address{}, err
	}
	return Address{Address: (cv17&0x3F)<<8 | cv18, Long: true}, nil
}
