package temporal

// Friendly date input, the Go counterpart of lib/tasks/dates.rb. Accepts:
//
//	today · tomorrow · next week · next month · next year
//	+3 · in 3 days · in 2 weeks · in 6 months · in a year
//	fri/friday · next fri
//	07-15 · 7/15 · 7/15/2026 · 2026-07-15 · 2026/07/15
//	aug 1 · august 1st · 1 aug 2026 · aug 1, 2026
//
// Every numeric slash/dash form except the YYYY-first ISO one is ambiguous
// between month-first and day-first (a year, even a four-digit one, says
// nothing about which number is which), so the caller supplies an Order.
//
// Ruby installs that choice in a process-wide global via `Dates.configure!`.
// Go does not: the order is a parameter, because Config already resolves it
// and threading it is what makes this function testable without a global to
// reset. `OrderNamed` keeps the config value's degrade-don't-crash behavior.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Order says whether the first number of an ambiguous numeric date is the
// month or the day.
type Order int

const (
	// MDY reads 8/1 as August 1st. It is the default, as in Ruby.
	MDY Order = iota
	// DMY reads 8/1 as the 1st of August's neighbour — the 8th of January.
	DMY
)

// OrderNamed reads a configured spelling. Anything but "mdy"/"dmy" degrades to
// MDY rather than failing, so a bad config value does not break date input.
func OrderNamed(name string) Order {
	if strings.EqualFold(strings.TrimSpace(name), "dmy") {
		return DMY
	}
	return MDY
}

// String is the config spelling of an order.
func (o Order) String() string {
	if o == DMY {
		return "dmy"
	}
	return "mdy"
}

var weekdayNames = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

var monthNames = []string{"january", "february", "march", "april", "may", "june",
	"july", "august", "september", "october", "november", "december"}

var (
	plusDays       = regexp.MustCompile(`\A\+(\d+)\z`)
	inUnits        = regexp.MustCompile(`\Ain (\d+|an?) (day|week|month|year)s?\z`)
	monthDayYear   = regexp.MustCompile(`\A([a-z]+)\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:\s+(\d{4}))?\z`)
	dayMonthYear   = regexp.MustCompile(`\A(\d{1,2})(?:st|nd|rd|th)?\s+([a-z]+)\.?(?:\s+(\d{4}))?\z`)
	isoLike       = regexp.MustCompile(`\A(\d{4})[-/](\d{1,2})[-/](\d{1,2})\z`)
	numericWithYr = regexp.MustCompile(`\A(\d{1,2})[-/](\d{1,2})[-/](\d{2}|\d{4})\z`)
	bareMonthDay  = regexp.MustCompile(`\A(\d{1,2})[-/](\d{1,2})\z`)
)

// ParseWhen reads a friendly date expression relative to `today`. It reports
// ok=false for anything it cannot understand, including a well-formed spelling
// of a day the calendar does not have.
func ParseWhen(input string, today Date, order Order) (Date, bool) {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return Date{}, false
	}
	if date, ok := parseKeyword(s, today); ok {
		return date, true
	}
	if date, ok := parseRelative(s, today); ok {
		return date, true
	}
	if date, ok := parseWeekday(s, today); ok {
		return date, true
	}
	if date, ok := parseMonthName(s, today); ok {
		return date, true
	}
	return parseNumeric(s, today, order)
}

func parseKeyword(s string, today Date) (Date, bool) {
	switch s {
	case "today":
		return today, true
	case "tomorrow":
		return today.AddDays(1), true
	case "next week":
		return today.AddDays(7), true
	case "next month":
		return today.AddMonths(1), true
	case "next year":
		return today.AddMonths(12), true
	}
	return Date{}, false
}

// parseRelative reads "+3" (days from today) and "in 3 days/weeks/months/years"
// (also "a"/"an" for one). Months and years step by calendar, which clamps an
// out-of-range day to the end of the target month.
func parseRelative(s string, today Date) (Date, bool) {
	if match := plusDays.FindStringSubmatch(s); match != nil {
		days, err := strconv.Atoi(match[1])
		if err != nil {
			return Date{}, false
		}
		return today.AddDays(days), true
	}
	match := inUnits.FindStringSubmatch(s)
	if match == nil {
		return Date{}, false
	}
	count := 1
	if match[1] != "a" && match[1] != "an" {
		parsed, err := strconv.Atoi(match[1])
		if err != nil {
			return Date{}, false
		}
		count = parsed
	}
	switch match[2] {
	case "day":
		return today.AddDays(count), true
	case "week":
		return today.AddDays(count * 7), true
	case "month":
		return today.AddMonths(count), true
	default:
		return today.AddMonths(count * 12), true
	}
}

// parseWeekday reads a bare weekday name ("fri", "friday") or "next <weekday>"
// — a convenience alias, not an extra week's skip. The same weekday as today
// rolls to next week rather than returning today.
func parseWeekday(s string, today Date) (Date, bool) {
	token := strings.TrimPrefix(s, "next ")
	if len(token) < 3 {
		return Date{}, false
	}
	index := -1
	for position, name := range weekdayNames {
		if strings.HasPrefix(name, token) {
			index = position
			break
		}
	}
	if index < 0 {
		return Date{}, false
	}
	delta := (index - int(today.Weekday())) % 7
	if delta < 0 {
		delta += 7
	}
	if delta == 0 {
		delta = 7
	}
	return today.AddDays(delta), true
}

