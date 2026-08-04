package recur

// Natural phrases: the half of the grammar a person types rather than stores.
//
//	every monday · mondays · weekdays          → w:mon / w:mon,tue,wed,thu,fri
//	every 2 weeks on mon and thu               → 2w:mon,thu
//	the 1st and 15th of the month              → m:1,15
//	last friday of the month                   → m:lastfri
//	3rd thursday of november                   → y:11:3thu
//	every 2 weeks · weekly · quarterly         → .+2w / .+1w / .+3m
//
// One entry point reads both shapes, so interval and calendar input reach the
// store through the same door and normalize the same way. A phrase that names
// calendar days with an interval unit that cannot carry them is refused with
// the spelling that would work, rather than silently picking one meaning.

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var (
	numericToken = regexp.MustCompile(`\A\d+\z`)
	ordinalToken = regexp.MustCompile(`\A#(\d+|last)\z`)
	dayOrdinal   = regexp.MustCompile(`\A#?(\d{1,2})\z`)
	anyOrdinal   = regexp.MustCompile(`\A#?(\d+|last)\z`)
	writtenDay   = regexp.MustCompile(`(\d+)(?:st|nd|rd|th)\b`)
	digitLetter  = regexp.MustCompile(`(\d)([a-z])`)
	letterDigit  = regexp.MustCompile(`([a-z])(\d)`)
)

// qualifiers are the trailing scope words ("the 15th of the month") that
// qualify a spec rather than adding to it.
var qualifiers = map[string]bool{
	"week": true, "weeks": true, "month": true, "months": true,
	"year": true, "years": true,
}

var wordOrdinals = map[string]string{
	"first": "1", "second": "2", "third": "3", "fourth": "4", "fifth": "5", "last": "last",
}

var monthAliases = func() map[string]int {
	table := map[string]int{}
	for position, name := range monthFull {
		key := strings.ToLower(name)
		table[key] = position + 1
		table[key[:3]] = position + 1
	}
	table["sept"] = 9
	return table
}()

// parseNatural reads a phrase into either an interval cookie or a calendar
// schedule. `echo` is the caller's own spelling, quoted in every rejection.
func parseNatural(s, defaultPrefix, echo string) Result {
	tokens := tokenize(s)
	if len(tokens) == 0 {
		return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
	}

	count, unit, spec := takeInterval(tokens)
	monthlyHint := false
	if len(spec) >= 2 && qualifiers[spec[len(spec)-1]] {
		last := spec[len(spec)-1]
		spec = spec[:len(spec)-1]
		monthlyHint = strings.HasPrefix(last, "month")
	}

	if len(spec) == 0 {
		if unit == "" || count == nil || count.Sign() <= 0 {
			return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
		}
		return Result{Canonical: prefixOrDefault("", defaultPrefix) + count.String() + unit}
	}

	if unit == "d" {
		return Result{Error: "a daily schedule cannot also name calendar days"}
	}

	interval := 1
	if count != nil && count.IsInt64() {
		interval = int(count.Int64())
	}

	if days, ok := expandDays(spec); ok {
		if unit != "" && unit != "w" {
			return Result{Error: `a list of weekdays needs a weekly schedule, e.g. "every 2 weeks on monday"`}
		}
		return built(schedule{prefix: "", interval: interval, kind: weekly, days: uniqueSortedDays(days)})
	}

	if namesAMonth(spec) {
		if unit != "" && unit != "y" {
			return Result{Error: `a month name needs a yearly schedule, e.g. "every 2 years on july 4"`}
		}
		return naturalYearly(spec, interval, echo)
	}

	if unit != "" && unit != "m" {
		return Result{Error: `day-of-month rules need a monthly schedule, e.g. "every 2 months on the 15th"`}
	}
	return naturalMonthly(spec, interval, monthlyHint || unit == "m", echo)
}

// built renders a parsed schedule into its single canonical spelling.
func built(parsed schedule) Result { return Result{Canonical: canonicalString(parsed)} }

