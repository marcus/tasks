package temporal

// Friendly date input, the Go counterpart of lib/tasks/dates.rb. Accepts:
//
//	today · tomorrow · next week · next month · next year
//	+3 · two weeks · in two weeks · two weeks from now · 2d · 2w · 2m · 2y
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
	plusDays      = regexp.MustCompile(`\A\+(\d+)\z`)
	shortRelative = regexp.MustCompile(`\A(?:in )?(\d+)([dwmy])(?: from now)?\z`)
	wordRelative  = regexp.MustCompile(`\A(?:in )?(.+?) (second|minute|hour|day|week|month|year)s?(?: from now)?\z`)
	monthDayYear  = regexp.MustCompile(`\A([a-z]+)\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:\s+(\d{4}))?\z`)
	dayMonthYear  = regexp.MustCompile(`\A(\d{1,2})(?:st|nd|rd|th)?\s+([a-z]+)\.?(?:\s+(\d{4}))?\z`)
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

// parseRelative reads calendar-relative phrases and compact day/week/month/year
// spellings. Clock-relative seconds/minutes/hours are recognized by the shared
// expression parser, where a current instant is available.
func parseRelative(s string, today Date) (Date, bool) {
	if match := plusDays.FindStringSubmatch(s); match != nil {
		days, err := strconv.Atoi(match[1])
		if err != nil {
			return Date{}, false
		}
		return addRelativeDays(today, days)
	}
	span, ok := parseRelativeSpan(s)
	if !ok || span.clock() {
		return Date{}, false
	}
	switch span.unit {
	case "day":
		return addRelativeDays(today, span.count)
	case "week":
		if span.count > relativeDayLimit(today)/7 {
			return Date{}, false
		}
		return addRelativeDays(today, span.count*7)
	case "month":
		return addRelativeMonths(today, span.count)
	default:
		if span.count > (9999 - today.Year) {
			return Date{}, false
		}
		return addRelativeMonths(today, span.count*12)
	}
}

func relativeDayLimit(today Date) int { return (10000 - today.Year) * 366 }

func addRelativeDays(today Date, count int) (Date, bool) {
	if count < 0 || count > relativeDayLimit(today) {
		return Date{}, false
	}
	date := today.AddDays(count)
	return date, date.Year >= 1 && date.Year <= 9999
}

func addRelativeMonths(today Date, count int) (Date, bool) {
	limit := (9999-today.Year)*12 + (12 - int(today.Month))
	if count < 0 || count > limit {
		return Date{}, false
	}
	return today.AddMonths(count), true
}

type relativeSpan struct {
	count int
	unit  string
}

func (s relativeSpan) clock() bool {
	return s.unit == "second" || s.unit == "minute" || s.unit == "hour"
}

// parseRelativeSpan is the one grammar used for both calendar and clock
// relative input. A compact `m` deliberately means month; clock units have no
// compact spelling, keeping `2m` aligned with the requested 2d/2w/2m/2y family.
func parseRelativeSpan(s string) (relativeSpan, bool) {
	if match := shortRelative.FindStringSubmatch(s); match != nil {
		count, err := strconv.Atoi(match[1])
		if err != nil {
			return relativeSpan{}, false
		}
		units := map[string]string{"d": "day", "w": "week", "m": "month", "y": "year"}
		return relativeSpan{count: count, unit: units[match[2]]}, true
	}
	match := wordRelative.FindStringSubmatch(s)
	if match == nil {
		return relativeSpan{}, false
	}
	count, ok := parseCount(match[1])
	if !ok {
		return relativeSpan{}, false
	}
	return relativeSpan{count: count, unit: match[2]}, true
}

var countWords = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14,
	"fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40,
	"fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

// parseCount accepts digits plus well-formed English number words through
// 999,999. Larger values remain available as digits; the word grammar stays
// deliberately small and predictable rather than trying to be prose parsing.
func parseCount(input string) (int, bool) {
	if count, err := strconv.Atoi(input); err == nil && count >= 0 {
		return count, true
	}
	words := strings.Fields(strings.ReplaceAll(input, "-", " "))
	if len(words) == 0 {
		return 0, false
	}
	if len(words) == 1 && (words[0] == "a" || words[0] == "an") {
		return 1, true
	}
	thousand := -1
	for index, word := range words {
		if word == "thousand" {
			if thousand >= 0 {
				return 0, false
			}
			thousand = index
		}
	}
	if thousand < 0 {
		return parseCountGroup(words)
	}
	if thousand == 0 {
		return 0, false
	}
	high, ok := parseCountGroup(words[:thousand])
	if !ok || high == 0 {
		return 0, false
	}
	if thousand == len(words)-1 {
		return high * 1000, true
	}
	lowWords := words[thousand+1:]
	if lowWords[0] == "and" {
		lowWords = lowWords[1:]
		if len(lowWords) == 0 {
			return 0, false
		}
	}
	low, ok := parseCountGroup(lowWords)
	if !ok || low == 0 {
		return 0, false
	}
	return high*1000 + low, true
}

func parseCountGroup(words []string) (int, bool) {
	if len(words) == 0 {
		return 0, false
	}
	total := 0
	if len(words) >= 2 && words[1] == "hundred" {
		leading, ok := countWords[words[0]]
		if !ok || leading < 1 || leading > 9 {
			return 0, false
		}
		total = leading * 100
		words = words[2:]
		if len(words) > 0 && words[0] == "and" {
			words = words[1:]
		}
		if len(words) == 0 {
			return total, true
		}
	}
	under, ok := parseCountUnderHundred(words)
	return total + under, ok
}

func parseCountUnderHundred(words []string) (int, bool) {
	if len(words) == 1 {
		value, ok := countWords[words[0]]
		return value, ok
	}
	if len(words) != 2 {
		return 0, false
	}
	tens, tensOK := countWords[words[0]]
	ones, onesOK := countWords[words[1]]
	if !tensOK || tens < 20 || tens%10 != 0 || !onesOK || ones < 1 || ones > 9 {
		return 0, false
	}
	return tens + ones, true
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
