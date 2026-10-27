package transport

import (
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"go.bug.st/serial"
)

// serialByID is where udev keeps a stable name for every USB serial
// device, made from its maker, product and serial number.
const serialByID = "/dev/serial/by-id"

// SerialPorts lists the serial ports a user might plug a node into, with
// the operating system's own pseudo devices left out. On Linux a port
// with a name under /dev/serial/by-id is listed by that name, since the
// ttyACM and ttyUSB numbers follow the order boards appear in and change
// when one is plugged in again.
func SerialPorts() ([]string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" {
		ports = preferByID(ports, serialByID)
	}
	return FilterSerialPorts(ports, runtime.GOOS), nil
}

// preferByID replaces each port that a link in dir points to with that
// link. Ports without one, and all of them when dir can't be read (no
// udev, or no USB serial devices), keep their names.
func preferByID(ports []string, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ports
	}
	byTarget := make(map[string]string)
	for _, e := range entries {
		link := filepath.Join(dir, e.Name())
		target, err := filepath.EvalSymlinks(link)
		if err != nil {
			continue
		}
		// ReadDir sorts by name, so the first link wins if there are two.
		if _, ok := byTarget[target]; !ok {
			byTarget[target] = link
		}
	}
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = p
		if link, ok := byTarget[p]; ok {
			out[i] = link
		}
	}
	return out
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