// tokenize lowercases into words, marks ordinals with a leading "#", and drops
// filler: "every 2 weeks on the 1st and 15th" becomes ["2","weeks","#1","#15"].
func tokenize(s string) []string {
	text := strings.Map(func(r rune) rune {
		if r == ',' || r == '&' || r == '/' {
			return ' '
		}
		return r
	}, s)
	// The ordinal marking runs FIRST, so "15th" becomes "#15" rather than being
	// split into "15 th" by the digit/letter separation below.
	text = writtenDay.ReplaceAllString(text, "#$1")
	text = digitLetter.ReplaceAllString(text, "$1 $2")
	text = letterDigit.ReplaceAllString(text, "$1 $2")

	tokens := []string{}
	for _, word := range strings.FieldsFunc(text, rubySpace) {
		if word == "" || filler[word] {
			continue
		}
		if ordinal, known := wordOrdinals[word]; known {
			tokens = append(tokens, "#"+ordinal)
			continue
		}
		tokens = append(tokens, word)
	}
	return tokens
}

// takeInterval peels a leading interval ("2 weeks", "monthly", "week") off the
// tokens. The count and unit are absent when the phrase does not open with one.
func takeInterval(tokens []string) (*big.Int, string, []string) {
	if len(tokens) >= 2 && numericToken.MatchString(tokens[0]) {
		if unit, known := units[tokens[1]]; known {
			count, ok := new(big.Int).SetString(tokens[0], 10)
			if ok {
				return count, unit, tokens[2:]
			}
		}
	}
	if word, known := words[tokens[0]]; known {
		return word.count, word.unit, tokens[1:]
	}
	if unit, known := bareUnits[tokens[0]]; known {
		return big.NewInt(1), unit, tokens[1:]
	}
	return nil, "", tokens
}

// expandDays reports the day list when EVERY token names weekdays, and ok=false
// as soon as one does not — a phrase is a day list or it is something else.
func expandDays(spec []string) ([]string, bool) {
	expanded := []string{}
	for _, token := range spec {
		switch token {
		case "weekday", "weekdays":
			expanded = append(expanded, weekdaySet...)
		case "weekend", "weekends":
			expanded = append(expanded, weekendSet...)
		default:
			day, known := dayAliases[token]
			if !known {
				return nil, false
			}
			expanded = append(expanded, day)
		}
	}
	return expanded, true
}

func namesAMonth(spec []string) bool {
	for _, token := range spec {
		if _, known := monthAliases[token]; known {
			return true
		}
	}
	return false
}

