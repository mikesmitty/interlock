package sim

import (
	"fmt"

	"github.com/mikesmitty/interlock/openlcb"
)

// OutpostHeads is how many signal heads an outpost's chain has room for.
const OutpostHeads = 16

const (
	aspectDark = 0
	aspectRed  = 1
	// lampOwn is the lamp value of an output with its own On and Off events,
	// outside the aspects.
	lampOwn = 8
)

// headOutputs are a head's outputs as the CDI names them.
var headOutputs = [...]string{"R", "G", "Y"}

// headAspects are a head's aspects in the firmware's order, each named as
// its event is in the CDI, with the lamp it lights by its value there.
var headAspects = [...]struct {
	name string
	lamp int
}{
	{"Dark", 0},
	{"Red", 1},
	{"Yellow", 2},
	{"Green", 3},
	{"Lunar", 4},
	{"S", 5},
	{"D", 6},
	{"Number", 7},
	{"Flashing Red", 1},
	{"Flashing Yellow", 2},
	{"Flashing Green", 3},
}

type headState struct {
	enabled bool
	aspect  int
	// lamps are the outputs set to their own events, on or off.
	lamps [len(headOutputs)]bool
}

func headPath(h int, name string) string {
	return fmt.Sprintf("Signal Heads/Head %d/%s", h+1, name)
}

// ownPath names one of an output's own events: on is its On event.
func ownPath(h, out int, on bool) string {
	which := "Off"
	if on {
		which = "On"
	}
	return fmt.Sprintf("Signal Heads/Head %d Outputs/%s %s", h+1, headOutputs[out], which)
}

// The signal test's events: from Start until Stop every chip on the chain
// lights red, yellow and green in turn. The firmware keeps them on the bus
// whether or not any head is enabled.
const (
	testStartPath = "Signal Heads/Signal Test/Start Event"
	testStopPath  = "Signal Heads/Signal Test/Stop Event"
)

// canShow reports whether a head has a lamp for an aspect. It can show
// dark if any of its lamps belongs to the aspects.
func (o *Outpost) canShow(h, a int) bool {
	for _, output := range headOutputs {
		lamp := o.intValue(headPath(h, output+" Lamp"))
		if a == aspectDark && lamp != 0 && lamp != lampOwn || a != aspectDark && lamp == headAspects[a].lamp {
			return true
		}
	}
	return false
}

// ownEvent returns one of an output's own events, or 0 when it isn't on
// the bus: only an enabled head's outputs set to Own Events have them.
func (o *Outpost) ownEvent(h, out int, on bool) openlcb.EventID {
	if o.intValue(headPath(h, "Enabled")) == 0 || o.intValue(headPath(h, headOutputs[out]+" Lamp")) != lampOwn {
		return 0
	}
	return o.event(ownPath(h, out, on))
}

// aspectEvent returns an aspect's event, or 0 when it isn't on the bus:
// only the aspects an enabled head can show are.
func (o *Outpost) aspectEvent(h, a int) openlcb.EventID {
	if o.intValue(headPath(h, "Enabled")) == 0 || !o.canShow(h, a) {
		return 0
	}
	return o.event(headPath(h, headAspects[a].name+" Event"))
}

// powerOnHeads starts every enabled head on its power-on aspect, with no
// signal test running.
func (o *Outpost) powerOnHeads() {
	o.mu.Lock()
	o.heads = [OutpostHeads]headState{}
	o.signalTest = false
	o.mu.Unlock()
	o.applyHeads()
}

// applyHeads brings the heads into line with the configuration: a head
// being enabled starts on its power-on aspect, and a disabled one is dark.
// An output set to Own Events starts out dark.
func (o *Outpost) applyHeads() {
	for h := range OutpostHeads {
		on := o.intValue(headPath(h, "Enabled")) != 0
		aspect := o.intValue(headPath(h, "Power-On Aspect"))
		if aspect >= len(headAspects) {
			aspect = aspectRed
		}
		if !on {
			aspect = aspectDark
		}
		o.mu.Lock()
		if s := &o.heads[h]; s.enabled != on {
			*s = headState{enabled: on, aspect: aspect}
		}
		o.mu.Unlock()
		for out := range headOutputs {
			if o.ownEvent(h, out, true) == 0 {
				o.mu.Lock()
				o.heads[h].lamps[out] = false
				o.mu.Unlock()
			}
		}
	}
}

