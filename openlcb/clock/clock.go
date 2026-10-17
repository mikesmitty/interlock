// Package clock encodes and decodes the events of the Broadcast Time
// protocol: a clock, named by a six byte ID, broadcasts its time, date,
// year and rate as event IDs whose last two bytes carry the value, and
// takes commands (set, start, stop, query) through events of the same
// shape with the set bit on. Pure functions; the clock itself lives in
// the application.
package clock

import (
	"fmt"
	"math"

	"github.com/mikesmitty/interlock/openlcb"
)

// Well-known clock IDs. A clock's events are its ID followed by a two
// byte suffix, so each clock owns a 16-bit event range.
const (
	DefaultFastClock     openlcb.NodeID = 0x010100000100
	DefaultRealTimeClock openlcb.NodeID = 0x010100000101
	AlternateClock1      openlcb.NodeID = 0x010100000102
	AlternateClock2      openlcb.NodeID = 0x010100000103
)

// Kind is what a clock event carries.
type Kind uint8

// Kinds. The first four are reports when Set is false and commands when
// it is true; the rest are commands and notices without values.
const (
	KindTime Kind = iota + 1
	KindDate
	KindYear
	KindRate
	KindQuery
	KindStop
	KindStart
	KindDateRollover
)

// String names the kind.
func (k Kind) String() string {
	switch k {
	case KindTime:
		return "time"
	case KindDate:
		return "date"
	case KindYear:
		return "year"
	case KindRate:
		return "rate"
	case KindQuery:
		return "query"
	case KindStop:
		return "stop"
	case KindStart:
		return "start"
	case KindDateRollover:
		return "date rollover"
	}
	return fmt.Sprintf("kind %d", k)
}

// Suffix layout.
const (
	suffixSet          = 0x8000
	suffixTypeMask     = 0xF000
	suffixDate         = 0x2000
	suffixYear         = 0x3000
	suffixRate         = 0x4000
	suffixQuery        = 0xF000
	suffixStop         = 0xF001
	suffixStart        = 0xF002
	suffixDateRollover = 0xF003
)

// Rate is a clock rate in quarters: 4 means real time, 16 means four
// times faster, negative runs backward. It is a 12-bit two's complement
// value on the wire, so it spans -512 to 511.75.
type Rate int16

// Rate limits.
const (
	MinRate Rate = -2048
	MaxRate Rate = 2047
)

// RateOf converts a multiplier such as 4.0 to a Rate, rounding to the
// nearest quarter and clamping to the wire's range.
func RateOf(multiplier float64) Rate {
	q := math.Round(multiplier * 4)
	return Rate(max(float64(MinRate), min(float64(MaxRate), q)))
}

// Float returns the multiplier.
func (r Rate) Float() float64 { return float64(r) / 4 }

// String reads like "4×" or "1.5×".
func (r Rate) String() string {
	f := r.Float()
	if f == math.Trunc(f) {
		return fmt.Sprintf("%.0f×", f)
	}
	return fmt.Sprintf("%g×", f)
}

// Event is a decoded clock event.
type Event struct {
	Clock openlcb.NodeID
	Kind  Kind
	// Set marks a command to change the clock rather than a report.
	Set bool
	// Hour and Minute for KindTime; Month and Day for KindDate; Year for
	// KindYear (0 to 4095); Rate for KindRate.
	Hour, Minute int
	Month, Day   int
	Year         int
	Rate         Rate
}

// ID encodes the event.
func (e Event) ID() openlcb.EventID {
	var suffix uint16
	switch e.Kind {
	case KindTime:
		suffix = uint16(e.Hour&0x1F)<<8 | uint16(e.Minute&0xFF)
	case KindDate:
		suffix = suffixDate | uint16(e.Month&0x0F)<<8 | uint16(e.Day&0xFF)
	case KindYear:
		suffix = suffixYear | uint16(e.Year&0x0FFF)
	case KindRate:
		suffix = suffixRate | uint16(e.Rate)&0x0FFF
	case KindQuery:
		return e.Clock.Event(suffixQuery)
	case KindStop:
		return e.Clock.Event(suffixStop)
	case KindStart:
		return e.Clock.Event(suffixStart)
	case KindDateRollover:
		return e.Clock.Event(suffixDateRollover)
	}
	if e.Set {
		suffix |= suffixSet
	}
	return e.Clock.Event(suffix)
}

