package automation

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// clock is a time of day in seconds since midnight, local time.
type clock int

// parseClock reads "HH:MM" or "HH:MM:SS".
func parseClock(s string) (clock, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, fmt.Errorf("%q is not a time of day such as 07:30", s)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || len(p) != 2 {
			return 0, fmt.Errorf("%q is not a time of day such as 07:30", s)
		}
		nums[i] = n
	}
	if nums[0] > 23 || nums[1] > 59 || nums[2] > 59 {
		return 0, fmt.Errorf("%q is not a time of day such as 07:30", s)
	}
	return clock(nums[0]*3600 + nums[1]*60 + nums[2]), nil
}

func (c clock) hms() (hour, minute, second int) {
	return int(c) / 3600, int(c) % 3600 / 60, int(c) % 60
}

// clockOf is the time of day of t.
func clockOf(t time.Time) clock {
	return clock(t.Hour()*3600 + t.Minute()*60 + t.Second())
}

var weekdayByName = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

// weekdayAllowed reports whether t's weekday is in days; an empty list
// allows every day.
func weekdayAllowed(days []string, t time.Time) bool {
	if len(days) == 0 {
		return true
	}
	return slices.ContainsFunc(days, func(d string) bool { return weekdayByName[strings.ToLower(d)] == t.Weekday() })
}

// nextAt returns the first instant after now at time of day c on an
// allowed weekday. Days are built with time.Date so daylight saving
// changes do not shift the hour.
func nextAt(now time.Time, c clock, days []string) time.Time {
	hour, minute, second := c.hms()
	for i := range 8 {
		day := now.AddDate(0, 0, i)
		at := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, now.Location())
		if at.After(now) && weekdayAllowed(days, at) {
			return at
		}
	}
	return now.Add(24 * time.Hour) // every weekday excluded; cannot happen past validation
}

// inWindow reports whether t is on an allowed weekday and inside the
// window from after to before. Either bound may be empty; the window
// wraps past midnight when after is later than before.
func inWindow(t time.Time, after, before string, days []string) bool {
	if !weekdayAllowed(days, t) {
		return false
	}
	now := clockOf(t)
	var a, b clock = -1, -1
	if after != "" {
		a, _ = parseClock(after)
	}
	if before != "" {
		b, _ = parseClock(before)
	}
	switch {
	case a < 0 && b < 0:
		return true
	case a < 0:
		return now < b
	case b < 0:
		return now >= a
	case a <= b:
		return now >= a && now < b
	default:
		return now >= a || now < b
	}
}
