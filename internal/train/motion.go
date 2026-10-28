package train

import "math"

// Momentum is the speed step a decoder reaches after dt seconds on its
// way from step to target (negative in reverse), at the rates its
// acceleration (CV 3) and deceleration (CV 4) give: CV × 0.896 s for
// the 128 steps, as S-9.2.2 has it. Going the other way it slows to a
// stop first.
func Momentum(step, target, dt float64, cv3, cv4 byte) float64 {
	rate := func(cv byte) float64 {
		if cv == 0 {
			return math.Inf(1)
		}
		return 128 / (float64(cv) * 0.896)
	}
	if step != 0 && target != 0 && (step < 0) != (target < 0) {
		target = 0
	}
	if math.Abs(target) > math.Abs(step) {
		change := rate(cv3) * dt
		if math.Abs(target-step) <= change {
			return target
		}
		return step + math.Copysign(change, target)
	}
	change := rate(cv4) * dt
	if math.Abs(target-step) <= change {
		return target
	}
	return step - math.Copysign(change, step)
}

// SpeedCurve is the share of its top speed a decoder gives at a speed
// step, from 0 to 1: its speed table from the start, middle and top
// voltages (CVs 2, 6 and 5, out of 255) over the 126 steps. A top of 0
// or 1 counts as full and a middle of 0 or 1 as halfway, as decoders
// take them.
func SpeedCurve(step float64, vStart, vMid, vHigh byte) float64 {
	if step < 0.5 {
		return 0
	}
	step = min(step, MaxSpeed)
	high := float64(vHigh)
	if high <= 1 {
		high = 255
	}
	start := float64(vStart)
	mid := float64(vMid)
	if mid <= 1 || mid > high {
		mid = (start + high) / 2
	}
	u := (max(step, 1) - 1) / (MaxSpeed - 1)
	var v float64
	if u <= 0.5 {
		v = start + (mid-start)*u*2
	} else {
		v = mid + (high-mid)*(u-0.5)*2
	}
	return v / 255
}
