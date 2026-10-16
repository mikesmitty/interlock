package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCreatesDefaultFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != Default() {
		t.Fatalf("Load = %+v, want defaults %+v", cfg, Default())
	}
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("default file was not written: %v", err)
	}
	if !strings.Contains(string(data), "listen:") {
		t.Fatalf("default file lacks a listen setting:\n%s", data)
	}

	// The written file must parse back to the same defaults.
	again, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	if again != Default() {
		t.Fatalf("default file parsed to %+v, want %+v", again, Default())
	}
	if err := again.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("listen: \"127.0.0.1:9000\"\nnode_id: 05.01.01.01.22.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Default()
	want.Listen = "127.0.0.1:9000"
	want.NodeID = "05.01.01.01.22.00"
	if cfg != want {
		t.Fatalf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadRejectsBadYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("listen: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted malformed YAML")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "defaults", mutate: func(*Config) {}},
		{name: "node id", mutate: func(c *Config) { c.NodeID = "05.01.01.01.22.00" }},
		{name: "empty listen", mutate: func(c *Config) { c.Listen = "" }, wantErr: "listen"},
		{name: "bad node id", mutate: func(c *Config) { c.NodeID = "nope" }, wantErr: "node_id"},
		{name: "zero node id", mutate: func(c *Config) { c.NodeID = "00.00.00.00.00.00" }, wantErr: "node_id"},
		{name: "bad level", mutate: func(c *Config) { c.Log.Level = "loud" }, wantErr: "log level"},
		{name: "bad format", mutate: func(c *Config) { c.Log.Format = "xml" }, wantErr: "log format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	if got, want := Path("data"), filepath.Join("data", "interlock.yaml"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got, want := DBPath("data"), filepath.Join("data", "interlock.db"); got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}
}
