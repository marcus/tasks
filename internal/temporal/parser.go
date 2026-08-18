package temporal

// The expression parser, lib/tasks/temporal_parser.rb: a friendly date with an
// optional trailing wall time, plus the flags that say how that wall time is
// to be interpreted.
//
//	tomorrow             an all-day value
//	today 5pm            a floating wall time
//	2026-07-20T17:00     the same, ISO spelling
//	fri noon             noon and midnight are spellings, not numbers
//
// A bare digit is deliberately NOT a time: "fri 5" is refused rather than
// stored as 05:00, because a user who meant five o'clock has three unambiguous
// ways to say so and a user who meant something else would never find out.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/timezones"
)

// timeToken is TemporalParser::TIME_TOKEN. Minutes may be omitted only when a
// meridiem disambiguates.
const timeToken = `(?:noon|midnight|(?:[01]?\d|2[0-3]):[0-5]\d(?:am|pm)?|(?:1[0-2]|0?[1-9])(?:am|pm))`

var (
	trailingTime = regexp.MustCompile(`(?i)\A(.+?)(?:[ T])(` + timeToken + `)\z`)
	atSeparator  = regexp.MustCompile(`(?i)\s+at\s+`)
	clockToken   = regexp.MustCompile(`\A(\d{1,2})(?::(\d{2}))?(am|pm)?\z`)
)

// ErrNotADate is what a caller gets when the expression names no date at all —
// distinct from an expression that names an impossible one.
var ErrNotADate = errors.New("could not understand that date")

// ParseOptions carries the flags the CLI's --timezone/--floating/--fold set.
type ParseOptions struct {
	// Today is the calendar day relative expressions are measured from.
	Today Date
	// Order resolves ambiguous numeric dates.
	Order Order
	// Timezone pins the wall time to a zone. Mutually exclusive with Floating.
	Timezone string
	// Floating keeps the wall time zoneless, so it means the same clock
	// reading wherever it is read.
	Floating bool
	// Fold picks the second instant of a DST fall-back overlap.
	Fold int
	// FoldSpecified distinguishes an explicit earlier choice from the default
	// zero value. Clock-relative durations reject either fold modifier because
	// their computed instant already selects the side of an overlap.
	FoldSpecified bool
	// Context, when supplied, resolves a timed value immediately so an
	// impossible local time is refused at parse rather than at read.
	Context *Context
}

// ParseExpression reads a date expression with an optional trailing time.
//
// The three outcomes are distinct on purpose: a value, a refusal naming an
// unusable combination of flags or an impossible time (error), and "that is not
// a date" (ErrNotADate), which the CLI reports differently from a flag mistake.
func ParseExpression(expression string, options ParseOptions) (Value, error) {
	input := strings.TrimSpace(expression)
	if input == "" {
		return Value{}, ErrNotADate
	}
	if options.Timezone != "" && options.Floating {
		return Value{}, errors.New("--timezone and --floating are mutually exclusive")
	}
	if options.Fold != 0 && options.Fold != 1 {
		return Value{}, errors.New("fold must be 0 or 1")
	}
	if span, ok := parseRelativeSpan(strings.ToLower(input)); ok && span.clock() {
		return parseClockRelative(span, options)
	}

	dateText, local, err := splitExpression(input)
	if err != nil {
		return Value{}, err
	}
	date, ok := ParseWhen(dateText, options.Today, options.Order)
	if !ok {
		return Value{}, ErrNotADate
	}
	if local == "" && (options.Timezone != "" || options.Floating || options.FoldSpecified || options.Fold != 0) {
		return Value{}, errors.New("a time is required with --timezone, --floating, or --fold")
	}

	zone := options.Timezone
	if options.Floating {
		zone = ""
	}
	value, err := NewValue(date, local, zone, options.Fold, true)
	if err != nil {
		return Value{}, err
	}
	if local != "" && options.Context != nil {
		if _, err := value.Instant(*options.Context); err != nil {
			return Value{}, err
		}
	}
	return value, nil
}

