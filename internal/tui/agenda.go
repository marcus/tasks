package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// The agenda is the one view whose question is "when", so it is the one view
// laid out as a CALENDAR rather than as a list of rows that happen to carry
// dates.
//
// Three things follow from that, and all three are geometry:
//
//   - Rows are grouped under day headings (OVERDUE / TODAY / TOMORROW / LATER)
//     and each heading carries the count of what is under it. The heading rule
//     runs to the right edge, so the eye can find the next group without
//     reading any of the rows in this one.
//   - Every date shares ONE right-aligned column. A date column that floats
//     after a title of arbitrary length is not a column, and the whole value of
//     a "when" view is being able to read the whens down a single edge.
//   - Priority is a fixed field at the head of the row rather than a `[A] `
//     prefix on the title, so titles start at the same column whether or not a
//     task is prioritized, and a prioritized task is visible as a shape rather
//     than as text to be read.
//
// All three need the width of the list, which is why BuildRequest carries one.
// With no width (a caller that has not measured, or a pane too narrow to hold
// a date column) the layout degrades to a plain left-aligned row: the date is
// carried inline instead, and nothing is lost but the alignment.
const (
	// AgendaGutterWidth is the priority field at the head of an agenda row. It
	// sits between the renderer's one-cell cursor gutter and the outliner's
	// two-cell fold marker, which together put the priority letter in the third
	// column of every row and every title in the seventh.
	AgendaGutterWidth = 3
	// AgendaDateWidth is the right-aligned date column. Nine cells is the
	// widest thing it holds: `tue 08-11`.
	AgendaDateWidth = 9
	// AgendaMinTitleWidth is how much title has to survive before the date
	// column is worth having at all.
	AgendaMinTitleWidth = 16
)

// agendaBucket names a day group. The keys are internal; the labels are what
// the screen says.
const (
	bucketOverdue  = "overdue"
	bucketToday    = "today"
	bucketTomorrow = "tomorrow"
	bucketLater    = "later"
)

// agendaSections is the painted order, with the slot each heading is painted
// in. The slots are the existing urgency ladder rather than four new ones: a
// group heading and the dates under it mean the same thing, and they should not
// be able to disagree about what "soon" looks like in a theme.
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

// dayBucket classifies a day delta. `scheduled` marks a date that is a
// start date rather than a deadline: a start date that has already passed is
// not overdue, it is startable, so it lands in TODAY.
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

// agendaPriority is the fixed-width priority field. Priority is painted by
// LETTER — A is the loudest thing on the row, B is calm, C and below are quiet
// — because "there is an A here" is the only priority question a scan asks.
func agendaPriority(request BuildRequest, item store.Item) string {
	if item.Priority == "" {
		return strings.Repeat(" ", AgendaGutterWidth)
	}
	styler := request.styler()
	letter := item.Priority
	if styler.Width(letter) > 1 {
		letter = styler.Truncate(letter, 1)
	}
	return " " + styler.Paint(prioritySlot(item.Priority), letter) + " "
}

// prioritySlot maps a priority letter to its theme slot.
func prioritySlot(priority string) string {
	switch priority {
	case "A":
		return "priority_a"
	case "B":
		return "priority_b"
	case "C":
		return "priority_c"
	default:
		return "priority"
	}
}

// agendaBody is the left half of an agenda row: title, contexts, badges. The
// priority field leads the row rather than the body, so a nested rider's thread
// line cannot push its parent's priority letter out of the column — see
// appendSubtree's `lead`.
func agendaBody(request BuildRequest, item store.Item) string {
	styler := request.styler()
	return styler.Paint("title", item.Title) + contextTags(request, item) + badge(request, item)
}

// agendaDateCell is the right column: what this row's date says, and the slot
// it says it in.
//
// The vocabulary is relative where relative is shorter and absolute where it is
// not, which is how a person answers "when" out loud: something a day late is
// "1d ago", something today is the time of day it is due, something this week
// is a weekday, and something further out is a date.
//
// A start date (no deadline) is prefixed `~` and painted muted. The two are not
// the same promise, and the column must not claim they are.
func agendaDateCell(request BuildRequest, item store.Item) (string, string) {
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

// agendaColumns reports whether this frame is wide enough for the date column,
// and how much of it the left half may use.
func agendaColumns(request BuildRequest) (int, bool) {
	width := request.Width
	if width < AgendaGutterWidth+AgendaMinTitleWidth+AgendaDateWidth+1 {
		return 0, false
	}
	return width - AgendaDateWidth - 1, true
}

// withDateColumn is the alignment pass. It runs over BUILT rows rather than
// inside a body builder because the left half of a row is assembled by the
// subtree walker — indent, thread, fold marker — and only the finished row
// knows how wide it actually is.
func withDateColumn(request BuildRequest, row Row) Row {
	if row.Item == nil {
		return row
	}
	styler := request.styler()
	text, slot := agendaDateCell(request, *row.Item)
	left, ok := agendaColumns(request)
	if !ok {
		// Narrow fallback: carry the date inline so it is never simply lost.
		if text != "" {
			row.Text += styler.Paint(slot, "  "+text)
		}
		return row
	}
	body := styler.Truncate(row.Text, left)
	if pad := left - styler.Width(body); pad > 0 {
		body += strings.Repeat(" ", pad)
	}
	stamp := text
	if pad := AgendaDateWidth - styler.Width(stamp); pad > 0 {
		stamp = strings.Repeat(" ", pad) + stamp
	}
	row.Text = body + " " + styler.Paint(slot, stamp)
	return row
}

// agendaSectionRow is a day heading: the label, a rule to the right edge, and
// the count sharing the date column's edge so headings and rows line up on the
// same two verticals.
func agendaSectionRow(request BuildRequest, label, slot string, count int) Row {
	styler := request.styler()
	tally := fmt.Sprintf("%d", count)
	head := styler.Paint(slot, label)
	left, ok := agendaColumns(request)
	if !ok {
		return headerRow(head + styler.Paint("muted", " "+tally))
	}
	rule := max(left-styler.Width(label)-1, 0)
	pad := max(AgendaDateWidth-styler.Width(tally), 0)
	return headerRow(head + " " + styler.Paint("outline_thread", strings.Repeat("─", rule)) +
		" " + strings.Repeat(" ", pad) + styler.Paint("muted", tally))
}

// agendaGrouped assembles the finished view from per-bucket row runs: a heading
// with its count, the rows, and a blank line between groups. An empty group is
// not painted at all — an empty OVERDUE heading is a heading that costs two
// lines to say nothing.
func agendaGrouped(request BuildRequest, byBucket map[string][]Row) []Row {
	rows := []Row{}
	for _, section := range agendaSections {
		group := byBucket[section.Key]
		if len(group) == 0 {
			continue
		}
		count := 0
		for _, row := range group {
			if row.Item != nil {
				count++
			}
		}
		if len(rows) > 0 {
			rows = append(rows, headerRow(""))
		}
		rows = append(rows, agendaSectionRow(request, section.Label, section.Slot, count))
		for _, row := range group {
			rows = append(rows, withDateColumn(request, row))
		}
	}
	return rows
}

// anchorDateItem is the item whose date decides which day group an anchor's
// whole subtree lands in: the soonest date anywhere in it. An anchor's own
// later date must not hide an earlier qualifying descendant, exactly as the
// anchor SORT already refuses to.
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
