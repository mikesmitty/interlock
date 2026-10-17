package cdi

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *CDI {
	t.Helper()
	data, err := os.ReadFile("../sim/" + name)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return doc
}

// Addresses from canbus-outpost's config_layout.h, which its own tests
// check against the same XML.
func TestOutpostLayout(t *testing.T) {
	doc := load(t, "canbus-outpost.cdi.xml")
	if doc.Identification == nil || doc.Identification.Model != "CAN Bus Outpost" || doc.Identification.SoftwareVersion != "0.2.1" {
		t.Errorf("identification = %+v", doc.Identification)
	}
	if len(doc.Segments) != 4 {
		t.Fatalf("segments = %d, want 4", len(doc.Segments))
	}
	wantSeg := []struct {
		name   string
		origin uint32
		size   uint32
	}{
		{"Node Identity", 0, 128},
		{"Identify", 128, 2},
		{"Switch Machines", 132, 480},
		{"RailCom Detectors", 612, 384},
	}
	for i, w := range wantSeg {
		seg := doc.Segments[i]
		if seg.Space != 253 || seg.Name != w.name || seg.Origin != w.origin || seg.Size != w.size {
			t.Errorf("segment %d = %s space %d origin %d size %d, want %+v", i, seg.Name, seg.Space, seg.Origin, seg.Size, w)
		}
	}
	servos := doc.Segments[2].Items[0]
	if servos.Kind != KindGroup || servos.Replication != 4 || servos.Size != 120 || servos.Address != 132 {
		t.Errorf("servo group = %+v", servos)
	}
	if servos.RepName(1) != "Servo 2" {
		t.Errorf("RepName(1) = %q", servos.RepName(1))
	}
	if len(servos.Children) != 17 {
		t.Errorf("servo children = %d, want 17", len(servos.Children))
	}
	if last := servos.Children[16]; last.Kind != KindString || last.Size != 3 || last.Address != 132+117 || last.Name != "" {
		t.Errorf("servo padding = %+v", last)
	}

	index := doc.Lookup()
	if len(index) != 2+2+4*17+4*6 {
		t.Errorf("variables = %d, want %d", len(index), 2+2+4*17+4*6)
	}
	wantVar := map[string]string{
		"253:0":   "Node Identity/User Name",
		"253:64":  "Node Identity/User Description",
		"253:129": "Identify/LED Timeout",
		"253:132": "Switch Machines/Servo 1/Throw Command Event",
		"253:164": "Switch Machines/Servo 1/Center Command Event",
		"253:172": "Switch Machines/Servo 1/Thrown Position",
		"253:185": "Switch Machines/Servo 1/Description",
		"253:252": "Switch Machines/Servo 2/Throw Command Event",
		"253:539": "Switch Machines/Servo 4/Hold Time",
		"253:540": "Switch Machines/Servo 4/Enabled",
		"253:612": "RailCom Detectors/Track 1/Description",
		"253:692": "RailCom Detectors/Track 1/Reporter ID",
		"253:700": "RailCom Detectors/Track 1/Orientation",
		"253:988": "RailCom Detectors/Track 4/Orientation",
	}
	for key, path := range wantVar {
		v, ok := index[key]
		if !ok {
			t.Errorf("no variable at %s (want %s)", key, path)
			continue
		}
		if got := strings.Join(v.Path, "/"); got != path {
			t.Errorf("%s = %s, want %s", key, got, path)
		}
	}
	if v := index["253:172"]; v.Element.Kind != KindInt || v.Size != 2 || v.Element.Min != "0" || v.Element.Max != "65535" || v.Element.Default != "16384" {
		t.Errorf("Thrown Position = %+v", v.Element)
	}
	if v := index["253:181"]; v.Element.Name != "Default State" || len(v.Element.Map) != 3 || v.Element.Map[2].Value != "No Change" || v.Element.Map[2].Property != "2" {
		t.Errorf("Default State = %+v", v.Element)
	}

	layout := doc.Layout()
	data, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	var back Layout
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	group := back.Segments[2].Items[0]
	if len(group.Replicas) != 4 || group.Replicas[3].Name != "Servo 4" || group.Replicas[3].Items[0].Address != 132+3*120 || group.Replicas[3].Items[0].Key != "253:492" {
		t.Errorf("layout servo 4 = %+v", group.Replicas[3])
	}
	if back.Segments[0].Items[0].Key != "253:0" || back.Segments[0].Items[0].Kind != KindString {
		t.Errorf("layout user name = %+v", back.Segments[0].Items[0])
	}
}

