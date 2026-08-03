// recur-probe mirrors porting/runners/ruby/recur-probe for direct conformance.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"tasks-go/internal/recur"
)

type nextSpec struct {
	From  string `json:"from"`
	Today string `json:"today"`
}

type caseSpec struct {
	CaseID        string    `json:"case_id"`
	Input         string    `json:"input"`
	DefaultPrefix *string   `json:"default_prefix"`
	Humanize      *string   `json:"humanize"`
	Next          *nextSpec `json:"next"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: recur-probe CASES.jsonl")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var spec caseSpec
		if err := json.Unmarshal([]byte(line), &spec); err != nil {
			panic(err)
		}
		prefix := ".+"
		if spec.DefaultPrefix != nil {
			prefix = *spec.DefaultPrefix
		}
		result := recur.Parse(spec.Input, prefix)
		out := map[string]any{
			"case_id": spec.CaseID,
			"parse":   map[string]any{"canonical": nullIfEmpty(result.Canonical), "error": nullIfEmpty(result.Error)},
			"cookie":  recur.Cookie(spec.Input),
		}
		if spec.Humanize != nil {
			out["humanize"] = recur.Humanize(*spec.Humanize)
		}
		if spec.Next != nil {
			out["next"] = nextOutcome(spec.Input, *spec.Next)
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(encoded))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nextOutcome mirrors the Ruby probe's next_outcome, whose `rescue
// ArgumentError` also covers the two Date.iso8601 calls in the argument list —
// Date::Error is an ArgumentError — so a rejected `from` or `today` is reported
// as a next-error, not as a crash. Ruby evaluates `from` before `today`.
func nextOutcome(input string, spec nextSpec) map[string]any {
	next := map[string]any{"value": nil, "error": nil}
	from, err := civilDate(spec.From)
	if err != nil {
		next["error"] = dateError(spec.From, err)
		return next
	}
	today, err := civilDate(spec.Today)
	if err != nil {
		next["error"] = dateError(spec.Today, err)
		return next
	}
	value, err := recur.NextDate(input, from, today)
	if err != nil {
		next["error"] = err.Error()
		return next
	}
	next["value"] = value.String()
	return next
}

// dateError reports the rejection Ruby would have reported, and exits rather
// than answer for a shape the probe does not model.
func dateError(value string, err error) string {
	if errors.Is(err, errUnsupportedDateShape) {
		fmt.Fprintf(os.Stderr, "recur-probe: %q is outside the probe's ISO 8601 date domain\n", value)
		os.Exit(2)
	}
	return err.Error()
}

// errUnsupportedDateShape marks a string outside the probe's modelled domain.
// Date.iso8601 also accepts basic ("20260101"), ordinal, week-date, truncated
// ("--01-01") and datetime forms, and expands an unsigned two-or-three-digit
// year ("123-01-01" is 2023-01-01). None of those are exercised by the case
// files, and guessing at them would be a silent divergence, so the probe fails
// loudly instead of answering for them.
var errUnsupportedDateShape = errors.New("unsupported date shape")

// civilDate parses the extended ISO 8601 calendar dates the case files use:
// an optional sign, a year, and two-digit month and day. The leading sign is
// part of the year, not a field separator, and it also suppresses Ruby's
// short-year expansion, so a signed year of any length is literal while an
// unsigned one must be four digits or more. Field widths and calendar validity
// are rejected exactly where Date.iso8601 rejects them.
func civilDate(value string) (recur.CivilDate, error) {
	rest := value
	sign := ""
	if strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "+") {
		sign, rest = rest[:1], rest[1:]
	}
	parts := strings.Split(rest, "-")
	if len(parts) != 3 || !allDigits(parts[0]) || !allDigits(parts[1]) || !allDigits(parts[2]) {
		return recur.CivilDate{}, errUnsupportedDateShape
	}
	if sign == "" && len(parts[0]) >= 2 && len(parts[0]) <= 3 {
		return recur.CivilDate{}, errUnsupportedDateShape
	}
	if sign == "" && len(parts[0]) < 4 {
		return recur.CivilDate{}, recur.ErrInvalidDate
	}
	if len(parts[1]) != 2 || len(parts[2]) != 2 {
		return recur.CivilDate{}, recur.ErrInvalidDate
	}
	year, ok := new(big.Int).SetString(sign+parts[0], 10)
	if !ok {
		return recur.CivilDate{}, errUnsupportedDateShape
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil {
		return recur.CivilDate{}, errUnsupportedDateShape
	}
	day, err := strconv.Atoi(parts[2])
	if err != nil {
		return recur.CivilDate{}, errUnsupportedDateShape
	}
	return recur.NewCheckedCivilDate(year, month, day)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
