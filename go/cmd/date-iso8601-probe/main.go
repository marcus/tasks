// date-iso8601-probe answers the Go side of the `Store#to_date` grammar
// comparison. It reads {"today": "YYYY-MM-DD", "cases": [...]} on stdin and
// emits one result per case, so the Ruby oracle and this port can be compared
// under the same today — the truncated ISO 8601 forms complete from the current
// date, and that dependency is injected rather than normalized away.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tasks-go/internal/record"
	"tasks-go/internal/store"
)

type request struct {
	Today string   `json:"today"`
	Cases []string `json:"cases"`
}

type result struct {
	Input string  `json:"input"`
	Date  *string `json:"date"`
}

func main() {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fail(err)
	}
	today, err := time.Parse("2006-01-02", input.Today)
	if err != nil {
		fail(fmt.Errorf("today: %w", err))
	}

	results := make([]result, 0, len(input.Cases))
	for _, text := range input.Cases {
		results = append(results, result{Input: text, Date: parse(text, today)})
	}
	encoded, err := json.Marshal(map[string]any{"today": input.Today, "cases": results})
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
}

// parse routes through the production coercion seam rather than a test-only
// entry point, so the comparison covers what a snapshot read actually does.
func parse(text string, today time.Time) *string {
	line := fmt.Sprintf(`{"type":"task","id":"probe","scheduled":%s}`, quote(text))
	snapshot := store.NewSnapshotOn(record.Parse([]byte(line)).Records, nil, today)
	items := snapshot.Items()
	if len(items) != 1 || items[0].Scheduled == nil {
		return nil
	}
	formatted := iso8601(*items[0].Scheduled)
	return &formatted
}

// iso8601 spells a date the way Ruby's Date#iso8601 does: a sign only for
// negative years, and at least four year digits.
func iso8601(value time.Time) string {
	year := value.Year()
	sign := ""
	if year < 0 {
		sign, year = "-", -year
	}
	return fmt.Sprintf("%s%04d-%02d-%02d", sign, year, int(value.Month()), value.Day())
}

func quote(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		fail(err)
	}
	return string(encoded)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
