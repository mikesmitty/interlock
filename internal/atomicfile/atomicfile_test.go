package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

func TestWrite(t *testing.T) {
	tests := []struct {
		name     string
		existing string
	}{
		{name: "new file in a new directory"},
		{name: "replaces an existing file", existing: "old contents\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sub", "doc.yaml")
			if tt.existing != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := atomicfile.Write(path, []byte("new contents\n"), 0o644); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "new contents\n" {
				t.Fatalf("contents = %q, %v", got, err)
			}
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Errorf("temporary file left behind: %v", err)
			}
		})
	}
}

func TestRenameMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := atomicfile.Rename(filepath.Join(dir, "missing"), filepath.Join(dir, "to"))
	if !os.IsNotExist(err) {
		t.Fatalf("Rename of a missing file = %v, want not exist", err)
	}
}
