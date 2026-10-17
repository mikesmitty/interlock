package decoder

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestIdentify(t *testing.T) {
	tests := []struct {
		name string
		cvs  map[int]int
		fail map[string]bool
		want Identity
		ops  string
	}{
		{
			name: "ESU LokSound 5",
			cvs:  map[int]int{8: 151, 7: 255, 261: 0x9A, 262: 0x00, 263: 0x00, 264: 0x01},
			want: Identity{Manufacturer: 151, Version: 255, ProductID: 16777370, HasProductID: true},
			ops:  "R8 R7 W31=0 W32=255 R261 R262 R263 R264",
		},
		{
			name: "older ESU",
			cvs:  map[int]int{8: 151, 7: 70},
			want: Identity{Manufacturer: 151, Version: 70},
			ops:  "R8 R7",
		},
		{
			name: "Digitrax has no product ID",
			cvs:  map[int]int{8: 129, 7: 50},
			want: Identity{Manufacturer: 129, Version: 50},
			ops:  "R8 R7",
		},
		{
			name: "DIY big-endian",
			cvs:  map[int]int{8: 13, 7: 1, 47: 0, 48: 1, 49: 2, 50: 3},
			want: Identity{Manufacturer: 13, Version: 1, ProductID: 0x010203, HasProductID: true},
			ops:  "R8 R7 R47 R48 R49 R50",
		},
		{
			name: "TCS mobile",
			cvs:  map[int]int{8: 153, 7: 5, 249: 4},
			want: Identity{Manufacturer: 153, Version: 5, ProductID: 4, HasProductID: true},
			ops:  "R8 R7 R249",
		},
		{
			name: "TCS WOW version 4",
			cvs:  map[int]int{8: 153, 7: 4, 249: 170, 248: 5, 111: 9, 110: 9},
			want: Identity{Manufacturer: 153, Version: 4, ProductID: 1450, HasProductID: true},
			ops:  "R8 R7 R249 R248 R111 R110",
		},
		{
			name: "PIKO decimal digits",
			cvs:  map[int]int{8: 162, 7: 1, 315: 3, 316: 65, 317: 37},
			want: Identity{Manufacturer: 162, Version: 1, ProductID: 36537, HasProductID: true},
			ops:  "R8 R7 W31=0 W32=255 R315 R316 R317",
		},
		{
			name: "QSI",
			cvs:  map[int]int{8: 113, 7: 9},
			want: Identity{Manufacturer: 113, Version: 9, ProductID: 0, HasProductID: true},
			ops:  "R8 R7 W49=254 W50=4 R56 W50=5 R56",
		},
		{
			name: "Doehler & Haass without the optional CV",
			cvs:  map[int]int{8: 97, 7: 3},
			fail: map[string]bool{"R261": true},
			want: Identity{Manufacturer: 97, Version: 3},
			ops:  "R8 R7 R261 R261 R261",
		},
		{
			name: "retries",
			cvs:  map[int]int{8: 145, 7: 40, 250: 221},
			fail: map[string]bool{"R250": false},
			want: Identity{Manufacturer: 145, Version: 40, ProductID: 221, HasProductID: true},
			ops:  "R8 R7 R250",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := &fakeBus{cvs: tt.cvs, fail: tt.fail}
			got, err := Identify(context.Background(), bus)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Identify = %+v, want %+v", got, tt.want)
			}
			if ops := strings.Join(bus.log, " "); ops != tt.ops {
				t.Errorf("operations = %s, want %s", ops, tt.ops)
			}
		})
	}
	bus := &fakeBus{cvs: map[int]int{}, fail: map[string]bool{"R8": true}}
	if _, err := Identify(context.Background(), bus); err == nil || !strings.Contains(err.Error(), "CV 8") {
		t.Errorf("no decoder = %v", err)
	}
	if got := strings.Join(bus.log, " "); got != "R8 R8 R8" {
		t.Errorf("CV 8 tried %s", got)
	}
}

