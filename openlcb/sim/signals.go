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
)

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
}

func headPath(h int, name string) string {
	return fmt.Sprintf("Signal Heads/Head %d/%s", h+1, name)
}

// canShow reports whether a head has a lamp for an aspect.
func (o *Outpost) canShow(h, a int) bool {
	if a == aspectDark {
		return true
	}
	for _, output := range []string{"OUTR Lamp", "OUTG Lamp", "OUTB Lamp"} {
		if o.intValue(headPath(h, output)) == headAspects[a].lamp {
			return true
		}
	}
	return false
}

// aspectEvent returns an aspect's event, or 0 when it isn't on the bus:
// only the aspects an enabled head can show are.
func (o *Outpost) aspectEvent(h, a int) openlcb.EventID {
	if o.intValue(headPath(h, "Enabled")) == 0 || !o.canShow(h, a) {
		return 0
	}
	return o.event(headPath(h, headAspects[a].name+" Event"))
}

// powerOnHeads starts every enabled head on its power-on aspect.
func (o *Outpost) powerOnHeads() {
	o.mu.Lock()
	o.heads = [OutpostHeads]headState{}
	o.mu.Unlock()
	o.applyHeads()
}

// applyHeads brings the heads into line with the configuration: a head
// being enabled starts on its power-on aspect, and a disabled one is dark.
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
	}
}

// headEvent shows the aspect an event chooses, if any.
func (o *Outpost) headEvent(ev openlcb.EventID) {
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

func (o *Outpost) aspectState(h, a int) eventState {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.heads[h].aspect == a {
		return stateValid
	}
	return stateInvalid
}

func (o *Outpost) identifyHeads() {
	for h := range OutpostHeads {
		for a := range headAspects {
			o.sendIdentified(o.aspectEvent(h, a), false, o.aspectState(h, a))
		}
	}
}

// identifyHead answers Identify Consumer for a head's aspect event.
func (o *Outpost) identifyHead(ev openlcb.EventID) {
	for h := range OutpostHeads {
		for a := range headAspects {
			if ev == o.aspectEvent(h, a) {
				o.sendIdentified(ev, false, o.aspectState(h, a))
			}
		}
	}
}
