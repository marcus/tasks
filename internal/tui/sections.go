package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/store"
)

// The shared list vocabulary — the part of the design system that is not any
// one view's business.
//
// Three rules, and every view obeys all three:
//
//   - EVERY block opens with a section rule: an uppercase label, a `─` run to
//     the right edge, and a right-aligned count or status word. A view with no
//     natural grouping still gets one rule, because the rule is what says where
//     a block begins and how much is in it.
//   - EVERY list on the screen shares ONE right meta column. Dates, counts and
//     status words line up down a single edge across sections and across views,
//     so switching tabs does not move the column the eye is already reading.
//   - EVERY row has the same head: two cells of cursor, then two of urgency
//     band. A title starts at the same column whether or not a row is selected
//     and whether or not it is prioritized.
//
// A view that cannot fill the meta column leaves it empty. It does not reclaim
// the space, because a column that moves when a value is missing is not a
// column.
const (
	// CursorField is the selection gutter: `❯ ` on the selected row, two spaces
	// on every other. Two cells, matching the collapse marker, so a selected
	// collapsed row reads `❯ ▸ title` rather than as a doubled marker.
	CursorField = 2
	// BandField is the urgency band: a `▌` painted in the row's due slot, plus
	// its separation. It is the leftmost thing in a row's own text, so a block
	// of overdue work reads as a red edge down the side of the list before any
	// word on it has been read.
	BandField = 2
	// PriorityField is the priority letter plus its separation: `A  ` or three
	// spaces. It sits INSIDE the body, immediately after the state glyph, where
	// the design puts it — a letter next to the title it ranks, not a lonely
	// column at the far left.
	PriorityField = 3
	// MetaColumn is the shared right-hand column. Ten cells is the widest thing
	// any view puts there: `tue 08-11`, `1d ago`, `12/34`.
	MetaColumn = 10
	// ContextColumn is the second right-hand column, holding the row's `@`
	// contexts right-aligned against the meta column's left edge. Contexts used
	// to trail the title inline, which made every title end at a different
	// place and turned the contexts into noise attached to the words; as a
	// column they become a thing you can scan down. Thirteen cells fits
	// `@computer` and a second short tag.
	ContextColumn = 13
	// MinTitleWidth is how much title has to survive before the meta column is
	// worth having at all. Below it the layout degrades to a plain left-aligned
	// row with the meta carried inline.
	MinTitleWidth = 16
)

// Band is the urgency band glyph — see BandField.
const Band = "▌"

// Cursor is the selection glyph, gold in the comp and the same two cells as
// term/frame's own. It is deliberately distinct from the collapsed-row marker.
const Cursor = "❯ "

// MoreGlyph marks a row that stands for rows you cannot see.
const MoreGlyph = "▾"

// placeholderIndent lines a section's empty-state message up with the titles of
// the rows it stands in for. A placeholder is not chrome — it occupies a row's
// place — so it wears a row's indent rather than a rule's.
var placeholderIndent = strings.Repeat(" ", CursorField+BandField+PriorityField)

// Section is one labelled block of a view: the rule, then its rows.
//
// Right is the badge on the shared right edge. Left empty it becomes the count
// of selectable rows in the block, which is what it almost always is — spelling
// that once here is why no view can drift into counting its own rows wrong.
type Section struct {
	Label     string
	Slot      string
	Right     string
	RightSlot string
	Rows      []Row
	// Empty is the row painted when the block has nothing in it. A nil Empty
	// drops the whole block, rule and all: an empty heading costs two lines to
	// say nothing.
	Empty *Row
}

// metaColumns reports whether this frame is wide enough for the meta column,
// and how much of it the left half may use.
//
// `extra` is width the caller occupies that a task row does not. A section rule
// is the only such caller: it is painted FLUSH to the pane's left edge, where a
// row's cursor field sits, so it has those cells to spend and must spend them or
// its right edge would not land on the shared column.
func metaColumns(request BuildRequest, extra int) (int, bool) {
	if request.Width < CursorField+BandField+MinTitleWidth+MetaColumn+1 {
		return 0, false
	}
	return request.Width + extra - MetaColumn - 1, true
}

// contextColumnFits reports whether this frame can afford the context column on
// top of the meta column. It is the FIRST thing given up on a narrow terminal:
// a date is what the list is sorted and coloured by, an `@context` is a filter
// you already have a key for. When it goes, contexts fall back to trailing the
// title inline rather than vanishing.
func contextColumnFits(request BuildRequest) bool {
	return request.Width >= CursorField+BandField+MinTitleWidth+ContextColumn+MetaColumn+1
}