// parseMonthName reads month-name forms in either order: "aug 1", "august 1st",
// "1 aug 2026", "aug 1, 2026". A comma is normalized to a space so "aug 1,2026"
// still lines the day and year up.
func parseMonthName(s string, today Date) (Date, bool) {
	cleaned := strings.ReplaceAll(s, ",", " ")
	if match := monthDayYear.FindStringSubmatch(cleaned); match != nil {
		if month, ok := monthIndex(match[1]); ok {
			return buildMonthDate(today, month, match[2], match[3])
		}
	}
	if match := dayMonthYear.FindStringSubmatch(cleaned); match != nil {
		if month, ok := monthIndex(match[2]); ok {
			return buildMonthDate(today, month, match[1], match[3])
		}
	}
	return Date{}, false
}

// monthIndex is a first-match prefix lookup over the month names, requiring at
// least three characters. Every standard three-letter English abbreviation
// happens to be a unique prefix (mar/may and jun/jul do not collide), so a bare
// abbreviation always resolves to the month a human means — while "ju" is
// genuinely ambiguous and refused.
func monthIndex(token string) (time.Month, bool) {
	if len(token) < 3 {
		return 0, false
	}
	for position, name := range monthNames {
		if strings.HasPrefix(name, token) {
			return time.Month(position + 1), true
		}
	}
	return 0, false
}

// buildMonthDate resolves a month-name form. A bare year-less month/day rolls
// to next year if already past — but only when the rolled date actually exists,
// so "feb 29" asked for after this year's Feb 29 is refused rather than
// silently landing on a day the user did not type.
func buildMonthDate(today Date, month time.Month, dayText, yearText string) (Date, bool) {
	day, err := strconv.Atoi(dayText)
	if err != nil {
		return Date{}, false
	}
	if yearText != "" {
		year, err := strconv.Atoi(yearText)
		if err != nil {
			return Date{}, false
		}
		return NewDate(year, month, day)
	}
	return rollForward(today, month, day)
}

// rollForward resolves a year-less month/day to the next time it comes round,
// searching this year and then the next. It never CLAMPS: "feb 29" resolves to
// a real February 29 or to nothing at all, because landing on the 28th would be
// a day the user did not type.
//
// Ruby refuses a year-less "feb 29" typed in any non-leap year, because it
// builds this year's date before deciding whether to roll and lets the
// resulting Date::Error fall out as nil. That is incidental rather than
// intended — a person typing "feb 29" in 2023 means the 2024 one — so the roll
// here is tried in both cases. The one-year horizon is kept: asked AFTER a leap
// February, the next real February 29 is four years out, which is not what
// anybody typing a bare month-day means, so it is still refused.
func rollForward(today Date, month time.Month, day int) (Date, bool) {
	if candidate, ok := NewDate(today.Year, month, day); ok && !candidate.Before(today) {
		return candidate, true
	}
	return NewDate(today.Year+1, month, day)
}

// parseNumeric reads the all-numeric forms: YYYY-MM-DD (or YYYY/MM/DD, which is
// unambiguous), MM/DD/YYYY (or /YY), and bare MM/DD. The latter two are
// ambiguous regardless of year length and are resolved by `order`.
func parseNumeric(s string, today Date, order Order) (Date, bool) {
	if match := isoLike.FindStringSubmatch(s); match != nil {
		return newDateFromText(match[1], match[2], match[3])
	}
	if match := numericWithYr.FindStringSubmatch(s); match != nil {
		monthText, dayText := orderParts(match[1], match[2], order)
		year, err := strconv.Atoi(match[3])
		if err != nil {
			return Date{}, false
		}
		// A two-digit year is always in the 2000s: every caller here is
		// scheduling a task, so a past century is never the intended meaning.
		if year < 100 {
			year += 2000
		}
		month, day, ok := monthDayInts(monthText, dayText)
		if !ok {
			return Date{}, false
		}
		return NewDate(year, month, day)
	}
	if match := bareMonthDay.FindStringSubmatch(s); match != nil {
		monthText, dayText := orderParts(match[1], match[2], order)
		month, day, ok := monthDayInts(monthText, dayText)
		if !ok {
			return Date{}, false
		}
		// A bare month-day in the past rolls forward a year — the same
		// refuse-don't-clamp rule the month-name form uses, so "2/29" and
		// "feb 29" behave identically.
		return rollForward(today, month, day)
	}
	return Date{}, false
}

func orderParts(first, second string, order Order) (string, string) {
	if order == DMY {
		return second, first
	}
	return first, second
}

func monthDayInts(monthText, dayText string) (time.Month, int, bool) {
	month, err := strconv.Atoi(monthText)
	if err != nil {
		return 0, 0, false
	}
	day, err := strconv.Atoi(dayText)
	if err != nil {
		return 0, 0, false
	}
	return time.Month(month), day, true
}

func newDateFromText(yearText, monthText, dayText string) (Date, bool) {
	year, err := strconv.Atoi(yearText)
	if err != nil {
		return Date{}, false
	}
	month, day, ok := monthDayInts(monthText, dayText)
	if !ok {
		return Date{}, false
	}
	return NewDate(year, month, day)
}
