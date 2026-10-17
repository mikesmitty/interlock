// Package jmri reads the locomotive roster JMRI keeps: roster.xml, which
// lists the locomotives, and a file per locomotive with its decoder and
// CV values, in the roster folder beside it.
package jmri

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/internal/decoder"
	"github.com/mikesmitty/interlock/internal/train"
)

// Limits on what is read.
const (
	maxFileSize = 16 << 20
	maxFiles    = 5000
)

// File is one file given to Read: a roster file or a zip of them.
type File struct {
	Name string
	Data []byte
}

// Locomotive is one roster entry as JMRI wrote it.
type Locomotive struct {
	// File is where it was read from.
	File string
	// ID is JMRI's name for the entry.
	ID           string
	RoadName     string
	RoadNumber   string
	Manufacturer string
	Model        string
	Owner        string
	Comment      string
	Address      uint16
	Long         bool
	// DecoderFamily and DecoderModel name the decoder definition.
	DecoderFamily string
	DecoderModel  string
	// ManufacturerID and ProductID are the decoder identity JMRI noted,
	// empty when it did not.
	ManufacturerID string
	ProductID      string
	Functions      map[int]string
	// Momentary lists the functions JMRI marks as not latching.
	Momentary []int
	CVs       map[string]int
	Updated   time.Time
	// Values is whether the entry came with CV values; roster.xml lists
	// locomotives without them.
	Values bool
}

// Roster is what Read found.
type Roster struct {
	Locomotives []Locomotive
	// Problems lists what could not be read, in plain words.
	Problems []string
}

