package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// The delegation row markers. A single arrow means "handed off and idle" — a
// person we are waiting on, or agent-ready work nobody has picked up. A doubled
// arrow means a worker is actively holding the task, and it is the one
// delegation state where acting on the row would collide with someone else's
// work, so it is deliberately the loudest.
//
// The two are distinguishable WITHOUT color: different glyphs, and the accent
// slot resolves to bold in every monochrome theme while muted resolves to dim.
const (
	DelegatedGlyph = "→"
	ClaimedGlyph   = "⇒"
	// DelegationAssigneeWidth is the cells an assignee may occupy before it is
	// cut back to `local@…`. Wide enough for pat@example.com, narrow enough to
	// leave the title readable in a split pane.
	DelegationAssigneeWidth = 16
)

// dueSlot is the urgency ladder as theme slots.
func dueSlot(days int) string {
	switch {
	case days <= 0:
		return "due_overdue"
	case days <= 2:
		return "due_soon"
	case days <= 7:
		return "due_week"
	default:
		return "due_far"
	}
}

// contextTags is the trailing `@` context run, shared so every view that shows
// contexts inline spells them the same way.
//
// `except` is the context the surrounding section is already named after. The
// Next view groups BY context, so repeating the group's own name on every row
// under it says nothing — while the row's OTHER contexts are exactly what that
// grouping hides, and they stay.
func contextTags(request BuildRequest, item store.Item, except string) string {
	styler := request.styler()
	painted := make([]string, 0, len(item.Contexts))
	for _, context := range item.Contexts {
		if context == except {
			continue
		}
		painted = append(painted, styler.Paint("context", context))
	}
	if len(painted) == 0 {
		return ""
	}
	return "  " + strings.Join(painted, " ")
}

func priorityPrefix(request BuildRequest, item store.Item) string {
	if item.Priority == "" {
		return ""
	}
	return request.styler().Paint("priority", "["+item.Priority+"] ")
}

func relativeDays(days int) string {
	switch {
	case days < 0:
		return fmt.Sprintf("%dd ago", -days)
	case days == 0:
		return "today"
	default:
		return fmt.Sprintf("in %dd", days)
	}
}

// outlineBody is the outline's row: the state word, then the shared task body.
// The outline is the one view whose question is "what state is everything in",
// so the state word earns a column of its own here and nowhere else.
func outlineBody(request BuildRequest, item store.Item) string {
	slot := "muted"
	switch {
	case isProposedState(item.State):
		slot = "warning"
	case item.State == "NEXT":
		slot = "state_next"
	case item.State == "WAITING":
		slot = "state_waiting"
	case !isOpenState(item.State):
		slot = "state_done"
	}
	styler := request.styler()
	return styler.Paint(slot, padRight(item.State, 9)) + " " + taskBody(request, item)
}

func shortDue(request BuildRequest, item store.Item) string {
	if item.Deadline == "" {
		return ""
	}
	value, ok := request.Queries.DeadlineValue(item)
	if !ok {
		return ""
	}
	date := value.Date
	label := fmt.Sprintf("%d/%d", int(date.Month), date.Day)
	if value.LocalTime != "" {
		if projected, err := value.Projected(request.Queries.Context()); err == nil {
			date = projected.Date
			label = projected.Local
		} else {
			label = value.LocalTime
		}
	}
	return request.styler().Paint(dueSlot(date.Sub(request.Queries.Today())), label)
}

// badge is the trailing marker run. The markers are deliberately distinct:
// timed deferral carries the release date, indefinite On Hold carries the pause
// glyph, and an up-arrow identifies a blocker inherited from an ancestor.
//
// The delegation marker is the widest thing a badge can carry (an address
// rather than a glyph), and it goes LAST so a narrow pane truncates it before
// the recur and availability markers. Those answer "why can't I act on this
// row" and a delegated task is exactly the one that must not lose them.
func badge(request BuildRequest, item store.Item) string {
	styler := request.styler()
	out := ""
	if taskquery.Recurring(item) {
		out += styler.Paint("muted", " ↻")
	}
	availability := request.Queries.AvailabilityFor(item)
	switch availability.Reason {
	case taskquery.ReasonScheduled:
		out += styler.Paint("muted", " ⏳ "+availabilityStamp(request, availability))
	case taskquery.ReasonAncestorScheduled:
		out += styler.Paint("muted", " ⏳ "+availabilityStamp(request, availability)+" ↑")
	case taskquery.ReasonOnHold:
		out += styler.Paint("muted", " ⏸")
	case taskquery.ReasonAncestorOnHold:
		out += styler.Paint("muted", " ⏸ ↑")
	}
	return out + delegationMarker(request, item)
}

