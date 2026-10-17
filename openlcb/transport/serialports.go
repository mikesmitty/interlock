package transport

import (
	"cmp"
	"runtime"
	"slices"
	"strings"

	"go.bug.st/serial"
)

// SerialPorts lists the serial ports a user might plug a node into, with
// the operating system's own pseudo devices left out.
func SerialPorts() ([]string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	return FilterSerialPorts(ports, runtime.GOOS), nil
}

// FilterSerialPorts drops devices that are never a node: on macOS the
// Bluetooth and debug console ports, and the tty.* twin of every cu.*
// device (cu.* is the one to open). The result is sorted with numbered
// ports in numeric order, so COM9 comes before COM10.
func FilterSerialPorts(ports []string, goos string) []string {
	out := make([]string, 0, len(ports))
	if goos == "darwin" {
		cu := make(map[string]bool)
		for _, p := range ports {
			if rest, ok := strings.CutPrefix(p, "/dev/cu."); ok {
				cu[rest] = true
			}
		}
		for _, p := range ports {
			if strings.Contains(p, "Bluetooth-Incoming-Port") || strings.Contains(p, "debug-console") {
				continue
			}
			if rest, ok := strings.CutPrefix(p, "/dev/tty."); ok && cu[rest] {
				continue
			}
			out = append(out, p)
		}
	} else {
		out = append(out, ports...)
	}
	slices.SortFunc(out, comparePorts)
	return out
}

// comparePorts orders port names alphabetically, except that names that
// differ only in their trailing number are ordered by that number.
func comparePorts(a, b string) int {
	pa, na := splitNumber(a)
	pb, nb := splitNumber(b)
	if pa == pb && na != "" && nb != "" {
		na, nb = strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
		if c := cmp.Compare(len(na), len(nb)); c != 0 {
			return c
		}
		if c := strings.Compare(na, nb); c != 0 {
			return c
		}
	}
	return strings.Compare(a, b)
}

// splitNumber splits a name into its text and the digits that end it.
func splitNumber(s string) (text, digits string) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	return s[:i], s[i:]
}
