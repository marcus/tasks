package lead

// The span grammar: turning what a person types into the one canonical stored
// spelling, or into a reason it cannot be.
//
//	3w · 2d · 1m · 10y · 5h        already canonical
//	3 weeks · a week · 2 wks       phrasings that normalize to one
//	the week before · 4 days early filler carries no span meaning
//	off · none · never             clear the lead
//
// The stored spelling is exactly `<count><unit>` with no prefix: a lead has no
// equivalent of a repeater cookie's `+`/`.+`/`++` axis, because it is measured
// from one stamp in one direction.

import (
	"fmt"
	"strconv"
	"strings"
)

// Off is the canonical answer for input that clears the lead. It is not a span,
// so it is spelled as its own value rather than as an empty string, which a
// caller could confuse with "no input".
const Off = "off"

// offWords clear the lead. They are Recur::OFF_WORDS: one vocabulary for both
// fields, so "never" means the same thing wherever it is typed.
var offWords = map[string]bool{
	"off": true, "none": true, "never": true, "clear": true, "no": true, "stop": true,
}

// words are the single words that name a span. A lead is a span, so "daily"
// and "weekly" are deliberately absent — they would be a category error.
var words = map[string][2]string{
	"day":       {"1", "d"},
	"week":      {"1", "w"},
	"fortnight": {"2", "w"},
	"month":     {"1", "m"},
	"quarter":   {"3", "m"},
	"year":      {"1", "y"},
}

// unitWords are Recur::UNIT_WORDS: the recurrence grammar's own unit table,
// shared so a unit letter cannot come to mean two things.
var unitWords = map[string]string{
	"day": "d", "days": "d", "week": "w", "weeks": "w",
	"month": "m", "months": "m", "year": "y", "years": "y",
	"d": "d", "w": "w", "m": "m", "y": "y",
}

// clockWords are the hour spellings. `m`/`min` are deliberately ABSENT: `m` is
// months here and in the recurrence grammar, and a lead precise to the minute
// would be a different feature, not a spelling.
var clockWords = map[string]string{
	"h": "h", "hr": "h", "hrs": "h", "hour": "h", "hours": "h",
}

// filler carries no span meaning and is dropped before the phrase is read, so
// "a week before" and "2 weeks ahead" land on the same span.
var filler = map[string]bool{
	"a": true, "an": true, "the": true, "in": true, "of": true, "before": true,
	"ahead": true, "early": true, "earlier": true, "prior": true, "advance": true,
}

// Result is Parse's outcome: a canonical span, Off, or a reason.
type Result struct {
	Canonical string
	Error     string
}

// Off reports the clear-the-lead outcome.
func (r Result) IsOff() bool { return r.Error == "" && r.Canonical == Off }

// Parse normalizes user input to a canonical stored span, Off, or a refusal
// naming why. The refusal text is the user-visible one.
func Parse(input string) Result {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Result{Error: "no lead time given"}
	}
	s := strings.ToLower(raw)
	if offWords[s] {
		return Result{Canonical: Off}
	}
	if Span(s) {
		return Result{Canonical: s}
	}
	count, unit, ok := parsePhrase(s)
	if !ok {
		return Result{Error: fmt.Sprintf("unrecognized lead time: %q", raw)}
	}
	if count < 1 {
		return Result{Error: fmt.Sprintf("a lead time must be at least 1 %s", unitNames[unit])}
	}
	return Result{Canonical: strconv.Itoa(count) + unit}
}

// Canonical is Parse for a caller that only wants the answer. ok=false covers
// both a refusal and the clear-the-lead outcome, which have no span.
func Canonical(input string) (string, bool) {
	result := Parse(input)
	if result.Error != "" || result.Canonical == Off {
		return "", false
	}
	return result.Canonical, true
}

// parsePhrase reads a friendly phrase into a count and a unit. A count and unit
// can arrive glued together ("2wks") or spaced.
func parsePhrase(s string) (int, string, bool) {
	tokens := []string{}
	for _, token := range strings.FieldsFunc(strings.Map(spaceOut, s), asciiSpace) {
		if token == "" || filler[token] {
			continue
		}
		if number, letters, split := splitCountUnit(token); split {
			tokens = append(tokens, number, letters)
			continue
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return 0, "", false
	}
	if len(tokens) == 1 {
		singular := singularOf(tokens[0])
		if word, known := words[singular]; known {
			count, _ := strconv.Atoi(word[0])
			return count, word[1], true
		}
		if _, known := clockWords[singular]; known {
			return 1, "h", true
		}
		return 0, "", false
	}
	if len(tokens) != 2 || !allDigits(tokens[0]) {
		return 0, "", false
	}
	unit, known := unitWords[tokens[1]]
	if !known {
		unit, known = clockWords[singularOf(tokens[1])]
	}
	if !known {
		unit, known = abbreviatedUnit(tokens[1])
	}
	if !known {
		return 0, "", false
	}
	count, err := strconv.Atoi(tokens[0])
	if err != nil {
		return 0, "", false
	}
	return count, unit, true
}

// abbreviatedUnit reads "wks"/"yrs"/"mos" and friends — spellings a human types
// that the recurrence table, which reads schedule phrases rather than spans,
// has no reason to carry.
func abbreviatedUnit(word string) (string, bool) {
	switch singularOf(word) {
	case "dy":
		return "d", true
	case "wk":
		return "w", true
	case "mo", "mon", "mth":
		return "m", true
	case "yr":
		return "y", true
	}
	return "", false
}

func singularOf(word string) string { return strings.TrimSuffix(word, "s") }

// spaceOut turns the separators a person types into spaces, so "2-3 weeks"
// tokenizes the way "2 3 weeks" does rather than as one word.
func spaceOut(r rune) rune {
	if r == ',' || r == '-' {
		return ' '
	}
	return r
}

func asciiSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\r', '\n', '\f', '\v':
		return true
	default:
		return false
	}
}

// splitCountUnit peels "2wks" into "2" and "wks".
func splitCountUnit(token string) (string, string, bool) {
	index := 0
	for index < len(token) && token[index] >= '0' && token[index] <= '9' {
		index++
	}
	if index == 0 || index == len(token) {
		return "", "", false
	}
	for position := index; position < len(token); position++ {
		if token[position] < 'a' || token[position] > 'z' {
			return "", "", false
		}
	}
	return token[:index], token[index:], true
}

func allDigits(value string) bool {
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
