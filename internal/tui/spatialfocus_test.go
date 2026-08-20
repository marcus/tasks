package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSpatialFocusStopsUseRenderedLayoutGeometry(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)

	listOnly := harness.model.VisibleSpatialFocusStops()
	if len(listOnly) != 1 || listOnly[0].ID != SpatialFocusList {
		t.Fatalf("initial stops = %#v", listOnly)
	}
	harness.model.OpenDetail()

	for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 30}, {Width: 8, Height: 6}} {
		harness.model.Update(size)
		layout := harness.model.Layout()
		bodyBegin, bodyEnd := layout.BodyRows()
		listBegin, listEnd := layout.ListCols()
		panelBegin, panelEnd := layout.PanelCols()
		got := harness.model.VisibleSpatialFocusStops()
		want := []SpatialFocusStop{
			{ID: SpatialFocusList, Rect: ScreenRect{X: listBegin, Y: bodyBegin, Width: listEnd - listBegin, Height: bodyEnd - bodyBegin}},
			{ID: SpatialFocusDetail, Rect: ScreenRect{X: panelBegin, Y: bodyBegin, Width: panelEnd - panelBegin, Height: bodyEnd - bodyBegin}},
		}
		if len(got) != len(want) {
			t.Fatalf("%dx%d stops = %#v, want %#v", size.Width, size.Height, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%dx%d stop %d = %#v, want %#v", size.Width, size.Height, i, got[i], want[i])
			}
			if got[i].Rect.Width <= 0 || got[i].Rect.Height <= 0 {
				t.Fatalf("%dx%d exposed empty stop %#v", size.Width, size.Height, got[i])
			}
		}
	}
}

func TestSpatialFocusDirectSetAndDisappearanceAreSafe(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	if harness.model.SetSpatialFocus(SpatialFocusDetail) {
		t.Fatal("hidden detail accepted focus")
	}
	if harness.model.SetSpatialFocus(SpatialFocus("unknown")) {
		t.Fatal("unknown focus accepted")
	}

	harness.model.OpenDetail()
	if harness.model.CurrentSpatialFocus() != SpatialFocusDetail ||
		!harness.model.SetSpatialFocus(SpatialFocusList) ||
		harness.model.CurrentSpatialFocus() != SpatialFocusList ||
		!harness.model.SetSpatialFocus(SpatialFocusDetail) {
		t.Fatalf("direct focus failed: current=%q", harness.model.CurrentSpatialFocus())
	}
	harness.model.ClosePanel()
	if harness.model.CurrentSpatialFocus() != SpatialFocusList || len(harness.model.VisibleSpatialFocusStops()) != 1 {
		t.Fatalf("closed detail survived: current=%q stops=%#v",
			harness.model.CurrentSpatialFocus(), harness.model.VisibleSpatialFocusStops())
	}
}

func TestSpatialFocusControlsKeyRoutingAndFocusContext(t *testing.T) {
	list := newModelHarness(t, harnessOptions{})
	list.model.SwitchView(ViewNext)
	list.selectRowByID(fixFlight)
	list.model.OpenDetail()
	if !list.model.SetSpatialFocus(SpatialFocusList) || list.model.FocusContext() != "list" {
		t.Fatalf("list focus/context = %q/%q", list.model.CurrentSpatialFocus(), list.model.FocusContext())
	}
	list.pressKeys("e")
	if list.model.Mode() == ModeTaskEdit {
		t.Fatal("detail-only edit command ran while list owned spatial focus")
	}

	detail := newModelHarness(t, harnessOptions{})
	detail.model.SwitchView(ViewNext)
	detail.selectRowByID(fixFlight)
	detail.model.OpenDetail()
	if detail.model.FocusContext() != "detail" {
		t.Fatalf("detail context = %q", detail.model.FocusContext())
	}
	detail.pressKeys("e")
	if detail.model.Mode() != ModeTaskEdit || !detail.model.TabOwnsFocus() {
		t.Fatalf("detail edit mode=%q owns-tab=%v", detail.model.Mode(), detail.model.TabOwnsFocus())
	}
}

func TestSpatialFocusFollowsListAndDetailClicks(t *testing.T) {
	harness := mouseHarness(t, "")
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()
	if !harness.model.SetSpatialFocus(SpatialFocusList) {
		t.Fatal("could not prepare list focus")
	}
	layout := harness.model.Layout()
	bodyBegin, _ := layout.BodyRows()
	panelBegin, _ := layout.PanelCols()
	if !harness.model.HandleMouse(tea.MouseClickMsg{X: panelBegin + 1, Y: bodyBegin, Button: tea.MouseLeft}) ||
		harness.model.CurrentSpatialFocus() != SpatialFocusDetail {
		t.Fatalf("panel click focus=%q", harness.model.CurrentSpatialFocus())
	}
	listBegin, _ := layout.ListCols()
	if !harness.model.HandleMouse(tea.MouseClickMsg{X: listBegin, Y: bodyBegin, Button: tea.MouseLeft}) ||
		harness.model.CurrentSpatialFocus() != SpatialFocusList {
		t.Fatalf("list click focus=%q", harness.model.CurrentSpatialFocus())
	}
}

func TestSpatialFocusProjectsResponseContexts(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()
	harness.model.respOpen = true
	if got := harness.model.FocusContext(); got != "response_detail" {
		t.Fatalf("detail response context = %q", got)
	}
	harness.model.SetSpatialFocus(SpatialFocusList)
	if got := harness.model.FocusContext(); got != "response" {
		t.Fatalf("list response context = %q", got)
	}
}

func TestEveryInputAndOverlayModeOwnsTab(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for _, mode := range []Mode{
		ModePrompt, ModeFilter, ModeModal, ModeModalFilter, ModeForm,
		ModeFieldModal, ModePalette, ModeContextPalette, ModeLinkPicker, ModeTaskEdit,
	} {
		harness.model.mode = mode
		if !harness.model.TabOwnsFocus() {
			t.Errorf("mode %q yielded Tab to its host", mode)
		}
	}
	harness.model.mode = ModeList
	for _, responseOpen := range []bool{false, true} {
		harness.model.respOpen = responseOpen
		if harness.model.TabOwnsFocus() {
			t.Errorf("passive list response=%v unexpectedly owns Tab", responseOpen)
		}
	}
}
