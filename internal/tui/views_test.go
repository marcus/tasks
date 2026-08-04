package tui

import (
	"strings"
	"testing"
)

// A subtree fixture: an open parent with an open child, plus a closed parent
// hiding an open child (which must be hoisted), plus a deferred parent (which
// must take its whole subtree with it).
const nestedStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action","tags":["@computer"]}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"Child action","tags":["@computer"]}
{"type":"task","id":"bbbb0003","parent":"aaaa0003","state":"DONE","title":"Closed parent","closed":"2026-06-01"}
{"type":"task","id":"bbbb0004","parent":"bbbb0003","state":"NEXT","title":"Hoisted child","tags":["@computer"]}
{"type":"task","id":"bbbb0005","parent":"aaaa0003","state":"NEXT","title":"Deferred parent","scheduled":"2099-01-01"}
{"type":"task","id":"bbbb0006","parent":"bbbb0005","state":"NEXT","title":"Buried child","tags":["@computer"]}
`

func rowTexts(harness *modelHarness) string {
	return strings.Join(harness.titles(), "\n")
}

func TestEveryTabProducesRows(t *testing.T) {
	// A view that silently renders nothing is the failure mode this packet is
	// most at risk of. Every tab has to answer with something.
	for _, view := range ViewKeys() {
		harness := newModelHarness(t, harnessOptions{})
		harness.model.SwitchView(view)
		if len(harness.model.Rows()) == 0 {
			t.Errorf("%s produced no rows at all", view)
		}
	}
}

func TestAgendaShowsOnlyDatedOpenAvailableTasksSoonestFirst(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewAgenda)
	text := rowTexts(harness)
	if !strings.Contains(text, "Book flight in Concur") || !strings.Contains(text, "Midyear self-eval") {
		t.Fatalf("agenda is missing a dated task:\n%s", text)
	}
	if strings.Contains(text, "Water the plants") {
		t.Fatalf("agenda showed an undated task:\n%s", text)
	}
	if strings.Contains(text, "Old finished thing") {
		t.Fatalf("agenda showed a closed task:\n%s", text)
	}
	if strings.Index(text, "Book flight") > strings.Index(text, "Midyear") {
		t.Fatalf("agenda is not soonest-first:\n%s", text)
	}
}

func TestAgendaPadsAnUndatedRiderToTheStampWidth(t *testing.T) {
	// A dated parent rides its undated child along; the child's title must line
	// up under the parent's, so the blank stamp is the same width as a real one.
	live := strings.ReplaceAll(nestedStore,
		`{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action","tags":["@computer"]}`,
		`{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action","tags":["@computer"],"deadline":"2026-07-20"}`)
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewAgenda)
	for _, row := range harness.model.Rows() {
		if row.Item != nil && row.Item.Title == "Child action" {
			if !strings.HasPrefix(row.Text, "│ "+strings.Repeat(" ", AgendaStampWidth)) {
				t.Fatalf("undated rider is not padded to the stamp width: %q", row.Text)
			}
			return
		}
	}
	t.Fatal("the undated child never rendered")
}

func TestNextGroupsByContextAndSortsByPriority(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	text := rowTexts(harness)
	if strings.Index(text, "@computer") > strings.Index(text, "@home") {
		t.Fatalf("context groups are not sorted:\n%s", text)
	}
	if strings.Index(text, "Book flight") > strings.Index(text, "Review PR") {
		t.Fatalf("priority A did not sort above B:\n%s", text)
	}
}

func TestNextGivesAContextlessTaskItsOwnGroup(t *testing.T) {
	live := strings.ReplaceAll(fixtureStore, `"title":"Water the plants","tags":["@home"]`,
		`"title":"Water the plants"`)
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewNext)
	if !strings.Contains(rowTexts(harness), "(no context)") {
		t.Fatalf("a contextless NEXT task lost its group:\n%s", rowTexts(harness))
	}
}

func TestQuadrantsAlwaysPaintsAllFourBucketsWithAPlaceholder(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewQuadrants)
	text := rowTexts(harness)
	for _, label := range []string{"Q1", "Q2", "Q3", "Q4"} {
		if !strings.Contains(text, label) {
			t.Fatalf("quadrant %s is missing:\n%s", label, text)
		}
	}
	if !strings.Contains(text, "—") {
		t.Fatalf("an empty quadrant did not get its placeholder:\n%s", text)
	}
}

