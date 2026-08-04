package taskquery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/timezones"
)

func unmarshalString(raw json.RawMessage, into *string) error {
	if len(raw) == 0 {
		return fmt.Errorf("absent")
	}
	return json.Unmarshal(raw, into)
}

func splitLines(text string) []string { return strings.Split(text, "\n") }

func joinLines(lines []string) string { return strings.Join(lines, "") }

// containsFold is Ruby's `haystack.downcase.include?(needle)`; the needle is
// already folded by the filter that produced it.
func containsFold(haystack, needle string) bool {
	return strings.Contains(query.Downcase(haystack), needle)
}

func recurCookie(value string) bool { return recur.Cookie(value) }

func earliestOn(date temporal.Date, location *time.Location) (time.Time, error) {
	return timezones.EarliestOn(date.Year, date.Month, date.Day, location)
}

func formatClock(hour, minute int) string { return fmt.Sprintf("%02d:%02d", hour, minute) }
