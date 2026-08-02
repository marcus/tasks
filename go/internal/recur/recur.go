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
	return Result{Error: fmt.Sprintf("unrecognized schedule: %q", raw)}
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
		return CivilDate{}, fmt.Errorf("not a repeater cookie: %q", value)
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
	if word, ok := words[value]; ok {
		return "", word, true
	}
	parts := strings.Fields(value)
	if len(parts) > 0 && parts[0] == "every" {
		parts = parts[1:]
	}
	// Ruby tokenizes a joined count/unit pair ("2w") but does not merge
	// separate numeric tokens ("2 3days" must remain invalid).
	if len(parts) == 1 {
		match := countUnit.FindStringSubmatch(parts[0])
		if match != nil {
			n, parsed := new(big.Int).SetString(match[1], 10)
			unit, ok := units[match[2]]
			if parsed && n.Sign() > 0 && ok {
				return "", interval{count: n, unit: unit}, true
			}
			return "", interval{}, false
		}
	}
	if len(parts) == 1 {
		if unit, ok := bareUnits[parts[0]]; ok {
			return "", interval{count: big.NewInt(1), unit: unit}, true
		}
		return "", interval{}, false
	}
	if len(parts) != 2 {
		return "", interval{}, false
	}
	n, parsed := new(big.Int).SetString(parts[0], 10)
	if !parsed || n.Sign() < 1 {
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
