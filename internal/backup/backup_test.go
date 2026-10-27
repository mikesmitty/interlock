package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/backup"
	"github.com/mikesmitty/interlock/internal/store"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("interlock.yaml", "listen: \":8080\"\n")
	write("trains.yaml", "trains: []\n")
	write("profiles/mine.yaml", "name: mine\n")
	write("profiles/notes.txt", "not a profile\n")
	write("cvs/loksound.yaml", "cvs: {}\n")
	write("trackplan.yaml", "tiles: []\n")
	write("backgrounds/background-0123456789abcdef.png", "\x89PNG\r\n\x1a\n")
	write("backgrounds/notes.txt", "not an image\n")
	write("interlock.log", "not kept\n")
	st, err := store.Open(ctx, filepath.Join(dir, "interlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "clock.rate", "6"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := backup.Write(ctx, &buf, dir, st, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	manifest, err := backup.Inspect(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.2.3" || strings.Join(manifest.Files, ",") != "interlock.yaml,trains.yaml,trackplan.yaml,profiles/mine.yaml,cvs/loksound.yaml,backgrounds/background-0123456789abcdef.png,interlock.db" {
		t.Errorf("manifest = %+v", manifest)
	}

	// Change things, then stage the backup and apply it as a start would.
	write("interlock.yaml", "listen: \":9090\"\n")
	write("entities.yaml", "entities: []\n")
	st, err = store.Open(ctx, filepath.Join(dir, "interlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "clock.rate", "99"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Stage(bytes.NewReader(buf.Bytes()), int64(buf.Len()), dir); err != nil {
		t.Fatal(err)
	}
	if !backup.Pending(dir) {
		t.Fatal("nothing pending after Stage")
	}
	kept, err := backup.Apply(dir)
	if err != nil {
		t.Fatal(err)
	}
	if kept == "" || backup.Pending(dir) {
		t.Fatalf("Apply kept %q, pending %v", kept, backup.Pending(dir))
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "interlock.yaml")); string(got) != "listen: \":8080\"\n" {
		t.Errorf("interlock.yaml after restore = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, kept, "interlock.yaml")); string(got) != "listen: \":9090\"\n" {
		t.Errorf("kept interlock.yaml = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "entities.yaml")); err != nil {
		t.Error("a file the backup did not hold should be left alone")
	}
	if _, err := os.Stat(filepath.Join(dir, "interlock.db-wal")); err == nil {
		t.Error("the old write-ahead log should have been set aside")
	}
	st, err = store.Open(ctx, filepath.Join(dir, "interlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _, _ := st.Setting(ctx, "clock.rate"); v != "6" {
		t.Errorf("restored database has clock.rate %q, want 6", v)
	}
	if kept2, err := backup.Apply(dir); err != nil || kept2 != "" {
		t.Errorf("second Apply = %q, %v", kept2, err)
	}
}

func TestInspectRejects(t *testing.T) {
	if _, err := backup.Inspect(bytes.NewReader([]byte("hello")), 5); err == nil {
		t.Error("a text file is not a backup")
	}
	var buf bytes.Buffer
	newZip(&buf, map[string]string{"interlock.yaml": "x"})
	if _, err := backup.Inspect(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil || !strings.Contains(err.Error(), backup.ManifestName) {
		t.Errorf("no manifest: %v", err)
	}
	buf.Reset()
	newZip(&buf, map[string]string{backup.ManifestName: "{}", "../etc/passwd": "x"})
	if _, err := backup.Inspect(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil || !strings.Contains(err.Error(), "not a file Interlock keeps") {
		t.Errorf("traversal: %v", err)
	}
	buf.Reset()
	newZip(&buf, map[string]string{backup.ManifestName: "{}", "profiles/evil.sh": "x"})
	if _, err := backup.Inspect(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil {
		t.Error("a script under profiles should be refused")
	}
}
