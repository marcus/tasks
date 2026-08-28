package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
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

// The detail panel ECHOES the list's own section idiom: a labelled rule that
// runs to the right edge, with a badge sharing that edge. The panel and the
// list are one surface read left to right, and giving the rail its own visual
// language — indented label/value pairs under a title bar — made it read as a
// second, unrelated application pinned to the side of the first.
//
// The rule label says what the block IS (TASK, NOTE, LINKS, ACTIONS); the badge
// says the one fact you would otherwise have to read the block to learn — the
// state and priority of the task, how many links there are. Everything the old
// label/value list carried is still here; it now sits UNDER the section whose
// question it answers, and the facts a person actually scans for (when, where,
// which project) are promoted into one meta line directly beneath the title.

// detailSection is a labelled rule with an optional right-aligned badge.
func detailSection(styler Styler, label, badge string, width int) string {
	return detailSectionIn(styler, label, "detail_label", badge, width)
}

// detailSectionIn is detailSection with the label's slot chosen by the caller —
// for the one rule whose label is a task's state rather than a block name.
func detailSectionIn(styler Styler, label, slot, badge string, width int) string {
	head := styler.Paint(slot, label)
	used := styler.Width(label) + 1
	if badge != "" {
		used += styler.Width(badge) + 1
	}
	rule := max(width-used, 0)
	line := head + " " + styler.Paint("outline_thread", strings.Repeat("─", rule))
	if badge != "" {
		line += " " + badge
	}
	return line
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
	today := queries.Today()

	// The rule's LABEL is the state and its badge is the id. A detail rail opens
	// on the one word that says what you are looking at — TODO, NEXT, WAITING —
	// and the id is the thing you would otherwise have to hunt for to quote it
	// to an agent or a command line.
	lines := []string{detailSectionIn(styler, item.State, stateSlotOf(item),
		styler.Paint("muted", item.ID), usable), ""}
	for _, line := range styler.Wrap(item.Title, usable) {
		lines = append(lines, styler.Paint("section", line))
	}
	// Truncated, never wrapped: this is a fielded line whose columns are made of
	// space runs, and a wrapper that reflows on whitespace would eat them.
	for _, meta := range taskMetaLine(styler, queries, item, projectName) {
		lines = append(lines, styler.Truncate(meta, usable))
	}

	// What the meta line could not say. A date row appears only when the stored
	// value carries more than the stamp already shown — a wall time, a fixed
	// zone, a projection into the local one.
	extra := []string{}
	if value, ok := queries.DeadlineValue(item); ok && !value.AllDay() {
		extra = append(extra, detailRow(styler, "deadline",
			temporalValue(styler, queries, value, "deadline", today)))
	}
	if value, ok := queries.ScheduledValue(item); ok && item.Deadline != "" {
		extra = append(extra, detailRow(styler, "available from",
			temporalValue(styler, queries, value, "scheduled", today)))
	}
	availability := queries.AvailabilityFor(item)
	if availability.Reason != taskquery.ReasonAvailable {
		extra = append(extra, detailRow(styler, "availability",
			availabilityValue(styler, queries, item, availability, today)))
	}
	if taskquery.Recurring(item) {
		extra = append(extra, detailRow(styler, "repeats", styler.Paint("muted", "↻")+" "+item.Recur))
	}
	if item.Lead != "" {
		extra = append(extra, detailRow(styler, "lead time", leadValue(styler, queries, item)))
	}
	if item.Closed != "" {
		extra = append(extra, detailRow(styler, "closed", item.Closed))
	}
	if len(extra) > 0 {
		lines = append(lines, "")
		lines = append(lines, extra...)
	}

	if delegation := delegationOf(item); delegation != nil {
		lines = append(lines, "", detailSection(styler, "DELEGATION",
			styler.Paint("muted", delegationText(delegation["status"])), usable))
		lines = append(lines, "")
		lines = append(lines, delegationLines(styler, queries.Context(), item, usable)...)
	}

	lines = append(lines, subtaskLines(styler, queries, item, usable)...)

	if notes := trimmedLines(queries.Body(item)); len(notes) > 0 {
		lines = append(lines, "", detailSection(styler, "DESCRIPTION", "", usable), "")
		for _, note := range notes {
			for _, wrapped := range styler.Wrap(note, usable) {
				lines = append(lines, styler.Paint("description", wrapped))
			}
		}
	}
	if found := queries.Links(item); len(found) > 0 {
		hint := "o opens"
		if len(found) > 1 {
			hint = "o to choose"
		}
		lines = append(lines, "", detailSection(styler, "LINKS",
			styler.Paint("muted", fmt.Sprintf("%d · %s", len(found), hint)), usable), "")
		for index, link := range found {
			marker := styler.Paint("muted", "   ")
			if index == 0 {
				marker = styler.Paint("accent", "o") + "  "
			}
			label := ""
			if link.Label != nil && *link.Label != "" {
				label = styler.Paint("description", *link.Label+" ")
			}
			lines = append(lines, styler.Truncate(marker+styler.Paint("link_system", link.System+" ")+
				label+styler.Paint("link", link.URL), usable))
		}
	}
	lines = append(lines, "", detailSection(styler, "ACTIONS", "", usable), "",
		detailActions(styler, item, usable))
	return DetailContent{Title: "task", Lines: lines}
}

