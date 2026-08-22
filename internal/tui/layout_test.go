package tui

import (
	"strings"
	"testing"
)

func layoutOf(request LayoutRequest) ScreenLayout {
	return NewScreenLayout(PlainStyler{}, request)
}

func TestBodyHeightReservesTheFixedRowsAndTheFooter(t *testing.T) {
	layout := layoutOf(LayoutRequest{Width: 80, Height: 24, Footer: []string{"a", "b"}})
	if layout.BodyHeight != 24-FixedRows-2 {
		t.Fatalf("body height %d", layout.BodyHeight)
	}
	if layout.BodyWidth != 76 {
		t.Fatalf("body width %d", layout.BodyWidth)
	}
}

func TestBodyHeightNeverGoesBelowOneRow(t *testing.T) {
	layout := layoutOf(LayoutRequest{Width: 8, Height: 6, Footer: []string{"a", "b", "c", "d"}})
	if layout.BodyHeight < 1 {
		t.Fatalf("body height %d", layout.BodyHeight)
	}
}

func TestFooterIsTrimmedToTheRowsTheFrameCanSpare(t *testing.T) {
	// The chrome rows plus one body row are reserved first; the LAST footer
	// lines survive, because the newest status line is the one the user is
	// waiting for.
	layout := layoutOf(LayoutRequest{Width: 80, Height: 7, Footer: []string{"a", "b", "c", "d", "e"}})
	if len(layout.Footer) != 3 {
		t.Fatalf("kept %d footer rows: %v", len(layout.Footer), layout.Footer)
	}
	if layout.Footer[2] != "e" {
		t.Fatalf("kept the wrong end of the footer: %v", layout.Footer)
	}
}

func TestPanelWidthLadder(t *testing.T) {
	cases := []struct {
		mode  string
		width int
	}{
		{"compact", EditMinContentWidth + 2},
		{"standard", 38},
		{"wide", 56},
		{"focus", 88},
	}
	for _, testCase := range cases {
		layout := layoutOf(LayoutRequest{Width: 100, Height: 30, Panel: true, PanelMode: testCase.mode})
		if layout.PanelWidth != testCase.width {
			t.Errorf("%s panel width %d, want %d", testCase.mode, layout.PanelWidth, testCase.width)
		}
		if layout.ListWidth != layout.BodyWidth-layout.PanelWidth {
			t.Errorf("%s list width does not complement the panel", testCase.mode)
		}
	}
}

func TestPanelKeepsTheListItsMinimumWidth(t *testing.T) {
	layout := layoutOf(LayoutRequest{Width: 40, Height: 30, Panel: true, PanelMode: "focus"})
	if layout.ListWidth < MinListWidth {
		t.Fatalf("list squeezed to %d columns", layout.ListWidth)
	}
}

func TestUnknownPanelModeDegradesToStandard(t *testing.T) {
	layout := layoutOf(LayoutRequest{Width: 100, Height: 30, Panel: true, PanelMode: "enormous"})
	if layout.PanelMode != "standard" {
		t.Fatalf("panel mode %q", layout.PanelMode)
	}
}

func TestPanelOffsetRidesOnTopOfTheModeWidth(t *testing.T) {
	base := layoutOf(LayoutRequest{Width: 100, Height: 30, Panel: true, PanelMode: "standard"})
	shifted := layoutOf(LayoutRequest{
		Width: 100, Height: 30, Panel: true, PanelMode: "standard", PanelOffset: 6,
	})
	if shifted.PanelWidth != base.PanelWidth+6 {
		t.Fatalf("offset panel width %d, base %d", shifted.PanelWidth, base.PanelWidth)
	}
	if shifted.PanelMode != "standard" {
		t.Fatal("an offset changed the panel MODE, which it must not")
	}
}

func TestPanelOffsetIsClampedToTheListMinimum(t *testing.T) {
	layout := layoutOf(LayoutRequest{
		Width: 100, Height: 30, Panel: true, PanelMode: "standard", PanelOffset: 900,
	})
	if layout.ListWidth < MinListWidth {
		t.Fatalf("a large offset ate the list: %d columns", layout.ListWidth)
	}
}

func TestEditingPromotesThePanelUpTheLadderUntilItFits(t *testing.T) {
	// A compact panel in a narrow terminal cannot hold the editor, so the
	// promotion ladder walks up until one can.
	layout := layoutOf(LayoutRequest{
		Width: 60, Height: 30, Panel: true, PanelMode: "compact", Editing: true,
	})
	if !layout.EditablePanel() {
		t.Fatalf("editing promotion left an uneditable panel: mode %q content %d",
			layout.PanelMode, layout.PanelContentWidth)
	}
}

func TestContentBreakpointLadder(t *testing.T) {
	cases := []struct {
		width int
		want  string
	}{
		{20, "below_minimum"},
		{EditMinContentWidth, "stacked"},
		{InlineContentWidth, "inline"},
	}
	for _, testCase := range cases {
		layout := ScreenLayout{PanelContentWidth: testCase.width}
		if got := layout.ContentBreakpoint(); got != testCase.want {
			t.Errorf("width %d gave %q, want %q", testCase.width, got, testCase.want)
		}
	}
}

