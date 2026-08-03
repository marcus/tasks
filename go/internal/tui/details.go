package tui

import (
	"fmt"
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
)

// stateSlot maps a task state to its theme slot in the detail panel.
var stateSlot = map[string]string{
	"NEXT":      "state_next",
	"WAITING":   "state_waiting",
	"DONE":      "state_done",
	"CANCELLED": "state_done",
}

// DetailContent is one panel's worth of content.
type DetailContent struct {
	Title string
	Lines []string
}

// BuildTaskDetails is the pure task-detail content builder — the port of
// lib/tui/task_details.rb. Its output is hosted by the right panel today and
// could be hosted by any other surface tomorrow, because it depends on no
// modal, no store, and no panel state.
func BuildTaskDetails(styler Styler, queries *taskquery.Queries, item store.Item,
	width int, projectName string) DetailContent {
	if styler == nil {
		styler = PlainStyler{}
	}
	usable := max(width, 1)
	lines := []string{}
	for _, line := range styler.Wrap(item.Title, usable) {
		lines = append(lines, styler.Paint("section", line))
	}
	lines = append(lines, "")

	state := item.State
	if slot, found := stateSlot[item.State]; found {
		state = styler.Paint(slot, item.State)
	}
	lines = append(lines, detailRow(styler, "state", state))

	priority := styler.Paint("muted", "—")
	if item.Priority != "" {
		priority = "[#" + item.Priority + "]"
	}
	lines = append(lines, detailRow(styler, "priority", priority))

	today := queries.Today()
	if value, ok := queries.DeadlineValue(item); ok {
		lines = append(lines, detailRow(styler, "deadline", temporalValue(styler, queries, value, "deadline", today)))
	}
	if value, ok := queries.ScheduledValue(item); ok {
		lines = append(lines, detailRow(styler, "available from",
			temporalValue(styler, queries, value, "scheduled", today)))
	}
	availability := queries.AvailabilityFor(item)
	if availability.Reason != taskquery.ReasonAvailable {
		lines = append(lines, detailRow(styler, "availability",
			availabilityValue(styler, queries, item, availability, today)))
	}
	if taskquery.Recurring(item) {
		lines = append(lines, detailRow(styler, "repeats", styler.Paint("muted", "↻")+" "+item.Recur))
	}
	if item.Lead != "" {
		lines = append(lines, detailRow(styler, "lead time", leadValue(styler, queries, item)))
	}
	if item.Closed != "" {
		lines = append(lines, detailRow(styler, "closed", item.Closed))
	}
	if projectName != "" {
		lines = append(lines, detailRow(styler, "project", styler.Paint("project", projectName)))
	}
	if len(item.Contexts) > 0 {
		painted := make([]string, 0, len(item.Contexts))
		for _, context := range item.Contexts {
			painted = append(painted, styler.Paint("context", context))
		}
		lines = append(lines, detailRow(styler, "contexts", strings.Join(painted, "  ")))
	}
	if len(item.Tags) > 0 {
		lines = append(lines, detailRow(styler, "tags", strings.Join(item.Tags, "  ")))
	}
	if item.ID != "" {
		lines = append(lines, detailRow(styler, "id", styler.Paint("muted", item.ID)))
	}
	lines = append(lines, delegationLines(styler, item)...)

	notes := trimmedLines(queries.Body(item))
	if len(notes) > 0 {
		lines = append(lines, "", styler.Paint("detail_label", "description"))
		for _, note := range notes {
			for _, wrapped := range styler.Wrap(note, max(usable-2, 1)) {
				lines = append(lines, "  "+styler.Paint("description", wrapped))
			}
		}
	}
	if found := queries.Links(item); len(found) > 0 {
		lines = append(lines, "", styler.Paint("detail_label", "links")+
			styler.Paint("muted", " (o opens the first)"))
		systemWidth := 0
		for _, link := range found {
			systemWidth = max(systemWidth, len(link.System))
		}
		for _, link := range found {
			lines = append(lines, "  "+styler.Paint("link_system", padRight(link.System, systemWidth))+
				" "+styler.Paint("link", link.URL))
		}
	}
	return DetailContent{Title: "task", Lines: lines}
}