func TestInboxPairsApprovalsWithTheCaptureQueue(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewInbox)
	text := rowTexts(harness)
	if !strings.Contains(text, "APPROVALS 0") || !strings.Contains(text, "INBOX 1") {
		t.Fatalf("inbox headers are wrong:\n%s", text)
	}
	if !strings.Contains(text, "No tasks pending approval") {
		t.Fatalf("the empty approvals section lost its message:\n%s", text)
	}
}

func TestInboxCountsTasksNotRows(t *testing.T) {
	// Collapsing an anchor hides rows without emptying the inbox, so the badge
	// must not shrink when a subtree is folded.
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"cccc0001","parent":"aaaa0001","state":"INBOX","title":"Parent capture"}
{"type":"task","id":"cccc0002","parent":"cccc0001","state":"INBOX","title":"Child capture"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewInbox)
	before := harness.model.intakeCounts(harness.model.filteredItems()).Inbox
	harness.model.collapsed["cccc0001"] = true
	harness.model.RefreshRows()
	after := harness.model.intakeCounts(harness.model.filteredItems()).Inbox
	if before != after || before != 2 {
		t.Fatalf("inbox count moved from %d to %d when a subtree was folded", before, after)
	}
}

func TestInboxEmptyMessage(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"cccc0001","parent":"aaaa0003","state":"NEXT","title":"Something"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewInbox)
	if !strings.Contains(rowTexts(harness), "Inbox empty") {
		t.Fatalf("an empty inbox said nothing:\n%s", rowTexts(harness))
	}
}

func TestOutlineShowsEveryLiveRecordIncludingClosedOnes(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	for _, want := range []string{"Inbox", "Work", "Home", "Old finished thing", "DONE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("outline is missing %q:\n%s", want, text)
		}
	}
}

func TestOutlineSectionRowsAreNotSelectable(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	for _, row := range harness.model.Rows() {
		if strings.TrimSpace(row.Text) == "Work" && row.Selectable() {
			t.Fatal("a section row is selectable in the outline")
		}
	}
}

func TestTreeHoistsAnOpenTaskOutOfAClosedParent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	text := rowTexts(harness)
	if !strings.Contains(text, "Hoisted child") {
		t.Fatalf("an open task under a closed parent vanished:\n%s", text)
	}
	if strings.Contains(text, "Closed parent") {
		t.Fatalf("the closed parent rendered a row of its own:\n%s", text)
	}
}

func TestTreeHidesAWholeDeferredSubtree(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	if strings.Contains(rowTexts(harness), "Buried child") {
		t.Fatalf("a child of a deferred parent rendered:\n%s", rowTexts(harness))
	}
	harness.model.ToggleDeferred()
	if !strings.Contains(rowTexts(harness), "Buried child") {
		t.Fatalf("showing unavailable work did not reveal the subtree:\n%s", rowTexts(harness))
	}
}

func TestAMatchingDescendantRidesItsMatchingAncestor(t *testing.T) {
	// Both parent and child are NEXT @computer. The child must appear under the
	// parent, once, not as a second anchor in the same group.
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	count := 0
	for _, row := range harness.model.Rows() {
		if row.Item != nil && row.Item.Title == "Child action" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the child rendered %d times:\n%s", count, rowTexts(harness))
	}
}

func TestCollapsingAnAnchorHidesItsSubtreeAndSaysHowMany(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID("bbbb0001")
	harness.press('h')

	text := rowTexts(harness)
	if strings.Contains(text, "Child action") {
		t.Fatalf("collapsing did not hide the child:\n%s", text)
	}
	if !strings.Contains(text, "(1)") {
		t.Fatalf("the folded row does not say how many rows it hides:\n%s", text)
	}
	if !strings.Contains(text, MarkCollapsed) {
		t.Fatalf("the folded row has no collapsed marker:\n%s", text)
	}
	if harness.model.SelectedID() != "bbbb0001" {
		t.Fatalf("collapsing moved the cursor to %q", harness.model.SelectedID())
	}
}

func TestASecondCollapseClimbsToTheParent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID("bbbb0002")
	harness.press('h') // a leaf: climbs
	if harness.model.SelectedID() != "bbbb0001" {
		t.Fatalf("h on a leaf did not climb to the parent, landed on %q",
			harness.model.SelectedID())
	}
}

func TestExpandRestoresTheSubtree(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID("bbbb0001")
	harness.press('h')
	harness.press('l')
	if !strings.Contains(rowTexts(harness), "Child action") {
		t.Fatalf("expanding did not restore the child:\n%s", rowTexts(harness))
	}
}

