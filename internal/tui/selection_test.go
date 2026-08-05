package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Selection stability is the contract most easily lost in a rewrite, so it gets
// the most tests. A list that reorders under the cursor is how a user completes
// the wrong task.

func TestSelectionSurvivesAnExternalWriteThatReordersRows(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)

	// A new, higher-priority @home task lands above the selection.
	harness.rewrite(fixtureStore +
		`{"type":"task","id":"aaaa000b","parent":"aaaa0009","state":"NEXT","priority":"A","title":"Urgent home thing","tags":["@home"]}` + "\n")
	harness.tick()

	if got := harness.model.SelectedID(); got != fixPlants {
		t.Fatalf("selection moved to %q after a reordering refresh; rows are\n%s",
			got, strings.Join(harness.titles(), "\n"))
	}
	if item := harness.model.CurrentItem(); item == nil || item.ID != fixPlants {
		t.Fatalf("cursor is not on the selected id after refresh: %+v", item)
	}
}

func TestSelectionFallsBackToTheNearestRowWhenItsTaskDisappears(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPR)
	before := harness.model.Selected()

	// Drop the selected task entirely.
	lines := []string{}
	for _, line := range strings.Split(strings.TrimRight(fixtureStore, "\n"), "\n") {
		if !strings.Contains(line, `"`+fixPR+`"`) {
			lines = append(lines, line)
		}
	}
	harness.rewrite(strings.Join(lines, "\n") + "\n")
	harness.tick()

	if harness.model.SelectedID() == fixPR {
		t.Fatal("selection still names a task the store no longer holds")
	}
	if !harness.model.Rows()[harness.model.Selected()].Selectable() {
		t.Fatal("fallback landed on a non-selectable row")
	}
	if distance := harness.model.Selected() - before; distance > 2 || distance < -2 {
		t.Fatalf("fallback landed %d rows from the prior coordinate, not the nearest", distance)
	}
}

func TestSelectionFallbackIsDeterministic(t *testing.T) {
	// Two identical runs must land the cursor in the same place. A fallback
	// that depended on map iteration order would pass one run and fail another.
	first := newModelHarness(t, harnessOptions{})
	second := newModelHarness(t, harnessOptions{})
	for _, harness := range []*modelHarness{first, second} {
		harness.model.SwitchView(ViewNext)
		harness.selectRowByID(fixPR)
		harness.rewrite(strings.ReplaceAll(fixtureStore, `"state":"NEXT","priority":"B","title":"Review PR backlog"`,
			`"state":"DONE","priority":"B","title":"Review PR backlog"`))
		harness.tick()
	}
	if first.model.Selected() != second.model.Selected() {
		t.Fatalf("two identical runs disagreed: %d vs %d",
			first.model.Selected(), second.model.Selected())
	}
}

func TestSelectionKeepsTheCurrentOccurrenceOfAMultiContextTask(t *testing.T) {
	// A task with two contexts appears twice in Next. A refresh must keep the
	// cursor on the occurrence it was on, not teleport it to the first.
	live := strings.ReplaceAll(fixtureStore,
		`"title":"Water the plants","tags":["@home"]`,
		`"title":"Water the plants","tags":["@home","@errand"]`)
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewNext)

	occurrences := []int{}
	for index, row := range harness.model.Rows() {
		if row.ID() == fixPlants {
			occurrences = append(occurrences, index)
		}
	}
	if len(occurrences) < 2 {
		t.Fatalf("expected the task to appear once per context, got %d occurrences",
			len(occurrences))
	}
	harness.model.selectRow(occurrences[1])
	harness.model.RefreshRows()
	if got := harness.model.Selected(); got != occurrences[1] {
		t.Fatalf("refresh moved the cursor from occurrence %d to %d",
			occurrences[1], got)
	}
}

func TestSelectionClearsAndClosesTheDetailPanelWhenNoRowIsSelectable(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)
	harness.model.OpenDetail()
	if harness.model.Panel() == nil {
		t.Fatal("detail panel did not open")
	}

	harness.rewrite("{\"type\":\"meta\",\"version\":2}\n")
	harness.tick()

	if harness.model.SelectedID() != "" {
		t.Fatalf("selection survived an empty store: %q", harness.model.SelectedID())
	}
	if harness.model.Panel() != nil {
		t.Fatal("the detail panel outlived every selectable row")
	}
}

func TestMoveStaysWithinTheSelectableRows(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	for range 50 {
		harness.press('j')
	}
	if !harness.model.Rows()[harness.model.Selected()].Selectable() {
		t.Fatal("j walked onto a header row")
	}
	last := harness.model.Selected()
	harness.press('j')
	if harness.model.Selected() != last {
		t.Fatal("j moved past the last selectable row")
	}
	for range 50 {
		harness.press('k')
	}
	if !harness.model.Rows()[harness.model.Selected()].Selectable() {
		t.Fatal("k walked onto a header row")
	}
}

func TestGAndShiftGJumpToTheEnds(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.press('G')
	selectable := harness.model.selectableIndexes()
	if harness.model.Selected() != selectable[len(selectable)-1] {
		t.Fatal("G did not reach the last selectable row")
	}
	harness.press('g')
	if harness.model.Selected() != selectable[0] {
		t.Fatal("g did not reach the first selectable row")
	}
}

func TestSelectionSurvivesAViewSwitchAndReturn(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)
	harness.model.SwitchView(ViewOutline)
	harness.model.SwitchView(ViewNext)
	if got := harness.model.SelectedID(); got != fixPlants {
		t.Fatalf("a round trip through another view lost the selection: %q", got)
	}
}

func TestEscapeNeverQuits(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	_, cmd := harness.model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("escape produced a command; a reflex press must not end the session")
	}
	if harness.model.quitting {
		t.Fatal("escape set the quitting flag")
	}
}
