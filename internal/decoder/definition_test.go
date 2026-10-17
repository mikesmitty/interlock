package decoder

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

const testProgrammer = `<programmer-config><programmer decoderFilePanes="yes">
  <pane><name>Basic</name>
    <column><dccaddress/><display item="Vstart"/><display item="Missing thing"/></column>
    <column><display item="Missing only"/></column>
  </pane>
  <pane><name>Lights</name><column><display item="Not here"/></column></pane>
  <pane><name>Function Map</name><column><fnmapping/></column></pane>
  <pane><name>Sound</name><column><display item="Volume"/></column></pane>
  <pane><name>CVs</name><column><cvtable/></column></pane>
</programmer></programmer-config>`

const testDecoder = `<?xml version="1.0" encoding="utf-8"?>
<decoder-config xmlns:xi="http://www.w3.org/2001/XInclude">
 <decoder>
  <family name="Acme" mfg="Acme Co" lowVersionID="1" highVersionID="9">
   <model model="A1" numOuts="2" numFns="4" productID="11"><output name="2" label="Red|Pin 2"/></model>
   <model model="A2" numOuts="4" productID="12"/>
   <functionlabels><functionlabel num="0" lockable="true"><text>Light</text></functionlabel><functionlabel num="2" lockable="false"><text>Horn</text></functionlabel></functionlabels>
  </family>
  <programming direct="yes" paged="no">
   <capability><name>Indexed CV access</name><parameter name="PI">31</parameter><parameter name="SI">32</parameter><parameter name="cvFirst">false</parameter></capability>
  </programming>
  <variables>
    <variables include="A2">
      <variable item="Volume" CV="16.0.259"><decVal max="15"/></variable>
    </variables>
    <constant item="Fixed" default="1"/>
    <variable item="Vstart" CV="2" default="3"><decVal/><label>Start voltage</label><label xml:lang="de">Anfahrspannung</label></variable>
    <xi:include href="http://jmri.org/xml/decoders/nmra/address.xml"/>
    <variable item="Mode" CV="50" mask="XXXXXXVV"><enumVal>
        <enumChoice choice="Off"/>
        <enumChoice choice="A2 only" include="A2"/>
        <enumChoiceGroup><name>More</name><enumChoice choice="Blink"/><enumChoice choice="Fade" value="7"/></enumChoiceGroup>
        <enumChoice choice="After"/>
    </enumVal></variable>
    <variable item="Delay" CV="51" mask="XXXXVVVV">
      <qualifier><variableref>Mode</variableref><relation>ne</relation><value>0</value></qualifier>
      <qualifier><variableref>Nonexistent</variableref><relation>eq</relation><value>1</value></qualifier>
      <qualifier><variableref>Volume</variableref><relation>exists</relation><value>1</value></qualifier>
      <decVal max="15"/>
    </variable>
    <variable item="Big" CV="0.255.261:4" readOnly="yes"><splitVal/></variable>
    <variable item="Addr2" CV="5" mask="XXVVVVVV"><splitVal highCV="6" upperMask="XXXXXVVV" offset="1"/></variable>
    <variable item="Table" CV="67"><speedTableVal entries="4"/></variable>
    <variable item="Third output" CV="40" minOut="3"><decVal/></variable>
    <variable item="FL(f) controls output 1" CV="33" mask="XXXXXXXV"><xi:include href="http://jmri.org/xml/decoders/parts/enum-NoYes.xml"/></variable>
    <variable item="F1 controls output 2" CV="35" mask="XXXXXXVX"><xi:include href="http://jmri.org/xml/decoders/parts/enum-NoYes.xml"/></variable>
    <variable item="Mixed" CV="60" mask="3"><enumVal><enumChoice choice="a"/><enumChoice choice="b"/><enumChoice choice="c"/></enumVal></variable>
    <variable item="Wide" CV="70"><decVal max="1000"/></variable>
    <variable item="Shared" CV="51" mask="VVVVXXXX" infoOnly="yes"><decVal max="15"/></variable>
    <variables><variable item="Nested" CV="61"><decVal/></variable></variables>
    <variable item="Later" CV="62"><decVal/></variable>
  </variables>
  <resets><factReset label="Reset all CVs" CV="8" default="8"/><factReset label="Broken" CV="905,1021=0" default="1"/></resets>
 </decoder>
 <pane><name>Lights</name><column><display item="Delay" format="hslider" label=""/></column></pane>
 <pane><name>Extra</name>
   <grid gridx="0" gridy="NEXT">
     <griditem><display item="Mode"/></griditem>
     <group include="A2"><griditem><display item="Volume"/></griditem></group>
     <griditem gridx="NEXT" gridy="CURRENT"><label><text>note</text></label></griditem>
   </grid>
 </pane>
 <pane><name>Notes</name><column><label><text>&lt;html&gt;Read &lt;b&gt;me&lt;/b&gt;&lt;/html&gt;</text></label></column></pane>
 <pane exclude="A1"><name>Hidden</name><column><display item="Vstart"/></column></pane>
</decoder-config>`

