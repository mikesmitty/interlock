package driving

import "github.com/mikesmitty/interlock/internal/tracking"

// Defaults for how a train is driven.
const (
	// DefaultBraking is how hard a train is braked, in millimeters a
	// second per second, when its decoder's momentum allows.
	DefaultBraking = 40.0
	// ApproachSpeed is how fast a train runs over the last of its way to
	// a stop, in millimeters a second, so that it stops where it should.
	ApproachSpeed = 20.0
	// approachRun is how far it runs at that speed before it stops.
	approachRun = 30.0
	// Latency is how long a speed command takes to reach the decoder, in
	// seconds.
	Latency = 0.1
)

// brakes works out the speed step a train is to run at to stop at a
// point: as fast as asked while there is room to brake, slower as the
// point nears, at the approach speed over the last of the way, then
// stopped where the decoder's momentum brings it to a stand at the
// point.
type brakes struct {
	cruise  int
	braking float64
}

// step is the speed step to set the train to, d millimeters short of
// where it is to stand, with its decoder at step by tracking's estimate.
func (b brakes) step(m tracking.Motion, step, d float64) int {
	v := m.Speed(step)
	if d <= m.StopDistance(step)+v*Latency {
		return 0
	}
	approach := min(max(m.Step(ApproachSpeed), 1), b.cruise)
	va := m.Speed(float64(approach))
	reserve := approachRun + m.StopDistance(float64(approach)) + va*Latency
	for c := b.cruise; c > approach; c-- {
		vc := m.Speed(float64(c))
		// The decoder slows no faster than its momentum lets it, however
		// fast it is asked to.
		braking := max((vc*vc-va*va)/(2*b.braking), m.Slowing(float64(c), float64(approach)))
		if braking+reserve+vc*Latency <= d {
			return c
		}
	}
	return approach
}

// stoppingDistance is how far a train runs from step braking as hard as
// it would to a stop.
func (b brakes) stoppingDistance(m tracking.Motion, step float64) float64 {
	v := m.Speed(step)
	return max(v*v/(2*b.braking), m.StopDistance(step)) + v*Latency
}
