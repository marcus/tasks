package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// The agenda is the one view whose question is "when", so it is the one view
// grouped as a CALENDAR: day headings rather than the context, quadrant or
// project headings the other views use. Everything else about its rows — the
// cursor field, the priority field, the shared meta column — is the vocabulary
// in sections.go, which every view speaks.
const (
	bucketOverdue  = "overdue"
	bucketToday    = "today"
	bucketTomorrow = "tomorrow"
	bucketLater    = "later"
)

// agendaSections is the painted order, with the slot each heading takes. The
// slots are the existing urgency ladder rather than four new ones: a group
// heading and the dates under it mean the same thing, and they must not be able
// to disagree about what "soon" looks like in a theme.
var agendaSections = []struct {
	Key   string
	Label string
	Slot  string
}{
	{bucketOverdue, "OVERDUE", "due_overdue"},
	{bucketToday, "TODAY", "due_soon"},
	{bucketTomorrow, "TOMORROW", "due_week"},
	{bucketLater, "LATER", "muted"},
}

// dayBucket classifies a day delta. `scheduled` marks a date that is a start
// date rather than a deadline: a start date that has already passed is not
// overdue, it is startable, so it lands in TODAY.
func dayBucket(days int, scheduled bool) string {
	if scheduled && days < 0 {
		days = 0
	}
	switch {
	case days < 0:
		return bucketOverdue
	case days == 0:
		return bucketToday
	case days == 1:
		return bucketTomorrow
	default:
		return bucketLater
	}
}

// itemBucket is the day group one item belongs to. An undated rider inherits
// nothing — it rides its anchor, and only anchors are bucketed.
func itemBucket(request BuildRequest, item store.Item) string {
	date, kind, _, ok := primaryDate(request.Queries, item)
	if !ok {
		return bucketLater
	}
	return dayBucket(date.Sub(request.Queries.Today()), kind != "deadline")
}

// dateCell is the shared meta value: what a row's date says, and the slot it
// says it in. Every view uses it, so a task's date reads the same in the agenda
// as it does in Next or in a project.
//
// The vocabulary is relative where relative is shorter and absolute where it is
// not, which is how a person answers "when" out loud: something a day late is
// "1d ago", something today is the time of day it is due, something this week
// is a weekday, and something further out is a date.
//
// A start date (no deadline) is prefixed `~` and painted muted. The two are not
// the same promise, and the column must not claim they are.
func dateCell(request BuildRequest, item store.Item) (string, string) {
	date, kind, value, ok := primaryDate(request.Queries, item)
	if !ok {
		return "", "muted"
	}
	if kind != "deadline" {
		return fmt.Sprintf("~%02d-%02d", int(date.Month), date.Day), "muted"
	}
	clock := ""
	if value.LocalTime != "" {
		clock = value.LocalTime
		if projected, err := value.Projected(request.Queries.Context()); err == nil {
			date, clock = projected.Date, projected.Local
		}
	}
	days := date.Sub(request.Queries.Today())
	slot := dueSlot(days)
	switch {
	case days < 0:
		return fmt.Sprintf("%dd ago", -days), slot
	case days == 0 && clock != "":
		return clock, slot
	case days == 0:
		return "today", slot
	case days <= 7:
		return fmt.Sprintf("%s %02d-%02d",
			strings.ToLower(date.Weekday().String()[:3]), int(date.Month), date.Day), slot
	default:
		return fmt.Sprintf("%02d-%02d", int(date.Month), date.Day), slot
	}
}

// dateMeta is dateCell bound to a request, as renderSections wants it.
func dateMeta(request BuildRequest) func(store.Item) (string, string) {
	return func(item store.Item) (string, string) { return dateCell(request, item) }
}

// taskBody is the left half of a row in every list view: the priority letter,
// the title, and any badge. The cursor and band fields lead it — see
// appendSubtree's `lead`. Contexts are NOT here: they are right-aligned into
// their own shared column by withMetaFor, so that every title ends where the
// title column ends rather than wherever its tags happened to run out.
func taskBody(request BuildRequest, item store.Item) string {
	styler := request.styler()
	return priorityField(request, item) + styler.Paint("title", item.Title) + badge(request, item)
}

// agendaGrouped assembles the day groups in painted order.
func agendaGrouped(request BuildRequest, byBucket map[string][]Row) []Row {
	sections := make([]Section, 0, len(agendaSections))
	for _, section := range agendaSections {
		sections = append(sections, Section{
			Label: section.Label, Slot: section.Slot, Rows: byBucket[section.Key],
		})
	}
	return renderSections(request, sections, dateMeta(request))
}

// anchorDateItem is the item whose date decides which day group an anchor's
// whole subtree lands in: the soonest date anywhere in it. An anchor's own later
// date must not hide an earlier qualifying descendant, exactly as the anchor
// SORT already refuses to.
func anchorDateItem(request BuildRequest, query ViewQuery, node *taskquery.Node) store.Item {
	best, key := *node.Item, query.temporalSortKey(*node.Item)
	for _, child := range visibleChildren(request, node) {
		candidate := anchorDateItem(request, query, child)
		if candidateKey := query.temporalSortKey(candidate); candidateKey < key {
			best, key = candidate, candidateKey
		}
	}
	return best
}
