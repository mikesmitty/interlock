package transport

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFilterSerialPorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []string
		goos  string
		want  []string
	}{
		{
			name:  "macos",
			ports: []string{"/dev/tty.usbmodem1", "/dev/cu.usbmodem1", "/dev/cu.Bluetooth-Incoming-Port", "/dev/tty.Bluetooth-Incoming-Port", "/dev/cu.debug-console", "/dev/tty.debug-console", "/dev/tty.only"},
			goos:  "darwin",
			want:  []string{"/dev/cu.usbmodem1", "/dev/tty.only"},
		},
		{
			name:  "linux untouched",
			ports: []string{"/dev/ttyUSB0", "/dev/ttyACM0"},
			goos:  "linux",
			want:  []string{"/dev/ttyACM0", "/dev/ttyUSB0"},
		},
		{
			name:  "linux numbers in order",
			ports: []string{"/dev/ttyACM10", "/dev/ttyACM2", "/dev/ttyUSB0", "/dev/ttyACM1"},
			goos:  "linux",
			want:  []string{"/dev/ttyACM1", "/dev/ttyACM2", "/dev/ttyACM10", "/dev/ttyUSB0"},
		},
		{
			name:  "windows numbers in order",
			ports: []string{"COM10", "COM3", "COM1", "COM9"},
			goos:  "windows",
			want:  []string{"COM1", "COM3", "COM9", "COM10"},
		},
		{name: "empty", ports: nil, goos: "darwin", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FilterSerialPorts(tt.ports, tt.goos); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FilterSerialPorts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreferByID(t *testing.T) {
	dev := t.TempDir()
	for _, name := range []string{"ttyACM0", "ttyACM1", "ttyS0"} {
		if err := os.WriteFile(filepath.Join(dev, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	byID := filepath.Join(dev, "by-id")
	if err := os.Mkdir(byID, 0o700); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"usb-simple-dcc_DCC_Command_Station_A171FAF5496D834F-if00": "../ttyACM1",
		"usb-Raspberry_Pi_Pico_0BBBB5FF9024A634-if00":              "../ttyACM0",
		"usb-gone-if00": "../ttyACM9",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(byID, name)); err != nil {
			t.Fatal(err)
		}
	}
	// EvalSymlinks also resolves links in the temporary directory's own
	// path (/var on macOS), so name the ports the way it does.
	resolved, err := filepath.EvalSymlinks(dev)
	if err != nil {
		t.Fatal(err)
	}
	acm0, acm1, ttyS0 := filepath.Join(resolved, "ttyACM0"), filepath.Join(resolved, "ttyACM1"), filepath.Join(resolved, "ttyS0")

	tests := []struct {
		name  string
		ports []string
		dir   string
		want  []string
	}{
		{
			name:  "linked ports renamed",
			ports: []string{acm0, acm1, ttyS0},
			dir:   byID,
			want: []string{
				filepath.Join(byID, "usb-Raspberry_Pi_Pico_0BBBB5FF9024A634-if00"),
				filepath.Join(byID, "usb-simple-dcc_DCC_Command_Station_A171FAF5496D834F-if00"),
				ttyS0,
			},
		},
		{
			name:  "no by-id directory",
			ports: []string{acm0, ttyS0},
			dir:   filepath.Join(dev, "missing"),
			want:  []string{acm0, ttyS0},
		},
		{name: "no ports", ports: nil, dir: byID, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preferByID(tt.ports, tt.dir); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("preferByID = %v, want %v", got, tt.want)
			}
		})
	}
}