// stateSlotOf is the theme slot a task's state word takes.
func stateSlotOf(item store.Item) string {
	if slot, found := stateSlot[item.State]; found {
		return slot
	}
	if isProposedState(item.State) {
		return "warning"
	}
	return "section"
}

// subtaskLines is the SUBTASKS block: the task's own children, each as a status
// glyph and a title, with `done/total` on the rule.
//
// It is the one thing the rail can say that the list beside it cannot. The list
// shows a subtree only when the parent is expanded and only in the views whose
// query the children satisfy; "what is left on this task" is a question about
// the task, and it belongs to the task's own panel.
func subtaskLines(styler Styler, queries *taskquery.Queries, item store.Item, width int) []string {
	node := queries.NodeFor(item)
	if node == nil {
		return nil
	}
	children := []store.Item{}
	for _, child := range node.Children {
		if child.Task() && !isProposedState(child.Item.State) {
			children = append(children, *child.Item)
		}
	}
	if len(children) == 0 {
		return nil
	}
	done := 0
	for _, child := range children {
		if !isOpenState(child.State) {
			done++
		}
	}
	lines := []string{"", detailSection(styler, "SUBTASKS",
		styler.Paint("muted", fmt.Sprintf("%d/%d", done, len(children))), width), ""}
	for _, child := range children {
		lines = append(lines, styler.Truncate(
			detailDot(styler, child)+styler.Paint(childSlot(child), child.Title), width))
	}
	return lines
}

// detailDot is the status glyph in front of a subtask.
func detailDot(styler Styler, item store.Item) string {
	switch {
	case !isOpenState(item.State):
		return styler.Paint("state_done", DotClosed) + " "
	case item.State == "NEXT":
		return styler.Paint("state_next", DotProgress) + " "
	default:
		return styler.Paint("muted", DotOpen) + " "
	}
}

func childSlot(item store.Item) string {
	if isOpenState(item.State) {
		return "title"
	}
	return "muted"
}

// taskMetaLine is the fielded line under the title: priority, when, and how far
// away — then a Labels line for where it lives and what it is tagged with.
//
// Absent facts drop out rather than printing a placeholder. An em dash where a
// date is not is a fact nobody needs.
func taskMetaLine(styler Styler, queries *taskquery.Queries, item store.Item,
	projectName string) []string {
	fields := []string{}
	if item.Priority != "" {
		fields = append(fields, styler.Paint(prioritySlot(item.Priority), item.Priority))
	}
	if date, kind, value, ok := primaryDate(queries, item); ok {
		lead := "due "
		if kind != "deadline" {
			lead = "from "
		}
		stamp := fmt.Sprintf("%s %02d-%02d",
			strings.ToLower(date.Weekday().String()[:3]), int(date.Month), date.Day)
		if value.LocalTime != "" {
			if projected, err := value.Projected(queries.Context()); err == nil {
				date = projected.Date
				stamp = fmt.Sprintf("%s %02d-%02d %s",
					strings.ToLower(date.Weekday().String()[:3]),
					int(date.Month), date.Day, projected.Local)
			}
		}
		days := date.Sub(queries.Today())
		fields = append(fields,
			styler.Paint("description", lead+stamp), styler.Paint(dueSlot(days), relativeDays(days)))
	}

	labels := []string{}
	if projectName != "" {
		labels = append(labels, styler.Paint("project", projectName))
	}
	for _, context := range item.Contexts {
		labels = append(labels, styler.Paint("context", context))
	}
	for _, tag := range item.Tags {
		labels = append(labels, styler.Paint("description", tag))
	}

	lines := []string{}
	if len(fields) > 0 {
		lines = append(lines, strings.Join(fields, "   "))
	}
	if len(labels) > 0 {
		lines = append(lines, styler.Paint("muted", "Labels: ")+strings.Join(labels, ", "))
	}
	return lines
}