// parseClockRelative resolves seconds/minutes/hours from the caller's pinned
// instant. Storage has minute precision, so a target between minute boundaries
// is rounded up: the stored boundary is never earlier than the duration asked
// for.
func parseClockRelative(span relativeSpan, options ParseOptions) (Value, error) {
	if options.Context == nil || options.Context.Timezone == nil {
		return Value{}, errors.New("a current time context is required for relative seconds, minutes, or hours")
	}
	if options.FoldSpecified || options.Fold != 0 {
		return Value{}, errors.New("--fold is not applicable to clock-relative input; the duration already selects an exact instant")
	}
	unit := time.Second
	switch span.unit {
	case "minute":
		unit = time.Minute
	case "hour":
		unit = time.Hour
	}
	if int64(span.count) > int64((time.Duration(1<<63-1))/unit) {
		return Value{}, errors.New("relative duration is too large")
	}
	target := options.Context.Now.Add(time.Duration(span.count) * unit).UTC()
	if target.Second() != 0 || target.Nanosecond() != 0 {
		target = target.Truncate(time.Minute).Add(time.Minute)
	}

	location := options.Context.Timezone
	if options.Timezone != "" {
		var err error
		location, err = timezones.Load(options.Timezone)
		if err != nil {
			return Value{}, err
		}
	}
	local := target.In(location)
	fold := 0
	instants := timezones.InstantsFor(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), location)
	if len(instants) > 1 && instants[len(instants)-1].Equal(target) {
		fold = 1
	}
	date := DateOf(local)
	if date.Year < 1 || date.Year > 9999 {
		return Value{}, errors.New("relative duration is outside the supported date range")
	}
	localTime := fmt.Sprintf("%02d:%02d", local.Hour(), local.Minute())
	value, err := NewValue(date, localTime, options.Timezone, fold, true)
	if err != nil {
		return Value{}, err
	}
	if _, err := value.Instant(*options.Context); err != nil {
		return Value{}, err
	}
	return value, nil
}

// splitExpression peels a trailing wall time off the expression, normalizing
// "at" out of the way first so "tomorrow at 09:30" and "tomorrow 09:30" are one
// input.
func splitExpression(input string) (string, string, error) {
	normalized := replaceFirst(atSeparator, input, " ")
	match := trailingTime.FindStringSubmatch(normalized)
	if match == nil {
		return normalized, "", nil
	}
	local, err := NormalizeTime(match[2])
	if err != nil {
		return "", "", err
	}
	return match[1], local, nil
}

// replaceFirst is Ruby String#sub: the FIRST match only, where Go's
// ReplaceAllString would take every one.
func replaceFirst(pattern *regexp.Regexp, value, replacement string) string {
	location := pattern.FindStringIndex(value)
	if location == nil {
		return value
	}
	return value[:location[0]] + replacement + value[location[1]:]
}

// NormalizeTime turns a written time into the stored HH:MM spelling. "noon" and
// "midnight" are spellings of 12:00 and 00:00; a meridiem folds a 12-hour
// reading into 24.
func NormalizeTime(token string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(token))
	switch value {
	case "noon":
		return "12:00", nil
	case "midnight":
		return "00:00", nil
	}
	match := clockToken.FindStringSubmatch(value)
	if match == nil {
		return "", fmt.Errorf("invalid time %q", token)
	}
	hour, err := strconv.Atoi(match[1])
	if err != nil {
		return "", fmt.Errorf("invalid time %q", token)
	}
	minute := 0
	if match[2] != "" {
		minute, err = strconv.Atoi(match[2])
		if err != nil {
			return "", fmt.Errorf("invalid time %q", token)
		}
	}
	if meridiem := match[3]; meridiem != "" {
		if hour < 1 || hour > 12 {
			return "", fmt.Errorf("invalid 12-hour time %q", token)
		}
		hour %= 12
		if meridiem == "pm" {
			hour += 12
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", fmt.Errorf("invalid time %q", token)
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}
