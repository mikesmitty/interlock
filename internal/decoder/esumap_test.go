package decoder

import (
	"slices"
	"testing"
	"testing/fstest"
)

func TestESUMapCVs(t *testing.T) {
	v5, err := esuMap("V5", -1, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	v4, err := esuMap("V4", -1, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name             string
		m                *ESUMap
		row, block, item int
		wantCV           string
		wantMask         uint32
	}{
		{"V5 motion", v5, 0, 0, 0, "16.3.257", 0x03},
		{"V5 direction", v5, 0, 0, 1, "16.3.257", 0x0C},
		{"V5 F2", v5, 0, 0, 4, "16.3.258", 0x03},
		{"V5 sensor 4", v5, 0, 0, 39, "16.3.266", 0xC0},
		{"V5 second row", v5, 1, 0, 0, "16.3.273", 0x03},
		{"V5 row 17 on the next page", v5, 16, 0, 0, "16.4.257", 0x03},
		{"V5 last row", v5, 71, 0, 0, "16.7.369", 0x03},
		{"V5 headlight", v5, 0, 1, 0, "16.8.257", 0x01},
		{"V5 AUX2 [2]", v5, 0, 1, 23, "16.8.259", 0x80},
		{"V5 logical 1", v5, 0, 2, 0, "16.8.260", 0x01},
		{"V5 sound slot 32", v5, 0, 3, 31, "16.8.266", 0x80},
		{"V5 sound slot 1 row 18", v5, 17, 3, 0, "16.9.279", 0x01},
		{"V4 sensor 4", v4, 0, 0, 35, "16.2.265", 0xC0},
		{"V4 headlight", v4, 0, 1, 0, "16.2.266", 0x01},
		{"V4 logical 1", v4, 0, 2, 0, "16.2.268", 0x01},
		{"V4 sound slot 24", v4, 0, 3, 23, "16.2.272", 0x80},
		{"V4 row 17", v4, 16, 1, 0, "16.3.266", 0x01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cv, mask := tt.m.CV(tt.row, tt.block, tt.item)
			if cv != tt.wantCV || mask != tt.wantMask {
				t.Errorf("CV(%d, %d, %d) = %s %#x, want %s %#x", tt.row, tt.block, tt.item, cv, mask, tt.wantCV, tt.wantMask)
			}
		})
	}
	// A V5 row is ten CVs of conditions and ten of outputs, logical
	// functions and sounds; a V4 row nine and seven.
	if n := len(v5.CVs()); n != 72*20 {
		t.Errorf("V5 has %d CVs, want %d", n, 72*20)
	}
	if n := len(v4.CVs()); n != 40*16 {
		t.Errorf("V4 has %d CVs, want %d", n, 40*16)
	}
	if _, err := esuMap("V6", -1, -1, nil); err == nil {
		t.Error("V6 was accepted")
	}
}