// simple-dcc nests unreplicated groups and uses an acdi element.
func TestCommandStationLayout(t *testing.T) {
	doc := load(t, "simple-dcc.cdi.xml")
	if doc.ACDI == nil || doc.ACDI.Fixed != 4 || doc.ACDI.Var != 2 {
		t.Errorf("acdi = %+v", doc.ACDI)
	}
	origins := []uint32{0, 127, 129, 133, 143, 147}
	if len(doc.Segments) != len(origins) {
		t.Fatalf("segments = %d", len(doc.Segments))
	}
	for i, o := range origins {
		if doc.Segments[i].Origin != o {
			t.Errorf("segment %d origin = %d, want %d", i, doc.Segments[i].Origin, o)
		}
	}
	index := doc.Lookup()
	want := map[string]string{
		"253:0":   "User Identification/User Name",
		"253:63":  "User Identification/User Description",
		"253:127": "DCC Features/Traction/Auto-claim DCC Addresses",
		"253:128": "DCC Features/Waveform/RailCom Cutout",
		"253:129": "Track Current Limits//Main Track Limit (mA)",
		"253:131": "Track Current Limits//Programming Track Limit (mA)",
		"253:138": "Hardware Pin Assignments/Programming Track (Channel B)/Signal Pin",
		"253:155": "MCP2515 CAN//Bit Rate",
	}
	for key, path := range want {
		v, ok := index[key]
		if !ok {
			t.Errorf("no variable at %s (want %s)", key, path)
			continue
		}
		if got := strings.Join(v.Path, "/"); got != path {
			t.Errorf("%s = %s, want %s", key, got, path)
		}
	}
	if doc.Segments[5].Size != 9 {
		t.Errorf("MCP2515 segment size = %d, want 9", doc.Segments[5].Size)
	}
}