// BuildProjectDetails is the ProjectView counterpart — the port of
// lib/tui/project_details.rb. `tasks` is the resolved open task list; the
// builder never reaches back into a store or an application.
func BuildProjectDetails(styler Styler, queries *taskquery.Queries, project taskquery.ProjectView,
	tasks []store.Item, width int) DetailContent {
	if styler == nil {
		styler = PlainStyler{}
	}
	usable := max(width, 1)
	lines := []string{}
	for _, line := range styler.Wrap(project.Title, usable) {
		lines = append(lines, styler.Paint("section", line))
	}
	lines = append(lines, "")
	lines = append(lines, detailRow(styler, "kind", project.Kind))
	lines = append(lines, detailRow(styler, "open", fmt.Sprintf("%d", project.OpenCount)))
	lines = append(lines, detailRow(styler, "next", fmt.Sprintf("%d", project.NextCount)))
	if project.Stuck {
		lines = append(lines, detailRow(styler, "stuck", styler.Paint("warning", "no open next action")))
	}
	if project.HasNextDate {
		label := dateValue(styler, project.NextDate, queries.Today())
		if project.HasNextTime && project.NextTime.Local != "" {
			label = project.NextDate.ISO() + " " + project.NextTime.Local
		}
		lines = append(lines, detailRow(styler, "next date", label))
	}
	if project.ID != "" {
		lines = append(lines, detailRow(styler, "id", styler.Paint("muted", project.ID)))
	}
	if notes := trimmedLines(strings.Split(project.Body, "\n")); len(notes) > 0 {
		lines = append(lines, "", styler.Paint("detail_label", "notes"))
		for _, note := range notes {
			for _, wrapped := range styler.Wrap(note, max(usable-2, 1)) {
				lines = append(lines, "  "+styler.Paint("description", wrapped))
			}
		}
	}
	if len(tasks) > 0 {
		lines = append(lines, "", styler.Paint("detail_label", "open tasks"))
		for _, task := range tasks {
			lines = append(lines, "  "+projectTaskLine(styler, queries, task))
		}
	}
	return DetailContent{Title: "project", Lines: lines}
}

// projectTaskLine is state · priority · title · date, in the shared idioms.
func projectTaskLine(styler Styler, queries *taskquery.Queries, task store.Item) string {
	state := task.State
	if slot, found := stateSlot[task.State]; found {
		state = styler.Paint(slot, task.State)
	}
	priority := ""
	if task.Priority != "" {
		priority = styler.Paint("priority", "[#"+task.Priority+"] ")
	}
	stamp := ""
	if date, _, value, ok := primaryDate(queries, task); ok {
		label := fmt.Sprintf("%02d-%02d", int(date.Month), date.Day)
		if value.LocalTime != "" {
			label = value.LocalTime
		}
		stamp = "  " + styler.Paint(dueSlot(date.Sub(queries.Today())), label)
	}
	return state + " " + priority + styler.Paint("title", task.Title) + stamp
}

func detailRow(styler Styler, label, value string) string {
	return styler.Paint("detail_label", padRight(label, 10)) + " " + value
}

func dateValue(styler Styler, date temporal.Date, today temporal.Date) string {
	days := date.Sub(today)
	return styler.Paint(dueSlot(days),
		fmt.Sprintf("%s %s · %s", date.ISO(), date.Weekday().String()[:3], relativeDays(days)))
}

func temporalValue(styler Styler, queries *taskquery.Queries, value temporal.Value,
	field string, today temporal.Date) string {
	if value.AllDay() {
		return dateValue(styler, value.Date, today)
	}
	date := value.Date
	text := date.ISO() + " " + value.LocalTime
	if value.Fixed() {
		text += " " + value.Timezone
	}
	if value.Floating() {
		text += " floating"
	}
	context := queries.Context()
	if value.Fixed() && value.Timezone != context.TimezoneID {
		if projected, err := value.Projected(context); err == nil {
			text += " → " + projected.Date.ISO() + " " + projected.Local + " " + context.TimezoneID
			date = projected.Date
		}
	}
	if relative := temporalRelative(queries, value, field); relative != "" {
		text += " · " + relative
	}
	return styler.Paint(dueSlot(date.Sub(today)), text)
}

