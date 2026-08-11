package layout

import (
	"testing"
)

// Mirrors test/test_screen_layout.rb.

func sel(n int) *int { return &n }

func lines(texts ...string) []FooterLine {
	out := make([]FooterLine, 0, len(texts))
	for _, t := range texts {
		out = append(out, Text(t))
	}
	return out
}

func TestOwnsFooterBodyViewportAndSelectedCoordinates(t *testing.T) {
	l := New(Options{Width: 42, Height: 12, Footer: lines("a", "b", "c"), Selected: sel(9)})
	if l.FooterSize() != 3 {
		t.Fatalf("footer size = %d", l.FooterSize())
	}
	if l.BodyHeight != 6 || l.BodyWidth != 38 {
		t.Fatalf("body = %dx%d", l.BodyWidth, l.BodyHeight)
	}
	if l.ViewportOffset != 4 {
		t.Fatalf("viewport offset = %d", l.ViewportOffset)
	}
	if l.SelectedScreenRow == nil || *l.SelectedScreenRow != 5 {
		t.Fatalf("selected screen row = %v", l.SelectedScreenRow)
	}
	all := make([]int, 13)
	for i := range all {
		all[i] = i
	}
	visible := VisibleRows(l, all)
	want := []int{4, 5, 6, 7, 8, 9}
	if len(visible) != len(want) {
		t.Fatalf("visible = %v", visible)
	}
	for i := range want {
		if visible[i] != want[i] {
			t.Fatalf("visible = %v, want %v", visible, want)
		}
	}
}

func TestShortTerminalPreservesActionableFooterTail(t *testing.T) {
	l := New(Options{Width: 8, Height: 5, Footer: lines("old", "response", "flash", "prompt"), Selected: sel(0)})
	if len(l.Footer) != 1 || l.Footer[0].Text != "prompt" {
		t.Fatalf("footer = %#v", l.Footer)
	}
	if l.BodyHeight != 1 || l.BodyWidth != 4 {
		t.Fatalf("body = %dx%d", l.BodyWidth, l.BodyHeight)
	}
}

// Ruby freezes the footer array and its strings; Go copies the slice, which
// gives the same guarantee for the only mutation Ruby could suffer (a caller
// appending to the source array after construction).
func TestFooterIsASnapshotOfTheCallersSlice(t *testing.T) {
	source := lines("prompt")
	l := New(Options{Width: 20, Height: 8, Footer: source, Selected: sel(5)})
	source = append(source, Text("another"))
	source[0] = Text("changed")
	if len(l.Footer) != 1 || l.Footer[0].Text != "prompt" {
		t.Fatalf("footer = %#v", l.Footer)
	}
	if l.BodyHeight != 4 {
		t.Fatalf("body height = %d", l.BodyHeight)
	}
}

func TestPopupPlacementUsesViewportSelectionAndClamps(t *testing.T) {
	popup := Popup{Lines: []string{"123456", "abcdef"}, Row: 99, Col: 99}

	below := New(Options{Width: 14, Height: 11, Selected: sel(1)}).PlacePopup(popup, 8)
	if below.Row != 2 || below.Col != 4 {
		t.Fatalf("below = (%d,%d), want (2,4)", below.Row, below.Col)
	}
	above := New(Options{Width: 14, Height: 11, Selected: sel(7)}).PlacePopup(popup, 8)
	if above.Row != 5 || above.Col != 4 {
		t.Fatalf("above = (%d,%d), want (5,4)", above.Row, above.Col)
	}
}

func TestModalPlacementIsFrozenToSampledFrame(t *testing.T) {
	modal := Modal{Title: "Details", Lines: []string{"one", "two"}, Width: 20}
	wide := New(Options{Width: 80, Height: 24, Footer: lines("prompt"), Selected: sel(0)})
	narrow := New(Options{Width: 30, Height: 10, Footer: lines("prompt"), Selected: sel(0)})

	if placed := wide.PlaceModal(modal); placed.Row != 8 || placed.Col != 28 {
		t.Fatalf("wide = (%d,%d), want (8,28)", placed.Row, placed.Col)
	}
	if placed := narrow.PlaceModal(modal); placed.Row != 1 || placed.Col != 3 {
		t.Fatalf("narrow = (%d,%d), want (1,3)", placed.Row, placed.Col)
	}
	if wide.Width != 80 || wide.Height != 24 {
		t.Fatal("later placement must not mutate an existing frame")
	}
}

func TestRightPanelUsesStablePercentageAndPreservesListSpace(t *testing.T) {
	l := New(Options{Width: 100, Height: 24, Footer: lines("prompt"), Selected: sel(0), Panel: true})
	if l.BodyWidth != 96 || l.PanelWidth != 38 || l.PanelContentWidth != 36 || l.ListWidth != 58 {
		t.Fatalf("body=%d panel=%d content=%d list=%d",
			l.BodyWidth, l.PanelWidth, l.PanelContentWidth, l.ListWidth)
	}
	sameWidth := New(Options{Width: 100, Height: 12, Selected: sel(0), Panel: true})
	if sameWidth.PanelWidth != l.PanelWidth {
		t.Fatal("panel width depends on terminal width, not content or height")
	}
}

