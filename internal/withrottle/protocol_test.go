package withrottle

import (
	"maps"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/train"
)

func TestParseKey(t *testing.T) {
	tests := []struct {
		key     string
		address uint16
		long    bool
		ok      bool
	}{
		{"L341", 341, true, true},
		{"L3", 3, true, true},
		{"S3", 3, false, true},
		{"S127", 127, false, true},
		{"S128", 0, false, false},
		{"L10239", 10239, true, true},
		{"L10240", 0, false, false},
		{"L0", 0, false, false},
		{"S", 0, false, false},
		{"X3", 0, false, false},
		{"Labc", 0, false, false},
		{"*", 0, false, false},
	}
	for _, tt := range tests {
		address, long, ok := parseKey(tt.key)
		if address != tt.address || long != tt.long || ok != tt.ok {
			t.Errorf("parseKey(%q) = %d, %v, %v; want %d, %v, %v", tt.key, address, long, ok, tt.address, tt.long, tt.ok)
		}
		if ok && locoKey(address, long) != tt.key {
			t.Errorf("locoKey(%d, %v) = %q", address, long, locoKey(address, long))
		}
	}
}

func TestClean(t *testing.T) {
	if got := clean(" A]\\[B}|{C<;>D\r\nE "); got != "A B C D  E" {
		t.Errorf("clean = %q", got)
	}
}

func TestFunctions(t *testing.T) {
	tests := []struct {
		name      string
		train     train.Train
		count     int
		momentary map[int]bool
	}{
		{"nothing known", train.Train{}, 29, map[int]bool{2: true}},
		{"roster labels", train.Train{Entry: train.Entry{Functions: map[int]string{0: "Light", 2: "Horn"}}}, 29, map[int]bool{}},
		{"roster momentary", train.Train{Entry: train.Entry{Momentary: []int{3}}}, 29, map[int]bool{3: true}},
		{"labels past F28", train.Train{Entry: train.Entry{Functions: map[int]string{30: "Mute"}}}, 31, map[int]bool{}},
		{"labels past F31", train.Train{Entry: train.Entry{Functions: map[int]string{31: "A", 40: "B"}}}, 32, map[int]bool{}},
	}
	for _, tt := range tests {
		if got := functionCount(tt.train); got != tt.count {
			t.Errorf("%s: functionCount = %d, want %d", tt.name, got, tt.count)
		}
		if got := momentary(tt.train); !maps.Equal(got, tt.momentary) {
			t.Errorf("%s: momentary = %v, want %v", tt.name, got, tt.momentary)
		}
	}
	if got := functionLabels(map[int]string{0: "Light", 2: "Horn"}, 4); got != "]\\[Light]\\[]\\[Horn]\\[" {
		t.Errorf("functionLabels = %q", got)
	}
}

func TestStates(t *testing.T) {
	for _, tt := range []struct {
		speed float32
		want  int
	}{{0, 0}, {0.2, 1}, {10.4, 10}, {10.5, 11}, {126, 126}} {
		if got := speedOf(train.Train{Speed: tt.speed}); got != tt.want {
			t.Errorf("speedOf(%v) = %d, want %d", tt.speed, got, tt.want)
		}
	}
	for _, tt := range []struct {
		state, commanded string
		want             int
	}{
		{entity.StateClosed, "", turnoutClosed},
		{entity.StateThrown, "", turnoutThrown},
		{entity.StateUnknown, "", turnoutUnknown},
		{entity.StateMoving, entity.StateThrown, turnoutThrown},
		{entity.StateMoving, "", turnoutUnknown},
	} {
		if got := turnoutState(entity.Entity{State: tt.state, Commanded: tt.commanded}); got != tt.want {
			t.Errorf("turnoutState(%s, %s) = %d, want %d", tt.state, tt.commanded, got, tt.want)
		}
	}
	now := time.Now()
	for _, tt := range []struct {
		layout train.Layout
		want   int
	}{
		{train.Layout{}, powerUnknown},
		{train.Layout{Updated: now}, powerUnknown},
		{train.Layout{PowerKnown: true, Updated: now}, powerOn},
		{train.Layout{PowerOff: true, PowerKnown: true, Updated: now}, powerOff},
	} {
		if got := powerState(tt.layout); got != tt.want {
			t.Errorf("powerState(%+v) = %d, want %d", tt.layout, got, tt.want)
		}
	}
	if got := turnoutList(nil); got != "" {
		t.Errorf("empty turnout list = %q", got)
	}
}
