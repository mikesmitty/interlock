package transport

import (
	"runtime"
	"sort"
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
// device (cu.* is the one to open). The result is sorted.
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
	sort.Strings(out)
	return out
}
