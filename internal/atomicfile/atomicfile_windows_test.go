package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

// Another program holding the file open, as an editor or a virus scanner
// might, delays the write instead of failing it.
func TestWriteWhileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = held.Close()
		close(released)
	}()
	if err := atomicfile.Write(path, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("Write while open: %v", err)
	}
	<-released
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new\n" {
		t.Fatalf("contents = %q, %v", got, err)
	}
}
