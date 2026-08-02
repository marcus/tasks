// recur-probe mirrors porting/runners/ruby/recur-probe for direct conformance.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
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

func civilDate(value string) (recur.CivilDate, error) {
	parts := strings.Split(value, "-")
	if len(parts) != 3 {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	year, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	var month, day int
	if _, err := fmt.Sscanf(parts[1]+"-"+parts[2], "%d-%d", &month, &day); err != nil {
		return recur.CivilDate{}, fmt.Errorf("invalid civil date %q", value)
	}
	return recur.CivilDate{Year: year, Month: month, Day: day}, nil
}
