package clock_test

import (
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/clock"
)

func TestEvents(t *testing.T) {
	fc := clock.DefaultFastClock
	tests := []struct {
		name string
		id   openlcb.EventID
		want clock.Event
	}{
		{"time", 0x010100000100062A, clock.Event{Clock: fc, Kind: clock.KindTime, Hour: 6, Minute: 42}},
		{"time 17:30", clock.TimeEvent(fc, 17, 30, false), clock.Event{Clock: fc, Kind: clock.KindTime, Hour: 17, Minute: 30}},
		{"set time", 0x0101000001008E3B, clock.Event{Clock: fc, Kind: clock.KindTime, Set: true, Hour: 14, Minute: 59}},
		{"date", 0x010100000100<<16 | 0x2A1F, clock.Event{Clock: fc, Kind: clock.KindDate, Month: 10, Day: 31}},
		{"set date", clock.DateEvent(fc, 2, 29, true), clock.Event{Clock: fc, Kind: clock.KindDate, Set: true, Month: 2, Day: 29}},
		{"year", clock.YearEvent(fc, 1957, false), clock.Event{Clock: fc, Kind: clock.KindYear, Year: 1957}},
		{"rate 4x", 0x010100000100<<16 | 0x4010, clock.Event{Clock: fc, Kind: clock.KindRate, Rate: 16}},
		{"rate -1x", 0x010100000100<<16 | 0x4FFC, clock.Event{Clock: fc, Kind: clock.KindRate, Rate: -4}},
		{"set rate", clock.RateEvent(fc, 6, true), clock.Event{Clock: fc, Kind: clock.KindRate, Set: true, Rate: 6}},
		{"query", 0x010100000100<<16 | 0xF000, clock.Event{Clock: fc, Kind: clock.KindQuery}},
		{"stop", clock.StopEvent(fc), clock.Event{Clock: fc, Kind: clock.KindStop}},
		{"start", clock.StartEvent(fc), clock.Event{Clock: fc, Kind: clock.KindStart}},
		{"rollover", clock.DateRolloverEvent(clock.AlternateClock1), clock.Event{Clock: clock.AlternateClock1, Kind: clock.KindDateRollover}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := clock.Parse(tc.id)
			if !ok || got != tc.want {
				t.Fatalf("Parse(%v) = %+v, %v; want %+v", tc.id, got, ok, tc.want)
			}
			if back := got.ID(); back != tc.id {
				t.Errorf("ID() = %v, want %v", back, tc.id)
			}
		})
	}
	for _, bad := range []openlcb.EventID{
		0x010100000100<<16 | 0x1800, // 24:00
		0x010100000100<<16 | 0x003C, // 00:60
		0x010100000100<<16 | 0x2D01, // month 13
		0x010100000100<<16 | 0x2100, // day 0
		0x010100000100<<16 | 0x5000, // reserved type
		0x010100000100<<16 | 0xF004, // unknown command
	} {
		if ev, ok := clock.Parse(bad); ok {
			t.Errorf("Parse(%v) accepted %+v", bad, ev)
		}
	}
}

func TestRate(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		rate clock.Rate
		s    string
	}{
		{1, 4, "1×"}, {4, 16, "4×"}, {1.5, 6, "1.5×"}, {-1, -4, "-1×"}, {0.1, 0, "0×"}, {1000, 2047, "511.75×"}, {-1000, -2048, "-512×"},
	} {
		if got := clock.RateOf(tc.in); got != tc.rate {
			t.Errorf("RateOf(%v) = %d, want %d", tc.in, got, tc.rate)
		}
		if got := tc.rate.String(); got != tc.s {
			t.Errorf("%d.String() = %q, want %q", tc.rate, got, tc.s)
		}
	}
	if clock.Rate(16).Float() != 4 {
		t.Error("16 quarters should be 4x")
	}
}

func TestRange(t *testing.T) {
	r := clock.Range(clock.DefaultFastClock)
	if !r.Contains(clock.TimeEvent(clock.DefaultFastClock, 1, 2, false)) || r.Contains(clock.TimeEvent(clock.DefaultRealTimeClock, 1, 2, false)) {
		t.Errorf("range %v is wrong", r)
	}
	if !clock.IsClock(clock.DefaultFastClock, clock.QueryEvent(clock.DefaultFastClock)) {
		t.Error("IsClock failed")
	}
	if s := (clock.Event{Clock: clock.DefaultFastClock, Kind: clock.KindTime, Hour: 6, Minute: 5}).String(); s != "clock reports time 06:05" {
		t.Errorf("String = %q", s)
	}
}
