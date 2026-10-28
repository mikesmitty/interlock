package train

import (
	"math"
	"testing"
)

func TestMomentum(t *testing.T) {
	for _, tc := range []struct {
		name             string
		step, target, dt float64
		cv3, cv4         byte
		want             float64
	}{
		// CV 3 at 5: 128 steps in 4.48 s, so a second takes it 28.6 steps.
		{"a second's acceleration", 0, 126, 1, 5, 5, 128 / 4.48},
		{"no momentum", 0, 80, 0.01, 0, 0, 80},
		{"turning about with no deceleration stops first", 40, -40, 0.1, 5, 0, 0},
		{"turning about slows first", -10, 20, 0.1, 5, 5, -10 + 128/4.48*0.1},
		{"slowing", 100, 20, 1, 0, 5, 100 - 128/4.48},
	} {
		if got := Momentum(tc.step, tc.target, tc.dt, tc.cv3, tc.cv4); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestSpeedCurve(t *testing.T) {
	for _, tc := range []struct {
		name             string
		step             float64
		start, mid, high byte
		want             float64
	}{
		{"top", 126, 0, 0, 255, 1},
		{"stopped", 0.2, 0, 0, 255, 0},
		{"halfway with no middle", 63.5, 0, 0, 255, 0.5},
		{"a slow middle", 63.5, 0, 64, 255, 64.0 / 255},
		{"top at 0 counts as full", 126, 0, 0, 0, 1},
		{"first step at the start voltage", 1, 30, 0, 255, 30.0 / 255},
	} {
		if got := SpeedCurve(tc.step, tc.start, tc.mid, tc.high); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: %v; want %v", tc.name, got, tc.want)
		}
	}
}
