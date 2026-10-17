package decoder

import (
	"os"
	"path"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTreeIncludes(t *testing.T) {
	fsys := fstest.MapFS{
		"decoders/Acme.xml": {Data: []byte(`<?xml version="1.0"?>
<decoder-config xmlns:xi="http://www.w3.org/2001/XInclude">
  <copyright xmlns="http://docbook.org/ns/docbook"><year>2020</year></copyright>
  <decoder>
    <variables>
      <xi:include href="http://jmri.org/xml/decoders/nmra/address.xml"/>
      <xi:include href="acme/extra.xml"/>
      <xi:include href="http://jmri.org/xml/decoders/nmra/gone.xml"/>
      <variable CV="2" item="Vstart"><label>Start voltage</label><label xml:lang="de">Anfahrspannung</label></variable>
    </variables>
  </decoder>
  <xi:include href="http://jmri.org/xml/programmers/parts/BasicPane.xml"/>
</decoder-config>`)},
		"decoders/nmra/address.xml":       {Data: []byte(`<variables><variable CV="1" item="Short Address"/></variables>`)},
		"decoders/acme/extra.xml":         {Data: []byte(`<variables><xi:include xmlns:xi="http://www.w3.org/2001/XInclude" href="../nmra/address.xml"/></variables>`)},
		"programmers/parts/BasicPane.xml": {Data: []byte(`<pane><name>Basic</name></pane>`)},
		"decoders/loop.xml":               {Data: []byte(`<variables xmlns:xi="http://www.w3.org/2001/XInclude"><xi:include href="loop.xml"/></variables>`)},
	}
	ld := &treeLoader{fsys: fsys}
	root, err := ld.load("decoders/Acme.xml")
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "decoder-config" || root.child("copyright") != nil {
		t.Fatalf("root = %s with children %v", root.Name, names(root.Children))
	}
	vars := root.child("decoder").child("variables")
	if got := strings.Join(names(vars.Children), " "); got != "variables variables variable" {
		t.Fatalf("variables children = %q", got)
	}
	if got := vars.Children[1].Children[0].Children[0].attr("item"); got != "Short Address" {
		t.Errorf("relative include gave %q", got)
	}
	labels := vars.Children[2].all("label")
	if len(labels) != 2 || labels[0].Text != "Start voltage" || labels[1].attr("xml:lang") != "de" {
		t.Errorf("labels = %+v", labels)
	}
	if pane := root.child("pane"); pane == nil || pane.child("name").Text != "Basic" {
		t.Errorf("pane include missing: %v", names(root.Children))
	}
	if got := strings.Join(ld.Missing, " "); got != "decoders/nmra/gone.xml" {
		t.Errorf("Missing = %q", got)
	}
	if _, err := ld.load("decoders/loop.xml"); err == nil || !strings.Contains(err.Error(), "nest") {
		t.Errorf("a file including itself = %v", err)
	}
}

func TestTreeLatin1(t *testing.T) {
	fsys := fstest.MapFS{"decoders/a.xml": {Data: []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><variables><variable item=\"V\xf6lume\"/></variables>")}}
	root, err := (&treeLoader{fsys: fsys}).load("decoders/a.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Children[0].attr("item"); got != "Völ"+"ume" {
		t.Errorf("item = %q", got)
	}
}

func TestResolveHref(t *testing.T) {
	tests := []struct {
		from, href, want string
		ok               bool
	}{
		{"decoders/ESU_LokSound5.xml", "http://jmri.org/xml/decoders/esu/v5standardCVs.xml", "decoders/esu/v5standardCVs.xml", true},
		{"decoders/a.xml", "https://www.jmri.org/xml/programmers/parts/BasicPane.xml", "programmers/parts/BasicPane.xml", true},
		{"decoders/a.xml", "/xml/decoders/nmra/x.xml", "decoders/nmra/x.xml", true},
		{"programmers/Advanced.xml", "file:xml/programmers/parts/BasicPane.xml", "programmers/parts/BasicPane.xml", true},
		{"decoders/esu/a.xml", "b.xml", "decoders/esu/b.xml", true},
		{"decoders/esu/a.xml", "../nmra/b.xml", "decoders/nmra/b.xml", true},
		{"decoders/a.xml", "../../../etc/passwd", "", false},
		{"decoders/a.xml", "http://jmri.org/xml/../../etc/passwd", "", false},
		{"decoders/a.xml", "https://example.com/x.xml", "", false},
		{"decoders/a.xml", "", "", false},
	}
	for _, tt := range tests {
		got, ok := resolveHref(tt.from, tt.href)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("resolveHref(%q, %q) = %q, %v; want %q, %v", tt.from, tt.href, got, ok, tt.want, tt.ok)
		}
	}
}

// TestRealDefinitions reads every decoder file in a JMRI xml folder named
// by INTERLOCK_JMRI_XML, to check the reader against the whole set.
func TestRealDefinitions(t *testing.T) {
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
	missing := map[string]bool{}
	for _, f := range idx.Families {
		ld := &treeLoader{fsys: lib.fsys}
		if _, err := ld.load(path.Join("decoders", f.File)); err != nil {
			t.Errorf("%s: %v", f.File, err)
		}
		for _, m := range ld.Missing {
			missing[m] = true
		}
	}
	t.Logf("%d families read; includes naming missing files: %v", len(idx.Families), missing)
}

func names(els []*element) []string {
	out := make([]string, len(els))
	for i, e := range els {
		out[i] = e.Name
	}
	return out
}

// TestRealModels loads every model's definition from the folder named by
// INTERLOCK_JMRI_XML and reports what could not be used.
func TestRealModels(t *testing.T) {
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
	models, problems, panes, vars := 0, map[string]int{}, 0, 0
	for _, f := range idx.Families {
		if !f.Programmable() {
			continue
		}
		ld := &treeLoader{fsys: lib.fsys}
		for _, m := range f.Models {
			d, err := loadDefinition(ld, f.File, m.Name, DefaultProgrammer)
			if err != nil {
				t.Errorf("%s %s: %v", f.File, m.Name, err)
				continue
			}
			models++
			panes += len(d.Panes)
			vars += len(d.Variables)
			for _, p := range d.Problems {
				key := p
				if i := strings.Index(p, ": "); i >= 0 {
					key = p[i+2:]
				}
				problems[key]++
			}
		}
	}
	t.Logf("%d models, %d panes, %d variables", models, panes, vars)
	for p, n := range problems {
		t.Logf("%5d  %s", n, p)
	}
}