func TestCollapseAllAndExpandAll(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	harness.press('H')
	if strings.Contains(rowTexts(harness), "Child action") {
		t.Fatalf("H left a descendant visible:\n%s", rowTexts(harness))
	}
	harness.press('L')
	if !strings.Contains(rowTexts(harness), "Child action") {
		t.Fatalf("L did not unfold everything:\n%s", rowTexts(harness))
	}
	if len(harness.model.collapsed) != 0 {
		t.Fatalf("L left %d ids collapsed", len(harness.model.collapsed))
	}
}

func TestProjectsListsSelectableHeaderRowsWithRolledUpCounts(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewProjects)
	found := false
	for _, row := range harness.model.Rows() {
		if row.Project != nil && row.Project.Title == "Work" {
			found = true
			if !row.Selectable() {
				t.Fatal("a project header row is not selectable")
			}
			if !strings.Contains(row.Text, "open") || !strings.Contains(row.Text, "next") {
				t.Fatalf("the project header lost its rollup: %q", row.Text)
			}
		}
	}
	if !found {
		t.Fatalf("no project header row:\n%s", rowTexts(harness))
	}
}

func TestSearchFilterNarrowsRowsAndFallsBackToFlat(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.press('/')
	for _, key := range "plants" {
		harness.press(key)
	}
	text := rowTexts(harness)
	if !strings.Contains(text, "Water the plants") {
		t.Fatalf("the search dropped its own match:\n%s", text)
	}
	if strings.Contains(text, "Book flight") {
		t.Fatalf("the search kept a non-match:\n%s", text)
	}
	if harness.model.useTree() {
		t.Fatal("a search must render flat")
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.model.filter = "PLANTS"
	harness.model.RefreshRows()
	if !strings.Contains(rowTexts(harness), "Water the plants") {
		t.Fatalf("search is case sensitive:\n%s", rowTexts(harness))
	}
}

func TestEscapeClearsACommittedSearch(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.model.filter = "plants"
	harness.model.RefreshRows()
	harness.pressTypeEsc()
	if harness.model.filter != "" {
		t.Fatalf("escape left the filter %q", harness.model.filter)
	}
	if !strings.Contains(rowTexts(harness), "Book flight") {
		t.Fatalf("clearing the filter did not restore the rows:\n%s", rowTexts(harness))
	}
}

func TestEscapeLadderClearsSearchThenContextsThenThePanel(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.model.filter = "plants"
	harness.model.contextFilters = []string{"@home"}
	harness.model.RefreshRows()
	harness.model.OpenDetail()

	harness.pressTypeEsc()
	if harness.model.filter != "" {
		t.Fatal("the first escape did not clear the search")
	}
	if len(harness.model.contextFilters) == 0 {
		t.Fatal("the first escape ALSO cleared the context filters; the ladder must take one rung at a time")
	}
	harness.pressTypeEsc()
	if len(harness.model.contextFilters) != 0 {
		t.Fatal("the second escape did not clear the context filters")
	}
	harness.pressTypeEsc()
	if harness.model.Panel() != nil {
		t.Fatal("the third escape did not close the panel")
	}
}

func TestContextFilterKeepsTheTreeOnListViewsAndGoesFlatElsewhere(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.contextFilters = []string{"@home"}
	for _, view := range []string{ViewAgenda, ViewNext, ViewQuadrants, ViewInbox} {
		harness.model.SwitchView(view)
		if !harness.model.useTree() {
			t.Errorf("%s dropped the tree under a context filter", view)
		}
	}
	for _, view := range []string{ViewOutline, ViewProjects} {
		harness.model.SwitchView(view)
		if harness.model.useTree() {
			t.Errorf("%s kept the tree under a context filter", view)
		}
	}
}

func TestContextFilterNarrowsTheList(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.contextFilters = []string{"@home"}
	harness.model.SwitchView(ViewNext)
	text := rowTexts(harness)
	if !strings.Contains(text, "Water the plants") {
		t.Fatalf("the context filter dropped its own match:\n%s", text)
	}
	if strings.Contains(text, "Book flight") {
		t.Fatalf("the context filter kept another context's task:\n%s", text)
	}
}

func TestTabCyclingWraps(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewInbox)
	harness.model.CycleView(1)
	if harness.model.view != ViewAgenda {
		t.Fatalf("cycling past the last tab landed on %q", harness.model.view)
	}
	harness.model.CycleView(-1)
	if harness.model.view != ViewInbox {
		t.Fatalf("cycling before the first tab landed on %q", harness.model.view)
	}
}

func TestNumberKeysJumpToTheirTab(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for index, tab := range Tabs {
		harness.press(rune('1' + index))
		if harness.model.view != tab.Key {
			t.Fatalf("key %d selected %q, want %q", index+1, harness.model.view, tab.Key)
		}
	}
}