func naturalMonthly(spec []string, interval int, monthlyHint bool, echo string) Result {
	specs := []monthSpec{}
	for index := 0; index < len(spec); {
		token := spec[index]
		next := ""
		if index+1 < len(spec) {
			next = spec[index+1]
		}

		if match := ordinalToken.FindStringSubmatch(token); match != nil {
			ordinal, isLast := readOrdinal(match[1])
			switch {
			case next == "day":
				if !isLast && (ordinal < 1 || ordinal > 31) {
					return Result{Error: fmt.Sprintf("day of month must be 1–31: %d", ordinal)}
				}
				specs = append(specs, dayOrLastSpec(ordinal, isLast))
				index += 2
			case next != "" && dayAliases[next] != "":
				if !isLast && (ordinal < 1 || ordinal > 5) {
					return Result{Error: `ordinal weekdays run from 1 to 5 or "last"`}
				}
				specs = append(specs, ordinalWeekdaySpec(ordinal, isLast, dayAliases[next]))
				index += 2
			case isLast:
				specs = append(specs, monthSpec{last: true})
				index++
			default:
				if ordinal < 1 || ordinal > 31 {
					return Result{Error: fmt.Sprintf("day of month must be 1–31: %d", ordinal)}
				}
				specs = append(specs, monthSpec{day: ordinal})
				index++
			}
			continue
		}

		if numericToken.MatchString(token) {
			ordinal, err := strconv.Atoi(token)
			if err != nil {
				return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
			}
			weekday := ""
			if next != "" {
				weekday = dayAliases[next]
			}
			switch {
			case weekday != "":
				// A cardinal number before a weekday reads as a cadence ("every
				// 2 tuesdays"), not as an ordinal — and the ordinal has its own
				// spelling. Only an ordinal marker or an explicit monthly scope
				// settles it.
				if !monthlyHint {
					name := strings.ToLower(dayFull[dayIndex[weekday]])
					return Result{Error: fmt.Sprintf(
						"%d %s is ambiguous: write %q for a cadence, or %q for the %s %s of each month",
						ordinal, next,
						fmt.Sprintf("every %d weeks on %s", ordinal, name),
						fmt.Sprintf("%s %s of the month", ordinalWord(ordinal), name),
						ordinalWord(ordinal), name)}
				}
				if ordinal < 1 || ordinal > 5 {
					return Result{Error: `ordinal weekdays run from 1 to 5 or "last"`}
				}
				specs = append(specs, monthSpec{ordinal: ordinal, weekday: weekday, ordinalY: true})
				index += 2
			case monthlyHint:
				if ordinal < 1 || ordinal > 31 {
					return Result{Error: fmt.Sprintf("day of month must be 1–31: %d", ordinal)}
				}
				specs = append(specs, monthSpec{day: ordinal})
				index++
			default:
				return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
			}
			continue
		}

		return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
	}
	if len(specs) == 0 {
		return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
	}
	return built(schedule{prefix: "", interval: interval, kind: monthly, specs: uniqueSortedSpecs(specs)})
}

func naturalYearly(spec []string, interval int, echo string) Result {
	positions := []int{}
	for index, token := range spec {
		if _, known := monthAliases[token]; known {
			positions = append(positions, index)
		}
	}
	if len(positions) != 1 {
		return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
	}
	month := monthAliases[spec[positions[0]]]
	rest := []string{}
	for index, token := range spec {
		if index != positions[0] {
			rest = append(rest, token)
		}
	}

	if len(rest) == 1 {
		if match := dayOrdinal.FindStringSubmatch(rest[0]); match != nil {
			day, err := strconv.Atoi(match[1])
			if err != nil {
				return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
			}
			// 2024 is the reference year, so February 29 is a real yearly date
			// and every non-leap year clamps to it rather than refusing it.
			if !validDate(2024, month, day) {
				return Result{Error: fmt.Sprintf("%s has no day %d", monthFull[month-1], day)}
			}
			return built(schedule{prefix: "", interval: interval, kind: yearly, month: month, day: day})
		}
	}

	if len(rest) == 2 {
		if match := anyOrdinal.FindStringSubmatch(rest[0]); match != nil {
			if weekday, known := dayAliases[rest[1]]; known {
				ordinal, isLast := readOrdinal(match[1])
				if !isLast && (ordinal < 1 || ordinal > 5) {
					return Result{Error: `ordinal weekdays run from 1 to 5 or "last"`}
				}
				position := ordinalWeekdaySpec(ordinal, isLast, weekday)
				return built(schedule{prefix: "", interval: interval, kind: yearly,
					month: month, ordinal: &position})
			}
		}
	}

	return Result{Error: "unrecognized schedule: " + rubyInspect(echo)}
}

func readOrdinal(text string) (int, bool) {
	if text == "last" {
		return 0, true
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	return value, false
}

func dayOrLastSpec(ordinal int, isLast bool) monthSpec {
	if isLast {
		return monthSpec{last: true}
	}
	return monthSpec{day: ordinal}
}

func ordinalWeekdaySpec(ordinal int, isLast bool, weekday string) monthSpec {
	if isLast {
		return monthSpec{ordinal: lastOrdinal, weekday: weekday, ordinalY: true}
	}
	return monthSpec{ordinal: ordinal, weekday: weekday, ordinalY: true}
}