func testDefinitionFS() fstest.MapFS {
	return fstest.MapFS{
		"decoderIndex.xml":                {Data: []byte(`<decoderIndex-config><decoderIndex version="1"><mfgList><manufacturer mfg="Acme Co" mfgID="13"/></mfgList><familyList><family name="Acme" mfg="Acme Co" lowVersionID="1" highVersionID="9" file="Acme.xml"><model model="A1"/><model model="A2"/></family></familyList></decoderIndex></decoderIndex-config>`)},
		"programmers/Comprehensive.xml":   {Data: []byte(testProgrammer)},
		"decoders/Acme.xml":               {Data: []byte(testDecoder)},
		"decoders/parts/enum-NoYes.xml":   {Data: []byte(`<enumVal><enumChoice choice="No"/><enumChoice choice="Yes"/></enumVal>`)},
		"decoders/nmra/address.xml": {Data: []byte(`<variables>
  <variable CV="1" item="Short Address" default="03"><shortAddressVal><shortAddressChanges cv="19"/></shortAddressVal><label>Primary Address</label></variable>
  <variable CV="29" mask="XXVXXXXX" item="Address Format"><enumVal><enumChoice choice="Short"/><enumChoice choice="Long"/></enumVal></variable>
  <variable CV="17" item="Long Address"><longAddressVal/></variable>
</variables>`)},
	}
}

