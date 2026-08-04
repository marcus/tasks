package tui

import (
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// Row is one painted line of the list — the port of Tui::Views::Row.
//
// A row is SELECTABLE (actions apply to it) when it carries an item or a
// project; a row with neither is a header or a blank. In tree mode a task row
// also carries its tree node so the model can answer hierarchy questions
// (collapse, expand, jump to parent) without re-deriving them.
//
// Rows never name a color. Builders paint through semantic slots so the active
// theme decides the final look, and the renderer highlights the selected row by
// reversing its text.
type Row struct {
	Text string

	// Item is the task this row acts on, nil for headers, blanks, and project
	// header rows.
	Item *store.Item
	// Node is the tree node behind a tree-mode task row, nil in flat mode.
	Node *taskquery.Node
	// Project is the rolled-up view behind a selectable Projects header row.
	Project *taskquery.ProjectView

	// MarkerSpan is the [begin, end) cell range of the collapse marker, for
	// mouse hit testing. Zero-width when the row carries no marker.
	MarkerBegin int
	MarkerEnd   int
}

// Selectable reports whether the cursor may land on this row.
func (r Row) Selectable() bool { return r.Item != nil || r.Project != nil }

// HasMarker reports whether this row has a clickable collapse marker.
func (r Row) HasMarker() bool { return r.MarkerEnd > r.MarkerBegin }

// ID is the durable identity selection follows across re-renders: a task id or
// a section id. Both live in the same 8-hex space, so one field suffices.
func (r Row) ID() string {
	if r.Item != nil {
		return r.Item.ID
	}
	if r.Project != nil {
		return r.Project.ID
	}
	return ""
}

// headerRow builds a non-selectable row.
func headerRow(text string) Row { return Row{Text: text} }

// The collapse markers, each exactly two terminal cells. Every tree-mode task
// row carries one so titles align regardless of whether a node has children.
const (
	MarkExpanded  = "▾ "
	MarkCollapsed = "▸ "
	MarkLeaf      = "  "
)
