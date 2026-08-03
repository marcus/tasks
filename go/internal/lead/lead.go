// Package lead is the read-path half of lib/tasks/lead.rb: how long before its
// occurrence date a task becomes visible.
//
//	lead    3w  2d  1m   a positive count and a calendar unit
//	        5h           or a clock duration in hours
//	anchor  the task's deadline if it has one, else its available-from date
//	gate    anchor - lead, released at local midnight of that date
//	        (a clock lead releases at anchor_instant - duration exactly)
//
// `h` is the one CLOCK unit: `5h` measures a real duration back from the
// anchor's instant, so it is arithmetic on instants rather than on dates and a
// gate DATE cannot express it. `m` always means months — never minutes —
// because a lead shares its unit letters with the recurrence grammar, and
// overloading one would silently change what an existing stored value means.
//
// There is deliberately no parser here: parsing user input belongs to the write
// path, which this build does not have. What a reader needs is the canonical
// guard, the unit split, and the two gate derivations.
package lead

import (
	"regexp"
	"strconv"
	"time"

	"tasks-go/internal/temporal"
)

// spanPattern is the canonical stored form. Zero is excluded: a `0d` lead is
// not a lead, and would read as "no window" while looking like one.
var spanPattern = regexp.MustCompile(`\A([1-9]\d*)([dwmyh])\z`)

// unitNames spells a unit for a human rendering.
var unitNames = map[string]string{"d": "day", "w": "week", "m": "month", "y": "year", "h": "hour"}

// Span reports whether a stored value is a lead span in its exact canonical
// spelling. Every reader calls this before deriving a gate, so a hand-edited
// value can never crash a read — Check reports it instead.
func Span(value string) bool { return spanPattern.MatchString(value) }

func parts(value string) (int, string, bool) {
	match := spanPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, "", false
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, "", false
	}
	return count, match[2], true
}

// Clock reports a span whose gate is an instant no date can express.
func Clock(value string) bool {
	_, unit, ok := parts(value)
	return ok && unit == "h"
}

// Duration is a clock span's length, or ok=false for a calendar span.
func Duration(value string) (time.Duration, bool) {
	count, unit, ok := parts(value)
	if !ok || unit != "h" {
		return 0, false
	}
	return time.Duration(count) * time.Hour, true
}

// GateDate is the date a calendar lead's window opens: the anchor stepped back
// by the span. Months and years step the way recurrence intervals do, so 1m
// before March 31 is February 28 in a common year.
func GateDate(anchor temporal.Date, span string) (temporal.Date, bool) {
	count, unit, ok := parts(span)
	if !ok || unit == "h" {
		return temporal.Date{}, false
	}
	switch unit {
	case "d":
		return anchor.AddDays(-count), true
	case "w":
		return anchor.AddDays(-7 * count), true
	case "m":
		return stepMonths(anchor, -count), true
	case "y":
		return stepMonths(anchor, -12*count), true
	}
	return temporal.Date{}, false
}

// stepMonths is Ruby Date#>>: the day of month clamps to the target month's
// length rather than overflowing into the next month.
func stepMonths(date temporal.Date, months int) temporal.Date {
	total := int(date.Month) - 1 + months
	year := date.Year + floorDiv(total, 12)
	month := time.Month(floorMod(total, 12) + 1)
	day := date.Day
	if length := daysIn(year, month); day > length {
		day = length
	}
	shifted, _ := temporal.NewDate(year, month, day)
	return shifted
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func floorDiv(value, divisor int) int {
	quotient := value / divisor
	if value%divisor != 0 && (value < 0) != (divisor < 0) {
		quotient--
	}
	return quotient
}

func floorMod(value, divisor int) int { return value - floorDiv(value, divisor)*divisor }

// GateInstant is the instant a clock lead's window opens: the anchor's own
// instant minus the duration. RAW — deliberately not rebuilt into a temporal
// value, which would re-resolve an ambiguous local time and could move the gate
// by an hour across a DST fall-back. An ALL-DAY anchor resolves to the first
// instant of its date, so `5h` before June 1 is 19:00 on May 31 local.
func GateInstant(anchor temporal.Value, span string, context temporal.Context) (time.Time, bool) {
	duration, ok := Duration(span)
	if !ok {
		return time.Time{}, false
	}
	instant, err := anchor.Instant(context)
	if err != nil {
		return time.Time{}, false
	}
	return instant.Add(-duration), true
}

// AnchorDate is the task's anchor: deadline first, available-from second — the
// same precedence recurrence completion rolls by, so a task never carries two
// notions of "its date".
func AnchorDate(deadline, scheduled temporal.Date) (temporal.Date, bool) {
	if !deadline.Zero() {
		return deadline, true
	}
	if !scheduled.Zero() {
		return scheduled, true
	}
	return temporal.Date{}, false
}

// Humanize renders a stored span: "3 weeks", "1 day". It echoes anything
// unparsable, matching Recur.humanize so the two fields read the same way.
func Humanize(span string) (string, bool) {
	if span == "" {
		return "", false
	}
	count, unit, ok := parts(span)
	if !ok {
		return span, true
	}
	name := unitNames[unit]
	if count != 1 {
		name += "s"
	}
	return strconv.Itoa(count) + " " + name, true
}
