package jmri

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRoster = `<?xml version="1.0" encoding="UTF-8"?>
<roster-config>
  <roster>
    <locomotive id="Yard switcher" fileName="Yard_switcher.xml" roadNumber="7" roadName="Acme" dccAddress="7">
      <decoder model="DH163" family="Series 3" />
      <locoaddress><dcclocoaddress number="7" longaddress="no" /><number>7</number><protocol>dcc_short</protocol></locoaddress>
    </locomotive>
    <locomotive id="GP40 1" fileName="GP40_1.xml" dccAddress="6641">
      <decoder model="LokSound 5" family="ESU LokSound 5" />
      <locoaddress><dcclocoaddress number="6641" longaddress="yes" /></locoaddress>
    </locomotive>
  </roster>
</roster-config>`

const testLocomotive = `<?xml version="1.0" encoding="UTF-8"?>
<locomotive-config>
  <locomotive id="GP40 1" fileName="GP40_1.xml" roadNumber="6641" roadName="CSX" mfg="Atlas" owner="Me" model="GP40" dccAddress="6641" comment="Weathered" manufacturerID="151" productID="16777370">
    <dateUpdated>2026-05-28T23:57:19.140+00:00</dateUpdated>
    <decoder model="LokSound 5" family="ESU LokSound 5" comment="" maxFnNum="31" />
    <locoaddress>
      <dcclocoaddress number="6641" longaddress="yes" />
      <number>6641</number>
      <protocol>dcc_long</protocol>
    </locoaddress>
    <functionlabels>
      <functionlabel num="0" lockable="true">Light</functionlabel>
      <functionlabel num="2" lockable="false">Horn</functionlabel>
      <functionlabel num="99" lockable="true">Out of range</functionlabel>
    </functionlabels>
    <values>
      <decoderDef><varValue item="Short Address" value="3" /></decoderDef>
      <CVvalue name="1" value="3" />
      <CVvalue name="7" value="255" />
      <CVvalue name="8" value="151" />
      <CVvalue name="0.255.261" value="154" />
      <CVvalue name="bad" value="x" />
    </values>
  </locomotive>
</locomotive-config>`

func TestRead(t *testing.T) {
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	for name, body := range map[string]string{"Subway.jmri/roster.xml": testRoster, "Subway.jmri/roster/GP40_1.xml": testLocomotive, "Subway.jmri/panels.xml": "<layout-config/>", "Subway.jmri/notes.txt": "x"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		files []File
	}{
		{"files", []File{{"roster.xml", []byte(testRoster)}, {"GP40_1.xml", []byte(testLocomotive)}}},
		{"zip", []File{{"profile.zip", zipped.Bytes()}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Read(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Locomotives) != 2 {
				t.Fatalf("locomotives = %+v", r.Locomotives)
			}
			gp, yard := r.Locomotives[0], r.Locomotives[1]
			if gp.ID != "GP40 1" || gp.Address != 6641 || !gp.Long || gp.RoadName != "CSX" || gp.Manufacturer != "Atlas" || gp.Comment != "Weathered" || !gp.Values {
				t.Errorf("GP40 = %+v", gp)
			}
			if gp.Functions[2] != "Horn" || len(gp.Functions) != 2 || len(gp.Momentary) != 1 || gp.Momentary[0] != 2 {
				t.Errorf("GP40 labels = %v, momentary %v", gp.Functions, gp.Momentary)
			}
			if len(gp.CVs) != 4 || gp.CVs["0.255.261"] != 154 || gp.Updated.Year() != 2026 {
				t.Errorf("GP40 CVs = %v, updated %v", gp.CVs, gp.Updated)
			}
			if yard.ID != "Yard switcher" || yard.Address != 7 || yard.Long || yard.Values || yard.DecoderModel != "DH163" {
				t.Errorf("yard = %+v", yard)
			}
			if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "Yard switcher has no locomotive file") {
				t.Errorf("problems = %q", r.Problems)
			}

			imp := gp.Import()
			if imp.Key != "GP40 1" || imp.Entry.Name != "GP40 1" || imp.Entry.RoadNumber != "6641" || imp.Decoder == nil || imp.Decoder.Model != "LokSound 5" {
				t.Fatalf("import = %+v", imp)
			}
			if id := imp.Decoder.Identity; id == nil || id.Manufacturer != 151 || id.Version != 255 || id.ProductID == nil || *id.ProductID != 16777370 {
				t.Errorf("identity = %+v", imp.Decoder.Identity)
			}
			if imp := yard.Import(); imp.Decoder == nil || imp.Decoder.Identity != nil {
				t.Errorf("yard import = %+v", imp.Decoder)
			}
		})
	}
	r, err := Read([]File{{"broken.xml", []byte("<locomotive-config><locomotive id=\"x\" dccAddress=\"0\"/></locomotive-config>")}, {"bad.zip", []byte("nope")}})
	if err != nil || len(r.Locomotives) != 0 || len(r.Problems) != 2 {
		t.Errorf("broken = %+v, %v", r, err)
	}
}

// TestReadRealRoster reads a JMRI roster folder named by
// INTERLOCK_JMRI_ROSTER (roster.xml and the locomotive files).
func TestReadRealRoster(t *testing.T) {
	dir := os.Getenv("INTERLOCK_JMRI_ROSTER")
	if dir == "" {
		t.Skip("INTERLOCK_JMRI_ROSTER is not set")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var files []File
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, File{Name: filepath.Base(p), Data: data})
	}
	r, err := Read(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range r.Locomotives {
		t.Logf("%s: %d%s, %s / %s, %d CVs, %d labels", l.ID, l.Address, map[bool]string{true: " long"}[l.Long], l.DecoderFamily, l.DecoderModel, len(l.CVs), len(l.Functions))
	}
	for _, p := range r.Problems {
		t.Log("problem:", p)
	}
}
