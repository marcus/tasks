package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/temporal"
)

const recurPopupHint = "weekly · every mon · m:15 · off · esc cancels"

// recurPreview is Ruby's live recurrence footer. It renders every Explain
// payload shape and drops whole projected dates from the end when space is
// tight; a clipped date can be read as a different date, so it is never shown.
func (m *Model) recurPreview(raw, anchor string, width int) string {
	if strings.TrimSpace(raw) == "" {
		return recurPopupHint
	}
	today := m.currentDate()
	payload := recur.Explain(raw,
		recur.NewCivilDate(int64(today.Year), int(today.Month), today.Day), 3, anchor)
	if payload.Human == "" {
		return payload.Error
	}
	if payload.Error != "" {
		return payload.Canonical + " — " + payload.Human + " — " + payload.Error
	}
	dates := make([]string, 0, len(payload.Next))
	for _, date := range payload.Next {
		label := date.String()
		if parsed, ok := temporal.ParseDate(label); ok {
			label += " " + parsed.Weekday().String()[:3]
		}
		dates = append(dates, label)
	}
	if len(dates) == 0 {
		return payload.Human
	}
	for len(dates) > 0 {
		line := fmt.Sprintf("%s → %s", payload.Human, strings.Join(dates, " · "))
		if width <= 0 || m.styler.Width(line) <= width {
			return line
		}
		dates = dates[:len(dates)-1]
	}
	return payload.Human
}
