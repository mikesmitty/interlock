// Package config loads Interlock's declarative configuration from its data
// directory. interlock.yaml holds the server settings; the same directory
// holds the database and, later, automations and profiles.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	// Log controls the server log.
	Log LogConfig `yaml:"log"`
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
		Listen: ":8080",
		Log:    LogConfig{Level: "info", Format: "text"},
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
