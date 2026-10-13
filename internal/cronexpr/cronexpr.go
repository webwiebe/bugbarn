// Package cronexpr parses standard 5-field cron expressions and computes the
// next run time in a time zone. It exists so the monitor miss detector can ask
// "when was this job expected next" without a third-party dependency.
//
// Fields are minute, hour, day of month, month and day of week. Each takes "*",
// a number, a range "a-b", a step "*/n" or "a-b/n" or "a/n", and comma
// separated lists of those. Month and weekday names (JAN, MON) work. A day of
// week of 7 means Sunday. Like Vixie cron, a day matches when either day field
// matches if both are restricted, and when the restricted one matches if only
// one is.
//
// Daylight saving: a wall-clock time that does not exist (the hour skipped in
// spring) resolves to the same offset shifted forward, as time.Date does, so
// "30 2 * * *" runs at 03:30 that day. A wall-clock time that occurs twice (the
// hour repeated in autumn) counts once, at the occurrence time.Date picks.
package cronexpr

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // zone names must resolve on a minimal container image
)

// Schedule is a parsed cron expression.
type Schedule struct {
	expr    string
	minutes uint64
	hours   uint64
	dom     uint64
	months  uint64
	dow     uint64
	domAny  bool
	dowAny  bool
}

// maxSearchDays bounds the search for the next run: eight years covers the
// longest gap of a valid expression (29 February).
const maxSearchDays = 366 * 8

type fieldSpec struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	minuteSpec = fieldSpec{name: "minute", min: 0, max: 59}
	hourSpec   = fieldSpec{name: "hour", min: 0, max: 23}
	domSpec    = fieldSpec{name: "day of month", min: 1, max: 31}
	monthSpec  = fieldSpec{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	dowSpec = fieldSpec{name: "day of week", min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

// Parse reads a 5-field cron expression.
func Parse(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("cron expression needs 5 fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}
	s := Schedule{expr: strings.Join(fields, " ")}
	var err error
	if s.minutes, _, err = parseField(fields[0], minuteSpec); err != nil {
		return Schedule{}, err
	}
	if s.hours, _, err = parseField(fields[1], hourSpec); err != nil {
		return Schedule{}, err
	}
	if s.dom, s.domAny, err = parseField(fields[2], domSpec); err != nil {
		return Schedule{}, err
	}
	if s.months, _, err = parseField(fields[3], monthSpec); err != nil {
		return Schedule{}, err
	}
	if s.dow, s.dowAny, err = parseField(fields[4], dowSpec); err != nil {
		return Schedule{}, err
	}
	if s.dow&(1<<7) != 0 { // 7 is Sunday
		s.dow = s.dow&^(1<<7) | 1
	}
	if s.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).IsZero() {
		return Schedule{}, fmt.Errorf("cron expression %q never runs", s.expr)
	}
	return s, nil
}

// String is the expression with single spaces between the fields.
func (s Schedule) String() string { return s.expr }

func parseField(field string, spec fieldSpec) (mask uint64, star bool, err error) {
	star = strings.HasPrefix(field, "*")
	for _, part := range strings.Split(field, ",") {
		bitsOfPart, err := parsePart(part, spec)
		if err != nil {
			return 0, false, err
		}
		mask |= bitsOfPart
	}
	return mask, star, nil
}

func parsePart(part string, spec fieldSpec) (uint64, error) {
	rangePart, stepText, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%s: invalid step %q", spec.name, stepText)
		}
		step = n
	}
	lo, hi := spec.min, spec.max
	switch {
	case rangePart == "*":
	case strings.Contains(rangePart, "-"):
		a, b, _ := strings.Cut(rangePart, "-")
		var err error
		if lo, err = parseValue(a, spec); err != nil {
			return 0, err
		}
		if hi, err = parseValue(b, spec); err != nil {
			return 0, err
		}
		if lo > hi {
			return 0, fmt.Errorf("%s: range %q runs backwards", spec.name, rangePart)
		}
	default:
		v, err := parseValue(rangePart, spec)
		if err != nil {
			return 0, err
		}
		lo, hi = v, v
		if hasStep { // "a/n" runs from a to the end of the field
			hi = spec.max
		}
	}
	var mask uint64
	for v := lo; v <= hi; v += step {
		mask |= 1 << uint(v)
	}
	return mask, nil
}

func parseValue(text string, spec fieldSpec) (int, error) {
	if v, ok := spec.names[strings.ToLower(text)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid value %q", spec.name, text)
	}
	if v < spec.min || v > spec.max {
		return 0, fmt.Errorf("%s: %d is outside %d-%d", spec.name, v, spec.min, spec.max)
	}
	return v, nil
}

// Next returns the first run strictly after the given time, evaluated on the
// wall clock of loc. It returns the zero time when the expression has no run
// within eight years.
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := after.In(loc)
	y, mo, d := local.Date()
	// Start a day early: a wall-clock time shifted by a daylight saving gap can
	// land after the instant that precedes it on the calendar.
	for i := 0; i < maxSearchDays; i++ {
		day := time.Date(y, mo, d-1+i, 0, 0, 0, 0, time.UTC)
		if !s.matchesDay(day) {
			continue
		}
		if t, ok := s.firstRunOn(day, after, loc); ok {
			return t
		}
	}
	return time.Time{}
}

func (s Schedule) matchesDay(day time.Time) bool {
	if s.months&(1<<uint(day.Month())) == 0 {
		return false
	}
	domOK := s.dom&(1<<uint(day.Day())) != 0
	dowOK := s.dow&(1<<uint(day.Weekday())) != 0
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dowOK
	case s.dowAny:
		return domOK
	default:
		return domOK || dowOK
	}
}

func (s Schedule) firstRunOn(day, after time.Time, loc *time.Location) (time.Time, bool) {
	y, mo, d := day.Date()
	for h := bits.TrailingZeros64(s.hours); h < 24; h++ {
		if s.hours&(1<<uint(h)) == 0 {
			continue
		}
		for m := 0; m < 60; m++ {
			if s.minutes&(1<<uint(m)) == 0 {
				continue
			}
			if t := time.Date(y, mo, d, h, m, 0, 0, loc); t.After(after) {
				return t, true
			}
		}
	}
	return time.Time{}, false
}