func TestESUMapItems(t *testing.T) {
	outs := []output{
		{name: "1,36", label: "|"},
		{name: "3,1", label: "Optional Load"},
		{name: "4,2", label: "|"},
		{name: "1", label: "Drive|Going|Halted"},
		{name: "Dim", label: "Bright|Dark"},
		{name: "unlabeled"},
		{name: "9,1", label: "Out of range"},
		{name: "500", label: "Out of range"},
	}
	m, err := esuMap("V5", 8, 6, outs)
	if err != nil {
		t.Fatal(err)
	}
	if m.Rows != 8 {
		t.Errorf("rows = %d, want the model's 8", m.Rows)
	}
	// Row 1's CVs leave out 16.8.258, which holds only AUX7 to AUX14, not
	// on this model.
	if cvs := m.CVs(); len(cvs) != 8*19 || slices.Contains(cvs, "16.8.258") || !slices.Contains(cvs, "16.8.259") {
		t.Errorf("%d CVs: %v", len(cvs), cvs[:20])
	}
	cond, phys, logic, sound := m.Blocks[0].Items, m.Blocks[1].Items, m.Blocks[2].Items, m.Blocks[3].Items
	if len(cond) != 40 || len(phys) != 24 || len(logic) != 24 || len(sound) != 32 {
		t.Fatalf("block sizes %d %d %d %d", len(cond), len(phys), len(logic), len(sound))
	}
	if cond[0] != (ESUItem{Name: "Drive", On: "Going", Off: "Halted"}) {
		t.Errorf("item 1 = %+v", cond[0])
	}
	// "Dim" is the fifth output element, so it renames the fifth item, F2.
	if cond[4] != (ESUItem{Name: "Dim", On: "Bright", Off: "Dark"}) {
		t.Errorf("fifth item = %+v", cond[4])
	}
	if cond[1] != (ESUItem{Name: "Direction", On: "Forward", Off: "Reverse"}) || cond[2].Name != "F0" || cond[33].Name != "F31" || cond[34].Name != "Wheel sensor" {
		t.Errorf("conditions = %+v", cond[:5])
	}
	if !cond[35].Hidden || cond[36].Name != "Sensor 1" {
		t.Errorf("reserved = %+v, next = %+v", cond[35], cond[36])
	}
	if logic[0].Name != "Optional Load" || logic[1].Name != "Logical function 2" || !sound[1].Hidden || sound[0].Name != "Sound slot 1" {
		t.Errorf("logical %+v, sounds %+v", logic[:2], sound[:2])
	}
	// Six outputs: the headlight, rear light and AUX1 to AUX4; the second
	// configurations stay.
	var shown []string
	for _, it := range phys {
		if !it.Hidden {
			shown = append(shown, it.Name)
		}
	}
	want := []string{"Headlight [1]", "Rear light [1]", "AUX1 [1]", "AUX2 [1]", "AUX3", "AUX4", "Headlight [2]", "Rear light [2]", "AUX1 [2]", "AUX2 [2]"}
	if !slices.Equal(shown, want) {
		t.Errorf("physical outputs = %v, want %v", shown, want)
	}
}

func TestESUMapDefinition(t *testing.T) {
	fsys := fstest.MapFS{
		"programmers/Comprehensive.xml": {Data: []byte(testProgrammer)},
		"decoders/ESU.xml": {Data: []byte(`<decoder-config><decoder>
  <family name="LokSound 5" mfg="Electronic Solutions Ulm GmbH">
    <model model="LS5" numOuts="20" extFnsESU="V5"/>
    <output name="3,1" label="Optional Load"/>
  </family>
  <programming direct="yes"><capability><name>Indexed CV access</name><parameter name="PI">31</parameter><parameter name="SI">32</parameter><parameter name="cvFirst">false</parameter></capability></programming>
  <variables><variable item="Vstart" CV="2"><decVal/></variable></variables>
</decoder></decoder-config>`)},
	}
	d, err := loadDefinition(&treeLoader{fsys: fsys}, "ESU.xml", "LS5")
	if err != nil {
		t.Fatal(err)
	}
	var node *Node
	for _, p := range d.Panes {
		if p.Name == "Function Map" {
			node = &p.Nodes[0].Children[0]
			// The browser walks every pane's items.
			if p.Items == nil {
				t.Error("the map's pane has items null")
			}
		}
	}
	if node == nil || node.Kind != NodeESUFnMap || node.ESUMap == nil {
		t.Fatalf("Function Map pane = %s", dump(d.Panes))
	}
	if node.ESUMap.Rows != 72 || node.ESUMap.Blocks[2].Items[0].Name != "Optional Load" {
		t.Errorf("map = %d rows, logical 1 %q", node.ESUMap.Rows, node.ESUMap.Blocks[2].Items[0].Name)
	}
	// The map's CVs are the definition's, so they can be read and written.
	has := func(cv string) bool { return slices.ContainsFunc(d.CVs, func(c CV) bool { return c.Name == cv }) }
	if !has("16.3.257") || !has("16.12.375") || len(d.CVs) != 1+72*20 {
		t.Errorf("%d CVs; 16.3.257 %v, 16.12.375 %v", len(d.CVs), has("16.3.257"), has("16.12.375"))
	}
	if _, err := d.Programming.Resolve("16.8.257"); err != nil {
		t.Errorf("resolve: %v", err)
	}
}
