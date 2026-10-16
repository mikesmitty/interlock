package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, path
}

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	s, path := openTestStore(t)

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	want := migrations[len(migrations)-1].version
	got, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if got != want {
		t.Fatalf("SchemaVersion = %d, want %d", got, want)
	}

	// Reopening an up to date database must not reapply anything.
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() {
		if err := again.Close(); err != nil {
			t.Errorf("Close after reopen: %v", err)
		}
	}()
	got, err = again.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion after reopen: %v", err)
	}
	if got != want {
		t.Fatalf("SchemaVersion after reopen = %d, want %d", got, want)
	}
}

func TestMigrationsAreWellFormed(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i, m := range migrations {
		if m.version != i+1 {
			t.Errorf("migration %q has version %d, want %d (versions must be consecutive)", m.name, m.version, i+1)
		}
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)

	value, ok, err := s.Setting(ctx, "node_id")
	if err != nil {
		t.Fatalf("Setting: %v", err)
	}
	if ok || value != "" {
		t.Fatalf("Setting on empty store = %q, %v; want \"\", false", value, ok)
	}

	steps := []struct {
		key, value string
	}{
		{"node_id", "0A.00.00.00.00.01"},
		{"node_id", "0A.00.00.00.00.02"},
		{"other", "x"},
	}
	for _, step := range steps {
		if err := s.SetSetting(ctx, step.key, step.value); err != nil {
			t.Fatalf("SetSetting(%q): %v", step.key, err)
		}
		got, ok, err := s.Setting(ctx, step.key)
		if err != nil {
			t.Fatalf("Setting(%q): %v", step.key, err)
		}
		if !ok || got != step.value {
			t.Fatalf("Setting(%q) = %q, %v; want %q, true", step.key, got, ok, step.value)
		}
	}
}

func TestOpenBadPath(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "missing", "dir", "test.db"))
	if err == nil {
		t.Fatal("Open succeeded on a path whose directory does not exist")
	}
}
