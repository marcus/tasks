// Package recur parses and projects interval repeater cookies.
package recur

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	cookie    = regexp.MustCompile(`^(\.\+|\+\+|\+)([1-9][0-9]*)([dwmy])$`)
	countUnit = regexp.MustCompile(`^([0-9]+)([a-z]+)$`)
)

var words = map[string]interval{
	"daily":       {count: 1, unit: "d"},
	"weekly":      {count: 1, unit: "w"},
	"monthly":     {count: 1, unit: "m"},
	"yearly":      {count: 1, unit: "y"},
	"annually":    {count: 1, unit: "y"},
	"biweekly":    {count: 2, unit: "w"},
	"fortnightly": {count: 2, unit: "w"},
	"quarterly":   {count: 3, unit: "m"},
}

var units = map[string]string{
	"day": "d", "days": "d", "week": "w", "weeks": "w",
	"month": "m", "months": "m", "year": "y", "years": "y",
	"d": "d", "w": "w", "m": "m", "y": "y",
}

var bareUnits = map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}

var unitNames = map[string]string{"d": "day", "w": "week", "m": "month", "y": "year"}

type interval struct {
	count int
	unit  string
}

// Result is Parse's outcome. Canonical is either a stored cookie or "off".
// Error is intentionally an observable Ruby-compatible rejection category.
type Result struct {
	Canonical string
	Error     string
}

// Parse normalizes an interval cookie or friendly interval spelling. A bare
// interval uses defaultPrefix, which is normally ".+". Calendar schedules are
// deliberately not accepted here: that grammar belongs to the next slice.
func Parse(input, defaultPrefix string) Result {
	raw := rubyStrip(input)
	if raw == "" {
		return Result{Error: "no schedule given"}
	}
	s := strings.ToLower(raw)
	if isOff(s) {
		return Result{Canonical: "off"}
	}
	if match := cookie.FindStringSubmatch(s); match != nil {
		return Result{Canonical: match[1] + match[2] + match[3]}
	}

	if prefix, value, ok := parseFriendlyInterval(s); ok {
		return Result{Canonical: prefixOrDefault(prefix, defaultPrefix) + strconv.Itoa(value.count) + value.unit}
	}
	return Result{Error: fmt.Sprintf("unrecognized schedule: %q", raw)}
}

// Cookie reports whether value is an exactly canonical interval cookie.
func Cookie(value string) bool { return cookie.MatchString(rubyStrip(value)) }

// Humanize renders an interval cookie as Ruby does. It returns value unchanged
// when it is not an interval cookie, allowing the calendar package to own its
// own rendering later.
func Humanize(value string) string {
	s := rubyStrip(value)
	match := cookie.FindStringSubmatch(s)
	if match == nil {
		return s
	}
	n, _ := strconv.Atoi(match[2])
	name := unitNames[match[3]]
	every := "every " + name
	if n != 1 {
		every = fmt.Sprintf("every %d %ss", n, name)
	}
	switch match[1] {
	case ".+":
		return every + " from completion"
	case "+":
		return every + " from the scheduled date"
	default:
		return every + " from the scheduled date (catching up)"
	}
}

// NextDate projects an interval cookie from its stored date and today's date.
// A catch-up cookie intentionally may land on today, matching Ruby's date-only
// projection (the temporal completion path decides whether a timed stamp has
// already passed today).
func NextDate(value string, from, today time.Time) (time.Time, error) {
	match := cookie.FindStringSubmatch(rubyStrip(value))
	if match == nil {
		return time.Time{}, fmt.Errorf("not a repeater cookie: %q", value)
	}
	n, err := strconv.Atoi(match[2])
	if err != nil {
		return time.Time{}, fmt.Errorf("not a repeater cookie: %q", value)
	}
	from, today = dateOnly(from), dateOnly(today)
	switch match[1] {
	case ".+":
		return Step(today, n, match[3]), nil
	case "+":
		return Step(from, n, match[3]), nil
	default:
		d := Step(from, n, match[3])
		for d.Before(today) {
			d = Step(d, n, match[3])
		}
		return d, nil
	}
}

// Step advances a civil date. Month and year units clamp overflowing days;
// time.Time.AddDate cannot be used because it normalizes Jan 31 + one month
// into March instead of Ruby Date#>>'s February clamp.
func Step(date time.Time, count int, unit string) time.Time {
	date = dateOnly(date)
	switch unit {
	case "d":
		return date.AddDate(0, 0, count)
	case "w":
		return date.AddDate(0, 0, 7*count)
	case "m":
		return addMonthsClamped(date, count)
	case "y":
		return addMonthsClamped(date, 12*count)
	default:
		return time.Time{}
	}
}

func parseFriendlyInterval(value string) (string, interval, bool) {
	if word, ok := words[value]; ok {
		return "", word, true
	}
	parts := strings.Fields(value)
	if len(parts) > 0 && parts[0] == "every" {
		parts = parts[1:]
	}
	if match := countUnit.FindStringSubmatch(strings.Join(parts, "")); match != nil {
		n, err := strconv.Atoi(match[1])
		unit, ok := units[match[2]]
		if err == nil && n > 0 && ok {
			return "", interval{count: n, unit: unit}, true
		}
		return "", interval{}, false
	}
	if len(parts) == 1 {
		if unit, ok := bareUnits[parts[0]]; ok {
			return "", interval{count: 1, unit: unit}, true
		}
		return "", interval{}, false
	}
	if len(parts) != 2 {
		return "", interval{}, false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 {
		return "", interval{}, false
	}
	unit, ok := units[parts[1]]
	if !ok {
		return "", interval{}, false
	}
	return "", interval{count: n, unit: unit}, true
}

func prefixOrDefault(prefix, defaultPrefix string) string {
	if prefix != "" {
		return prefix
	}
	if defaultPrefix == "+" || defaultPrefix == "++" || defaultPrefix == ".+" {
		return defaultPrefix
	}
	return ".+"
}

func isOff(value string) bool {
	switch value {
	case "off", "none", "never", "clear", "no", "stop":
		return true
	default:
		return false
	}
}

func rubyStrip(value string) string {
	return strings.Trim(value, "\x00\x09\x0a\x0b\x0c\x0d ")
}

func dateOnly(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func addMonthsClamped(date time.Time, months int) time.Time {
	year, month, day := date.Date()
	monthIndex := int(month) - 1 + months
	year += monthIndex / 12
	monthIndex %= 12
	if monthIndex < 0 {
		monthIndex += 12
		year--
	}
	targetMonth := time.Month(monthIndex + 1)
	if max := daysInMonth(year, targetMonth); day > max {
		day = max
	}
	return time.Date(year, targetMonth, day, 0, 0, 0, 0, time.UTC)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