func temporalRelative(queries *taskquery.Queries, value temporal.Value, field string) string {
	context := queries.Context()
	instant, err := value.ReleaseInstant(context)
	if field == "deadline" {
		instant, err = value.DueBoundary(context)
	}
	if err != nil {
		return ""
	}
	seconds := int(instant.Sub(context.Now).Seconds())
	absolute := seconds
	if absolute < 0 {
		absolute = -absolute
	}
	if absolute < 60 {
		if field == "deadline" {
			return "due now"
		}
		return "available now"
	}
	duration := compactDuration(absolute)
	switch {
	case seconds > 0 && field == "deadline":
		return "due in " + duration
	case seconds > 0:
		return "available in " + duration
	case field == "deadline":
		return "overdue by " + duration
	default:
		return "available for " + duration
	}
}

func compactDuration(seconds int) string {
	minutes := max(seconds/60, 1)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours, remainder := minutes/60, minutes%60
	if remainder == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, remainder)
}

func availabilityValue(styler Styler, queries *taskquery.Queries, item store.Item,
	availability taskquery.Availability, today temporal.Date) string {
	gate := ""
	if availability.Value != nil {
		gate = temporalValue(styler, queries, *availability.Value, "scheduled", today)
	} else if !availability.Scheduled.Zero() {
		gate = dateValue(styler, availability.Scheduled, today)
	}
	blockerTitle := ""
	if availability.BlockerID != "" && availability.BlockerID != item.ID {
		if blocker, found := queries.FindLive(availability.BlockerID); found {
			blockerTitle = blocker.Title
		}
	}
	switch availability.Reason {
	case taskquery.ReasonScheduled:
		return "unavailable until " + gate
	case taskquery.ReasonAncestorScheduled:
		suffix := " via parent"
		if blockerTitle != "" {
			suffix += " " + blockerTitle
		}
		if gate == "" {
			return "unavailable" + suffix
		}
		return "unavailable until " + gate + suffix
	case taskquery.ReasonOnHold:
		return "on hold"
	case taskquery.ReasonAncestorOnHold:
		if blockerTitle != "" {
			return "on hold via parent " + blockerTitle
		}
		return "on hold via parent"
	case taskquery.ReasonClosed:
		return "closed"
	default:
		return "available now"
	}
}

// leadValue is the window as prose plus the date it opens. The derived date is
// the whole point of the field, and a deadline-anchored lead task has no stored
// stamp that shows it.
func leadValue(styler Styler, queries *taskquery.Queries, item store.Item) string {
	text := item.Lead
	if opens, ok := queries.LeadOpens(item); ok {
		text += " before — opens " + opens.ISO()
	}
	return styler.Paint("muted", "⏳") + " " + text
}

// delegationLines is every field of the marker, in the record's own fixed key
// order, or nothing at all when the task is not delegated. It sits in its own
// block because a closed task keeps its delegation as provenance — "who held
// this and where the work landed" is a distinct question from the task's own
// fields.
//
// work_ref is painted with the link slot but is deliberately NOT part of the
// `o`-openable link list: that list comes from the task body, and one keypress
// must keep meaning one thing.
func delegationLines(styler Styler, item store.Item) []string {
	delegation := delegationOf(item)
	if delegation == nil {
		return nil
	}
	lines := []string{"", styler.Paint("detail_label", "delegation")}
	for _, key := range sortedDelegationKeys(delegation) {
		label, value := key, delegationText(delegation[key])
		switch key {
		case "status":
			slot := "muted"
			if value == delegationClaimed {
				slot = "accent"
			}
			value = styler.Paint(slot, value)
		case "at":
			value = styler.Paint("muted", value)
		case "work_ref":
			label, value = "work ref", styler.Paint("link", value)
		}
		lines = append(lines, "  "+styler.Paint("detail_label", padRight(label, 8))+" "+value)
	}
	return lines
}

func trimmedLines(values []string) []string {
	out := []string{}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
