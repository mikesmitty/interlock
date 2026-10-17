package automation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that reads and writes as text such as "3s",
// "1m30s" or "250ms". A bare number is taken as seconds.
type Duration time.Duration

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String formats the duration the way it is written in a document:
// "3s", "1m30s", "2h", "250ms".
func (d Duration) String() string {
	v := time.Duration(d)
	switch {
	case v == 0:
		return "0s"
	case v < 0:
		return "-" + Duration(-v).String()
	case v < time.Second:
		if v%time.Millisecond == 0 {
			return strconv.FormatInt(int64(v/time.Millisecond), 10) + "ms"
		}
		return v.String()
	}
	hours := v / time.Hour
	v -= hours * time.Hour
	minutes := v / time.Minute
	v -= minutes * time.Minute
	var b strings.Builder
	if hours > 0 {
		b.WriteString(strconv.FormatInt(int64(hours), 10))
		b.WriteByte('h')
	}
	if minutes > 0 {
		b.WriteString(strconv.FormatInt(int64(minutes), 10))
		b.WriteByte('m')
	}
	if v > 0 {
		b.WriteString(strconv.FormatFloat(v.Seconds(), 'f', -1, 64))
		b.WriteByte('s')
	}
	return b.String()
}

// ParseDuration reads "3s", "1m30s", "0.5s" or a bare number of seconds.
func ParseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return Duration(n * float64(time.Second)), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 3s or 1m30s", s)
	}
	return Duration(v), nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON accepts a string or a number of seconds.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		return d.UnmarshalText([]byte(s))
	}
	var n float64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("duration must be a string such as \"3s\" or a number of seconds")
	}
	*d = Duration(n * float64(time.Second))
	return nil
}

// IsZero lets omitzero and omitempty leave unset durations out.
func (d Duration) IsZero() bool { return d == 0 }

// MarshalYAML writes the duration as a string.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalYAML accepts a string or a number of seconds.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a string such as 3s", node.Line)
	}
	if err := d.UnmarshalText([]byte(node.Value)); err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	return nil
}