// sectionRow builds one section rule.
//
// The label is painted VERBATIM. Structural labels are written uppercase at the
// call site (OVERDUE, APPROVALS, PROJECTS, Q1…) because that is the system's
// idiom for chrome; a label that is user data — an outline section's title, a
// project's name, an @context — keeps the capitalization its author gave it.
// Shouting someone's own words back at them is not a section rule.
func sectionRow(request BuildRequest, label, slot, right, rightSlot string) Row {
	styler := request.styler()
	if slot == "" {
		slot = "muted"
	}
	if rightSlot == "" {
		rightSlot = "muted"
	}
	head := styler.Paint(slot, label)
	left, ok := metaColumns(request, CursorField)
	if !ok {
		if right == "" {
			return chromeRow(head)
		}
		return chromeRow(head + styler.Paint(rightSlot, " "+right))
	}
	return chromeRow(ruledHead(request, head, right, rightSlot, left))
}

// ruledHead fills the space between a rule's label and its count with `─`.
//
// The fill runs all the way to one cell before the count rather than stopping
// at the meta column's left edge: a rule is a line that LEADS the eye to the
// number, and a fill that quits ten cells early leaves the number floating
// unattached to the label it belongs to.
func ruledHead(request BuildRequest, head, right, rightSlot string, left int) string {
	styler := request.styler()
	// The count keeps its column no matter how long the label is. A rule whose
	// label runs into the badge used to push the badge off the frame, where the
	// renderer's truncation ate it — and a section rule that has lost its count
	// is the one thing a section rule exists to carry.
	body := max(left+MetaColumn-styler.Width(right), 0)
	if fill := body - styler.Width(head) - 1; fill > 0 {
		head += " " + styler.Paint("outline_thread", strings.Repeat("─", fill))
	}
	head = styler.Truncate(head, body)
	if pad := body - styler.Width(head); pad > 0 {
		head += strings.Repeat(" ", pad)
	}
	return head + " " + styler.Paint(rightSlot, right)
}

// withMeta right-aligns one row's meta value into the shared column.
//
// It runs over a BUILT row rather than inside a body builder because the left
// half of a row is assembled by the subtree walker — cursor field, indent,
// thread, fold marker — and only the finished row knows how wide it is.
func withMeta(request BuildRequest, row Row, text, slot string) Row {
	return withColumns(request, row, "", text, slot)
}

// withColumns right-aligns a row's context run and its meta value into the two
// shared right-hand columns, in that order.
//
// It runs over a BUILT row for the same reason withMeta does: the left half is
// assembled by the subtree walker, and only the finished row knows how wide it
// is. `contexts` is already painted; it is measured, not re-styled.
func withColumns(request BuildRequest, row Row, contexts, text, slot string) Row {
	styler := request.styler()
	if slot == "" {
		slot = "muted"
	}
	left, ok := metaColumns(request, 0)
	if !ok {
		// Narrow fallback: carry both values inline so neither is simply lost.
		if contexts != "" {
			row.Text += "  " + contexts
		}
		if text != "" {
			row.Text += styler.Paint(slot, "  "+text)
		}
		return row
	}
	tail := ""
	if contexts != "" && contextColumnFits(request) {
		left -= ContextColumn
		// One cell of the column is a mandatory gap. Without it a title that
		// fills its column abuts the context run with nothing between them and
		// the two read as one word.
		tail = styler.Truncate(contexts, ContextColumn-1)
		if pad := ContextColumn - styler.Width(tail); pad > 0 {
			tail = strings.Repeat(" ", pad) + tail
		}
	} else if contexts != "" {
		row.Text += "  " + contexts
	}
	body := styler.Truncate(row.Text, left)
	if pad := left - styler.Width(body); pad > 0 {
		body += strings.Repeat(" ", pad)
	}
	stamp := text
	if pad := MetaColumn - styler.Width(stamp); pad > 0 {
		stamp = strings.Repeat(" ", pad) + stamp
	}
	row.Text = body + tail + " " + styler.Paint(slot, stamp)
	return row
}

// renderSections lays out a whole view: rule, rows, blank, rule, rows…
//
// The meta pass runs here, over every row of every section at once, which is
// what makes the column shared rather than six views that each happen to align
// their own.
func renderSections(request BuildRequest, sections []Section,
	meta func(store.Item) (string, string)) []Row {

	rows := []Row{}
	for _, section := range sections {
		body := section.Rows
		if len(body) == 0 {
			if section.Empty == nil {
				continue
			}
			// The placeholder is padded like a row rather than left short: it
			// occupies a row's place, and a frame that paints selection or a
			// background must find the same width under it as anywhere else.
			body = []Row{withMeta(request, *section.Empty, "", "")}
		}
		right := section.Right
		if right == "" {
			right = fmt.Sprintf("%d", countSelectable(section.Rows))
		}
		if len(rows) > 0 {
			rows = append(rows, chromeRow(""))
		}
		rows = append(rows, sectionRow(request, section.Label, section.Slot, right, section.RightSlot))
		for _, row := range body {
			rows = append(rows, withMetaFor(request, row, meta))
		}
	}
	return rows
}