func TestReadAddress(t *testing.T) {
	tests := []struct {
		name string
		cvs  map[int]int
		want Address
		ops  string
	}{
		{"short", map[int]int{29: 0x06, 1: 3, 17: 0xC0, 18: 0}, Address{Address: 3}, "R29 R1"},
		{"long", map[int]int{29: 0x26, 17: 0xD9, 18: 0xF1}, Address{Address: 6641, Long: true}, "R29 R17 R18"},
		{"long under 128", map[int]int{29: 0x22, 17: 0xC0, 18: 5}, Address{Address: 5, Long: true}, "R29 R17 R18"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := &fakeBus{cvs: tt.cvs}
			got, err := ReadAddress(context.Background(), bus)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("ReadAddress = %+v, want %+v", got, tt.want)
			}
			if ops := strings.Join(bus.log, " "); ops != tt.ops {
				t.Errorf("operations = %s, want %s", ops, tt.ops)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	idx, err := parseIndex(strings.NewReader(testIndex))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		id   Identity
		want string
	}{
		{"exact product ID", Identity{Manufacturer: 151, Version: 255, ProductID: 33554679, HasProductID: true}, "LokSound 5 exact"},
		{"product ID only a retired model has", Identity{Manufacturer: 151, Version: 255, ProductID: 16777404, HasProductID: true}, "LokSound 5"},
		{"unknown product ID", Identity{Manufacturer: 151, Version: 255, ProductID: 1, HasProductID: true}, "LokSound 5"},
		{"wrong version", Identity{Manufacturer: 151, Version: 3}, ""},
		{"model version range", Identity{Manufacturer: 129, Version: 50}, "DH163"},
		{"outside the family's range", Identity{Manufacturer: 129, Version: 52}, ""},
		{"unknown manufacturer", Identity{Manufacturer: 7, Version: 1}, ""},
	}
	for _, tt := range tests {
		var got []string
		for _, c := range idx.Match(tt.id) {
			s := c.Model
			if c.Exact {
				s += " exact"
			}
			got = append(got, s)
		}
		if strings.Join(got, ", ") != tt.want {
			t.Errorf("%s: Match = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestVersions(t *testing.T) {
	fam := Family{LowVersion: 10, HighVersion: 20}
	open := Family{LowVersion: -1, HighVersion: -1}
	tests := []struct {
		name string
		f    Family
		m    Model
		want []VersionRange
	}{
		{"family range", fam, Model{LowVersion: -1, HighVersion: -1}, []VersionRange{{10, 20}}},
		{"model replaces one bound", fam, Model{LowVersion: 15, HighVersion: -1}, []VersionRange{{15, 20}}},
		{"one bound only", open, Model{LowVersion: 7, HighVersion: -1}, []VersionRange{{7, 7}}},
		{"none", open, Model{LowVersion: -1, HighVersion: -1}, nil},
		{"versionCV ranges", open, Model{LowVersion: -1, HighVersion: -1, VersionCVs: []VersionRange{{0, 31}, {32, 63}}}, []VersionRange{{0, 31}, {32, 63}}},
		{"versionCV open end", fam, Model{LowVersion: -1, HighVersion: -1, VersionCVs: []VersionRange{{30, -1}}}, []VersionRange{{10, 20}}},
	}
	for _, tt := range tests {
		got := tt.m.Versions(tt.f)
		if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) || (len(got) > 1 && got[1] != tt.want[1]) {
			t.Errorf("%s: Versions = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestRealMatch checks matching against the decoders on the user's roster.
func TestRealMatch(t *testing.T) {
	dir := os.Getenv("INTERLOCK_JMRI_XML")
	if dir == "" {
		t.Skip("INTERLOCK_JMRI_XML is not set")
	}
	lib, err := Open(Options{Source: SourceFolder, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := lib.Index()
	if err != nil {
		t.Fatal(err)
	}
	// ESU gave the LokPilot and LokSound micro DCC Direct one product ID,
	// so both match and the user picks.
	var models []string
	for _, c := range idx.Match(Identity{Manufacturer: 151, Version: 255, ProductID: 16777370, HasProductID: true}) {
		if c.Exact {
			models = append(models, c.Model)
		}
	}
	if got := strings.Join(models, ", "); got != "LokPilot 5 micro DCC Direct, LokSound 5 micro DCC Direct" {
		t.Errorf("product 16777370 matched %s", got)
	}
}
