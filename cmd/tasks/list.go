package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// list prints the tasks a filter names, grouped by state. It is the everyday
// read, so its defaults matter: `--open` scope, and within that only what is
// AVAILABLE — a task held behind its own future start date, or behind an
// ancestor's, is not something you can act on today and does not belong in the
// list you work from.
func (s *surfaceContext) list(args []string) int {
	parsed, err := query.ParseCLI(args)
	if err != nil {
		return abort(err.Error())
	}
	queries, status := s.readQueries(args, "list")
	if status != 0 {
		return status
	}
	filter := parsed.Filter()
	items := queries.List(filter)

	if parsed.JSON() {
		return s.emitItemsJSON(queries, items)
	}

	if len(items) == 0 {
		out("No matching tasks.")
		return 0
	}

	// The delegation scopes are about who HOLDS the task, not what state it is
	// in, so they print one flat ranked list of delegation summaries instead of
	// the state-grouped view.
	if filter.DelegatedOnly() || filter.AgentReadyOnly() {
		for _, item := range items {
			out(delegationHeadline(queries, item))
		}
		return 0
	}

	// The decline scope is about WHEN a proposal was declined, not what state it
	// is in — every row is CANCELLED — so it prints one flat newest-first list
	// with the decline date and the restore verb, the same way the delegation
	// scopes print who holds the task.
	if filter.RejectedOnly() {
		for _, item := range items {
			archived := ""
			if item.Source == store.SourceArchive {
				archived = dim("  (archived)")
			}
			out("  " + dim(item.Rejected+"  ") + format(item) + archived)
		}
		out("")
		out(dim("restore one with: tasks unreject \"<ref>\""))
		return 0
	}

	byState := map[string][]store.Item{}
	for _, item := range items {
		byState[item.State] = append(byState[item.State], item)
	}
	for _, state := range taskquery.StateOrder() {
		group, present := byState[state]
		if !present {
			continue
		}
		out(bold(state))
		// Priority orders the group; ties keep FILE order.
		//
		// This is a deliberate divergence, not a translation. tasks' cmd_list
		// sorts with a bare `sort_by { |i| i.priority || "Z" }`, and MRI's sort_by
		// is not stable, so rows that tie on priority come out wherever
		// introsort's partitioning left them. That order is reproducible for a
		// given array and meaningless as an order: capturing one unrelated task
		// into the same state group reshuffles rows you did not touch.
		//
		// Ruby's own read model already rejects this — TaskQueries#stable_sort
		// carries the source index precisely so the named views keep file order —
		// and cmd_list is the one place in the read path that never adopted it.
		// This stable file-order tie break is an intentional compatibility decision.
		sorted := append([]store.Item{}, group...)
		sort.SliceStable(sorted, func(left, right int) bool {
			return priorityKey(sorted[left]) < priorityKey(sorted[right])
		})
		for _, item := range sorted {
			due := shortDue(queries, item)
			if due != "" {
				due = "  " + due
			}
			archived := ""
			if item.Source == store.SourceArchive {
				archived = dim("  (archived)")
			}
			deferred := availabilityLabel(queries, item)
			recurring := ""
			if item.Recur != "" {
				recurring = dim("  ↻ " + recurSummary(item.Recur))
			}
			out(fmt.Sprintf("  %s%s%s%s%s", format(item), due, recurring, deferred, archived))
		}
		out("")
	}
	return 0
}