type locomotiveXML struct {
	ID             string `xml:"id,attr"`
	FileName       string `xml:"fileName,attr"`
	RoadNumber     string `xml:"roadNumber,attr"`
	RoadName       string `xml:"roadName,attr"`
	Mfg            string `xml:"mfg,attr"`
	Owner          string `xml:"owner,attr"`
	Model          string `xml:"model,attr"`
	DCCAddress     string `xml:"dccAddress,attr"`
	Comment        string `xml:"comment,attr"`
	ManufacturerID string `xml:"manufacturerID,attr"`
	ProductID      string `xml:"productID,attr"`
	DateUpdated    string `xml:"dateUpdated"`
	Decoder        struct {
		Model  string `xml:"model,attr"`
		Family string `xml:"family,attr"`
	} `xml:"decoder"`
	LocoAddress struct {
		DCC struct {
			Number string `xml:"number,attr"`
			Long   string `xml:"longaddress,attr"`
		} `xml:"dcclocoaddress"`
		Protocol string `xml:"protocol"`
	} `xml:"locoaddress"`
	FunctionLabels []struct {
		Num      string `xml:"num,attr"`
		Lockable string `xml:"lockable,attr"`
		Text     string `xml:",chardata"`
	} `xml:"functionlabels>functionlabel"`
	Values *struct {
		CVs []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"CVvalue"`
	} `xml:"values"`
}

type rosterXML struct {
	Locomotives []locomotiveXML `xml:"roster>locomotive"`
}

type locomotiveFileXML struct {
	Locomotive locomotiveXML `xml:"locomotive"`
}

// Read reads JMRI roster files: roster.xml, the locomotive files, or zips
// holding them (a roster folder or a whole JMRI profile). Locomotives
// with a file of their own come from it, with their CV values; the rest
// of roster.xml comes without. Other files are passed over.
func Read(files []File) (Roster, error) {
	var r Roster
	var listed []Locomotive
	own := make(map[string]Locomotive)
	count := 0
	var read func(name string, data []byte) error
	read = func(name string, data []byte) error {
		count++
		if count > maxFiles {
			return errors.New("too many files")
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".zip"):
			z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("%s is not a zip: %v", name, err))
				return nil
			}
			for _, f := range z.File {
				if f.FileInfo().IsDir() || strings.Contains(f.Name, "__MACOSX") || !strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
					continue
				}
				if f.UncompressedSize64 > maxFileSize {
					r.Problems = append(r.Problems, fmt.Sprintf("%s is too big to be a roster file", f.Name))
					continue
				}
				rc, err := f.Open()
				if err != nil {
					r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", f.Name, err))
					continue
				}
				inner, err := io.ReadAll(io.LimitReader(rc, maxFileSize))
				_ = rc.Close()
				if err != nil {
					r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", f.Name, err))
					continue
				}
				if err := read(f.Name, inner); err != nil {
					return err
				}
			}
			return nil
		case !strings.HasSuffix(lower, ".xml"):
			return nil
		}
		switch root := rootName(data); root {
		case "roster-config":
			var doc rosterXML
			if err := decode(data, &doc); err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", name, err))
				return nil
			}
			for _, x := range doc.Locomotives {
				l, err := convert(name, x)
				if err != nil {
					r.Problems = append(r.Problems, err.Error())
					continue
				}
				listed = append(listed, l)
			}
		case "locomotive-config":
			var doc locomotiveFileXML
			if err := decode(data, &doc); err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", name, err))
				return nil
			}
			l, err := convert(name, doc.Locomotive)
			if err != nil {
				r.Problems = append(r.Problems, err.Error())
				return nil
			}
			// The same locomotive twice (a backup copy, say): the newer.
			if had, ok := own[l.ID]; !ok || l.Updated.After(had.Updated) {
				own[l.ID] = l
			}
		}
		return nil
	}
	for _, f := range files {
		if err := read(f.Name, f.Data); err != nil {
			return r, err
		}
	}
	for _, l := range own {
		r.Locomotives = append(r.Locomotives, l)
	}
	for _, l := range listed {
		if _, ok := own[l.ID]; ok {
			continue
		}
		own[l.ID] = l
		r.Locomotives = append(r.Locomotives, l)
		r.Problems = append(r.Problems, fmt.Sprintf("%s has no locomotive file, so it comes without CV values", l.ID))
	}
	sort.Slice(r.Locomotives, func(i, j int) bool { return r.Locomotives[i].ID < r.Locomotives[j].ID })
	sort.Strings(r.Problems)
	return r, nil
}

// rootName is the name of a document's first element.
func rootName(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = decoder.CharsetReader
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

func decode(data []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = decoder.CharsetReader
	return dec.Decode(v)
}

// dateLayouts are the ways JMRI has written dateUpdated.
var dateLayouts = []string{"2006-01-02T15:04:05.000-07:00", time.RFC3339Nano, "02-Jan-2006 15:04:05", "Jan 2, 2006 3:04:05 PM"}

func convert(file string, x locomotiveXML) (Locomotive, error) {
	l := Locomotive{
		File: path.Base(file), ID: strings.TrimSpace(x.ID),
		RoadName: strings.TrimSpace(x.RoadName), RoadNumber: strings.TrimSpace(x.RoadNumber),
		Manufacturer: strings.TrimSpace(x.Mfg), Model: strings.TrimSpace(x.Model), Owner: strings.TrimSpace(x.Owner),
		Comment:       strings.TrimSpace(x.Comment),
		DecoderFamily: strings.TrimSpace(x.Decoder.Family), DecoderModel: strings.TrimSpace(x.Decoder.Model),
		ManufacturerID: strings.TrimSpace(x.ManufacturerID), ProductID: strings.TrimSpace(x.ProductID),
	}
	if l.ID == "" {
		return l, fmt.Errorf("%s has a locomotive with no name", file)
	}
	if x.FileName != "" {
		l.File = x.FileName
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, strings.TrimSpace(x.DateUpdated)); err == nil {
			l.Updated = t
			break
		}
	}
	number := x.LocoAddress.DCC.Number
	if number == "" {
		number = x.DCCAddress
	}
	n, err := strconv.Atoi(strings.TrimSpace(number))
	if err != nil || n < 1 || n > train.MaxAddress {
		return l, fmt.Errorf("%s: %q is not a DCC address Interlock can drive", l.ID, number)
	}
	l.Address = uint16(n)
	l.Long = x.LocoAddress.DCC.Long == "yes" || x.LocoAddress.Protocol == "dcc_long" || n > 127
	if x.LocoAddress.DCC.Long == "no" || x.LocoAddress.Protocol == "dcc_short" {
		l.Long = n > 127
	}
	for _, f := range x.FunctionLabels {
		num, err := strconv.Atoi(f.Num)
		label := strings.TrimSpace(f.Text)
		if err != nil || num < 0 || num > train.MaxFunction || label == "" {
			continue
		}
		if l.Functions == nil {
			l.Functions = make(map[int]string)
		}
		l.Functions[num] = label
		if f.Lockable == "false" {
			l.Momentary = append(l.Momentary, num)
		}
	}
	sort.Ints(l.Momentary)
	if x.Values != nil {
		l.Values = true
		l.CVs = make(map[string]int, len(x.Values.CVs))
		for _, cv := range x.Values.CVs {
			v, err := strconv.Atoi(strings.TrimSpace(cv.Value))
			if err != nil || cv.Name == "" || v < 0 || v > 0xFFFF {
				continue
			}
			l.CVs[cv.Name] = v
		}
	}
	return l, nil
}

// Import is the locomotive ready for the roster. Its decoder's identity
// is the manufacturer and version in CVs 8 and 7 with the product ID JMRI
// noted. The decoder's definition file is left for the caller to find.
func (l Locomotive) Import() train.Import {
	imp := train.Import{
		Key: l.ID,
		Entry: train.Entry{
			Name: l.ID, Address: l.Address, Long: l.Long, Functions: l.Functions, Momentary: l.Momentary,
			RoadName: l.RoadName, RoadNumber: l.RoadNumber, Manufacturer: l.Manufacturer, Model: l.Model,
			Owner: l.Owner, Comment: l.Comment,
		},
		CVs:     l.CVs,
		Updated: l.Updated,
	}
	if l.DecoderModel == "" && l.DecoderFamily == "" {
		return imp
	}
	d := &train.Decoder{Family: l.DecoderFamily, Model: l.DecoderModel}
	mfg, ok := l.CVs["8"]
	if !ok {
		mfg, _ = strconv.Atoi(l.ManufacturerID)
	}
	if mfg > 0 {
		id := &train.Identity{Manufacturer: mfg, Version: l.CVs["7"]}
		if p, err := strconv.ParseUint(l.ProductID, 10, 32); err == nil {
			v := uint32(p)
			id.ProductID = &v
		}
		d.Identity = id
	}
	imp.Decoder = d
	return imp
}
