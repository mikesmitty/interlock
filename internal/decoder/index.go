package decoder

import (
	"encoding/xml"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Index is JMRI's decoder index: every manufacturer with its NMRA ID and
// every family of decoders with the file that defines it.
type Index struct {
	// Version is the index's own revision number.
	Version       string         `json:"version"`
	Manufacturers []Manufacturer `json:"manufacturers"`
	Families      []Family       `json:"families"`
}

// Manufacturer is a decoder maker and the ID its decoders report in CV 8.
type Manufacturer struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// Family is a group of decoder models defined by one file.
type Family struct {
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	// File is the definition's path under the decoders folder.
	File string `json:"file"`
	// LowVersion and HighVersion bound the CV 7 values the family covers;
	// -1 when the index leaves them open.
	LowVersion  int    `json:"low_version"`
	HighVersion int    `json:"high_version"`
	Comment     string `json:"comment,omitempty"`
	// Type is "stationary" for accessory decoders; empty for locomotive
	// decoders.
	Type string `json:"type,omitempty"`
	// Modes names programming modes other than DCC's, such as the LocoNet
	// board modes; a family that needs them cannot be programmed through
	// a DCC programming track.
	Modes  string  `json:"modes,omitempty"`
	Models []Model `json:"models"`
}

// Model is one decoder product in a family.
type Model struct {
	Name string `json:"name"`
	// ProductIDs are the values the decoder reports in its manufacturer's
	// product ID CVs; most are numbers.
	ProductIDs []string `json:"product_ids,omitempty"`
	// Versions narrows the family's CV 7 range for this model; empty means
	// the family's range.
	Versions []VersionRange `json:"versions,omitempty"`
	NumFns   int            `json:"num_fns,omitempty"`
	NumOuts  int            `json:"num_outs,omitempty"`
	Comment  string         `json:"comment,omitempty"`
	// Show is "no" for models listed only to identify old decoders, and
	// "maybe" for ones shown when asked; empty means shown.
	Show string `json:"show,omitempty"`
	// ReplacementFamily and ReplacementModel name the definition to use
	// instead of a retired one.
	ReplacementFamily string `json:"replacement_family,omitempty"`
	ReplacementModel  string `json:"replacement_model,omitempty"`
}

// VersionRange is an inclusive range of CV 7 values; -1 leaves an end open.
type VersionRange struct {
	Low  int `json:"low"`
	High int `json:"high"`
}

// Programmable reports whether the family can be programmed through a
// DCC programming track or on the main.
func (f Family) Programmable() bool { return f.Modes == "" }

// Manufacturer returns the manufacturer with an NMRA ID.
func (x *Index) Manufacturer(id int) (Manufacturer, bool) {
	i := slices.IndexFunc(x.Manufacturers, func(m Manufacturer) bool { return m.ID == id })
	if i < 0 {
		return Manufacturer{}, false
	}
	return x.Manufacturers[i], true
}

// Family returns the family with a name; names are unique in the index
// only together with the manufacturer, so both are matched.
func (x *Index) Family(manufacturer, name string) (Family, bool) {
	i := slices.IndexFunc(x.Families, func(f Family) bool { return f.Manufacturer == manufacturer && f.Name == name })
	if i < 0 {
		return Family{}, false
	}
	return x.Families[i], true
}

type xmlIndex struct {
	Index struct {
		Version       string `xml:"version,attr"`
		Manufacturers []struct {
			Name string `xml:"mfg,attr"`
			ID   string `xml:"mfgID,attr"`
		} `xml:"mfgList>manufacturer"`
		Families []struct {
			Name        string     `xml:"name,attr"`
			Mfg         string     `xml:"mfg,attr"`
			File        string     `xml:"file,attr"`
			LowVersion  string     `xml:"lowVersionID,attr"`
			HighVersion string     `xml:"highVersionID,attr"`
			Comment     string     `xml:"comment,attr"`
			Type        string     `xml:"type,attr"`
			Modes       string     `xml:"modes,attr"`
			Models      []xmlModel `xml:"model"`
		} `xml:"familyList>family"`
	} `xml:"decoderIndex"`
}

type xmlModel struct {
	Name              string `xml:"model,attr"`
	ProductID         string `xml:"productID,attr"`
	LowVersion        string `xml:"lowVersionID,attr"`
	HighVersion       string `xml:"highVersionID,attr"`
	NumFns            string `xml:"numFns,attr"`
	NumOuts           string `xml:"numOuts,attr"`
	Comment           string `xml:"comment,attr"`
	Show              string `xml:"show,attr"`
	ReplacementFamily string `xml:"replacementFamily,attr"`
	ReplacementModel  string `xml:"replacementModel,attr"`
	VersionCVs        []struct {
		Low  string `xml:"lowVersionID,attr"`
		High string `xml:"highVersionID,attr"`
	} `xml:"versionCV"`
}

func parseIndex(r io.Reader) (*Index, error) {
	var doc xmlIndex
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	idx := &Index{Version: doc.Index.Version}
	for _, m := range doc.Index.Manufacturers {
		id, err := strconv.Atoi(strings.TrimSpace(m.ID))
		if err != nil {
			continue
		}
		idx.Manufacturers = append(idx.Manufacturers, Manufacturer{Name: m.Name, ID: id})
	}
	for _, f := range doc.Index.Families {
		fam := Family{
			Name: f.Name, Manufacturer: f.Mfg, File: f.File, Comment: f.Comment, Type: f.Type, Modes: f.Modes,
			LowVersion: number(f.LowVersion, -1), HighVersion: number(f.HighVersion, -1),
		}
		for _, m := range f.Models {
			model := Model{
				Name: m.Name, Comment: m.Comment, Show: m.Show,
				ReplacementFamily: m.ReplacementFamily, ReplacementModel: m.ReplacementModel,
				NumFns: number(m.NumFns, 0), NumOuts: number(m.NumOuts, 0),
			}
			for id := range strings.SplitSeq(m.ProductID, ",") {
				if id = strings.TrimSpace(id); id != "" {
					model.ProductIDs = append(model.ProductIDs, id)
				}
			}
			if m.LowVersion != "" || m.HighVersion != "" {
				model.Versions = append(model.Versions, VersionRange{Low: number(m.LowVersion, -1), High: number(m.HighVersion, -1)})
			}
			for _, v := range m.VersionCVs {
				model.Versions = append(model.Versions, VersionRange{Low: number(v.Low, -1), High: number(v.High, -1)})
			}
			fam.Models = append(fam.Models, model)
		}
		idx.Families = append(idx.Families, fam)
	}
	return idx, nil
}

// number parses a decimal attribute, or returns def when it is missing or
// not a number.
func number(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
