package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// agendaRowsAt builds the agenda at a given list width, which is the input the
// whole column layout is a function of.
func agendaRowsAt(t *testing.T, harness *modelHarness, width int) []Row {
	t.Helper()
	harness.model.SwitchView(ViewAgenda)
	read := harness.model.ReadModel()
	return BuildRows(BuildRequest{
		View: ViewAgenda, Styler: PlainStyler{}, Queries: read.Queries(),
		Items: read.Items(), Tree: read.Queries().Tree().Roots, UseTree: true,
		Collapsed: map[string]bool{}, Width: width,
	})
}

func TestAgendaGroupsRowsUnderDayHeadingsThatCountThem(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	rows := agendaRowsAt(t, harness, 78)

	heading, counted := "", 0
	seen := map[string]int{}
	for _, row := range rows {
		switch {
		case row.Item != nil:
			counted++
		case strings.TrimSpace(row.Text) != "":
			if heading != "" {
				seen[heading] = counted
			}
			heading, counted = strings.Fields(row.Text)[0], 0
		}
	}
	if heading != "" {
		seen[heading] = counted
	}
	if len(seen) == 0 {
		t.Fatalf("the agenda emitted no day headings:\n%s", agendaDump(rows))
	}
	for label, count := range seen {
		if count == 0 {
			t.Fatalf("heading %q has no rows under it — an empty group must not paint:\n%s",
				label, agendaDump(rows))
		}
		want := strings.TrimSpace(headingTally(rows, label))
		if want != strconv.Itoa(count) {
			t.Fatalf("heading %q counts %s but carries %d rows:\n%s",
				label, want, count, agendaDump(rows))
		}
	}
}

func TestAgendaDatesAndHeadingCountsShareOneRightEdge(t *testing.T) {
	// The point of the column is the shared edge. A date that stops one cell
	// short of the counts is not a column, it is a coincidence.
	harness := newModelHarness(t, harnessOptions{})
	const width = 78
	for _, row := range agendaRowsAt(t, harness, width) {
		if strings.TrimSpace(row.Text) == "" {
			continue
		}
		// A rule is painted flush to the pane edge and so is CursorField wider
		// than a row; both end on the same column, which is the point.
		want := width
		if row.Chrome {
			want += CursorField
		}
		if got := len([]rune(row.Text)); got != want {
			t.Fatalf("row is %d cells wide, want %d: %q", got, want, row.Text)
		}
	}
}

func TestAgendaSpellsAStartDateDifferentlyFromADeadline(t *testing.T) {
	// A deadline and an available-from date are not the same promise. The
	// column marks the second so a scan cannot read one as the other.
	harness := newModelHarness(t, harnessOptions{})
	for _, row := range agendaRowsAt(t, harness, 78) {
		if row.Item == nil || row.Item.Deadline != "" || row.Item.Scheduled == "" {
			continue
		}
		if !strings.Contains(row.Text, "~") {
			t.Fatalf("a start date was spelled as a deadline: %q", row.Text)
		}
		return
	}
	t.Skip("the fixture has no scheduled-only agenda row")
}

func TestAgendaKeepsTheDateWhenThereIsNoRoomForAColumn(t *testing.T) {
	// Degrade, never drop: a pane too narrow for a column still has to say when
	// the task is due.
	harness := newModelHarness(t, harnessOptions{})
	rows := agendaRowsAt(t, harness, 20)
	for _, row := range rows {
		if row.Item != nil && row.Item.Deadline != "" {
			if !strings.Contains(row.Text, "ago") && !strings.Contains(row.Text, "-") {
				t.Fatalf("a narrow agenda row lost its date: %q", row.Text)
			}
			return
		}
	}
	t.Fatal("no dated agenda row rendered")
}

func TestPastStartDateIsStartableRatherThanOverdue(t *testing.T) {
	if got := dayBucket(-3, true); got != bucketToday {
		t.Fatalf("a start date three days ago bucketed as %q, want today", got)
	}
	if got := dayBucket(-3, false); got != bucketOverdue {
		t.Fatalf("a deadline three days ago bucketed as %q, want overdue", got)
	}
}

func TestDraggingTheRailResizesThePanelFromTheDragOrigin(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.paths.Mouse = true
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()

	layout := harness.model.Layout()
	divider := layout.PanelDividerCol()
	bodyBegin, _ := layout.BodyRows()
	before := layout.PanelWidth

	if !harness.model.HandleMouse(tea.MouseClickMsg{
		X: divider, Y: bodyBegin, Button: tea.MouseLeft}) {
		t.Fatal("a press on the rail was not consumed")
	}
	if harness.model.Selected() != layout.Selected {
		t.Fatal("pressing the rail moved the list selection")
	}
	// Two motions from the same press. The second is absolute against the drag
	// ORIGIN, so a coalesced or dropped event cannot make the panel drift.
	harness.model.HandleMouse(tea.MouseMotionMsg{X: divider - 4, Y: bodyBegin})
	harness.model.HandleMouse(tea.MouseMotionMsg{X: divider - 6, Y: bodyBegin})
	if got := harness.model.Layout().PanelWidth; got != before+6 {
		t.Fatalf("the panel is %d cells wide after a 6-cell drag, want %d", got, before+6)
	}

	harness.model.HandleMouse(tea.MouseReleaseMsg{X: divider - 6, Y: bodyBegin})
	harness.model.HandleMouse(tea.MouseMotionMsg{X: divider - 20, Y: bodyBegin})
	if got := harness.model.Layout().PanelWidth; got != before+6 {
		t.Fatalf("motion after the release kept resizing: %d", got)
	}
}

func TestADetailPanelSpendsNoRowsOnATitleBar(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()
	layout := harness.model.Layout()
	lines := harness.model.panelColumn(layout,
		harness.model.panel.View(PlainStyler{}, layout.BodyHeight, layout.PanelContentWidth))
	// The rail opens on its own rule, whose label is the task's state.
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "NEXT ") {
		t.Fatalf("the detail panel did not open on its own section rule: %q", lines)
	}
}

// -- small helpers ---------------------------------------------------------

func agendaDump(rows []Row) string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Text)
	}
	return strings.Join(out, "\n")
}

// headingTally is the trailing number a heading row carries. Outline headings
// are selectable rows that lead with a fold marker; the marker is not part of
// the label.
func headingTally(rows []Row, label string) string {
	for _, row := range rows {
		if row.Item != nil {
			continue
		}
		text := strings.TrimSpace(row.Text)
		text = strings.TrimPrefix(strings.TrimPrefix(text, MarkExpanded), MarkCollapsed)
		if strings.HasPrefix(text, label+" ") {
			fields := strings.Fields(row.Text)
			return fields[len(fields)-1]
		}
	}
	return ""
}
