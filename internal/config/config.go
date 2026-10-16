// Package config loads Interlock's declarative configuration from its data
// directory. interlock.yaml holds the server settings; the same directory
// holds the database and, later, automations and profiles.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mikesmitty/interlock/openlcb"
)

const (
	// FileName is the configuration file inside the data directory.
	FileName = "interlock.yaml"
	// DBFileName is the sqlite database inside the data directory.
	DBFileName = "interlock.db"
)

// Config is the contents of interlock.yaml.
type Config struct {
	// Listen is the address the web interface and API bind to.
	Listen string `yaml:"listen"`
	// NodeID is Interlock's own LCC node ID as dotted hex. Empty means use
	// the locally assigned ID generated on first start.
	NodeID string `yaml:"node_id,omitempty"`
	// Node is how Interlock presents itself to other nodes.
	Node NodeConfig `yaml:"node"`
	// Links are the connections to the layout.
	Links []LinkConfig `yaml:"links"`
	// Hub configures the GridConnect TCP server other tools can join.
	Hub HubConfig `yaml:"hub"`
	// Discovery controls how nodes on the bus are found and watched.
	Discovery DiscoveryConfig `yaml:"discovery"`
	// Log controls the server log.
	Log LogConfig `yaml:"log"`
}

// NodeConfig is the user half of Interlock's SNIP.
type NodeConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// LinkConfig is one connection to a CAN segment.
type LinkConfig struct {
	// Name identifies the link in the UI and logs. Defaults to the device
	// or address.
	Name string `yaml:"name"`
	// Type is "serial" or "tcp".
	Type string `yaml:"type"`
	// Device is the serial port for type serial.
	Device string `yaml:"device,omitempty"`
	// Baud is the serial bit rate; USB CDC devices ignore it.
	Baud int `yaml:"baud,omitempty"`
	// Address is host:port for type tcp.
	Address string `yaml:"address,omitempty"`
}

// Target returns the device or address a link connects to.
func (l LinkConfig) Target() string {
	if l.Type == "serial" {
		return l.Device
	}
	return l.Address
}

// HubConfig configures the GridConnect TCP server.
type HubConfig struct {
	// Listen is the address to serve on; empty turns the server off.
	Listen string `yaml:"listen"`
}

// DiscoveryConfig controls node discovery.
type DiscoveryConfig struct {
	// Interval is how often every node is asked to check in. Zero turns
	// the periodic check off; nodes are still found when they announce
	// themselves.
	Interval time.Duration `yaml:"interval"`
}

// LogConfig selects the log level and format.
type LogConfig struct {
	// Level is debug, info, warn or error.
	Level string `yaml:"level"`
	// Format is text or json.
	Format string `yaml:"format"`
}

// Default returns the configuration used when the file sets nothing.
func Default() Config {
	return Config{
		Listen:    ":8080",
		Node:      NodeConfig{Name: "Interlock"},
		Links:     []LinkConfig{},
		Hub:       HubConfig{Listen: ":12021"},
		Discovery: DiscoveryConfig{Interval: 30 * time.Second},
		Log:       LogConfig{Level: "info", Format: "text"},
	}
}

// defaultFile is written when no configuration file exists yet, so the user
// has a commented file to edit. Its values match Default.
const defaultFile = `# Interlock configuration. Settings left out take their defaults.

# Address the web interface and API listen on.
listen: ":8080"

# Interlock's own LCC node ID as dotted hex, for example 05.01.01.01.22.00.
# Leave it unset to use the locally assigned ID generated on first start,
# which is kept in the database.
# node_id: ""

# How Interlock introduces itself to other nodes and tools.
node:
  name: Interlock
  description: ""

# Connections to the layout. Each CAN segment should reach Interlock through
# exactly one link; the hub joins all links into one bus.
links: []
#  - name: outpost
#    type: serial
#    device: /dev/cu.usbmodem1234
#  - name: jmri
#    type: tcp
#    address: 192.168.1.10:12021

# GridConnect TCP server, so JMRI or another Interlock can join this hub.
# Set to "" to turn it off.
hub:
  listen: ":12021"

discovery:
  # How often every node is asked to check in. 0 turns the periodic check
  # off; nodes are still found when they announce themselves.
  interval: 30s

log:
  # debug, info, warn or error
  level: info
  # text or json
  format: text
`

// Path returns the configuration file path inside dir.
func Path(dir string) string {
	return filepath.Join(dir, FileName)
}

// DBPath returns the database path inside dir.
func DBPath(dir string) string {
	return filepath.Join(dir, DBFileName)
}

// Load reads dir/interlock.yaml over the defaults. When the file does not
// exist it is created with the defaults and explanatory comments.
func Load(dir string) (Config, error) {
	cfg := Default()
	path := Path(dir)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(defaultFile), 0o644); err != nil {
			return cfg, fmt.Errorf("write default config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Links == nil {
		cfg.Links = []LinkConfig{}
	}
	for i := range cfg.Links {
		if cfg.Links[i].Name == "" {
			cfg.Links[i].Name = cfg.Links[i].Target()
		}
	}
	return cfg, nil
}

// Validate checks the configuration for values that cannot work.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("config: listen must be set")
	}
	if c.NodeID != "" {
		if _, err := openlcb.ParseNodeID(c.NodeID); err != nil {
			return fmt.Errorf("config: node_id: %w", err)
		}
	}
	names := make(map[string]bool, len(c.Links))
	for i, link := range c.Links {
		if err := link.validate(); err != nil {
			return fmt.Errorf("config: links[%d]: %w", i, err)
		}
		if names[link.Name] {
			return fmt.Errorf("config: links[%d]: name %q is used twice", i, link.Name)
		}
		names[link.Name] = true
	}
	if c.Discovery.Interval < 0 {
		return errors.New("config: discovery interval must not be negative")
	}
	if c.Discovery.Interval > 0 && c.Discovery.Interval < 5*time.Second {
		return errors.New("config: discovery interval must be at least 5s")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log level %q must be debug, info, warn or error", c.Log.Level)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return fmt.Errorf("config: log format %q must be text or json", c.Log.Format)
	}
	return nil
}

func (l LinkConfig) validate() error {
	if l.Name == "" {
		return errors.New("name must be set")
	}
	switch l.Type {
	case "serial":
		if l.Device == "" {
			return errors.New("serial link needs a device")
		}
		if l.Baud < 0 {
			return errors.New("baud must not be negative")
		}
	case "tcp":
		if l.Address == "" {
			return errors.New("tcp link needs an address")
		}
	default:
		return fmt.Errorf("type %q must be serial or tcp", l.Type)
	}
	return nil
}
