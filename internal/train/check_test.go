package train_test

import (
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/train"
)

func TestCheckDecoder(t *testing.T) {
	b := newBench(t)
	store := train.OpenCVStore(filepath.Join(t.TempDir(), train.CVDirName), nil)
	m := train.New(train.Options{Bus: b.local, Logger: slog.New(slog.DiscardHandler), CVs: store})
	pid := func(v uint32) *uint32 { return &v }
	lokPilot := &train.Identity{Manufacturer: 151, Version: 255, ProductID: pid(0x0200009C)}
	lokSound := &train.Identity{Manufacturer: 151, Version: 255, ProductID: pid(0x02000096)}
	add := func(name string, address uint16, d *train.Decoder) string {
		t.Helper()
		e, err := m.Create(train.Entry{Name: name, Address: address, Long: address > 127})
		if err != nil {
			t.Fatal(err)
		}
		if d == nil {
			return e.ID
		}
		_, got, err := m.AddDecoder(b.ctx, e.ID, *d, true)
		if err != nil {
			t.Fatal(err)
		}
		return e.ID + "/" + got.ID
	}
	set := func(loco string, values map[string]int) {
		t.Helper()
		if err := store.Set(decoderOf(loco), values); err != nil {
			t.Fatal(err)
		}
	}
	// A fleet of the same decoders: two at their own addresses, two
	// running as a pair at one address, and a locomotive with no decoder
	// details at that address too.
	p150 := add("P42 150", 150, &train.Decoder{File: "ESU_LokPilot5.xml", Model: "LokPilot 5 nano DCC", Identity: lokPilot})
	p156 := add("P42 156", 156, &train.Decoder{File: "ESU_LokPilot5.xml", Model: "LokPilot 5 nano DCC", Identity: lokPilot})
	gp40a := add("GP40 A", 3, &train.Decoder{File: "ESU_LokSound5.xml", Model: "LokSound 5", Identity: lokSound})
	gp40b := add("GP40 B", 3, &train.Decoder{File: "ESU_LokSound5.xml", Model: "LokSound 5", Identity: lokSound})
	goat := add("Yard goat", 3, nil)
	set(p150, map[string]int{"29": 34, "17": 192, "18": 150, "2": 5})
	if err := store.Import(decoderOf(p156), map[string]int{"29": 34, "17": 192, "18": 156}, time.Now()); err != nil {
		t.Fatal(err)
	}
	set(gp40a, map[string]int{"29": 6, "1": 3, "3": 10, "4": 10})
	set(gp40b, map[string]int{"29": 6, "1": 3, "3": 10, "4": 12})

	long := func(address int, id train.Identity, cv29 int) train.OnTrack {
		return train.OnTrack{Identity: id, Address: uint16(address), Long: true, CVs: map[string]int{"8": id.Manufacturer, "7": id.Version, "29": cv29, "17": 192 | address>>8, "18": address & 0xFF}}
	}
	short := func(address int, id train.Identity, cv29 int) train.OnTrack {
		return train.OnTrack{Identity: id, Address: uint16(address), CVs: map[string]int{"8": id.Manufacturer, "7": id.Version, "29": cv29, "1": address}}
	}
	digitrax := train.Identity{Manufacturer: 129, Version: 1}
	otherESU := train.Identity{Manufacturer: 151, Version: 255, ProductID: pid(0x02000123)}

	for _, tc := range []struct {
		name  string
		loco  string
		track train.OnTrack
		// decoder is what reading CVs of the decoder on the track gives.
		decoder map[string]int
		match   bool
		diffs   []train.Difference
		told    []string
		alike   []string
		others  []string
	}{
		{name: "the decoder at its address", loco: p150, track: long(150, *lokPilot, 34), match: true},
		{
			name: "another of the same model", loco: p150, track: long(156, *lokPilot, 34),
			diffs:  []train.Difference{{What: "address", Roster: 150, Track: 156, RosterLong: true, TrackLong: true}},
			others: []string{p156},
		},
		{
			name: "another manufacturer", loco: p150, track: long(150, digitrax, 34),
			diffs: []train.Difference{{What: "manufacturer", Roster: 151, Track: 129}},
		},
		{
			name: "another model", loco: p150, track: long(150, otherESU, 34),
			diffs: []train.Difference{{What: "product", Roster: 0x0200009C, Track: 0x02000123}},
		},
		{
			name: "a saved value differs", loco: p150, track: long(150, *lokPilot, 38),
			diffs: []train.Difference{{What: "cv", CV: "29", Roster: 34, Track: 38}},
		},
		{
			name: "an imported value differs", loco: p156, track: long(156, *lokPilot, 38),
			diffs: []train.Difference{{What: "cv", CV: "29", Roster: 34, Track: 38, Imported: true}},
		},
		{
			name: "told from its pair by a CV", loco: gp40a, track: short(3, *lokSound, 6), decoder: map[string]int{"4": 10},
			match: true, told: []string{"4"},
		},
		{
			name: "it is the other of the pair", loco: gp40a, track: short(3, *lokSound, 6), decoder: map[string]int{"4": 12},
			diffs: []train.Difference{{What: "cv", CV: "4", Roster: 10, Track: 12}}, told: []string{"4"},
			others: []string{gp40b, goat},
		},
		{
			name: "the pair, without reading", loco: gp40a, track: short(3, *lokSound, 6),
			alike: []string{gp40b},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opts train.CheckOptions
			var read []string
			if tc.decoder != nil {
				opts.Read = func(cv string) (int, error) {
					read = append(read, cv)
					v, ok := tc.decoder[cv]
					if !ok {
						return 0, errors.New("no such CV")
					}
					return v, nil
				}
			}
			id, dec := trainOf(tc.loco), decoderOf(tc.loco)
			got, err := m.CheckDecoder(id, dec, tc.track, opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.Match != tc.match {
				t.Errorf("match = %v, want %v", got.Match, tc.match)
			}
			if !slices.Equal(got.Differences, tc.diffs) {
				t.Errorf("differences = %+v, want %+v", got.Differences, tc.diffs)
			}
			if !slices.Equal(got.Told, tc.told) || !slices.Equal(read, tc.told) {
				t.Errorf("told = %v after reading %v, want %v", got.Told, read, tc.told)
			}
			if names := matchNames(got.Alike); !slices.Equal(names, tc.alike) {
				t.Errorf("alike = %v, want %v", names, tc.alike)
			}
			if names := matchNames(got.Others); !slices.Equal(names, tc.others) {
				t.Errorf("others = %v, want %v", names, tc.others)
			}
			for cv, v := range tc.track.CVs {
				if got.Read[cv] != v {
					t.Errorf("read %v, without CV %s from the track", got.Read, cv)
				}
			}
		})
	}

	// Without an identity saved, the definition says what to expect.
	spare := add("Spare", 3, &train.Decoder{File: "ESU_LokSound5.xml", Model: "LokSound 5"})
	got, err := m.CheckDecoder(trainOf(spare), decoderOf(spare), short(3, digitrax, 6), train.CheckOptions{Manufacturer: 151, ProductIDs: []uint32{0x02000096}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Match || len(got.Differences) != 1 || got.Differences[0].What != "manufacturer" {
		t.Errorf("another manufacturer than the definition's = %+v", got)
	}
	got, err = m.CheckDecoder(trainOf(spare), decoderOf(spare), short(3, train.Identity{Manufacturer: 151, Version: 255, ProductID: pid(0x02000096)}, 6), train.CheckOptions{Manufacturer: 151, ProductIDs: []uint32{0x02000096}})
	if err != nil {
		t.Fatal(err)
	}
	// It is alike to the pair, which have the same model at the same
	// address, and nothing saved for it tells it from them.
	if got.Match || len(got.Differences) != 0 || !slices.Equal(matchNames(got.Alike), []string{gp40a, gp40b}) {
		t.Errorf("a decoder with nothing saved = %+v", got)
	}
	if _, err := m.CheckDecoder("nope", "x", train.OnTrack{}, train.CheckOptions{}); !errors.Is(err, train.ErrNotFound) {
		t.Errorf("check of an unknown locomotive = %v", err)
	}
}

// trainOf and decoderOf split the "train/decoder" names the test uses.
func trainOf(loco string) string {
	id, _, _ := strings.Cut(loco, "/")
	return id
}

func decoderOf(loco string) string {
	_, dec, _ := strings.Cut(loco, "/")
	return dec
}

func matchNames(ms []train.DecoderMatch) []string {
	var names []string
	for _, m := range ms {
		if m.Decoder == "" {
			names = append(names, m.Train)
			continue
		}
		names = append(names, m.Train+"/"+m.Decoder)
	}
	return names
}