func loadTest(t *testing.T, model string) *Definition {
	t.Helper()
	d, err := loadDefinition(&treeLoader{fsys: testDefinitionFS()}, "Acme.xml", model, DefaultProgrammer)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func variable(t *testing.T, d *Definition, item string) Variable {
	t.Helper()
	for _, v := range d.Variables {
		if v.Item == item {
			return v
		}
	}
	t.Fatalf("no variable %q", item)
	return Variable{}
}

func TestDefinitionVariables(t *testing.T) {
	d := loadTest(t, "A1")
	var items []string
	for _, v := range d.Variables {
		items = append(items, v.Item)
	}
	// Variables first, then constants, then nested blocks (an included
	// file is one); A2-only and three-output settings are left out of A1.
	want := "Vstart|Mode|Delay|Big|Addr2|Table|FL(f) controls output 1|F1 controls output 2|Mixed|Wide|Shared|Later|Fixed|Short Address|Address Format|Long Address|Nested"
	if got := strings.Join(items, "|"); got != want {
		t.Errorf("variables:\n got %s\nwant %s", got, want)
	}
	if v := variable(t, d, "Vstart"); v.Label != "Start voltage" || v.Default != "3" || v.Type != TypeDecimal || *v.Max != 255 || v.Parts[0] != (Part{CV: "2", Mask: 0xFF, Width: 8}) {
		t.Errorf("Vstart = %+v", v)
	}
	if v := variable(t, d, "Short Address"); v.Type != TypeShortAddress || *v.Max != 127 || v.Label != "Primary Address" || strings.Join(v.AddressChanges, ",") != "19" {
		t.Errorf("Short Address = %+v", v)
	}
	if v := variable(t, d, "Long Address"); v.Type != TypeLongAddress || len(v.Parts) != 2 || v.Parts[1].CV != "18" {
		t.Errorf("Long Address = %+v", v)
	}
	mode := variable(t, d, "Mode")
	var choices []string
	for _, c := range mode.Choices {
		choices = append(choices, c.Label+"="+itoa(c.Value)+"@"+c.Group)
	}
	if got := strings.Join(choices, " "); got != "Off=0@ Blink=1@More Fade=7@More After=8@" {
		t.Errorf("Mode choices = %s", got)
	}
	if mode.Parts[0] != (Part{CV: "50", Mask: 0x03, Width: 2}) || mode.Modulus != 5 {
		t.Errorf("Mode part = %+v, modulus %d", mode.Parts[0], mode.Modulus)
	}
	delay := variable(t, d, "Delay")
	if len(delay.Qualifiers) != 2 || delay.Qualifiers[0] != (Qualifier{Item: "Mode", Relation: RelNe}) || delay.Qualifiers[1].Relation != RelNever {
		t.Errorf("Delay qualifiers = %+v", delay.Qualifiers)
	}
	big := variable(t, d, "Big")
	if len(big.Parts) != 4 || big.Parts[0].CV != "0.255.261" || big.Parts[3].CV != "0.255.264" || !big.ReadOnly || big.Max != nil {
		t.Errorf("Big = %+v", big)
	}
	addr2 := variable(t, d, "Addr2")
	if addr2.Parts[0] != (Part{CV: "5", Mask: 0x3F, Width: 6}) || addr2.Parts[1] != (Part{CV: "6", Mask: 0x07, Width: 3}) || addr2.Offset != 1 {
		t.Errorf("Addr2 = %+v", addr2)
	}
	if v := variable(t, d, "Table"); v.SpeedTable.Entries != 4 || v.Parts[3].CV != "70" {
		t.Errorf("Table = %+v", v)
	}
	if v := variable(t, d, "Mixed"); v.Parts[0].Radix != 3 || v.Modulus != 3 {
		t.Errorf("Mixed = %+v", v)
	}
	if v := variable(t, d, "Wide"); v.Parts[0].Mask != 0x3FF {
		t.Errorf("Wide mask = %#x, want ten bits", v.Parts[0].Mask)
	}
	if v := variable(t, d, "Fixed"); v.Type != TypeConstant || v.Default != "1" {
		t.Errorf("Fixed = %+v", v)
	}

	var cvs []string
	flags := map[string]CV{}
	for _, c := range d.CVs {
		cvs = append(cvs, c.Name)
		flags[c.Name] = c
	}
	wantCVs := "2 50 51 0.255.261 0.255.262 0.255.263 0.255.264 5 6 67 68 69 70 33 35 60 62 1 19 29 17 18 61"
	if got := strings.Join(cvs, " "); got != wantCVs {
		t.Errorf("CVs:\n got %s\nwant %s", got, wantCVs)
	}
	if !flags["0.255.262"].ReadOnly || !flags["51"].InfoOnly || flags["2"].ReadOnly {
		t.Errorf("CV flags = %+v %+v %+v", flags["0.255.262"], flags["51"], flags["2"])
	}
	if p := d.Programming; p.Paged || p.Index == nil || p.Index.PI != 31 || p.Index.SI != 32 || p.Index.CVFirst || !p.Index.Cache {
		t.Errorf("programming = %+v %+v", p, p.Index)
	}
	if len(d.Resets) != 1 || d.Resets[0] != (Reset{Label: "Reset all CVs", CV: "8", Value: 8}) {
		t.Errorf("resets = %+v", d.Resets)
	}
	if len(d.Problems) != 1 || !strings.Contains(d.Problems[0], "Broken") {
		t.Errorf("problems = %q", d.Problems)
	}
	if len(d.FunctionLabels) != 2 || d.FunctionLabels[1] != (FunctionText{Num: 2, Label: "Horn"}) {
		t.Errorf("function labels = %+v", d.FunctionLabels)
	}

	a2 := loadTest(t, "A2")
	var choices2 []string
	for _, c := range variable(t, a2, "Mode").Choices {
		choices2 = append(choices2, c.Label+"="+itoa(c.Value))
	}
	if got := strings.Join(choices2, " "); got != "Off=0 A2 only=1 Blink=2 Fade=7 After=8" {
		t.Errorf("A2 Mode choices = %s", got)
	}
	variable(t, a2, "Volume")
	variable(t, a2, "Third output")
	if q := variable(t, a2, "Delay").Qualifiers; len(q) != 1 {
		t.Errorf("A2 Delay qualifiers = %+v", q)
	}
}

func TestDefinitionPanes(t *testing.T) {
	d := loadTest(t, "A1")
	var names []string
	panes := map[string]Pane{}
	for _, p := range d.Panes {
		names = append(names, p.Name)
		panes[p.Name] = p
	}
	if got := strings.Join(names, ", "); got != "Basic, Lights, Function Map, CVs, Extra, Notes" {
		t.Fatalf("panes = %s", got)
	}
	basic := panes["Basic"]
	if len(basic.Nodes) != 1 || strings.Join(basic.Items, "|") != "Short Address|Address Format|Long Address|Vstart" {
		t.Errorf("Basic = %s", dump(basic))
	}
	if lights := panes["Lights"]; lights.Items[0] != "Delay" || lights.Nodes[0].Children[0].Format != "hslider" || *lights.Nodes[0].Children[0].Label != "" {
		t.Errorf("Lights = %s", dump(lights))
	}
	fm := panes["Function Map"].Nodes[0].Children[0].FnMap
	if fm == nil || len(fm.Columns) != 2 || fm.Columns[1] != (FnColumn{Name: "Red", Label: "Pin 2"}) || len(fm.Rows) != 2 ||
		fm.Rows[0].Label != "Headlight F0 (forward)" || fm.Rows[1].Cells[1][0] != "F1 controls output 2" || len(fm.Rows[1].Cells[0]) != 0 {
		t.Errorf("function map = %s", dump(fm))
	}
	if !panes["CVs"].AllCVs {
		t.Error("CVs pane does not read every CV")
	}
	grid := panes["Extra"].Nodes[0]
	if len(grid.Children) != 2 || *grid.Children[0].Grid != (GridPos{0, 0, 1, 1}) || *grid.Children[1].Grid != (GridPos{1, 1, 1, 1}) {
		t.Errorf("Extra grid = %s", dump(grid))
	}
	if notes := panes["Notes"]; notes.Nodes[0].Children[0].Text != "Read me" || len(notes.Items) != 0 {
		t.Errorf("Notes = %s", dump(notes))
	}

	a2 := loadTest(t, "A2")
	names = nil
	for _, p := range a2.Panes {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, ", "); got != "Basic, Lights, Function Map, Sound, CVs, Extra, Notes, Hidden" {
		t.Errorf("A2 panes = %s", got)
	}
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
