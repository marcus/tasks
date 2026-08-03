package check

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The recurrence grammar itself is a later campaign. What the linter needs —
// and all it is allowed to need — is Recur.cookie?: the boolean "is this
// already a stored recurrence value". Ruby's Check calls that predicate and
// discards every reason Recur produced, so a port that reproduced the richer
// per-reason diagnostics here would diverge on the surface it is porting.
// Recognition, not explanation, is what lives in this file.
//
// A stored calendar schedule is valid only when it is spelled exactly as
// Recur.canonical_calendar would spell it, so recognition is "reparse, then
// re-render, then compare bytes" — the same rule Recur.schedule applies.

var (
	// COOKIE: an interval cookie carries an explicit prefix and a positive count.
	recurCookiePattern = regexp.MustCompile(`\A(\.\+|\+\+|\+)([1-9]\d*)([dwmy])\z`)
	// CALENDAR. Recur gates on CALENDAR_SHAPE first, but every string this
	// pattern matches also matches the shape, so the gate is redundant here.
	recurCalendarPattern = regexp.MustCompile(`\A(\.\+|\+\+|\+)?(\d+)?([wmy]):(.+)\z`)
	recurMonthDayPattern = regexp.MustCompile(`\A\d{1,2}\z`)
	recurOrdinalPattern  = regexp.MustCompile(`\A(\d+|last)([a-z]+)\z`)
	recurYearlyDate      = regexp.MustCompile(`\A(\d{1,2})-(\d{1,2})\z`)
	recurYearlyOrdinal   = regexp.MustCompile(`\A(\d{1,2}):(\d+|last)([a-z]+)\z`)
)

var recurDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var recurDayAliases = buildRecurDayAliases()

func buildRecurDayAliases() map[string]string {
	full := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	aliases := map[string]string{}
	for index, name := range full {
		abbreviation := recurDays[index]
		for _, key := range []string{name, name + "s", abbreviation, abbreviation + "s"} {
			aliases[key] = abbreviation
		}
	}
	aliases["tues"] = "tue"
	aliases["thur"] = "thu"
	aliases["thurs"] = "thu"
	aliases["weds"] = "wed"
	return aliases
}

// recurCookie is Recur.cookie?: true when the value is already a stored
// recurrence value. It strips like Ruby does; check_task applies its own
// padding guard on top, because a stored value is the exact canonical
// spelling while Recur tolerates surrounding whitespace on input.
func recurCookie(value string) bool {
	stripped := rubyStrip(value)
	return recurCookiePattern.MatchString(stripped) || recurCalendarSchedule(stripped)
}

// recurCalendarSchedule is Recur.schedule reduced to its verdict: the value
// parses as a calendar schedule and canonical_string reproduces it byte for
// byte.
func recurCalendarSchedule(value string) bool {
	match := recurCalendarPattern.FindStringSubmatch(value)
	if match == nil {
		return false
	}
	prefix, digits, unit, body := match[1], match[2], match[3], match[4]
	// An interval prefix cannot lead a calendar schedule: the schedule already
	// advances past today.
	if prefix == ".+" || prefix == "++" {
		return false
	}
	interval := 1
	if digits != "" {
		parsed, err := strconv.Atoi(digits)
		if err != nil {
			return false
		}
		interval = parsed
	}
	if interval < 1 {
		return false
	}
	count := ""
	if interval != 1 {
		count = strconv.Itoa(interval)
	}

	var canonical string
	var ok bool
	switch unit {
	case "w":
		canonical, ok = recurCanonicalWeekly(prefix, count, body)
	case "m":
		canonical, ok = recurCanonicalMonthly(prefix, count, body)
	default:
		canonical, ok = recurCanonicalYearly(prefix, count, body)
	}
	return ok && canonical == value
}