func TestParseEdgeCases(t *testing.T) {
	t.Run("not cdi", func(t *testing.T) {
		if _, err := Parse([]byte("<html/>")); !errors.Is(err, ErrNotCDI) {
			t.Errorf("err = %v", err)
		}
		if _, err := Parse(nil); !errors.Is(err, ErrNotCDI) {
			t.Errorf("empty: err = %v", err)
		}
	})
	t.Run("hints offsets unknown elements and nul", func(t *testing.T) {
		src := `<?xml version="1.0" encoding="ISO-8859-1"?>
<cdi><segment space="251" origin="16">
 <name>Caf\xe9</name>
 <group replication="2" offset="4"><name>Line</name><repname>A</repname>
   <hints><visibility hideable="yes" hidden="true"/><readOnly/></hints>
   <int size="2" offset="1"><name>Level</name><min>-100</min><max>100</max><default>-5</default>
     <hints><slider tickSpacing="10" immediate="yes"/><radiobutton/></hints></int>
   <float size="4" formatting="%3.1f"><name>Gain</name></float>
   <eventid><name>Fired</name></eventid>
   <action size="1" offset="2"><name>Do it</name><buttonText>Go</buttonText><dialogText>Sure?</dialogText><value>7</value></action>
   <blob size="10" mode="readwrite"><name>Raw</name></blob>
   <mystery>ignored</mystery>
   <string size="4"><name>Tag</name><link ref="http://x">help</link></string>
 </group>
 <int><name>Tail</name></int>
</segment></cdi>` + "\x00garbage"
		doc, err := Parse([]byte(strings.ReplaceAll(src, `\xe9`, "\xe9")))
		if err != nil {
			t.Fatal(err)
		}
		seg := doc.Segments[0]
		if seg.Space != 251 || seg.Name != "Café" {
			t.Errorf("segment = %+v", seg)
		}
		g := seg.Items[0]
		if g.Address != 20 || g.Replication != 2 || g.Hints == nil || !g.Hints.Hideable || !g.Hints.Hidden || !g.Hints.ReadOnly {
			t.Errorf("group = %+v hints %+v", g, g.Hints)
		}
		if g.RepName(0) != "A" || g.RepName(1) != "Line 2" {
			t.Errorf("rep names = %q %q", g.RepName(0), g.RepName(1))
		}
		// One replica: an offset of 1, the 2 byte int, the 4 byte float, the
		// event ID, an offset of 2, the action, the blob and the string.
		if g.Size != 32 {
			t.Errorf("group size = %d, want 32", g.Size)
		}
		level := g.Children[0]
		if level.Address != 21 || !level.Signed() || level.Hints.Slider == nil || level.Hints.Slider.TickSpacing != 10 || !level.Hints.Slider.Immediate || !level.Hints.RadioButton {
			t.Errorf("level = %+v hints %+v", level, level.Hints)
		}
		if g.Children[1].Formatting != "%3.1f" || g.Children[1].Size != 4 {
			t.Errorf("gain = %+v", g.Children[1])
		}
		action := g.Children[3]
		if action.Kind != KindAction || action.Address != 20+1+2+4+8+2 || action.ButtonText != "Go" || action.DialogText != "Sure?" || action.Value != "7" {
			t.Errorf("action = %+v", action)
		}
		if g.Children[4].Kind != KindBlob || g.Children[4].Mode != "readwrite" || g.Children[4].Size != 10 {
			t.Errorf("blob = %+v", g.Children[4])
		}
		if len(g.Children) != 6 || g.Children[5].Name != "Tag" {
			t.Errorf("children = %d", len(g.Children))
		}
		tail := seg.Items[1]
		if tail.Address != 20+2*32 || tail.Size != 1 {
			t.Errorf("tail = %+v", tail)
		}
		if seg.Size != 20+64+1-16 {
			t.Errorf("segment size = %d", seg.Size)
		}
		vars := doc.Variables()
		if len(vars) != 13 || strings.Join(vars[6].Path, "/") != "Café/Line 2/Level" || vars[6].Address != 21+32 {
			t.Errorf("variables = %d, [6] = %+v", len(vars), vars[6])
		}
	})
	t.Run("truncated", func(t *testing.T) {
		doc, err := Parse([]byte(`<cdi><segment space="253"><name>S</name><int size="2"><name>A</name></int><int size="1"><name>B`))
		if err != nil {
			t.Fatalf("truncated document should still parse: %v", err)
		}
		if len(doc.Segments) != 1 || len(doc.Segments[0].Items) != 2 || !doc.Truncated {
			t.Errorf("doc = %+v truncated %v", doc.Segments, doc.Truncated)
		}
		if full := load(t, "simple-dcc.cdi.xml"); full.Truncated {
			t.Error("complete document reported truncated")
		}
	})
	t.Run("string without size", func(t *testing.T) {
		if _, err := Parse([]byte(`<cdi><segment space="253"><string><name>A</name></string></segment></cdi>`)); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestValues(t *testing.T) {
	el := func(kind Kind, size int, minimum, maximum string, m ...Relation) *Element {
		return &Element{Kind: kind, Name: "x", Size: size, Min: minimum, Max: maximum, Map: m}
	}
	cases := []struct {
		name    string
		el      *Element
		value   string
		bytes   []byte
		decoded string // if different from value
		bad     bool
	}{
		{"u8", el(KindInt, 1, "", ""), "200", []byte{200}, "", false},
		{"u8 overflow", el(KindInt, 1, "", ""), "256", nil, "", true},
		{"u16", el(KindInt, 2, "0", "65535"), "16384", []byte{0x40, 0x00}, "", false},
		{"u16 above max", el(KindInt, 2, "0", "1000"), "1001", nil, "", true},
		{"u32", el(KindInt, 4, "", ""), "4294967295", []byte{0xFF, 0xFF, 0xFF, 0xFF}, "", false},
		{"u64", el(KindInt, 8, "", ""), "18446744073709551615", bytes.Repeat([]byte{0xFF}, 8), "", false},
		{"s8", el(KindInt, 1, "-100", "100"), "-5", []byte{0xFB}, "", false},
		{"s16", el(KindInt, 2, "-1000", "1000"), "-2", []byte{0xFF, 0xFE}, "", false},
		{"s8 below min", el(KindInt, 1, "-100", "100"), "-101", nil, "", true},
		{"not a number", el(KindInt, 1, "", ""), "ten", nil, "", true},
		{"map ok", el(KindInt, 1, "", "", Relation{"0", "Off"}, Relation{"1", "On"}), "1", []byte{1}, "", false},
		{"map bad", el(KindInt, 1, "", "", Relation{"0", "Off"}, Relation{"1", "On"}), "2", nil, "", true},
		{"string", el(KindString, 8, "", ""), "Yard", []byte{'Y', 'a', 'r', 'd', 0, 0, 0, 0}, "", false},
		{"string full", el(KindString, 5, "", ""), "Yard", []byte{'Y', 'a', 'r', 'd', 0}, "", false},
		{"string too long", el(KindString, 4, "", ""), "Yard", nil, "", true},
		{"string utf8", el(KindString, 8, "", ""), "Café", []byte{'C', 'a', 'f', 0xC3, 0xA9, 0, 0, 0}, "", false},
		{"eventid", el(KindEventID, 8, "", ""), "05.01.01.01.22.00.00.01", []byte{5, 1, 1, 1, 0x22, 0, 0, 1}, "", false},
		{"eventid bad", el(KindEventID, 8, "", ""), "05.01", nil, "", true},
		{"float16", el(KindFloat, 2, "", ""), "1.5", []byte{0x3E, 0x00}, "", false},
		{"float32", el(KindFloat, 4, "", ""), "0.5", []byte{0x3F, 0x00, 0x00, 0x00}, "", false},
		{"float64", el(KindFloat, 8, "", ""), "2", []byte{0x40, 0, 0, 0, 0, 0, 0, 0}, "", false},
		{"float above max", el(KindFloat, 4, "0", "1"), "1.5", nil, "", true},
		{"blob", el(KindBlob, 4, "", ""), "DEADBEEF", []byte{0xDE, 0xAD, 0xBE, 0xEF}, "", false},
		{"blob short", el(KindBlob, 4, "", ""), "DEAD", nil, "", true},
		{"action", &Element{Kind: KindAction, Name: "go", Size: 1, Value: "7"}, "7", []byte{7}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.el.Encode(c.value)
			if c.bad {
				if err == nil {
					t.Fatalf("Encode(%q) = % X, want an error", c.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Encode(%q): %v", c.value, err)
			}
			if !bytes.Equal(got, c.bytes) {
				t.Fatalf("Encode(%q) = % X, want % X", c.value, got, c.bytes)
			}
			back, err := c.el.Decode(got)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			want := c.value
			if c.decoded != "" {
				want = c.decoded
			}
			if back != want {
				t.Errorf("Decode = %q, want %q", back, want)
			}
		})
	}
	// Strings decode up to the first NUL and tolerate junk after it.
	s := el(KindString, 6, "", "")
	if got, _ := s.Decode([]byte{'a', 'b', 0, 'z', 'z', 'z'}); got != "ab" {
		t.Errorf("Decode string = %q", got)
	}
	if got, _ := s.Decode([]byte{'a', 'b', 'c', 'd', 'e', 'f', 'g'}); got != "abcdef" {
		t.Errorf("Decode unterminated string = %q", got)
	}
	if _, err := s.Decode([]byte{'a'}); err == nil {
		t.Error("Decode of short buffer should fail")
	}
	if got := el(KindInt, 2, "", "").DefaultBytes(); !bytes.Equal(got, []byte{0, 0}) {
		t.Errorf("DefaultBytes without default = % X", got)
	}
	d := el(KindInt, 2, "0", "65535")
	d.Default = "49152"
	if got := d.DefaultBytes(); !bytes.Equal(got, []byte{0xC0, 0x00}) {
		t.Errorf("DefaultBytes = % X", got)
	}
	if sp, addr, err := ParseKey("253:132"); err != nil || sp != 253 || addr != 132 {
		t.Errorf("ParseKey = %d %d %v", sp, addr, err)
	}
	if _, _, err := ParseKey("nope"); err == nil {
		t.Error("ParseKey(nope) should fail")
	}
}
