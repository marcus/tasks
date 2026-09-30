package temporal

// The free-text date grammar: ParseExpression plus the two trailing modifier
// words a single text field has to carry, because it has no separate flags to
// carry them in.
//
//	fri 4pm                                 a floating wall time
//	fri 4pm Europe/Berlin                   the same wall time, fixed to a zone
//	fri 4pm UTC · fri 4pm floating          the two spelled zone words
//	nov 1 1:30 America/New_York fold=later  the second of a repeated hour
//
// The CLI spells the same three choices as --timezone, --floating, and --fold;
// the TUI's date fields and the HTTP parse preview spell them inline, and this
// is the one reader both use so "what does this text mean" has one answer.

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ParseText reads a date expression with optional trailing zone and fold words.
//
// The modifier words are peeled off the END only — fold first, then zone — and
// fill the corresponding fields of options. Everything else in options (Today,
// Order, Context) is used as given, so a caller that honors the configured date
// order passes it here exactly as it would to ParseExpression.
//
// A caller with flags of its own (the CLI's --timezone/--floating/--fold) sets
// them in options too; naming the same choice twice — a flag AND a word — is
// refused rather than letting one silently win.
func ParseText(text string, options ParseOptions) (Value, error) {
	tokens := strings.Fields(strings.TrimSpace(text))
	if len(tokens) > 0 {
		last := tokens[len(tokens)-1]
		if last == "fold=earlier" || last == "fold=later" {
			if options.FoldSpecified || options.Fold != 0 {
				return Value{}, errors.New("the fold is given twice — use either --fold or a trailing " + last)
			}
			options.FoldSpecified = true
			if last == "fold=later" {
				options.Fold = 1
			}
			tokens = tokens[:len(tokens)-1]
		}
	}
	if len(tokens) > 0 && zoneWord(tokens[len(tokens)-1]) {
		word := tokens[len(tokens)-1]
		if options.Timezone != "" || options.Floating {
			return Value{}, errors.New("the zone is given twice — use either --timezone/--floating or a trailing " + word)
		}
		tokens = tokens[:len(tokens)-1]
		switch {
		case word == "floating":
			options.Timezone, options.Floating = "", true
		case strings.EqualFold(word, "UTC"):
			// Spelled any way, it is the one canonical zone id.
			options.Timezone, options.Floating = "UTC", false
		default:
			options.Timezone, options.Floating = word, false
		}
	}
	return ParseExpression(strings.Join(tokens, " "), options)
}

// zoneWord reports a trailing token that names a zone mode rather than part of
// the date. An IANA identifier always has a region and a slash
// ("Europe/Berlin"); requiring a LETTER as well is what keeps a numeric date
// ("7/15", "2026/07/15") from being mistaken for one and silently eaten.
func zoneWord(token string) bool {
	if token == "floating" || strings.EqualFold(token, "UTC") {
		return true
	}
	return strings.Contains(token, "/") && strings.IndexFunc(token, unicode.IsLetter) >= 0
}

// Explanation is the answer to "what would this date text mean", computed
// without a task and without a write. Exactly one of Value and Error is set.
type Explanation struct {
	Input    string
	Value    Value
	HasValue bool
	// Human is the value as a person reads it, in the reader's clock format.
	Human string
	// Error is the reason the input is not a usable date. It is the same
	// sentence the CLI prints on the write path.
	Error string
}

// Explain parses text with ParseText and renders the outcome for a preview.
// Blank input is reported as unrecognized, like any other non-date; a caller
// that wants to refuse blank input as a request error does so before calling.
func Explain(text string, options ParseOptions, context Context) Explanation {
	if options.Context == nil && context.Timezone != nil {
		options.Context = &context
	}
	value, err := ParseText(text, options)
	if err != nil {
		if errors.Is(err, ErrNotADate) {
			return Explanation{Input: text, Error: "unrecognized date: " + strings.TrimSpace(text)}
		}
		return Explanation{Input: text, Error: err.Error()}
	}
	return Explanation{Input: text, Value: value, HasValue: true, Human: context.ValueLabel(value)}
}

// ValueLabel renders a stored value for a reader: "Fri 2 Oct", "Fri 2 Oct,
// 4:00p", "Fri 2 Oct, 16:00 Europe/Berlin (later fold)". The weekday leads for
// the reason StampLabel gives; the year appears only when it is not the
// reader's own, and the clock follows the configured time format.
func (c Context) ValueLabel(value Value) string {
	date := value.Date
	text := fmt.Sprintf("%s %d %s", date.Weekday().String()[:3], date.Day, date.Month.String()[:3])
	if c.Timezone == nil || date.Year != c.LocalDate().Year {
		text += fmt.Sprintf(" %04d", date.Year)
	}
	if value.AllDay() {
		return text
	}
	hour, minute, ok := splitLocal(value.LocalTime)
	if !ok {
		return text + ", " + value.LocalTime
	}
	text += ", " + ClockLabel(hour, minute, c.TimeFormat)
	if value.Timezone != "" {
		text += " " + value.Timezone
	}
	if value.Fold == 1 {
		text += " (later fold)"
	}
	return text
}
