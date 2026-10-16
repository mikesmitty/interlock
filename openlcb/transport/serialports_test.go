package transport

import (
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