// readQueries takes the coherent snapshot every read surface renders from, and
// builds the reader's own view over it.
//
// The unsupported-schema gate lives HERE rather than in each command, for the
// reason Ruby learned the hard way: wired per command it drifted, with three
// call sites carrying it and thirty not, and nothing making the gap visible. A
// read that cannot interpret the file must refuse, not answer — an empty list
// and a list this build cannot read are indistinguishable to a caller.
func (s *surfaceContext) readQueries(args []string, action string) (*taskquery.Queries, int) {
	if status := s.refuseUnsupportedSchema(args, action); status != 0 {
		return nil, status
	}
	instant, err := determinism.NowForAdapter(env)
	if err != nil {
		return nil, abort(err.Error())
	}
	context, err := temporal.NewContext(instant, s.paths.Timezone, s.paths.TimeFormat)
	if err != nil {
		return nil, abort(err.Error())
	}
	snapshot, err := s.store.ReadSnapshot(true)
	if err != nil {
		return nil, abort(store.UnavailableMessage(err))
	}
	// The link configuration rides along because `show`, `links` and `open` all
	// read it, and a read model built without it would silently answer "no
	// links" for every task whose note uses a configured shorthand.
	return taskquery.New(snapshot, context,
		taskquery.WithLinkConfig(s.paths.Links, s.paths.LinkSystems)), 0
}

// format is one row's headline: the priority cookie, the title, and the
// context cluster.
func format(item store.Item) string {
	priority := ""
	if item.Priority != "" {
		priority = "[" + item.Priority + "] "
	}
	tag := ""
	if len(item.Contexts) > 0 {
		tag = dim("  " + strings.Join(item.Contexts, " "))
	}
	return priority + item.Title + tag
}

// priorityKey is Ruby's `item.priority || "Z"`: unprioritized sorts after C.
func priorityKey(item store.Item) string {
	if item.Priority == "" {
		return "Z"
	}
	return item.Priority
}

// shortDue is the small dated tag beside a row: "7/1", or "6/14 5:00p" when the
// stamp carries a wall time. It prefers the DEADLINE and falls back to the
// available-from date, marked with a leading "~" so "starts" never reads as
// "due". Colour grades it by proximity.
func shortDue(queries *taskquery.Queries, item store.Item) string {
	deadline, hasDeadline := queries.DeadlineValue(item)
	scheduled, hasScheduled := queries.ScheduledValue(item)
	value := deadline
	if !hasDeadline {
		if !hasScheduled {
			return ""
		}
		value = scheduled
	}
	days := value.Date.Sub(queries.Today())
	label := fmt.Sprintf("%d/%d", int(value.Date.Month), value.Date.Day)
	if !value.AllDay() {
		if projected, err := value.Projected(queries.Context()); err == nil {
			hour, minute, ok := splitClock(projected.Local)
			if ok {
				label = fmt.Sprintf("%d/%d %s", int(projected.Date.Month), projected.Date.Day,
					clockLabel(hour, minute, queries.Context().TimeFormat))
				if value.Fixed() && value.Timezone != queries.Context().TimezoneID {
					label += " " + value.Timezone
				}
			}
		}
	}
	if !hasDeadline {
		label = "~" + label
	}
	return colorize(label, dueColor(days))
}

// clockLabel renders a wall time in the configured format: 24-hour zero-padded,
// or 12-hour with an a/p suffix and no leading zero.
func clockLabel(hour, minute, timeFormat int) string {
	if timeFormat == 24 {
		return fmt.Sprintf("%02d:%02d", hour, minute)
	}
	display := hour % 12
	if display == 0 {
		display = 12
	}
	suffix := "p"
	if hour < 12 {
		suffix = "a"
	}
	return fmt.Sprintf("%d:%02d%s", display, minute, suffix)
}

func splitClock(value string) (int, int, bool) {
	hourText, minuteText, found := strings.Cut(value, ":")
	if !found {
		return 0, 0, false
	}
	hour, hourErr := parseInt(hourText)
	minute, minuteErr := parseInt(minuteText)
	return hour, minute, hourErr == nil && minuteErr == nil
}

