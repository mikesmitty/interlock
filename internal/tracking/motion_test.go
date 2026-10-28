package tracking

import (
	"math"
	"testing"
)

func TestMotion(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	// A straight curve from 0 to full, so the speed is in proportion to
	// the step.
	m := Motion{High: 255, Mid: 128, Decel: 10, Speeds: []SpeedPoint{{Step: 26, Speed: 30, Runs: 2}, {Step: 76, Speed: 100, Runs: 2}}}
	tests := []struct {
		step, want float64
	}{
		{0, 0},
		{26, 30},
		{51, 65},
		{76, 100},
		{-76, 100},
		// Beyond those measured, by the curve: step 126 is 1.66 times
		// step 76 on it.
		{126, 100 * m.curve(126) / m.curve(76)},
		{1, 30 * m.curve(1) / m.curve(26)},
	}
	for _, tt := range tests {
		if got := m.Speed(tt.step); !near(got, tt.want) {
			t.Errorf("Speed(%v) = %v, want %v", tt.step, got, tt.want)
		}
	}
	if s := m.Step(65); s != 51 {
		t.Errorf("Step(65) = %d, want 51", s)
	}
	if s := m.Step(1e6); s != 126 {
		t.Errorf("Step(a million) = %d, want 126", s)
	}
	if s := m.Step(0); s != 0 {
		t.Errorf("Step(0) = %d", s)
	}
	// Slowing from 76 to 26 takes 50 steps at 10 × 0.896 / 128 s each,
	// at 65 mm/s on average.
	if got, want := m.Slowing(76, 26), 50*10*0.896/128*65; math.Abs(got-want) > 0.5 {
		t.Errorf("Slowing(76, 26) = %.2f, want %.2f", got, want)
	}
	if got := (Motion{Top: 100}).StopDistance(80); got != 0 {
		t.Errorf("a decoder without momentum stops in %v", got)
	}
	none := Motion{Start: 0, Mid: 0, High: 0, Top: 160}
	if got, want := none.Speed(126), 160.0; !near(got, want) {
		t.Errorf("no speeds measured: Speed(126) = %v, want the top speed %v", got, want)
	}
	if (Motion{}).Known() || !none.Known() {
		t.Error("Known")
	}
}