func recurCanonicalWeekly(prefix, count, body string) (string, bool) {
	parts := strings.Split(body, ",")
	selected := map[string]bool{}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		day, known := recurDayAliases[part]
		if !known {
			return "", false
		}
		selected[day] = true
	}
	days := make([]string, 0, len(selected))
	for _, day := range recurDays {
		if selected[day] {
			days = append(days, day)
		}
	}
	return prefix + count + "w:" + strings.Join(days, ","), true
}

// recurMonthlySpec is one monthly rule: a day of the month, the last day, or
// an ordinal weekday. sortKey mirrors Recur.spec_key, which both orders the
// rules and defines which two rules are the same rule.
type recurMonthlySpec struct {
	text    string
	sortKey [3]int
}

func recurCanonicalMonthly(prefix, count, body string) (string, bool) {
	parts := strings.Split(body, ",")
	specs := make([]recurMonthlySpec, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		spec, ok := recurMonthlyRule(part)
		if !ok {
			return "", false
		}
		specs = append(specs, spec)
	}
	sort.SliceStable(specs, func(left, right int) bool {
		return recurSortKeyLess(specs[left].sortKey, specs[right].sortKey)
	})
	rendered := make([]string, 0, len(specs))
	var previous [3]int
	for index, spec := range specs {
		if index > 0 && spec.sortKey == previous {
			continue
		}
		previous = spec.sortKey
		rendered = append(rendered, spec.text)
	}
	return prefix + count + "m:" + strings.Join(rendered, ","), true
}

func recurMonthlyRule(part string) (recurMonthlySpec, bool) {
	if recurMonthDayPattern.MatchString(part) {
		day, err := strconv.Atoi(part)
		if err != nil || day < 1 || day > 31 {
			return recurMonthlySpec{}, false
		}
		return recurMonthlySpec{text: strconv.Itoa(day), sortKey: [3]int{0, day, 0}}, true
	}
	if part == "last" {
		return recurMonthlySpec{text: "last", sortKey: [3]int{1, 0, 0}}, true
	}
	match := recurOrdinalPattern.FindStringSubmatch(part)
	if match == nil {
		return recurMonthlySpec{}, false
	}
	text, key, ok := recurOrdinalWeekday(match[1], match[2])
	if !ok {
		return recurMonthlySpec{}, false
	}
	return recurMonthlySpec{text: text, sortKey: key}, true
}

// recurOrdinalWeekday renders "3thu" / "lastfri" and returns its sort key.
// Recur files a `last` ordinal at position 6, after the fifth week.
func recurOrdinalWeekday(ordinal, name string) (string, [3]int, bool) {
	weekday, known := recurDayAliases[name]
	if !known {
		return "", [3]int{}, false
	}
	if ordinal == "last" {
		return "last" + weekday, [3]int{2, 6, recurDayIndex(weekday)}, true
	}
	position, err := strconv.Atoi(ordinal)
	if err != nil || position < 1 || position > 5 {
		return "", [3]int{}, false
	}
	return strconv.Itoa(position) + weekday, [3]int{2, position, recurDayIndex(weekday)}, true
}

func recurCanonicalYearly(prefix, count, body string) (string, bool) {
	if match := recurYearlyDate.FindStringSubmatch(body); match != nil {
		month, monthErr := strconv.Atoi(match[1])
		day, dayErr := strconv.Atoi(match[2])
		if monthErr != nil || dayErr != nil || month < 1 || month > 12 || !validDate(2024, month, day) {
			return "", false
		}
		return fmt.Sprintf("%s%sy:%02d-%02d", prefix, count, month, day), true
	}
	match := recurYearlyOrdinal.FindStringSubmatch(body)
	if match == nil {
		return "", false
	}
	month, err := strconv.Atoi(match[1])
	if err != nil || month < 1 || month > 12 {
		return "", false
	}
	text, _, ok := recurOrdinalWeekday(match[2], match[3])
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s%sy:%02d:%s", prefix, count, month, text), true
}

func recurDayIndex(day string) int {
	for index, name := range recurDays {
		if name == day {
			return index
		}
	}
	return len(recurDays)
}

func recurSortKeyLess(left, right [3]int) bool {
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return false
}