// detailActions is the keys that act on the task under the cursor. They are the
// REAL bindings from the shortcut registry, spelled here the way the footer
// hint spells its own: a short, stable, hand-chosen row rather than everything
// the registry would happily list.
//
// A narrow rail drops whole pairs from the end rather than truncating the row,
// for the reason the footer hint gives: `… z defer   K` teaches nothing, and a
// shorter list that ends on a word still does.
func detailActions(styler Styler, item store.Item, width int) string {
	pairs := [][2]string{
		{"c", "done"}, {"d", "date"}, {"r", "recur"}, {"z", "defer"}, {"K", "priority"},
	}
	// A proposal answers to the review keys, and `r` is reject there rather than
	// recur. `c` approves AND completes, which is the only thing it can honestly
	// mean on a row the store refuses to complete on its own.
	if isProposedState(item.State) {
		pairs = [][2]string{
			{"a", "approve"}, {"c", "approve+done"}, {"r", "reject"}, {"d", "date"}, {"K", "priority"},
		}
	}
	painted := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		painted = append(painted, styler.Paint("accent", pair[0])+styler.Paint("muted", " "+pair[1]))
	}
	for keep := len(painted); keep > 1; keep-- {
		line := strings.Join(painted[:keep], styler.Paint("muted", "   "))
		if styler.Width(line) <= width {
			return line
		}
	}
	return styler.Truncate(painted[0], width)
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
	lines := []string{detailSection(styler, "PROJECT",
		styler.Paint("muted", strings.ToUpper(project.Kind)), usable), ""}
	for _, line := range styler.Wrap(project.Title, usable) {
		lines = append(lines, styler.Paint("section", line))
	}
	lines = append(lines, "")
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
		lines = append(lines, "", detailSection(styler, "NOTE", "", usable), "")
		for _, note := range notes {
			for _, wrapped := range styler.Wrap(note, usable) {
				lines = append(lines, styler.Paint("description", wrapped))
			}
		}
	}
	if len(tasks) > 0 {
		lines = append(lines, "", detailSection(styler, "TASKS",
			styler.Paint("muted", fmt.Sprintf("%d", len(tasks))), usable), "")
		for _, task := range tasks {
			lines = append(lines, projectTaskLine(styler, queries, task))
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
func delegationLines(styler Styler, context temporal.Context, item store.Item,
	usable int) []string {

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
			// Projected, not painted-as-stored: the panel is a reading surface
			// and this is the same instant `show` prints, in the same zone.
			value = styler.Paint("muted", context.StampLabel(value))
		case "work_ref":
			label, value = "work ref", styler.Paint("link", value)
		case "note":
			// The briefing is prose the receiver has to READ, and it may carry
			// its own line breaks, so it wraps into the field's column instead
			// of being cut at the panel edge like a one-word value.
			lines = append(lines, delegationNoteLines(styler, delegation[key], usable)...)
			continue
		}
		lines = append(lines, "  "+styler.Paint("detail_label", padRight(label, 8))+" "+value)
	}
	return lines
}

// delegationNoteLines wraps the briefing under its own label, continuing lines
// aligned with the first. The label column is the same one every other
// delegation field uses, so the block reads as one field, not as free text that
// escaped the list.
func delegationNoteLines(styler Styler, note string, usable int) []string {
	indent := "  " + strings.Repeat(" ", 8) + " "
	width := max(usable-len([]rune(indent)), 8)
	out := []string{}
	for _, paragraph := range strings.Split(note, "\n") {
		text := delegationText(paragraph)
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, wrapped := range styler.Wrap(text, width) {
			lead := indent
			if len(out) == 0 {
				lead = "  " + styler.Paint("detail_label", padRight("note", 8)) + " "
			}
			out = append(out, lead+styler.Paint("description", wrapped))
		}
	}
	return out
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
