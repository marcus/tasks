package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// Plain-text and markdown renderings of a task, for yanking out of the TUI.
//
// Go port of lib/tui/export.rb.

// ExportReference is the stable id to paste into the agent prompt.
//
// It is the ID and not the title on purpose: an exact id stays unambiguous
// across duplicate titles and across a later retitle, which is exactly what a
// reference in a conversation has to survive.
func ExportReference(item store.Item) string { return item.ID }

// ExportMarkdown is the whole task as pasteable markdown. `notes` is the item's
// prose lines, already filtered.
func ExportMarkdown(item store.Item, notes []string) string {
	return exportMarkdown(item, notes, nil)
}

// exportMarkdown renders with an optional read model, which supplies the
// availability line a bare item cannot answer.
func exportMarkdown(item store.Item, notes []string, queries *taskquery.Queries) string {
	out := []string{"## " + item.Title, ""}
	out = append(out, "- state: "+item.State)
	if item.Priority != "" {
		out = append(out, "- priority: "+item.Priority)
	}
	if item.Deadline != "" {
		out = append(out, "- deadline: "+temporalText(item, "deadline", queries))
	}
	if item.Scheduled != "" {
		out = append(out, "- available from: "+temporalText(item, "scheduled", queries))
	}
	if lead.Span(item.Lead) {
		if display, ok := leadDisplay(item, queries); ok {
			out = append(out, "- lead time: "+display)
		}
	}
	if containsString(item.AllTags, store.DeferTag) {
		out = append(out, "- on hold: yes")
	}
	if queries != nil {
		if availability := queries.AvailabilityFor(item); !availability.Available() {
			reason := strings.ReplaceAll(string(availability.Reason), "_", " ")
			line := "- availability: " + reason
			if blocker := availability.BlockerID; blocker != "" {
				line += " via " + blocker
			}
			out = append(out, line)
		}
	}
	if item.Closed != "" {
		out = append(out, "- closed: "+item.Closed)
	}
	if len(item.Contexts) > 0 {
		out = append(out, "- contexts: "+strings.Join(item.Contexts, " "))
	}
	if len(item.Tags) > 0 {
		out = append(out, "- tags: "+strings.Join(item.Tags, ", "))
	}

	kept := []string{}
	for _, note := range notes {
		if trimmed := strings.TrimSpace(note); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	if len(kept) > 0 {
		out = append(out, "")
		out = append(out, kept...)
	}
	return strings.Join(out, "\n") + "\n"
}

// temporalText renders a date with its wall time and zone when it has one, so a
// yanked task carries what the record carries rather than just the day.
func temporalText(item store.Item, field string, queries *taskquery.Queries) string {
	stored := item.Deadline
	if field == "scheduled" {
		stored = item.Scheduled
	}
	if queries == nil {
		return stored
	}
	var value temporal.Value
	var present bool
	if field == "scheduled" {
		value, present = queries.ScheduledValue(item)
	} else {
		value, present = queries.DeadlineValue(item)
	}
	if !present || value.LocalTime == "" {
		return stored
	}
	zone := ""
	if value.Timezone != "" {
		zone = " [" + value.Timezone + "]"
	}
	fold := ""
	if value.Fold == 1 {
		fold = " fold=later"
	}
	return value.Date.ISO() + " " + value.LocalTime + zone + fold
}

func leadDisplay(item store.Item, queries *taskquery.Queries) (string, bool) {
	if queries != nil {
		if anchor, present := queries.DeadlineValue(item); present {
			return lead.DisplayInstant(item.Lead, anchor, queries.Context())
		}
		if anchor, present := queries.ScheduledValue(item); present {
			return lead.DisplayInstant(item.Lead, anchor, queries.Context())
		}
	}
	stored := item.Deadline
	if stored == "" {
		stored = item.Scheduled
	}
	date, ok := temporal.ParseDate(stored)
	return lead.DisplayDate(item.Lead, date, ok)
}