// withMetaFor applies the view's meta function and the shared context column to
// a row that carries an item.
func withMetaFor(request BuildRequest, row Row, meta func(store.Item) (string, string)) Row {
	if row.Item == nil || meta == nil {
		return row
	}
	text, slot := meta(*row.Item)
	return withColumns(request, row, contextTags(request, *row.Item, row.ContextExcept), text, slot)
}

// withMetaRows applies the shared date column to a flat run of rows — the shape
// a view that builds its own section rules produces.
func withMetaRows(request BuildRequest, rows []Row) []Row {
	meta := dateMeta(request)
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		out = append(out, withMetaFor(request, row, meta))
	}
	return out
}

func countSelectable(rows []Row) int {
	total := 0
	for _, row := range rows {
		if row.Item != nil {
			total++
		}
	}
	return total
}

// -- the shared row head ---------------------------------------------------

// urgencyBand is the row's leading field: a `▌` painted in the same due slot
// the date column uses, or two blank cells for a row with no date at all.
//
// It is the one field that is legible without being read. A run of overdue work
// becomes a red edge down the left of the list, and the eye finds the block
// before it finds a word — which is what makes the overdue/today/later grouping
// visible at a glance rather than only at the sub-rule that names it.
func urgencyBand(request BuildRequest, item store.Item) string {
	return urgencyBandIn(request, item, "")
}

// urgencyBandIn is urgencyBand inside a band group. `fallback` is the group's
// slot, painted when the row has no date of its own so the stripe runs unbroken
// down the whole band; empty outside a banded list, where an undated row simply
// has nothing to say.
func urgencyBandIn(request BuildRequest, item store.Item, fallback string) string {
	slot, ok := bandSlot(request, item)
	if !ok {
		slot = fallback
	}
	if slot == "" {
		return strings.Repeat(" ", BandField)
	}
	return request.styler().Paint(slot, Band) + " "
}

// bandSlot is the due slot a row's band takes, and whether it has one.
//
// Only a DEADLINE bands, and only on an open task — exactly the rule the date
// column already paints by. A scheduled date is when work became available, not
// when it is late, and banding it red would call a task overdue for the crime
// of having been startable since Tuesday. A closed task never bands: its
// urgency is settled.
func bandSlot(request BuildRequest, item store.Item) (string, bool) {
	days, ok := bandDays(request, item)
	if !ok {
		return "", false
	}
	return dueSlot(days), true
}

// bandDays is the days-to-deadline a row bands on, and whether it bands at all.
//
// It is kept separate from bandSlot because the two answer different questions.
// dueSlot paints a deadline due TODAY as loudly as one already missed — at the
// wire and past it are the same colour, deliberately. The outline's bands are a
// grouping, and there "today" and "overdue" are the two things a reader most
// needs told apart, so the grouping counts days rather than reading the colour.
func bandDays(request BuildRequest, item store.Item) (int, bool) {
	if !isOpenState(item.State) {
		return 0, false
	}
	date, kind, _, ok := primaryDate(request.Queries, item)
	if !ok || kind != "deadline" {
		return 0, false
	}
	return date.Sub(request.Queries.Today()), true
}

// priorityField is the fixed-width priority letter. Priority is painted by
// LETTER — A is the loudest thing on the row, B is calm, C and below are quiet
// — because "is there an A here" is the only priority question a scan asks.
//
// It sits inside the body, after the state glyph and before the title, so a
// nested row carries its own rank beside its own words. The letter is what a
// scan looks for; a column of mostly-blank cells at the far left was not.
func priorityField(request BuildRequest, item store.Item) string {
	if item.Priority == "" {
		return strings.Repeat(" ", PriorityField)
	}
	styler := request.styler()
	letter := item.Priority
	if styler.Width(letter) > 1 {
		letter = styler.Truncate(letter, 1)
	}
	return styler.Paint(prioritySlot(item.Priority), letter) + "  "
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

// The record-state glyphs. A state word says which of eight states a task is
// in; the glyph says the only thing a scan needs — done, being done, not
// started — and it says it in one cell at the head of the row.
const (
	DotClosed   = "●"
	DotProgress = "■"
	DotOpen     = "○"
)

// stateDot is the leading glyph for a task row, painted by state.
func stateDot(request BuildRequest, item store.Item) string {
	styler := request.styler()
	switch {
	case !isOpenState(item.State):
		return styler.Paint("state_done", DotClosed) + " "
	case item.State == "NEXT":
		return styler.Paint("state_next", DotProgress) + " "
	case item.State == "WAITING":
		return styler.Paint("state_waiting", DotOpen) + " "
	default:
		return styler.Paint("muted", DotOpen) + " "
	}
}

// foldedCount is the marker a collapsed row carries: how many rows it stands
// in for, in the same glyph the "N more" row uses.
func foldedCount(request BuildRequest, hidden int) string {
	return request.styler().Paint("muted", fmt.Sprintf(" %s %d", MoreGlyph, hidden))
}
