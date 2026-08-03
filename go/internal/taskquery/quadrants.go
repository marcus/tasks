package taskquery

import (
	"sort"

	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// Covey's Important/Urgent 2x2, computed from an Item. This is the single
// source of truth for quadrant classification — the CLI and the TUI both call
// through here, so they can never disagree.
//
// Hybrid model: the axes are derived from the fields you already set, with the
// :important:/:urgent: tags as explicit overrides.
//
//	important = priority A or B, OR the :important: tag
//	urgent    = a DEADLINE within urgentDays (overdue counts), OR the :urgent:
//	            tag. A SCHEDULED start date alone is not urgent.

// DefaultUrgentDays is the deadline window that counts as urgent.
const DefaultUrgentDays = 3

// QuadrantLabels are the canonical headings, shared so the CLI and TUI never
// drift. Each frontend applies its own formatting around the text.
var QuadrantLabels = [][2]string{
	{"Q1", "Q1 · Important + Urgent  (do now)"},
	{"Q2", "Q2 · Important, Not Urgent  (schedule)"},
	{"Q3", "Q3 · Urgent, Not Important  (delegate)"},
	{"Q4", "Q4 · Neither  (eliminate)"},
}

// Important is priority A or B, or the explicit tag.
func Important(item store.Item) bool {
	return contains(item.AllTags, "important") || item.Priority == "A" || item.Priority == "B"
}

// Urgent is a deadline inside the window — overdue counts — or the explicit
// tag. An available-from date alone never makes work urgent: "I can start" is
// not "I must finish".
func Urgent(item store.Item, today temporal.Date, urgentDays int) bool {
	if contains(item.AllTags, "urgent") {
		return true
	}
	deadline, ok := temporal.ParseDate(item.Deadline)
	if !ok {
		return false
	}
	return deadline.Sub(today) <= urgentDays
}

// Quadrant is "Q1".."Q4" for an item.
func Quadrant(item store.Item, today temporal.Date, urgentDays int) string {
	important, urgent := Important(item), Urgent(item, today, urgentDays)
	switch {
	case important && urgent:
		return "Q1"
	case important:
		return "Q2"
	case urgent:
		return "Q3"
	default:
		return "Q4"
	}
}

// QuadrantItems is the `quadrants` view: every OPEN, AVAILABLE task in file
// order, each paired with the quadrant it falls in. Order is the file's — the
// grouping is the view, not a sort.
func (q *Queries) QuadrantItems() []store.Item {
	selected := []store.Item{}
	for _, item := range q.snapshot.Items {
		if !isOpen(item.State) || !q.AvailabilityFor(item).Available() {
			continue
		}
		selected = append(selected, item)
	}
	return selected
}

// NextItems is the `next` view: every available NEXT action, ranked by
// priority. Ties keep file order — Ruby's sort_by is unstable, so this has to
// be an explicitly stable sort or same-priority rows shuffle between runs, and
// the canonical order the API and the TUI share would be nondeterministic.
func (q *Queries) NextItems() []store.Item {
	selected := []store.Item{}
	for _, item := range q.snapshot.Items {
		if item.State != "NEXT" || !q.AvailabilityFor(item).Available() {
			continue
		}
		selected = append(selected, item)
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return priorityKey(selected[left]) < priorityKey(selected[right])
	})
	return selected
}

// InboxItems is the `inbox` view: unfiled captures that are workable now, in
// file order. There is no ranking — an inbox is a queue to triage, not a list
// to choose from.
func (q *Queries) InboxItems() []store.Item {
	selected := []store.Item{}
	for _, item := range q.snapshot.Items {
		if item.State != "INBOX" || !q.AvailabilityFor(item).Available() {
			continue
		}
		selected = append(selected, item)
	}
	return selected
}

// QuadrantOf classifies against this reader's own today.
func (q *Queries) QuadrantOf(item store.Item, urgentDays int) string {
	return Quadrant(item, q.Today(), urgentDays)
}
