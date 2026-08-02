// Package recur parses and projects interval repeater cookies.
package recur

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var (
	cookie    = regexp.MustCompile(`^(\.\+|\+\+|\+)([1-9][0-9]*)([dwmy])$`)
	countUnit = regexp.MustCompile(`^([0-9]+)([a-z]+)$`)
)

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
		return Result{Canonical: prefixOrDefault(prefix, defaultPrefix) + value.count.String() + value.unit}
	}
	return Result{Error: "unrecognized schedule: " + rubyInspect(raw)}
}

// Cookie reports whether value is an exactly canonical interval cookie.
func Cookie(value string) bool { return cookie.MatchString(rubyStrip(value)) }

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

// NextDate projects an interval cookie from its stored date and today's date.
// A catch-up cookie intentionally may land on today, matching Ruby's date-only
// projection (the temporal completion path decides whether a timed stamp has
// already passed today).
func NextDate(value string, from, today CivilDate) (CivilDate, error) {
	match := cookie.FindStringSubmatch(rubyStrip(value))
	if match == nil {
		return CivilDate{}, fmt.Errorf("not a repeater cookie: %s", rubyInspect(value))
	}
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

func parseFriendlyInterval(value string) (string, interval, bool) {
	parts := tokenize(value)
	if len(parts) >= 2 && digits(parts[0]) {
		if unit, ok := units[parts[1]]; ok {
			n, parsed := new(big.Int).SetString(parts[0], 10)
			if parsed && n.Sign() > 0 && len(parts) == 2 {
				return "", interval{count: n, unit: unit}, true
			}
		}
	}
	if len(parts) >= 1 {
		if word, ok := words[parts[0]]; ok && len(parts) == 1 {
			return "", word, true
		}
	}
	if len(parts) == 1 {
		if unit, ok := bareUnits[parts[0]]; ok {
			return "", interval{count: big.NewInt(1), unit: unit}, true
		}
	}
	return "", interval{}, false
}

// tokenize is the interval-only counterpart of Ruby Recur.tokenize. It keeps
// token boundaries intact: count/unit pairs are recognized only when adjacent
// tokens say so, while filler and punctuation are discarded before the peel.
func tokenize(value string) []string {
	var text strings.Builder
	for _, r := range value {
		switch r {
		case ',', '&', '/':
			text.WriteByte(' ')
		default:
			text.WriteRune(r)
		}
	}
	value = text.String()
	var split strings.Builder
	var previous rune
	for index, r := range value {
		if index > 0 && ((previous >= '0' && previous <= '9' && r >= 'a' && r <= 'z') ||
			(previous >= 'a' && previous <= 'z' && r >= '0' && r <= '9')) {
			split.WriteByte(' ')
		}
		split.WriteRune(r)
		previous = r
	}
	parts := strings.Fields(split.String())
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if !filler[part] {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
