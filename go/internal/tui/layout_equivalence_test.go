package tui

import (
	"fmt"
	"testing"

	"tasks-go/internal/tui/term/layout"
)

// Both halves of the TUI ported lib/tui/screen_layout.rb independently: this
// package's ScreenLayout, which the model and the renderer use, and
// term/layout, which the hit map and the frame builder use.
//
// Duplicated geometry is a latent bug with a specific and nasty symptom. The
// hit map decides which row a click lands on; the renderer decides which row
// the user sees. If the two ever disagree by a single line, a click selects a
// task the user was not pointing at — and in a list where the next keystroke
// might be `x`, that is the wrong task completed.
//
// This test is the guard until the duplication is removed. It is deliberately
// exhaustive over the sizes and modes that exercise both width ladders, the
// panel promotion ladder, and the editing rows, because the disagreements that
// matter appear at the breakpoints rather than in the middle of a range.
//
// term/layout is the lower-level package and the one to keep: `term` must not
// import `tui`, so consolidation means this package adopting term/layout, not
// the reverse.
func TestBothScreenLayoutPortsAgree(t *testing.T) {
	footers := [][]string{
		{"j/k · 1-6 views · q quit"},
		{"j/k · 1-6 views", "enter details · / search"},
	}
	for _, width := range []int{20, 40, 44, 60, 72, 80, 100, 120, 160, 200} {
		for _, height := range []int{8, 12, 16, 24, 40} {
			for _, panel := range []bool{false, true} {
				for _, mode := range []string{"compact", "standard", "wide", "focus"} {
					for _, editing := range []bool{false, true} {
						for _, selected := range []int{0, 3, 25} {
							for _, footer := range footers {
								assertLayoutsAgree(t, LayoutRequest{
									Width: width, Height: height, Footer: footer,
									Selected: selected, HasSelection: true,
									Panel: panel, PanelMode: mode, Editing: editing,
								})
							}
						}
					}
				}
			}
		}
	}
}

// A layout with no selection is its own case: the two ports spell "no
// selection" differently (a bool pair here, a nil pointer there), which is
// exactly the kind of seam a translation gets wrong.
func TestBothScreenLayoutPortsAgreeWithoutASelection(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		for _, height := range []int{12, 24} {
			for _, panel := range []bool{false, true} {
				assertLayoutsAgree(t, LayoutRequest{
					Width: width, Height: height, Footer: []string{"q quit"},
					HasSelection: false, Panel: panel, PanelMode: "standard",
				})
			}
		}
	}
}

func assertLayoutsAgree(t *testing.T, request LayoutRequest) {
	t.Helper()

	mine := NewScreenLayout(PlainStyler{}, request)

	options := layout.Options{
		Width: request.Width, Height: request.Height,
		Panel: request.Panel, PanelMode: layout.PanelMode(request.PanelMode),
		PanelOffset: request.PanelOffset, Editing: request.Editing,
	}
	for _, line := range request.Footer {
		options.Footer = append(options.Footer, layout.Text(line))
	}
	if request.HasSelection {
		selected := request.Selected
		options.Selected = &selected
	}
	theirs := layout.New(options)

	where := fmt.Sprintf("%dx%d panel=%v mode=%s editing=%v selected=%d/%v footer=%d",
		request.Width, request.Height, request.Panel, request.PanelMode,
		request.Editing, request.Selected, request.HasSelection, len(request.Footer))

	equal := func(field string, mineValue, theirsValue int) {
		t.Helper()
		if mineValue != theirsValue {
			t.Errorf("%s: %s = %d (tui) vs %d (term/layout)", where, field, mineValue, theirsValue)
		}
	}

	equal("BodyHeight", mine.BodyHeight, theirs.BodyHeight)
	equal("BodyWidth", mine.BodyWidth, theirs.BodyWidth)
	equal("ListWidth", mine.ListWidth, theirs.ListWidth)
	equal("PanelWidth", mine.PanelWidth, theirs.PanelWidth)
	equal("PanelContentWidth", mine.PanelContentWidth, theirs.PanelContentWidth)
	equal("EditContentHeight", mine.EditContentHeight, theirs.EditContentHeight)
	equal("FooterSize", mine.FooterSize(), theirs.FooterSize())

	if mine.HasPanel() != theirs.HasPanel() {
		t.Errorf("%s: HasPanel = %v vs %v", where, mine.HasPanel(), theirs.HasPanel())
	}
	if string(theirs.PanelMode) != mine.PanelMode {
		t.Errorf("%s: PanelMode = %q vs %q", where, mine.PanelMode, theirs.PanelMode)
	}
	if string(theirs.RequestedPanelMode) != mine.RequestedMode {
		t.Errorf("%s: RequestedMode = %q vs %q", where, mine.RequestedMode, theirs.RequestedPanelMode)
	}

	// The viewport and the selected screen row are what the hit map reads, so
	// they are the two this test exists for.
	equal("ViewportOffset", mine.ViewportOffset, theirs.ViewportOffset)
	switch {
	case request.HasSelection && theirs.SelectedScreenRow == nil:
		t.Errorf("%s: term/layout reports no selected row, tui reports %d", where, mine.SelectedScreenRow)
	case request.HasSelection:
		equal("SelectedScreenRow", mine.SelectedScreenRow, *theirs.SelectedScreenRow)
	case theirs.SelectedScreenRow != nil:
		t.Errorf("%s: term/layout reports selected row %d with no selection", where, *theirs.SelectedScreenRow)
	}
}
