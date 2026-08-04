// Package recur parses and projects interval repeater cookies.
package recur

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var cookie = regexp.MustCompile(`^(\.\+|\+\+|\+)([1-9][0-9]*)([dwmy])$`)

var words = map[string]interval{
	"daily":       {count: big.NewInt(1), unit: "d"},
	"weekly":      {count: big.NewInt(1), unit: "w"},
	"monthly":     {count: big.NewInt(1), unit: "m"},
	"yearly":      {count: big.NewInt(1), unit: "y"},
	"annually":    {count: big.NewInt(1), unit: "y"},
	"biweekly":    {count: big.NewInt(2), unit: "w"},
	"fortnightly": {count: big.NewInt(2), unit: "w"},
	"quarterly":   {count: big.NewInt(3), unit: "m"},
}

var units = map[string]string{
	"day": "d", "days": "d", "week": "w", "weeks": "w",
	"month": "m", "months": "m", "year": "y", "years": "y",
	"d": "d", "w": "w", "m": "m", "y": "y",
}

var bareUnits = map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}

var unitNames = map[string]string{"d": "day", "w": "week", "m": "month", "y": "year"}

var filler = map[string]bool{
	"on": true, "of": true, "the": true, "each": true, "every": true,
	"a": true, "an": true, "in": true, "at": true, "and": true,
}

type interval struct {
	count *big.Int
	unit  string
}

// Result is Parse's outcome. Canonical is either a stored cookie or "off".
// Error is intentionally an observable Ruby-compatible rejection category.
type Result struct {
	Canonical string
	Error     string
}

// Parse normalizes any accepted spelling into the one canonical stored value,
// "off" to clear recurrence, or a refusal naming the reason.
//
//	".+1w" "+2d" "++1m"          passthrough (validated)
//	"weekly" "2w" "every 3 days" ".+…" — a bare interval takes defaultPrefix
//	"w:mon" "every monday"       "w:mon"
//	"off" / "none" / "never"     off
//
// defaultPrefix picks the semantics for a bare INTERVAL: ".+" (from completion)
// normally, "+" when the caller wants the date-anchored form. Calendar
// schedules carry their own prefix and ignore it — bare calendar input is
// catch-up.
func Parse(input, defaultPrefix string) Result {
	raw := rubyStrip(input)
	if raw == "" {
		return Result{Error: "no schedule given"}
	}
	s := rubyDowncase(raw)
	if isOff(s) {
		return Result{Canonical: "off"}
	}
	// A cookie's own prefix wins over defaultPrefix.
	if match := cookie.FindStringSubmatch(s); match != nil {
		return Result{Canonical: match[1] + match[2] + match[3]}
	}
	// Anything SHAPED like a calendar schedule reports its own reason rather
	// than falling through to natural-phrase parsing, which would explain the
	// wrong mistake. A natural phrase never contains a colon.
	if calendarShape.MatchString(s) {
		parsed, err := canonicalCalendar(s, raw)
		if err != nil {
			return Result{Error: err.Error()}
		}
		return built(parsed)
	}
	// Parsing reads the downcased form; rejections quote `raw`, so what the
	// caller sees echoed back is exactly what they typed.
	return parseNatural(s, defaultPrefix, raw)
}

// Cookie reports whether value is already a STORED recurrence value: an
// interval cookie or a calendar schedule, each in its exact canonical
// spelling. Both halves matter — a store holds one spelling per schedule, so
// "w:monday" is readable input and not a stored value.
func Cookie(value string) bool {
	stripped := rubyStrip(value)
	return cookie.MatchString(stripped) || Calendar(stripped)
}

// Humanize renders an interval cookie as Ruby does. It returns nil for blank
// input and the trimmed value when it is not an interval cookie, allowing the
// calendar package to own its own rendering later.
func Humanize(value string) *string {
	s := rubyStrip(value)
	if s == "" {
		return nil
	}
	match := cookie.FindStringSubmatch(s)
	if match == nil {
		if parsed, ok := parseSchedule(s); ok {
			rendered := humanizeCalendar(parsed)
			return &rendered
		}
		return &s
	}
	n, _ := new(big.Int).SetString(match[2], 10)
	name := unitNames[match[3]]
	every := "every " + name
	if n.Cmp(big.NewInt(1)) != 0 {
		every = fmt.Sprintf("every %s %ss", n, name)
	}
	switch match[1] {
	case ".+":
		result := every + " from completion"
		return &result
	case "+":
		result := every + " from the scheduled date"
		return &result
	default:
		result := every + " from the scheduled date (catching up)"
		return &result
	}
}

