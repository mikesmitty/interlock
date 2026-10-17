package decoder

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testIndex = `<?xml version="1.0" encoding="UTF-8"?>
<decoderIndex-config>
  <decoderIndex version="717">
    <mfgList>
      <manufacturer mfg="NMRA" mfgID="999" />
      <manufacturer mfg="Electronic Solutions Ulm GmbH" mfgID="151" />
      <manufacturer mfg="Digitrax" mfgID="129" />
    </mfgList>
    <familyList>
      <family name="ESU LokSound 5" mfg="Electronic Solutions Ulm GmbH" lowVersionID="255" highVersionID="255" file="ESU_LokSound5.xml">
        <model model="LokSound 5" numOuts="20" productID="33554582,33554679" />
        <model model="LokSound 5 micro E24 DCC" show="no" replacementModel="LokSound 5 nano DCC" productID="16777404" />
      </family>
      <family name="Series 3" mfg="Digitrax" lowVersionID="49" highVersionID="51" file="Digitrax_01x3.xml">
        <model model="DH163" numOuts="6" numFns="10">
          <output name="4" label="Violet" />
          <versionCV lowVersionID="49" highVersionID="50" />
        </model>
      </family>
      <family name="LNCV board" mfg="Digitrax" file="board.xml" modes="LOCONETLNCVMODE" type="stationary">
        <model model="Board" />
      </family>
    </familyList>
  </decoderIndex>
</decoderIndex-config>
`

func TestFolderSource(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"xml folder", map[string]string{"xml/decoderIndex.xml": testIndex}},
		{"the xml folder itself", map[string]string{"decoderIndex.xml": testIndex}},
		{"unpacked release", map[string]string{"JMRI/xml/decoderIndex.xml": testIndex}},
		{"repository checkout", map[string]string{"JMRI-5.16/xml/decoderIndex.xml": testIndex, "JMRI-5.16/README.md": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" on disk", func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), body)
			}
			checkIndex(t, Options{Source: SourceFolder, Path: dir})
		})
		t.Run(tt.name+" in a zip", func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "jmri.zip")
			f, err := os.Create(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(f)
			for name, body := range tt.files {
				w, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(w, body)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			checkIndex(t, Options{Source: SourceFolder, Path: zipPath})
		})
	}
}

func TestFolderSourceErrors(t *testing.T) {
	empty := t.TempDir()
	notZip := filepath.Join(t.TempDir(), "notes.txt")
	writeFile(t, notZip, "hello")
	tests := []struct {
		name string
		path string
		want string
	}{
		{"no path", "", "no path"},
		{"missing", filepath.Join(empty, "nope"), "no such file"},
		{"no index", empty, "no decoderIndex.xml"},
		{"not a zip", notZip, "neither a folder nor a zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(Options{Source: SourceFolder, Path: tt.path})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Open = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestOpenRejects(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{"unknown source", Options{Source: "ftp", CacheDir: t.TempDir()}},
		{"bad ref", Options{Source: SourceGitHub, Ref: "../../etc", CacheDir: t.TempDir()}},
		{"no cache", Options{Source: SourceGitHub}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Open(tt.opts); err == nil {
				t.Fatal("Open succeeded")
			}
		})
	}
}

// fakeGitHub serves files under /<ref>/xml/ and counts requests.
type fakeGitHub struct {
	files    map[string]string
	requests atomic.Int64
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.requests.Add(1)
	name, ok := strings.CutPrefix(r.URL.Path, "/v5.16/xml/")
	body, found := g.files[name]
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, body)
}

func TestGitHubSource(t *testing.T) {
	gh := &fakeGitHub{files: map[string]string{indexFile: testIndex, "decoders/a.xml": "<decoder-config/>"}}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	cache := t.TempDir()
	opts := Options{Source: SourceGitHub, CacheDir: cache, RawURL: srv.URL}
	checkIndex(t, opts)
	if _, err := os.Stat(filepath.Join(cache, "v5.16", indexFile)); err != nil {
		t.Fatalf("index not cached: %v", err)
	}

	lib, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	before := gh.requests.Load()
	if _, err := lib.Index(); err != nil {
		t.Fatal(err)
	}
	if got := gh.requests.Load(); got != before {
		t.Errorf("a cached index was fetched again (%d requests)", got-before)
	}
	for range 2 {
		if _, err := lib.fsys.Open("decoders/missing.xml"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing file: %v, want not exist", err)
		}
	}
	if got := gh.requests.Load() - before; got != 1 {
		t.Errorf("a missing file was asked for %d times, want once", got)
	}
	if st := lib.Status(); st.Source != SourceGitHub || st.Ref != DefaultRef || st.Archive != nil {
		t.Errorf("Status = %+v", st)
	}
}

func TestGitHubSourceServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	lib, err := Open(Options{Source: SourceGitHub, CacheDir: t.TempDir(), RawURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Index(); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Index = %v, want the server's status", err)
	}
}

func TestArchiveSource(t *testing.T) {
	archive := tarball(t, map[string]string{
		"JMRI-5.16/xml/decoderIndex.xml":           testIndex,
		"JMRI-5.16/xml/decoders/ESU_LokSound5.xml": "<decoder-config/>",
		"JMRI-5.16/xml/decoders/nmra/cv29.xml":     "<variables/>",
		"JMRI-5.16/xml/programmers/Basic.xml":      "<programmer/>",
		"JMRI-5.16/xml/signals/big.xml":            "<signals/>",
		"JMRI-5.16/java/src/Main.java":             "class Main {}",
		"JMRI-5.16/xml/../../escape.xml":           "<x/>",
	})
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v5.16" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	cache := t.TempDir()
	opts := Options{Source: SourceArchive, CacheDir: cache, ArchiveURL: srv.URL, RawURL: srv.URL + "/raw"}
	lib, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := lib.Status(); st.Archive == nil || st.Archive.State != ArchiveWaiting {
		t.Fatalf("Status before = %+v", st)
	}
	if err := lib.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := lib.Status()
	if st.Archive.State != ArchiveDone || st.Archive.Files != 4 || st.Archive.Bytes != int64(len(archive)) {
		t.Errorf("Status after = %+v, want done with 4 files and %d bytes", *st.Archive, len(archive))
	}
	root := filepath.Join(cache, "v5.16")
	for _, name := range []string{indexFile, "decoders/ESU_LokSound5.xml", "decoders/nmra/cv29.xml", "programmers/Basic.xml", completeMarker} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s not kept: %v", name, err)
		}
	}
	for _, name := range []string{"signals/big.xml", "../escape.xml", "../../escape.xml"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s kept", name)
		}
	}
	checkIndex(t, opts)

	// A finished download is remembered across restarts.
	again, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("archive downloaded %d times, want once", hits.Load())
	}
}

func TestArchiveSourceFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball(t, map[string]string{"JMRI-5.16/README.md": "nothing here"}))
	}))
	defer srv.Close()
	lib, err := Open(Options{Source: SourceArchive, CacheDir: t.TempDir(), ArchiveURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Download(context.Background()); err == nil {
		t.Fatal("Download of an archive without definitions succeeded")
	}
	if st := lib.Status(); st.Archive.State != ArchiveFailed || st.Archive.Error == "" {
		t.Errorf("Status = %+v, want failed with a reason", *st.Archive)
	}
}

func TestIndex(t *testing.T) {
	idx, err := parseIndex(strings.NewReader(testIndex))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Version != "717" || len(idx.Manufacturers) != 3 || len(idx.Families) != 3 {
		t.Fatalf("index = version %s, %d manufacturers, %d families", idx.Version, len(idx.Manufacturers), len(idx.Families))
	}
	if m, ok := idx.Manufacturer(151); !ok || m.Name != "Electronic Solutions Ulm GmbH" {
		t.Errorf("Manufacturer(151) = %+v, %v", m, ok)
	}
	esu, ok := idx.Family("Electronic Solutions Ulm GmbH", "ESU LokSound 5")
	if !ok {
		t.Fatal("ESU LokSound 5 not found")
	}
	if esu.File != "ESU_LokSound5.xml" || esu.LowVersion != 255 || esu.HighVersion != 255 || !esu.Programmable() {
		t.Errorf("ESU family = %+v", esu)
	}
	if got := strings.Join(esu.Models[0].ProductIDs, " "); got != "33554582 33554679" {
		t.Errorf("product IDs = %q", got)
	}
	if retired := esu.Models[1]; retired.Show != "no" || retired.ReplacementModel != "LokSound 5 nano DCC" {
		t.Errorf("retired model = %+v", retired)
	}
	dh163 := idx.Families[1].Models[0]
	if dh163.NumOuts != 6 || dh163.NumFns != 10 || len(dh163.VersionCVs) != 1 || dh163.VersionCVs[0] != (VersionRange{49, 50}) {
		t.Errorf("DH163 = %+v", dh163)
	}
	board := idx.Families[2]
	if board.Programmable() || board.LowVersion != -1 || board.Type != "stationary" {
		t.Errorf("LNCV board = %+v", board)
	}
}

func checkIndex(t *testing.T, opts Options) {
	t.Helper()
	lib, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lib.Close() }()
	idx, err := lib.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Families) != 3 {
		t.Fatalf("index has %d families, want 3", len(idx.Families))
	}
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(tw, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