func TestPanelContentWidthsCharacterizeEditorBreakpointBoundaries(t *testing.T) {
	expected := map[int]int{87: 31, 89: 32, 126: 47, 129: 48}
	for terminalWidth, contentWidth := range expected {
		l := New(Options{Width: terminalWidth, Height: 24, Selected: sel(0), Panel: true})
		if l.PanelContentWidth != contentWidth {
			t.Fatalf("width %d: content = %d, want %d", terminalWidth, l.PanelContentWidth, contentWidth)
		}
		want := Inline
		if contentWidth < EditMinContentWidth {
			want = BelowMinimum
		} else if contentWidth < InlineContentWidth {
			want = Stacked
		}
		if l.ContentBreakpoint() != want {
			t.Fatalf("width %d: breakpoint = %s, want %s", terminalWidth, l.ContentBreakpoint(), want)
		}
		if l.ListWidth < MinListWidth {
			t.Fatalf("width %d: list starved at %d", terminalWidth, l.ListWidth)
		}
	}
}

func TestNamedPanelModesAreCentrallyResolved(t *testing.T) {
	expected := map[PanelMode]int{PanelCompact: 32, PanelStandard: 36, PanelWide: 54, PanelFocus: 86}
	for mode, contentWidth := range expected {
		l := New(Options{Width: 100, Height: 24, Panel: true, PanelMode: mode})
		if l.PanelMode != mode {
			t.Fatalf("mode = %s, want %s", l.PanelMode, mode)
		}
		if l.PanelContentWidth != contentWidth {
			t.Fatalf("%s content width = %d, want %d", mode, l.PanelContentWidth, contentWidth)
		}
		if l.ListWidth+l.PanelWidth != 96 {
			t.Fatalf("%s does not divide the body: %d + %d", mode, l.ListWidth, l.PanelWidth)
		}
	}
}

func TestPanelOffsetShiftsOneColumnEachDirection(t *testing.T) {
	base := New(Options{Width: 100, Height: 24, Panel: true}).PanelWidth
	grow := New(Options{Width: 100, Height: 24, Panel: true, PanelOffset: 1})
	shrink := New(Options{Width: 100, Height: 24, Panel: true, PanelOffset: -1})
	if grow.PanelWidth != base+1 || shrink.PanelWidth != base-1 {
		t.Fatalf("grow=%d shrink=%d base=%d", grow.PanelWidth, shrink.PanelWidth, base)
	}
	if grow.ListWidth+grow.PanelWidth != 96 {
		t.Fatal("the list must absorb the opposite move")
	}
}

func TestPanelOffsetClampsToListAndBodyInvariants(t *testing.T) {
	hi := New(Options{Width: 100, Height: 24, Panel: true, PanelOffset: 1000})
	lo := New(Options{Width: 100, Height: 24, Panel: true, PanelOffset: -1000})
	if hi.PanelWidth != 96-MinListWidth || hi.ListWidth < MinListWidth {
		t.Fatalf("grown panel = %d, list = %d", hi.PanelWidth, hi.ListWidth)
	}
	if lo.PanelWidth != 3 {
		t.Fatalf("shrunk panel = %d, want the read-mode floor of 3", lo.PanelWidth)
	}
}

func TestPanelOffsetNeverStarvesTheEditorContentMinimum(t *testing.T) {
	l := New(Options{Width: 100, Height: 24, Panel: true, PanelOffset: -1000, Editing: true})
	if l.PanelContentWidth < EditMinContentWidth || !l.EditablePanel() {
		t.Fatalf("content = %d, editable = %v", l.PanelContentWidth, l.EditablePanel())
	}
}

func TestPanelOffsetRidesRatioBaseAcrossTerminalResize(t *testing.T) {
	narrow := New(Options{Width: 100, Height: 24, Panel: true, PanelOffset: 4})
	wide := New(Options{Width: 120, Height: 24, Panel: true, PanelOffset: 4})
	narrowBase := New(Options{Width: 100, Height: 24, Panel: true}).PanelWidth
	wideBase := New(Options{Width: 120, Height: 24, Panel: true}).PanelWidth
	if narrowBase == wideBase {
		t.Fatal("the ratio base must adapt to terminal width")
	}
	if narrow.PanelWidth != narrowBase+4 || wide.PanelWidth != wideBase+4 {
		t.Fatalf("offsets did not ride the base: %d/%d vs %d/%d",
			narrow.PanelWidth, narrowBase, wide.PanelWidth, wideBase)
	}
}