// NextDate projects a STORED value — an interval cookie or a calendar schedule
// — from its stamp's current date and today's date.
//
// A catch-up projection intentionally may land on today rather than past it:
// that is exactly where the completion path's own fast-forward stops, and a
// candidate landing on today can still be in the future by its end-of-day
// boundary. A date-only projection has no time to compare, so it must name the
// same day the write would produce.
func NextDate(value string, from, today CivilDate) (CivilDate, error) {
	s := rubyStrip(value)
	if match := cookie.FindStringSubmatch(s); match != nil {
		n, _ := new(big.Int).SetString(match[2], 10)
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

	parsed, ok := parseSchedule(s)
	if !ok {
		return CivilDate{}, fmt.Errorf("not a repeater cookie: %s", rubyInspect(value))
	}
	after := from
	if parsed.prefix != "+" && after.Before(today) {
		after = today
	}
	return occurrenceAfter(parsed, from, after)
}

// Occurrences is the next `count` dates a stored value fires on. Each landing
// becomes the anchor for the next, so an every-Nth series keeps its parity and
// a from-completion cookie keeps stepping.
func Occurrences(value string, from, today CivilDate, count int) ([]CivilDate, error) {
	dates := make([]CivilDate, 0, max(count, 0))
	cursorFrom, cursorToday := from, today
	for index := 0; index < count; index++ {
		date, err := NextDate(value, cursorFrom, cursorToday)
		if err != nil {
			return nil, err
		}
		dates = append(dates, date)
		cursorFrom, cursorToday = date, date
	}
	return dates, nil
}

// Explanation is the discoverability payload every surface renders: what was
// typed, what it stores as, how it reads, and when it next fires.
//
// Three shapes, and the middle one is why this is a struct rather than a pair:
//
//	understood and projected   Canonical, Human, Next
//	understood, never fires    Canonical, Human, empty Next, Error
//	not understood             Input and Error only
//
// Whether a schedule ever fires depends on the ANCHOR, not on the schedule
// alone, so a projection failure must still identify what was typed rather than
// masquerading as a parse error.
type Explanation struct {
	Input        string
	Canonical    string
	HasCanonical bool
	Human        string
	Next         []CivilDate
	HasNext      bool
	Error        string
}

// explainLimit is the largest projection Explain will produce.
const explainLimit = 50

// Explain parses, normalizes, and projects without touching the store. `from`
// is the stamp the projection is anchored on, in the stored spelling; empty
// means today.
func Explain(input string, today CivilDate, count int, from string) Explanation {
	if count < 0 {
		count = 0
	}
	if count > explainLimit {
		count = explainLimit
	}
	result := Parse(input, ".+")
	if result.Error != "" {
		return Explanation{Input: input, Error: result.Error}
	}
	if result.Canonical == "off" {
		return Explanation{Input: input, Human: "no recurrence", HasCanonical: true, HasNext: true,
			Next: []CivilDate{}}
	}

	anchor := today
	if from != "" {
		parsed, ok := parseStoredDate(from)
		if !ok {
			return Explanation{Input: input,
				Error: "stamp must be a real YYYY-MM-DD date: " + rubyInspect(from)}
		}
		anchor = parsed
	}

	human := ""
	if rendered := Humanize(result.Canonical); rendered != nil {
		human = *rendered
	}
	payload := Explanation{Input: input, Canonical: result.Canonical, HasCanonical: true, Human: human}
	dates, err := Occurrences(result.Canonical, anchor, today, count)
	if err != nil {
		payload.Next = []CivilDate{}
		payload.HasNext = true
		payload.Error = err.Error()
		return payload
	}
	payload.Next = dates
	payload.HasNext = true
	return payload
}

// parseStoredDate reads the stored YYYY-MM-DD spelling and only real dates.
func parseStoredDate(value string) (CivilDate, bool) {
	if !storedDate.MatchString(value) {
		return CivilDate{}, false
	}
	year, _ := new(big.Int).SetString(value[0:4], 10)
	month, _ := strconv.Atoi(value[5:7])
	day, _ := strconv.Atoi(value[8:10])
	date, err := NewCheckedCivilDate(year, month, day)
	if err != nil {
		return CivilDate{}, false
	}
	return date, true
}

var storedDate = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}\z`)

// Step advances a civil date. Month and year units clamp overflowing days;
// time.Time.AddDate cannot be used because it normalizes Jan 31 + one month
// into March instead of Ruby Date#>>'s February clamp.
func Step(date CivilDate, count *big.Int, unit string) CivilDate {
	switch unit {
	case "d":
		return date.addDays(count)
	case "w":
		return date.addDays(new(big.Int).Mul(count, big.NewInt(7)))
	case "m":
		return date.addMonths(count)
	case "y":
		return date.addMonths(new(big.Int).Mul(count, big.NewInt(12)))
	default:
		return CivilDate{}
	}
}

func rubySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\r', '\n', '\f', '\v':
		return true
	default:
		return false
	}
}

// Ruby String#downcase uses full Unicode mappings. U+0130 is the only
// expanding mapping that changes whether this package recognizes a keyword.
func rubyDowncase(value string) string {
	value = strings.ReplaceAll(value, "\u0130", "i\u0307")
	return strings.ToLower(value)
}

func prefixOrDefault(prefix, defaultPrefix string) string {
	if prefix != "" {
		return prefix
	}
	return defaultPrefix
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