// String describes the event for logs and the monitor.
func (e Event) String() string {
	verb := "reports"
	if e.Set {
		verb = "set"
	}
	switch e.Kind {
	case KindTime:
		return fmt.Sprintf("clock %s time %02d:%02d", verb, e.Hour, e.Minute)
	case KindDate:
		return fmt.Sprintf("clock %s date %02d-%02d", verb, e.Month, e.Day)
	case KindYear:
		return fmt.Sprintf("clock %s year %d", verb, e.Year)
	case KindRate:
		return fmt.Sprintf("clock %s rate %s", verb, e.Rate)
	}
	return "clock " + e.Kind.String()
}

// Parse decodes a clock event. ok is false for IDs that are not valid
// clock events of that clock, such as a time of 25:00.
func Parse(id openlcb.EventID) (ev Event, ok bool) {
	ev.Clock = id.Node()
	suffix := id.Suffix()
	switch suffix {
	case suffixQuery:
		ev.Kind = KindQuery
		return ev, true
	case suffixStop:
		ev.Kind = KindStop
		return ev, true
	case suffixStart:
		ev.Kind = KindStart
		return ev, true
	case suffixDateRollover:
		ev.Kind = KindDateRollover
		return ev, true
	}
	if suffix&suffixSet != 0 {
		ev.Set = true
		suffix &^= suffixSet
	}
	switch suffix & suffixTypeMask {
	case 0x0000, 0x1000:
		ev.Kind = KindTime
		ev.Hour, ev.Minute = int(suffix>>8), int(suffix&0xFF)
		return ev, ev.Hour <= 23 && ev.Minute <= 59
	case suffixDate:
		ev.Kind = KindDate
		ev.Month, ev.Day = int(suffix>>8&0x0F), int(suffix&0xFF)
		return ev, ev.Month >= 1 && ev.Month <= 12 && ev.Day >= 1 && ev.Day <= 31
	case suffixYear:
		ev.Kind = KindYear
		ev.Year = int(suffix & 0x0FFF)
		return ev, true
	case suffixRate:
		ev.Kind = KindRate
		q := int16(suffix & 0x0FFF)
		if q&0x0800 != 0 {
			q -= 0x1000 // sign extend the 12 bits
		}
		ev.Rate = Rate(q)
		return ev, true
	}
	return Event{}, false
}

// IsClock reports whether id belongs to clock's event range.
func IsClock(clock openlcb.NodeID, id openlcb.EventID) bool {
	return id.Node() == clock
}

// Range is the clock's whole event range, for Range Identified replies.
func Range(clock openlcb.NodeID) openlcb.EventRange {
	return openlcb.NewEventRange(clock.Event(0), 16)
}

// Builders for the common events.

// TimeEvent reports or sets a time of day.
func TimeEvent(clock openlcb.NodeID, hour, minute int, set bool) openlcb.EventID {
	return Event{Clock: clock, Kind: KindTime, Hour: hour, Minute: minute, Set: set}.ID()
}

// DateEvent reports or sets the month and day.
func DateEvent(clock openlcb.NodeID, month, day int, set bool) openlcb.EventID {
	return Event{Clock: clock, Kind: KindDate, Month: month, Day: day, Set: set}.ID()
}

// YearEvent reports or sets the year.
func YearEvent(clock openlcb.NodeID, year int, set bool) openlcb.EventID {
	return Event{Clock: clock, Kind: KindYear, Year: year, Set: set}.ID()
}

// RateEvent reports or sets the rate.
func RateEvent(clock openlcb.NodeID, rate Rate, set bool) openlcb.EventID {
	return Event{Clock: clock, Kind: KindRate, Rate: rate, Set: set}.ID()
}

// QueryEvent asks the clock to broadcast its state.
func QueryEvent(clock openlcb.NodeID) openlcb.EventID {
	return Event{Clock: clock, Kind: KindQuery}.ID()
}

// StartEvent starts the clock, or reports that it runs.
func StartEvent(clock openlcb.NodeID) openlcb.EventID {
	return Event{Clock: clock, Kind: KindStart}.ID()
}

// StopEvent stops the clock, or reports that it is stopped.
func StopEvent(clock openlcb.NodeID) openlcb.EventID { return Event{Clock: clock, Kind: KindStop}.ID() }

// DateRolloverEvent announces that the next time event starts a new day.
func DateRolloverEvent(clock openlcb.NodeID) openlcb.EventID {
	return Event{Clock: clock, Kind: KindDateRollover}.ID()
}