// availabilityLabel explains, in the row itself, why a task the list is showing
// is not workable now. `list --open` never reaches most of these — it filters
// unavailable rows out — but the deferred and unavailable scopes exist to show
// exactly those, and a row that appeared with no reason attached would be a
// mystery.
func availabilityLabel(queries *taskquery.Queries, item store.Item) string {
	if queries.Deferred(item) {
		return dim("  (on hold)")
	}
	availability := queries.AvailabilityFor(item)
	if availability.Available() || availability.Reason == taskquery.ReasonClosed {
		return ""
	}
	blocker, hasBlocker := queries.FindLive(availability.BlockerID)
	switch availability.Reason {
	case taskquery.ReasonScheduled:
		return dim("  (unavailable until " + temporalLabel(availability.Value) + leadSpanSuffix(queries, item) + ")")
	case taskquery.ReasonAncestorScheduled:
		// The effective gate, which for an ancestor with a lead is the derived
		// date rather than any stamp the ancestor carries.
		value := availability.Value
		if value == nil && hasBlocker {
			if scheduled, ok := queries.ScheduledValue(blocker); ok {
				value = &scheduled
			}
		}
		until := ""
		if value != nil {
			until = " until " + temporalLabel(value)
		}
		return dim("  (unavailable" + until + " via " + blockerName(blocker, hasBlocker, availability.BlockerID) + ")")
	case taskquery.ReasonAncestorOnHold:
		return dim("  (on hold via " + blockerName(blocker, hasBlocker, availability.BlockerID) + ")")
	default:
		return dim("  (unavailable)")
	}
}

func blockerName(blocker store.Item, found bool, id string) string {
	if found && blocker.Title != "" {
		return blocker.Title
	}
	return id
}

// temporalLabel spells a stored value in full: the date, then whatever
// qualifies it.
func temporalLabel(value *temporal.Value) string {
	if value == nil {
		return ""
	}
	text := value.Date.ISO()
	if value.LocalTime != "" {
		text += " " + value.LocalTime
	}
	if value.Timezone != "" {
		text += " " + value.Timezone
	}
	if value.Fold == 1 {
		text += " (later fold)"
	}
	return text
}

// leadSpanSuffix is " · 3w before 11/1" on a row a lead is hiding, so an
// unavailable review shows the intent beside the derived date rather than only
// the date it produced.
//
// The span is the STORED cookie, not its rendering. A list row is already dense
// — priority, title, contexts, date, recurrence, availability — and "3w" is
// what the file says and what `tasks lead` would take back. `show` has room for
// the sentence and spells it out there; this line does not.
func leadSpanSuffix(queries *taskquery.Queries, item store.Item) string {
	opens, hasOpens := queries.LeadOpens(item)
	if !hasOpens {
		return ""
	}
	availability := queries.AvailabilityFor(item)
	if availability.Value == nil || !availability.Value.Date.Equal(opens) {
		return ""
	}
	anchorText := item.Deadline
	if anchorText == "" {
		anchorText = item.Scheduled
	}
	anchor, ok := temporal.ParseDate(anchorText)
	if !ok {
		return ""
	}
	// An unparseable stored value prints nothing rather than echoing itself
	// into the middle of a row.
	if !lead.Span(item.Lead) {
		return ""
	}
	return fmt.Sprintf(" · %s before %d/%d", item.Lead, int(anchor.Month), anchor.Day)
}

// recurSummary is the human gloss of a stored recurrence value. An unparsable
// value echoes verbatim rather than being hidden.
func recurSummary(cookie string) string {
	if human := recur.Humanize(cookie); human != nil {
		return *human
	}
	return cookie
}

// delegationHeadline is the one-line summary the delegation scopes print
// instead of a state-grouped row: who holds the task, and in what capacity.
func delegationHeadline(queries *taskquery.Queries, item store.Item) string {
	marker := delegationFields(item.Delegation)
	if marker == nil {
		return "not delegated: " + item.Title
	}
	switch marker["status"] {
	case "delegated":
		return fmt.Sprintf("delegated → %s (%s): %s", marker["assignee"], item.State, item.Title)
	case "ready":
		return fmt.Sprintf("agent-ready (%s): %s", marker["mode"], item.Title)
	case "claimed":
		return fmt.Sprintf("claimed by %s: %s", marker["assignee"], item.Title)
	default:
		return "delegated: " + item.Title
	}
}

func init() {
	register("list", (*surfaceContext).list)
}