// availabilityStamp is the EFFECTIVE gate the query already resolved, which for
// a lead is a derived date no stamp on either task carries.
func availabilityStamp(request BuildRequest, availability taskquery.Availability) string {
	if availability.Value != nil && availability.Value.LocalTime != "" {
		if projected, err := availability.Value.Projected(request.Queries.Context()); err == nil {
			return projected.Local
		}
		return availability.Value.LocalTime
	}
	if !availability.Scheduled.Zero() {
		return fmt.Sprintf("%d/%d", int(availability.Scheduled.Month), availability.Scheduled.Day)
	}
	return ""
}

// Delegation statuses, spelled here rather than imported so this package does
// not take a dependency on the delegation vocabulary for four string compares.
const (
	delegationDelegated = "delegated"
	delegationReady     = "ready"
	delegationClaimed   = "claimed"
)

// delegationMarker is one compact marker, or "" for an undelegated task. Kept
// to a glyph plus one word so it rides at the end of a row without pushing the
// title out of a narrow pane; the detail panel carries the full picture.
func delegationMarker(request BuildRequest, item store.Item) string {
	delegation := delegationOf(item)
	if delegation == nil {
		return ""
	}
	styler := request.styler()
	switch delegation["status"] {
	case delegationDelegated:
		return styler.Paint("muted", " "+DelegatedGlyph+
			delegationAssignee(styler, delegation["assignee"]))
	case delegationReady:
		return styler.Paint("muted", " "+DelegatedGlyph+delegationText(delegation["mode"]))
	case delegationClaimed:
		return styler.Paint("accent", " "+ClaimedGlyph+delegationText(delegation["mode"]))
	default:
		return ""
	}
}

// delegationOf decodes the delegation marker, or nil when there is none.
func delegationOf(item store.Item) map[string]string {
	if len(item.Delegation) == 0 {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(item.Delegation, &raw) != nil || len(raw) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range raw {
		if text, ok := value.(string); ok {
			out[key] = text
		}
	}
	if out["status"] == "" {
		return nil
	}
	return out
}

// delegationKeyOrder is the record's own fixed key order, which the detail
// panel prints in.
var delegationKeyOrder = []string{"kind", "mode", "status", "assignee", "at", "work_ref"}

func sortedDelegationKeys(delegation map[string]string) []string {
	present := []string{}
	for _, key := range delegationKeyOrder {
		if delegation[key] != "" {
			present = append(present, key)
		}
	}
	extra := []string{}
	for key := range delegation {
		if !containsString(delegationKeyOrder, key) {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return append(present, extra...)
}

// delegationText is every delegation string on its way to the screen. The
// schema refuses control characters, but a record written by an older binary, a
// foreign writer, or a merge can still carry them, and the TUI must never be
// corrupted by data it merely displays: an escape in an assignee bleeds reverse
// video into the following rows and the frame border. Dropping the bytes leaves
// the payload as inert text.
func delegationText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
}

// delegationAssignee truncates to DelegationAssigneeWidth cells, cutting the
// domain first: pat@example.com survives intact, a long address degrades to
// `pat@…` rather than to a meaningless prefix of the local part.
func delegationAssignee(styler Styler, assignee string) string {
	text := delegationText(assignee)
	if text == "" || styler.Width(text) <= DelegationAssigneeWidth {
		return text
	}
	head := text
	if local, _, found := strings.Cut(text, "@"); found && local != "" {
		head = local + "@"
	}
	budget := DelegationAssigneeWidth - 1
	if styler.Width(head) > budget {
		head = styler.Truncate(head, budget)
	}
	return head + "…"
}

// primaryDate is the deadline if there is one, else the available-from date.
func primaryDate(queries *taskquery.Queries, item store.Item) (temporal.Date, string, temporal.Value, bool) {
	if item.Deadline != "" {
		if value, ok := queries.DeadlineValue(item); ok {
			return value.Date, "deadline", value, true
		}
	}
	if item.Scheduled != "" {
		if value, ok := queries.ScheduledValue(item); ok {
			return value.Date, "scheduled", value, true
		}
	}
	return temporal.Date{}, "", temporal.Value{}, false
}

func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len(text))
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