func TestViewportScrollsOnlyOnceTheSelectionLeavesTheBody(t *testing.T) {
	layout := layoutOf(LayoutRequest{
		Width: 80, Height: 24, Selected: 5, HasSelection: true,
	})
	if layout.ViewportOffset != 0 {
		t.Fatalf("scrolled early: offset %d", layout.ViewportOffset)
	}
	scrolled := layoutOf(LayoutRequest{
		Width: 80, Height: 24, Selected: 40, HasSelection: true,
	})
	if scrolled.SelectedScreenRow != scrolled.BodyHeight-1 {
		t.Fatalf("selection landed on screen row %d of %d",
			scrolled.SelectedScreenRow, scrolled.BodyHeight)
	}
}

func TestRectanglesAreContiguousAndAgreeWithEachOther(t *testing.T) {
	layout := layoutOf(LayoutRequest{
		Width: 100, Height: 30, Footer: []string{"one"}, Panel: true, PanelMode: "standard",
	})
	bodyBegin, bodyEnd := layout.BodyRows()
	footerBegin, _ := layout.FooterRows()
	if bodyEnd+1 != footerBegin {
		t.Fatalf("one blank row must sit between the body (ends %d) and the footer (starts %d)",
			bodyEnd, footerBegin)
	}
	if bodyBegin != 2 {
		t.Fatalf("body starts at row %d", bodyBegin)
	}
	listBegin, listEnd := layout.ListCols()
	if listBegin != 2 || listEnd != 2+layout.ListWidth {
		t.Fatalf("list columns %d..%d", listBegin, listEnd)
	}
	if layout.PanelDividerCol() != listEnd {
		t.Fatal("the divider does not sit where the list ends")
	}
	panelBegin, panelEnd := layout.PanelCols()
	if panelBegin != listEnd+2 || panelEnd != 2+layout.BodyWidth {
		t.Fatalf("panel columns %d..%d", panelBegin, panelEnd)
	}
}

func TestNoPanelMeansNoPanelColumns(t *testing.T) {
	layout := layoutOf(LayoutRequest{Width: 80, Height: 24})
	if layout.HasPanel() || layout.PanelDividerCol() != -1 {
		t.Fatal("a frame with no panel reported panel geometry")
	}
	begin, end := layout.PanelCols()
	if begin != 0 || end != 0 {
		t.Fatalf("panel columns %d..%d with no panel", begin, end)
	}
}

func TestMinimumEditTerminalSize(t *testing.T) {
	if got := MinimumEditTerminalWidth(); got != 4+MinListWidth+2+EditMinContentWidth {
		t.Fatalf("minimum edit width %d", got)
	}
	if got := MinimumEditTerminalHeight(2); got != FixedRows+2+EditPanelChromeRows+EditMinVisibleRows {
		t.Fatalf("minimum edit height %d", got)
	}
}

func TestVisibleRowsWindowsTheList(t *testing.T) {
	rows := make([]Row, 50)
	for index := range rows {
		rows[index] = headerRow("row")
	}
	layout := layoutOf(LayoutRequest{Width: 80, Height: 24, Selected: 40, HasSelection: true})
	visible := layout.VisibleRows(rows)
	if len(visible) != layout.BodyHeight {
		t.Fatalf("visible %d rows, body holds %d", len(visible), layout.BodyHeight)
	}
}

// The main frame draws NO border of its own: section rules may use "─", but
// corners and edges belong to the popups, and the body keeps one blank cell of
// separation on each edge. This is the regression guard for that decision.
func TestTheFrameDrawsNoBorderAndSeparatesWithSpace(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewAgenda)
	lines := strings.Split(renderAt(t, harness, 80, 20), "\n")

	for index, line := range lines {
		// Section rules legitimately draw "─"; a FRAME is corners and edges.
		if strings.ContainsAny(line, "┌┐└┘├┤") {
			t.Fatalf("line %d still draws frame chrome: %q", index, line)
		}
		cells := []rune(line)
		if cells[0] != ' ' || cells[len(cells)-1] != ' ' {
			t.Fatalf("line %d still has an edge cell: %q", index, line)
		}
		if got := len([]rune(line)); got != 80 {
			t.Fatalf("line %d is %d cells wide", index, got)
		}
	}
	layout := harness.model.Layout()
	bodyBegin, bodyEnd := layout.BodyRows()
	for _, blank := range []int{bodyBegin - 1, bodyEnd} {
		if strings.TrimSpace(lines[blank]) != "" {
			t.Fatalf("row %d should be the blank that replaced a rule: %q", blank, lines[blank])
		}
	}
	if strings.TrimSpace(lines[layout.HeaderRow()]) == "" {
		t.Fatal("the header row is blank")
	}
}
