package tracking

import (
	"math"
	"slices"

	"github.com/mikesmitty/interlock/internal/train"
)

// SpeedPoint is a speed measured at a speed step.
type SpeedPoint struct {
	// Step is the speed step, 1 to 126.
	Step int `json:"step"`
	// Speed is how fast the train ran at it, in millimeters a second.
	Speed float64 `json:"speed"`
	// Runs is how many runs from one block into the next it was measured
	// over.
	Runs int `json:"runs"`
}

// Motion is how a train moves at the speed steps it is set to: its
// decoder's speed curve and momentum, the top speed measured for that
// curve, and the speeds measured at particular steps, which go before
// the curve where there are any.
type Motion struct {
	// Start, Mid and High are the speed curve's start, middle and top
	// voltages (CVs 2, 6 and 5); Accel and Decel the momentum (CVs 3
	// and 4).
	Start, Mid, High, Accel, Decel byte
	// Top is the speed at full on the curve, in millimeters a second; 0
	// until measured.
	Top float64
	// Speeds are the speeds measured, by step.
	Speeds []SpeedPoint
}

func (m Motion) curve(step float64) float64 {
	return train.SpeedCurve(step, m.Start, m.Mid, m.High)
}

// Speed is how fast the train goes at a step, in millimeters a second:
// between two steps measured, in proportion to them; beyond those
// measured, the curve's shape from the nearest; with none measured, the
// curve times the top speed. It is 0 while nothing is known.
func (m Motion) Speed(step float64) float64 {
	step = math.Abs(step)
	if step < 0.5 {
		return 0
	}
	step = min(step, train.MaxSpeed)
	if len(m.Speeds) == 0 {
		return m.Top * m.curve(step)
	}
	i, _ := slices.BinarySearchFunc(m.Speeds, step, func(p SpeedPoint, s float64) int {
		switch {
		case float64(p.Step) < s:
			return -1
		case float64(p.Step) > s:
			return 1
		}
		return 0
	})
	switch {
	case i < len(m.Speeds) && float64(m.Speeds[i].Step) == step:
		return m.Speeds[i].Speed
	case i == 0:
		return m.from(m.Speeds[0], step)
	case i == len(m.Speeds):
		return m.from(m.Speeds[i-1], step)
	}
	a, b := m.Speeds[i-1], m.Speeds[i]
	f := (step - float64(a.Step)) / float64(b.Step-a.Step)
	return a.Speed + (b.Speed-a.Speed)*f
}

// from is the speed at step by the curve's shape through a speed
// measured.
func (m Motion) from(p SpeedPoint, step float64) float64 {
	if at := m.curve(float64(p.Step)); at > 0 {
		return p.Speed * m.curve(step) / at
	}
	return p.Speed * step / float64(p.Step)
}

// Known reports whether anything says how fast the train goes.
func (m Motion) Known() bool { return m.Top > 0 || len(m.Speeds) > 0 }

// Step is the lowest step that runs the train at speed or faster, in
// millimeters a second: 0 for no speed, the top step if none is that
// fast.
func (m Motion) Step(speed float64) int {
	if speed <= 0 {
		return 0
	}
	for s := 1; s < train.MaxSpeed; s++ {
		if m.Speed(float64(s)) >= speed {
			return s
		}
	}
	return train.MaxSpeed
}

// Next is the step the decoder reaches after dt seconds on its way from
// step to target, by its momentum.
func (m Motion) Next(step, target, dt float64) float64 {
	return train.Momentum(step, target, dt, m.Accel, m.Decel)
}

// Slowing is how far the train runs while its decoder slows from step
// to the slower step to at the rate its deceleration (CV 4) allows: all
// of it at once with none.
func (m Motion) Slowing(step, to float64) float64 {
	step, to = math.Abs(step), math.Abs(to)
	if m.Decel == 0 || to >= step {
		return 0
	}
	// The decoder takes CV 4 × 0.896 s over the 128 steps, so each step
	// takes as long as any other.
	perStep := float64(m.Decel) * 0.896 / 128
	const du = 0.5
	d := 0.0
	for u := to; u < step; u += du {
		d += m.Speed(min(u+du/2, step)) * min(du, step-u)
	}
	return d * perStep
}

// StopDistance is how far the train runs from step to a stand, set to 0
// now.
func (m Motion) StopDistance(step float64) float64 { return m.Slowing(step, 0) }
