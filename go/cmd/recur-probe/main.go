// recur-probe mirrors porting/runners/ruby/recur-probe for direct conformance.
package main

import (
	"bufio"
	"encoding/json"
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
			from, err := civilDate(spec.Next.From)
			if err != nil {
				panic(err)
			}
			today, err := civilDate(spec.Next.Today)
			if err != nil {
				panic(err)
			}
			value, err := recur.NextDate(spec.Input, from, today)
			next := map[string]any{"value": nil, "error": nil}
			if err != nil {
				next["error"] = err.Error()
			} else {
				next["value"] = value.String()
			}
			out["next"] = next
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

// civilDate parses the extended ISO 8601 calendar dates Date.iso8601 accepts in
// the Ruby probe, including the signed years ("-0044-03-15", "+2026-01-01")
// whose leading sign is part of the year, not a field separator.
func civilDate(value string) (recur.CivilDate, error) {
	rest := value
	sign := ""
	if strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "+") {
		sign, rest = rest[:1], rest[1:]
	}
	parts := strings.Split(rest, "-")
	if len(parts) != 3 {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	if !allDigits(parts[0]) || !allDigits(parts[1]) || !allDigits(parts[2]) {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	year, ok := new(big.Int).SetString(sign+parts[0], 10)
	if !ok {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	day, err := strconv.Atoi(parts[2])
	if err != nil {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	return recur.CivilDate{Year: year, Month: month, Day: day}, nil
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
