package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/config"
)

func mouseHarness(t *testing.T, live string) *modelHarness {
	t.Helper()
	return newModelHarness(t, harnessOptions{
		live:  live,
		paths: func(paths *config.Paths) { paths.Mouse = true },
	})
}

func TestMouseIsOffUnlessTheConfigAsksForIt(t *testing.T) {
	// Turning mouse reporting on takes the terminal's own text selection away.
	// A user who turned it off must not have it turned back on by the port.
	harness := newModelHarness(t, harnessOptions{})
	if harness.model.MouseEnabled() {
		t.Fatal("mouse is on with mouse: false")
	}
	if harness.model.HandleMouse(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft}) {
		t.Fatal("a click was consumed with the mouse disabled")
	}
}

func TestClickingARowSelectsIt(t *testing.T) {
	harness := mouseHarness(t, "")
	harness.model.SwitchView(ViewNext)
	layout := harness.model.Layout()
	bodyBegin, _ := layout.BodyRows()

	// Row index 1 is the first selectable row under the @computer header.
	consumed := harness.model.HandleMouse(tea.MouseClickMsg{X: 6, Y: bodyBegin + 1, Button: tea.MouseLeft})
	if !consumed {
		t.Fatal("the click was not consumed")
	}
	if harness.model.Selected() != 1 {
		t.Fatalf("click selected row %d, want 1", harness.model.Selected())
	}
}

func TestClickingAHeaderRowSelectsNothing(t *testing.T) {
	harness := mouseHarness(t, "")
	harness.model.SwitchView(ViewNext)
	before := harness.model.Selected()
	layout := harness.model.Layout()
	bodyBegin, _ := layout.BodyRows()

	// The click belongs to the list — it blurs the prompt, so it is consumed —
	// but a heading is not a row and the cursor must not move to it.
	if !harness.model.HandleMouse(tea.MouseClickMsg{X: 6, Y: bodyBegin, Button: tea.MouseLeft}) {
		t.Fatal("a click in the list was not consumed")
	}
	if harness.model.Selected() != before {
		t.Fatal("a click on a header moved the cursor")
	}
}

func TestClickingBelowTheLastRowDoesNothing(t *testing.T) {
	harness := mouseHarness(t, "")
	harness.model.SwitchView(ViewNext)
	before := harness.model.Selected()
	_, bodyEnd := harness.model.Layout().BodyRows()
	harness.model.HandleMouse(tea.MouseClickMsg{X: 6, Y: bodyEnd - 1, Button: tea.MouseLeft})
	if harness.model.Selected() != before {
		t.Fatal("a click on empty body space moved the cursor")
	}
}

func TestClickingTheFoldMarkerTogglesTheSubtree(t *testing.T) {
	harness := mouseHarness(t, nestedStore)
	harness.model.SwitchView(ViewNext)

	index, row := -1, Row{}
	for candidate, current := range harness.model.Rows() {
		if current.HasMarker() {
			index, row = candidate, current
			break
		}
	}
	if index < 0 {
		t.Fatalf("no row carries a fold marker:\n%s", rowTexts(harness))
	}
	layout := harness.model.Layout()
	bodyBegin, _ := layout.BodyRows()
	listBegin, _ := layout.ListCols()
	column := listBegin + CursorField + row.MarkerBegin

	harness.model.HandleMouse(tea.MouseClickMsg{X: column, Y: bodyBegin + index, Button: tea.MouseLeft})
	if !harness.model.collapsed[row.Item.ID] {
		t.Fatal("clicking the marker did not fold the subtree")
	}
	harness.model.HandleMouse(tea.MouseClickMsg{X: column, Y: bodyBegin + index, Button: tea.MouseLeft})
	if harness.model.collapsed[row.Item.ID] {
		t.Fatal("clicking the marker again did not unfold the subtree")
	}
}

func TestClickingATabSwitchesToIt(t *testing.T) {
	harness := mouseHarness(t, "")
	layout := harness.model.Layout()
	variant := harness.model.tabVariant(harness.model.tabBudget(layout))
	column := 2
	for _, tab := range Tabs {
		width := harness.model.styler.Width(harness.model.tabCell(tab, variant))
		harness.model.HandleMouse(tea.MouseClickMsg{
			X: column, Y: layout.HeaderRow(), Button: tea.MouseLeft,
		})
		if harness.model.view != tab.Key {
			t.Fatalf("clicking column %d selected %q, want %q", column, harness.model.view, tab.Key)
		}
		column += width + 1
	}
}

func TestWheelMovesTheSelectionOverTheList(t *testing.T) {
	harness := mouseHarness(t, "")
	harness.model.SwitchView(ViewQuadrants)
	before := harness.model.Selected()
	harness.model.HandleMouse(tea.MouseWheelMsg{X: 6, Y: 5, Button: tea.MouseWheelDown})
	if harness.model.Selected() == before {
		t.Fatal("the wheel did not move the selection")
	}
}

func TestWheelScrollsThePanelWhenThePointerIsOverIt(t *testing.T) {
	harness := mouseHarness(t, "")
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()
	harness.model.Panel().Lines = linesOf(200)
	beforeSelection := harness.model.Selected()

	layout := harness.model.Layout()
	panelBegin, _ := layout.PanelCols()
	harness.model.HandleMouse(tea.MouseWheelMsg{X: panelBegin + 1, Y: 5, Button: tea.MouseWheelDown})

	if harness.model.Panel().Scroll == 0 {
		t.Fatal("the wheel over the panel did not scroll it")
	}
	if harness.model.Selected() != beforeSelection {
		t.Fatal("the wheel over the panel also moved the list selection")
	}
}
