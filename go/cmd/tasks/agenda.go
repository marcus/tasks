package main

import (
	"fmt"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// agenda lists everything with a date, soonest first. It is the "what is
// coming" read, so unlike `list` it groups by nothing and leads with the stamp:
// one flat chronological column.
func (s *surfaceContext) agenda(args []string) int {
	queries, status := s.readQueries(args, "agenda")
	if status != 0 {
		return status
	}
	items := queries.AgendaItems()
	if hasFlag(args, "--json") {
		return s.emitItemsJSON(queries, items)
	}
	for _, item := range items {
		out(agendaRow(queries, item))
	}
	return 0
}

func agendaRow(queries *taskquery.Queries, item store.Item) string {
	deadline, hasDeadline := queries.DeadlineValue(item)
	value := deadline
	if !hasDeadline {
		value, _ = queries.ScheduledValue(item)
	}
	kind := "STRT"
	if hasDeadline {
		kind = "DUE "
	}
	days := value.Date.Sub(queries.Today())
	when := fmt.Sprintf("in %dd", days)
	switch {
	case days < 0:
		when = fmt.Sprintf("%dd ago", -days)
	case days == 0:
		when = "today"
	}
	// A timed stamp leads with its WALL time; a date-only one leads with the
	// month and day. The distinction is the information: "17:00" says more than
	// "06-18" about a task you have to be somewhere for.
	stamp := fmt.Sprintf("%02d-%02d", int(value.Date.Month), value.Date.Day)
	if value.LocalTime != "" {
		stamp = value.LocalTime
	}
	row := fmt.Sprintf("%s %s (%s)", stamp, kind, when)
	if days <= 2 {
		row = red(row)
	}
	return fmt.Sprintf("%s  %s", row, format(item))
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func init() {
	register("agenda", (*surfaceContext).agenda)
}