func TestEditingPromotesWithoutOverwritingRequestedReadPreference(t *testing.T) {
	l := New(Options{Width: 87, Height: 24, Panel: true, PanelMode: PanelStandard, Editing: true})
	if l.RequestedPanelMode != PanelStandard {
		t.Fatalf("requested = %s", l.RequestedPanelMode)
	}
	if l.PanelMode != PanelWide {
		t.Fatalf("promoted mode = %s, want wide", l.PanelMode)
	}
	if l.PanelContentWidth < EditMinContentWidth || !l.EditablePanel() {
		t.Fatalf("content = %d editable = %v", l.PanelContentWidth, l.EditablePanel())
	}
}

func TestEditingAdmissionIsExactAtMinimumTerminalWidth(t *testing.T) {
	below := New(Options{Width: 45, Height: 18, Panel: true, PanelMode: PanelCompact, Editing: true})
	exact := New(Options{Width: 46, Height: 18, Panel: true, PanelMode: PanelCompact, Editing: true})
	if below.PanelContentWidth != 31 || below.EditablePanel() {
		t.Fatalf("below: content = %d editable = %v", below.PanelContentWidth, below.EditablePanel())
	}
	if exact.PanelContentWidth != 32 || !exact.EditablePanel() {
		t.Fatalf("exact: content = %d editable = %v", exact.PanelContentWidth, exact.EditablePanel())
	}
	if MinimumEditTerminalWidth() != 46 {
		t.Fatalf("minimum width = %d", MinimumEditTerminalWidth())
	}
}

func TestEditingAdmissionIsExactAtNamedHeightAcrossWidthsAndModes(t *testing.T) {
	for _, mode := range PanelModes {
		for _, width := range []int{45, 46, 80, 120} {
			for _, height := range []int{4, 5, 6, 7} {
				l := New(Options{Width: width, Height: height, Panel: true, PanelMode: mode, Editing: true})
				want := width >= 46 && height >= 6
				if l.EditablePanel() != want {
					t.Fatalf("%s at %dx%d: editable = %v, want %v", mode, width, height, l.EditablePanel(), want)
				}
				wantHeight := height - 5
				if wantHeight < 0 {
					wantHeight = 0
				}
				if l.EditContentHeight != wantHeight {
					t.Fatalf("%s at %dx%d: content height = %d, want %d", mode, width, height, l.EditContentHeight, wantHeight)
				}
			}
		}
	}
	if MinimumEditTerminalHeight(0) != 6 || MinimumEditTerminalWidth() != 46 {
		t.Fatalf("minimum size = %dx%d", MinimumEditTerminalWidth(), MinimumEditTerminalHeight(0))
	}
}

func TestEachFooterRowRaisesEditHeightMinimum(t *testing.T) {
	below := New(Options{Width: 46, Height: 6, Footer: lines("message"), Panel: true, PanelMode: PanelFocus, Editing: true})
	exact := New(Options{Width: 46, Height: 7, Footer: lines("message"), Panel: true, PanelMode: PanelFocus, Editing: true})
	if below.EditablePanel() {
		t.Fatal("a footer row must consume an editor row")
	}
	if !exact.EditablePanel() {
		t.Fatal("one more terminal row must restore the editor")
	}
	if MinimumEditTerminalHeight(1) != 7 {
		t.Fatalf("minimum height with one footer row = %d", MinimumEditTerminalHeight(1))
	}
}

func TestRectanglesAreHalfOpenAndAgreeWithTheBodyOrigin(t *testing.T) {
	l := New(Options{Width: 80, Height: 24, Footer: lines("prompt"), Selected: sel(0), Panel: true})
	row, col := l.BodyOrigin()
	if row != 2 || col != 2 {
		t.Fatalf("body origin = (%d,%d)", row, col)
	}
	if l.HeaderRow() != 0 {
		t.Fatalf("header row = %d", l.HeaderRow())
	}
	if l.BodyRows().Begin != 2 || l.BodyRows().End != 2+l.BodyHeight {
		t.Fatalf("body rows = %#v", l.BodyRows())
	}
	if l.ListCols().Begin != 2 || l.ListCols().End != 2+l.ListWidth {
		t.Fatalf("list cols = %#v", l.ListCols())
	}
	if l.PanelDividerCol() != 2+l.ListWidth {
		t.Fatalf("divider col = %d", l.PanelDividerCol())
	}
	if l.PanelCols().Begin != 2+l.ListWidth+2 || l.PanelCols().End != 2+l.BodyWidth {
		t.Fatalf("panel cols = %#v", l.PanelCols())
	}
	if l.FooterRows().Begin != l.BodyHeight+3 {
		t.Fatalf("footer rows = %#v", l.FooterRows())
	}
	if l.BodyRows().Covers(1) || !l.BodyRows().Covers(2) || l.BodyRows().Covers(l.BodyRows().End) {
		t.Fatal("body rows span must be half-open")
	}
	noPanel := New(Options{Width: 80, Height: 24, Selected: sel(0)})
	if noPanel.PanelDividerCol() != -1 || noPanel.PanelCols() != (Span{0, 0}) {
		t.Fatal("a frame with no panel must publish no panel rectangles")
	}
}
