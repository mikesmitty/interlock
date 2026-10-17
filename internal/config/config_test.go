package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadCreatesDefaultFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
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
	if !reflect.DeepEqual(again, Default()) {
		t.Fatalf("default file parsed to %+v, want %+v", again, Default())
	}
	if err := again.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	text := `listen: "127.0.0.1:9000"
node_id: 05.01.01.01.22.00
links:
  - type: serial
    device: /dev/cu.usbmodem1
  - name: jmri
    type: tcp
    address: 10.0.0.2:12021
discovery:
  interval: 1m
`
	if err := os.WriteFile(Path(dir), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Default()
	want.Listen = "127.0.0.1:9000"
	want.NodeID = "05.01.01.01.22.00"
	want.Links = []LinkConfig{
		{Name: "/dev/cu.usbmodem1", Type: "serial", Device: "/dev/cu.usbmodem1"},
		{Name: "jmri", Type: "tcp", Address: "10.0.0.2:12021"},
	}
	want.Discovery.Interval = time.Minute
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("Load = %+v, want %+v", cfg, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
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
	serial := LinkConfig{Name: "a", Type: "serial", Device: "/dev/x"}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "defaults", mutate: func(*Config) {}},
		{name: "node id", mutate: func(c *Config) { c.NodeID = "05.01.01.01.22.00" }},
		{name: "links", mutate: func(c *Config) {
			c.Links = []LinkConfig{serial, {Name: "b", Type: "tcp", Address: "h:1"}}
		}},
		{name: "hub off", mutate: func(c *Config) { c.Hub.Listen = "" }},
		{name: "discovery off", mutate: func(c *Config) { c.Discovery.Interval = 0 }},
		{name: "empty listen", mutate: func(c *Config) { c.Listen = "" }, wantErr: "listen"},
		{name: "bad node id", mutate: func(c *Config) { c.NodeID = "nope" }, wantErr: "node_id"},
		{name: "zero node id", mutate: func(c *Config) { c.NodeID = "00.00.00.00.00.00" }, wantErr: "node_id"},
		{name: "link without name", mutate: func(c *Config) { c.Links = []LinkConfig{{Type: "serial", Device: "/dev/x"}} }, wantErr: "name"},
		{name: "olcbtcp without address", mutate: func(c *Config) { c.Links = []LinkConfig{{Name: "h", Type: "olcbtcp"}} }, wantErr: "address"},
		{name: "socketcan without interface", mutate: func(c *Config) { c.Links = []LinkConfig{{Name: "c", Type: "socketcan"}} }, wantErr: "interface"},
		{name: "new link types", mutate: func(c *Config) {
			c.Links = []LinkConfig{{Name: "h", Type: "olcbtcp", Address: "h:1"}, {Name: "c", Type: "socketcan", Interface: "can0"}}
		}},
		{name: "link bad type", mutate: func(c *Config) { c.Links = []LinkConfig{{Name: "a", Type: "usb"}} }, wantErr: "type"},
		{name: "serial without device", mutate: func(c *Config) { c.Links = []LinkConfig{{Name: "a", Type: "serial"}} }, wantErr: "device"},
		{name: "tcp without address", mutate: func(c *Config) { c.Links = []LinkConfig{{Name: "a", Type: "tcp"}} }, wantErr: "address"},
		{name: "duplicate link names", mutate: func(c *Config) { c.Links = []LinkConfig{serial, serial} }, wantErr: "twice"},
		{name: "negative baud", mutate: func(c *Config) { c.Links = []LinkConfig{{Name: "a", Type: "serial", Device: "/dev/x", Baud: -1}} }, wantErr: "baud"},
		{name: "discovery too fast", mutate: func(c *Config) { c.Discovery.Interval = time.Second }, wantErr: "discovery"},
		{name: "discovery negative", mutate: func(c *Config) { c.Discovery.Interval = -time.Second }, wantErr: "discovery"},
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

func TestLinkTarget(t *testing.T) {
	if got := (LinkConfig{Type: "serial", Device: "/dev/x", Address: "h:1"}).Target(); got != "/dev/x" {
		t.Errorf("serial Target = %q", got)
	}
	if got := (LinkConfig{Type: "tcp", Device: "/dev/x", Address: "h:1"}).Target(); got != "h:1" {
		t.Errorf("tcp Target = %q", got)
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