// headEvent shows the aspect an event chooses, if any, or starts or stops
// the signal test.
func (o *Outpost) headEvent(ev openlcb.EventID) {
	if ev != 0 && (ev == o.event(testStartPath) || ev == o.event(testStopPath)) {
		start := ev == o.event(testStartPath)
		o.log.Info("sim: signal test", "running", start)
		o.mu.Lock()
		o.signalTest = start
		o.mu.Unlock()
	}
	for h := range OutpostHeads {
		for a := range headAspects {
			if ev != o.aspectEvent(h, a) {
				continue
			}
			o.log.Info("sim: signal head", "head", h+1, "aspect", headAspects[a].name)
			o.mu.Lock()
			o.heads[h].aspect = a
			o.mu.Unlock()
		}
		for out := range headOutputs {
			for _, on := range []bool{true, false} {
				if ev != 0 && ev == o.ownEvent(h, out, on) {
					o.log.Info("sim: signal head lamp", "head", h+1, "output", headOutputs[out], "on", on)
					o.mu.Lock()
					o.heads[h].lamps[out] = on
					o.mu.Unlock()
				}
			}
		}
	}
}

// EnableHeads enables the first n heads, with the firmware's default
// lamps, as a board set up that way would come up.
func (o *Outpost) EnableHeads(n int) error {
	for h := range min(n, OutpostHeads) {
		v, ok := o.vars[headPath(h, "Enabled")]
		if !ok {
			return fmt.Errorf("no variable %s", headPath(h, "Enabled"))
		}
		if err := o.Config.Write(v.Address, []byte{1}); err != nil {
			return fmt.Errorf("enabling head %d: %w", h+1, err)
		}
	}
	return nil
}

// Head returns the aspect a head shows, named as in the CDI.
func (o *Outpost) Head(h int) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return headAspects[o.heads[h].aspect].name
}

// Lamp reports whether a head's output set to Own Events is lit.
func (o *Outpost) Lamp(h, out int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.heads[h].lamps[out]
}

// SignalTest reports whether the signal test is running.
func (o *Outpost) SignalTest() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.signalTest
}

// testState is a test event's state: Start is valid while the test runs,
// Stop while it doesn't.
func (o *Outpost) testState(start bool) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.signalTest == start {
		return stateValid
	}
	return stateInvalid
}

func (o *Outpost) aspectState(h, a int) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.heads[h].aspect == a {
		return stateValid
	}
	return stateInvalid
}

// lampState is an own event's state: On is valid while the output is lit,
// Off while it's dark.
func (o *Outpost) lampState(h, out int, on bool) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.heads[h].lamps[out] == on {
		return stateValid
	}
	return stateInvalid
}

func (o *Outpost) identifyHeads() {
	for h := range OutpostHeads {
		for a := range headAspects {
			o.sendIdentified(o.aspectEvent(h, a), false, o.aspectState(h, a))
		}
		for out := range headOutputs {
			for _, on := range []bool{true, false} {
				o.sendIdentified(o.ownEvent(h, out, on), false, o.lampState(h, out, on))
			}
		}
	}
	o.sendIdentified(o.event(testStartPath), false, o.testState(true))
	o.sendIdentified(o.event(testStopPath), false, o.testState(false))
}

// identifyHead answers Identify Consumer for a head's aspect event, an
// output's own event or a signal test event.
func (o *Outpost) identifyHead(ev openlcb.EventID) {
	for h := range OutpostHeads {
		for a := range headAspects {
			if ev == o.aspectEvent(h, a) {
				o.sendIdentified(ev, false, o.aspectState(h, a))
			}
		}
		for out := range headOutputs {
			for _, on := range []bool{true, false} {
				if ev != 0 && ev == o.ownEvent(h, out, on) {
					o.sendIdentified(ev, false, o.lampState(h, out, on))
				}
			}
		}
	}
	if ev == o.event(testStartPath) {
		o.sendIdentified(ev, false, o.testState(true))
	}
	if ev == o.event(testStopPath) {
		o.sendIdentified(ev, false, o.testState(false))
	}
}
