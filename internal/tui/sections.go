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
//   - EVERY row has the same head: two cells of cursor, three of priority. A
//     title starts at the same column whether or not a row is selected and
//     whether or not it is prioritized.
//
// A view that cannot fill the meta column leaves it empty. It does not reclaim
// the space, because a column that moves when a value is missing is not a
// column.
const (
	// CursorField is the selection gutter: `❯ ` on the selected row, two spaces
	// on every other. Two cells, matching the collapse marker, so a selected
	// collapsed row reads `❯ ▸ title` rather than as a doubled marker.
	CursorField = 2
	// PriorityField is the priority letter plus its separation: `A  ` or three
	// spaces.
	PriorityField = 3
	// MetaColumn is the shared right-hand column. Ten cells is the widest thing
	// any view puts there: `tue 08-11`, `1d ago`, `12/34`.
	MetaColumn = 10
	// MinTitleWidth is how much title has to survive before the meta column is
	// worth having at all. Below it the layout degrades to a plain left-aligned
	// row with the meta carried inline.
	MinTitleWidth = 16
)

// Cursor is the selection glyph, gold in the comp and the same two cells as
// term/frame's own. It is deliberately distinct from the collapsed-row marker.
const Cursor = "❯ "

// MoreGlyph marks a row that stands for rows you cannot see.
const MoreGlyph = "▾"

// placeholderIndent lines a section's empty-state message up with the titles of
// the rows it stands in for. A placeholder is not chrome — it occupies a row's
// place — so it wears a row's indent rather than a rule's.
var placeholderIndent = strings.Repeat(" ", CursorField+PriorityField)

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
	if request.Width < CursorField+PriorityField+MinTitleWidth+MetaColumn+1 {
		return 0, false
	}
	return request.Width + extra - MetaColumn - 1, true
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
	rule := max(left-styler.Width(label)-1, 0)
	pad := max(MetaColumn-styler.Width(right), 0)
	return chromeRow(head + " " + styler.Paint("outline_thread", strings.Repeat("─", rule)) +
		" " + strings.Repeat(" ", pad) + styler.Paint(rightSlot, right))
}

// withMeta right-aligns one row's meta value into the shared column.
//
// It runs over a BUILT row rather than inside a body builder because the left
// half of a row is assembled by the subtree walker — cursor field, indent,
// thread, fold marker — and only the finished row knows how wide it is.
func withMeta(request BuildRequest, row Row, text, slot string) Row {
	styler := request.styler()
	if slot == "" {
		slot = "muted"
	}
	left, ok := metaColumns(request, 0)
	if !ok {
		// Narrow fallback: carry the value inline so it is never simply lost.
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
	if pad := MetaColumn - styler.Width(stamp); pad > 0 {
		stamp = strings.Repeat(" ", pad) + stamp
	}
	row.Text = body + " " + styler.Paint(slot, stamp)
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

// withMetaFor applies the view's meta function to a row that carries an item.
func withMetaFor(request BuildRequest, row Row, meta func(store.Item) (string, string)) Row {
	if row.Item == nil || meta == nil {
		return row
	}
	text, slot := meta(*row.Item)
	return withMeta(request, row, text, slot)
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

// priorityField is the fixed-width priority letter. Priority is painted by
// LETTER — A is the loudest thing on the row, B is calm, C and below are quiet
// — because "is there an A here" is the only priority question a scan asks.
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
