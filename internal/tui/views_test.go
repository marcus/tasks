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

func TestAgendaLeavesAnUndatedRidersDateColumnEmpty(t *testing.T) {
	// A dated parent rides its undated child along. The child has no date of
	// its own, and the shared right-hand column must say so by being blank —
	// inheriting the parent's stamp would claim a deadline nobody set.
	live := strings.ReplaceAll(nestedStore,
		`{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action","tags":["@computer"]}`,
		`{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action","tags":["@computer"],"deadline":"2026-07-20"}`)
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewAgenda)
	for _, row := range harness.model.Rows() {
		if row.Item != nil && row.Item.Title == "Child action" {
			// An undated row bands blank, so the head is BandField spaces —
			// the field is still there, it just has nothing to say.
			if !strings.HasPrefix(row.Text, strings.Repeat(" ", BandField)) {
				t.Fatalf("undated rider lost the band column: %q", row.Text)
			}
			if trimmed := strings.TrimRight(row.Text, " "); strings.HasSuffix(trimmed, "ago") ||
				strings.Contains(trimmed[max(len(trimmed)-MetaColumn, 0):], "-") {
				t.Fatalf("undated rider inherited a date: %q", row.Text)
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
	// Both counts sit on the shared right edge rather than beside their labels.
	if !strings.Contains(text, "APPROVALS ") || !strings.Contains(text, "INBOX ") {
		t.Fatalf("inbox headers are wrong:\n%s", text)
	}
	for _, want := range []string{"0", "1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("an inbox section lost its count %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "Nothing pending approval") {
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
	// Closed tasks speak the shared dot vocabulary now, not a state word: the
	// DONE row is the closed dot with its title kept on the row.
	// The priority letter now sits between the dot and the title — see
	// priorityField — so a closed C-priority row reads `● C  title`.
	for _, want := range []string{"Inbox", "Work", "Home", DotClosed + " C  Old finished thing"} {
		if !strings.Contains(text, want) {
			t.Fatalf("outline is missing %q:\n%s", want, text)
		}
	}
}

func TestOutlineSectionRowsAreSelectableAndFoldable(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	found := false
	for _, row := range harness.model.Rows() {
		if row.Project == nil || row.Project.Title != "Work" {
			continue
		}
		found = true
		if !row.Selectable() {
			t.Fatal("the Work section row is not selectable in the outline")
		}
		if !row.HasMarker() {
			t.Fatal("the Work section row carries no fold marker")
		}
		if row.Project.ID != fixWork {
			t.Fatalf("the Work section row resolves to %q, want %q", row.Project.ID, fixWork)
		}
	}
	if !found {
		t.Fatalf("no selectable Work section row in the outline:\n%s", rowTexts(harness))
	}
}

func TestOutlineFoldsASectionAndKeepsItsBadge(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.model.collapsed[fixWork] = true
	harness.model.RefreshRows()
	text := rowTexts(harness)
	if strings.Contains(text, "Book flight in Concur") {
		t.Fatalf("a folded section still shows its tasks:\n%s", text)
	}
	if tally := headingTally(harness.model.Rows(), "Work"); tally != "5" {
		t.Fatalf("folded Work badge %q, want the full task count 5:\n%s", tally, text)
	}
}

func TestOutlineCalendarSectionShowsItsRangeInline(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Calendar / Hard Landscape"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Europe trip                <2026-07-02>--<2026-07-14>"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	if strings.Contains(text, "<2026-07-02>") {
		t.Fatalf("the raw date stamps leaked into the row:\n%s", text)
	}
	if !strings.Contains(text, "Europe trip") || !strings.Contains(text, "2–14 jul") {
		t.Fatalf("the calendar entry lost its title or its human range:\n%s", text)
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
			// The rollup moved to the shared meta column as `next/open`.
			if !strings.HasSuffix(strings.TrimRight(row.Text, " "), "2/4") {
				t.Fatalf("the project header lost its rollup: %q", row.Text)
			}
		}
	}
	if !found {
		t.Fatalf("no project header row:\n%s", rowTexts(harness))
	}
}

// projectsNestedStore mirrors the Ruby PROJ_NESTED fixture: an open parent with
// an open child, plus a leaf sibling, under one area section.
const projectsNestedStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","priority":"A","title":"parent task","deadline":"2026-07-03"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"TODO","title":"child task"}
{"type":"task","id":"bbbb0003","parent":"aaaa0003","state":"TODO","title":"leaf sibling"}
`

// projectsDoubleNestedStore: Projects → child section → task (canonical nested
// project heading; topLevelTaskNodes must not drop the task).
const projectsDoubleNestedStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"NEXT","title":"pick a generator","deadline":"2026-07-03"}
`

// projectsDoneParentStore: open task under a DONE ancestor — hoisted under the
// enclosing section, never under a "done middle" header.
const projectsDoneParentStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"DONE","title":"done middle","closed":"2026-06-01"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"hoisted child"}
`

func TestProjectsTreeRendersChildDirectlyBelowParent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsNestedStore})
	harness.model.SwitchView(ViewProjects)
	rows := harness.model.Rows()
	parentIdx, childIdx := -1, -1
	for i, row := range rows {
		if row.Item != nil && row.Item.Title == "parent task" {
			parentIdx = i
		}
		if row.Item != nil && row.Item.Title == "child task" {
			childIdx = i
		}
	}
	if parentIdx < 0 || childIdx < 0 {
		t.Fatalf("missing parent or child row:\n%s", rowTexts(harness))
	}
	if childIdx != parentIdx+1 {
		t.Fatalf("child is not immediately below parent (parent=%d child=%d):\n%s",
			parentIdx, childIdx, rowTexts(harness))
	}
	if rows[childIdx].Node == nil || rows[parentIdx].Node == nil {
		t.Fatal("tree-mode project task rows must carry Node")
	}
	if rows[childIdx].Node.Level <= rows[parentIdx].Node.Level {
		t.Fatalf("child level %d is not deeper than parent level %d",
			rows[childIdx].Node.Level, rows[parentIdx].Node.Level)
	}
	if !strings.Contains(rows[childIdx].Text, "│") {
		t.Fatalf("nested child row lacks the thread glyph: %q", rows[childIdx].Text)
	}
}

func TestProjectsTreeBoldsContainersNotLeaves(t *testing.T) {
	// PlainStyler is a no-op painter, so assert the structural stand-in for
	// outline_container: containers get ▾/▸ markers, leaves get the blank leaf
	// marker column.
	harness := newModelHarness(t, harnessOptions{live: projectsNestedStore})
	harness.model.SwitchView(ViewProjects)
	var parent, child, leaf Row
	var foundParent, foundChild, foundLeaf bool
	for _, row := range harness.model.Rows() {
		if row.Item == nil {
			continue
		}
		switch row.Item.Title {
		case "parent task":
			parent, foundParent = row, true
		case "child task":
			child, foundChild = row, true
		case "leaf sibling":
			leaf, foundLeaf = row, true
		}
	}
	if !foundParent || !foundChild || !foundLeaf {
		t.Fatalf("missing a row:\n%s", rowTexts(harness))
	}
	if !strings.Contains(parent.Text, MarkExpanded) {
		t.Fatalf("container parent has no expanded marker: %q", parent.Text)
	}
	if strings.Contains(child.Text, MarkExpanded) || strings.Contains(child.Text, MarkCollapsed) {
		t.Fatalf("leaf child should not carry a collapse marker: %q", child.Text)
	}
	if strings.Contains(leaf.Text, MarkExpanded) || strings.Contains(leaf.Text, MarkCollapsed) {
		t.Fatalf("leaf sibling should not carry a collapse marker: %q", leaf.Text)
	}
}

func TestProjectsTreeNoSpuriousHeaderForIntermediateTask(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsNestedStore})
	harness.model.SwitchView(ViewProjects)
	headers := []string{}
	for _, row := range harness.model.Rows() {
		if row.Project != nil {
			headers = append(headers, row.Project.Title)
		}
	}
	if len(headers) != 1 || headers[0] != "Work" {
		t.Fatalf("expected only the Work section header, got %v:\n%s",
			headers, rowTexts(harness))
	}
	for _, row := range harness.model.Rows() {
		if row.Project != nil && row.Project.Title == "parent task" {
			t.Fatal("open parent task must not become a pseudo-project header")
		}
	}
}

func TestProjectsFlatFallbackMatchesLegacyShape(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsNestedStore})
	harness.model.SwitchView(ViewProjects)
	// `/` forces the flat path; Projects then uses projectsFlat (no nodes, no
	// thread glyphs, fixed indent).
	harness.model.filter = "task"
	harness.model.RefreshRows()
	if harness.model.useTree() {
		t.Fatal("a search must render flat")
	}
	for _, row := range harness.model.Rows() {
		if row.Node != nil {
			t.Fatalf("flat projects row carries a node: %q", row.Text)
		}
		if strings.Contains(row.Text, "│") {
			t.Fatalf("flat projects has a thread glyph: %q", row.Text)
		}
		if strings.Contains(row.Text, MarkExpanded) || strings.Contains(row.Text, MarkCollapsed) {
			t.Fatalf("flat projects has a collapse marker: %q", row.Text)
		}
	}
	text := rowTexts(harness)
	if !strings.Contains(text, "Work") {
		t.Fatalf("flat projects dropped the project header:\n%s", text)
	}
	if !strings.Contains(text, "parent task") || !strings.Contains(text, "child task") {
		t.Fatalf("flat projects dropped a task:\n%s", text)
	}
}

func TestTaskUnderNestedProjectHeadingIsNotDropped(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsDoubleNestedStore})
	for _, view := range []string{ViewAgenda, ViewNext, ViewQuadrants} {
		harness.model.SwitchView(view)
		if !strings.Contains(rowTexts(harness), "pick a generator") {
			t.Errorf("%s tree view dropped a task under a nested project heading:\n%s",
				view, rowTexts(harness))
		}
	}
	harness.model.SwitchView(ViewProjects)
	text := rowTexts(harness)
	if !strings.Contains(text, "Launch the site") {
		t.Fatalf("nested project header missing:\n%s", text)
	}
	if !strings.Contains(text, "pick a generator") {
		t.Fatalf("task under nested project heading dropped from Projects:\n%s", text)
	}
	// Body row must nest under the ProjectView header, with Node for collapse.
	found := false
	for _, row := range harness.model.Rows() {
		if row.Item != nil && row.Item.Title == "pick a generator" {
			found = true
			if row.Node == nil {
				t.Fatal("nested project task row must carry Node in tree mode")
			}
		}
	}
	if !found {
		t.Fatalf("no task row for pick a generator:\n%s", text)
	}
}

func TestProjectsHoistsOpenTaskUnderDoneParent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsDoneParentStore})
	harness.model.SwitchView(ViewProjects)
	text := rowTexts(harness)
	for _, row := range harness.model.Rows() {
		if row.Project != nil && row.Project.Title == "done middle" {
			t.Fatal("a DONE parent must not head an active project group")
		}
		if row.Item != nil && row.Item.Title == "done middle" {
			t.Fatal("a DONE parent must not render a body row")
		}
	}
	if !strings.Contains(text, "Work") {
		t.Fatalf("Work section header missing:\n%s", text)
	}
	if !strings.Contains(text, "hoisted child") {
		t.Fatalf("hoisted child missing under Work:\n%s", text)
	}
}

func TestProjectsCollapseHidesDescendants(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsNestedStore})
	harness.model.SwitchView(ViewProjects)
	harness.selectRowByID("bbbb0001")
	harness.press('h')

	text := rowTexts(harness)
	if strings.Contains(text, "child task") {
		t.Fatalf("collapsing did not hide the child:\n%s", text)
	}
	if !strings.Contains(text, "(1)") {
		t.Fatalf("the folded row does not say how many it hides:\n%s", text)
	}
	if !strings.Contains(text, MarkCollapsed) {
		t.Fatalf("the folded row has no collapsed marker:\n%s", text)
	}
	if harness.model.SelectedID() != "bbbb0001" {
		t.Fatalf("collapsing moved the cursor to %q", harness.model.SelectedID())
	}
	// Expand restores the child.
	harness.press('l')
	if !strings.Contains(rowTexts(harness), "child task") {
		t.Fatalf("expanding did not restore the child:\n%s", rowTexts(harness))
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

func TestOutlineFoldKeysWorkOnASectionRow(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixWork)

	harness.press('h')
	if !harness.model.collapsed[fixWork] {
		t.Fatal("h did not fold the selected section")
	}
	if strings.Contains(rowTexts(harness), "Book flight in Concur") {
		t.Fatalf("the folded section still shows its tasks:\n%s", rowTexts(harness))
	}
	harness.press('l')
	if harness.model.collapsed[fixWork] {
		t.Fatal("l did not unfold the selected section")
	}
}

func TestOutlineSecondFoldPressClimbsToTheParentSection(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixFlight)

	// The flight is a leaf, so h climbs — and in the outline the parent row it
	// climbs to is the Work SECTION, which is now a row a cursor can land on.
	harness.press('h')
	if harness.model.SelectedID() != fixWork {
		t.Fatalf("h on a leaf selected %q, want the parent section %q",
			harness.model.SelectedID(), fixWork)
	}
}

func TestOutlineArchivesASelectedSection(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixHome)

	harness.press('x')
	if harness.model.modal == nil || harness.model.modal.Kind() != ModalProjectArchiveConfirm {
		t.Fatalf("x on a section row did not open the archive-project confirmation")
	}
	harness.press('y')
	if strings.Contains(rowTexts(harness), "Water the plants") {
		t.Fatalf("the archived section's tasks are still listed:\n%s", rowTexts(harness))
	}
}

func TestOutlineRenamesASelectedSection(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixHome)

	harness.press('e')
	if harness.model.form == nil || harness.model.form.Kind != QuickFormProjectRename {
		t.Fatal("e on a section row did not open the rename form")
	}
}

func TestOutlineEditsACalendarSectionsDates(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Calendar / Hard Landscape"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Europe trip                <2026-07-02>--<2026-07-14>"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID("aaaa0002")

	harness.press('d')
	if harness.model.form == nil || harness.model.form.Kind != QuickFormSectionDates {
		t.Fatal("d on a calendar section did not open the dates form")
	}
	// Submitting the pre-filled range unchanged rewrites the title in the ONE
	// canonical spelling — the ad-hoc padding run the raw file carried is gone.
	harness.pressTypeEnter()
	view, ok := harness.model.read.Queries().SectionView("aaaa0002")
	if !ok {
		t.Fatal("the calendar section vanished")
	}
	if view.Title != "Europe trip <2026-07-02>--<2026-07-14>" {
		t.Fatalf("normalized title %q", view.Title)
	}
}

func TestOutlineRefusesDatesOnAnOrdinarySection(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixWork)

	harness.press('d')
	if harness.model.form != nil {
		t.Fatal("d on an ordinary section opened a dates form")
	}
	if !strings.Contains(harness.model.FlashMessage(), "calendar") {
		t.Fatalf("the refusal did not say where dates live: %q", harness.model.FlashMessage())
	}
}

// -- design 6b: the Projects view only shows what is live ------------------

// projectsDormantStore: one project with work, three with none.
const projectsDormantStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"NEXT","title":"pick a generator"}
{"type":"section","id":"aaaa0003","parent":"aaaa0001","title":"Mid-year reviews"}
{"type":"section","id":"aaaa0004","parent":"aaaa0001","title":"RaaS consolidation"}
{"type":"section","id":"aaaa0005","parent":"aaaa0001","title":"Fix the deck"}
`

func TestProjectsFoldsEveryEmptyProjectIntoOneRow(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsDormantStore})
	harness.model.SwitchView(ViewProjects)
	frame := rowTexts(harness)
	for _, title := range []string{"Mid-year reviews", "RaaS consolidation", "Fix the deck"} {
		if !strings.Contains(frame, title) {
			t.Fatalf("dormant project %q went missing:\n%s", title, frame)
		}
	}
	tail := ""
	own := 0
	for _, row := range harness.model.Rows() {
		if strings.Contains(row.Text, " · ") {
			tail = row.Text
		}
		if row.Project != nil {
			own++
		}
	}
	if tail == "" {
		t.Fatalf("no rolled-up dormant row:\n%s", frame)
	}
	if !strings.Contains(tail, "no tasks") {
		t.Fatalf("the dormant row does not say why it is rolled up: %q", tail)
	}
	// Only the live project keeps a row of its own; the other three share one.
	if own != 1 {
		t.Fatalf("expected 1 selectable project row, got %d:\n%s", own, frame)
	}
}

func TestProjectsSectionBadgeNamesTheLiveShare(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsDormantStore})
	harness.model.SwitchView(ViewProjects)
	if !strings.Contains(rowTexts(harness), "1/4 open") {
		t.Fatalf("the PROJECTS badge does not name the live share:\n%s", rowTexts(harness))
	}
}

func TestProjectHeadingFoldsItsTasks(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsDormantStore})
	harness.model.SwitchView(ViewProjects)
	harness.selectRowByID("aaaa0002")
	if !strings.Contains(rowTexts(harness), MarkExpanded+"Launch the site") {
		t.Fatalf("a live project heading has no expanded marker:\n%s", rowTexts(harness))
	}
	harness.model.CollapseSelected()
	frame := rowTexts(harness)
	if strings.Contains(frame, "pick a generator") {
		t.Fatalf("folding the project left its task on screen:\n%s", frame)
	}
	if !strings.Contains(frame, MarkCollapsed+"Launch the site") {
		t.Fatalf("the folded project heading kept its expanded marker:\n%s", frame)
	}
	harness.model.ExpandSelected()
	if !strings.Contains(rowTexts(harness), "pick a generator") {
		t.Fatalf("unfolding did not bring the task back:\n%s", rowTexts(harness))
	}
}

// -- design 6b: the outline bands its lists by urgency ---------------------

// outlineBandStore: one plain GTD list holding overdue, due-today and undated
// work, plus a list whose children are all in one band.
const outlineBandStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"bbbb0001","parent":"aaaa0001","state":"TODO","title":"late thing","deadline":"2026-07-01"}
{"type":"task","id":"bbbb0002","parent":"aaaa0001","state":"TODO","title":"due today","deadline":"2026-07-14"}
{"type":"task","id":"bbbb0003","parent":"aaaa0001","state":"TODO","title":"someday thing"}
{"type":"section","id":"aaaa0002","title":"Home"}
{"type":"task","id":"bbbb0004","parent":"aaaa0002","state":"TODO","title":"no date at all"}
{"type":"task","id":"bbbb0005","parent":"aaaa0002","state":"TODO","title":"also no date"}
`

func TestOutlineBandsAListIntoOverdueTodayAndLater(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: outlineBandStore})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	for _, band := range []string{"overdue", "today", "later"} {
		if !strings.Contains(text, Band+" "+band) {
			t.Fatalf("no %q band rule:\n%s", band, text)
		}
	}
	// Order is the urgency ladder, not file order.
	overdue := strings.Index(text, Band+" overdue")
	today := strings.Index(text, Band+" today")
	later := strings.Index(text, Band+" later")
	if !(overdue < today && today < later) {
		t.Fatalf("bands are out of order (%d, %d, %d):\n%s", overdue, today, later, text)
	}
}

// A lone band rule says only that nothing is due, which the absence of any red
// band already said.
func TestOutlineDoesNotBandAListThatIsAllOneBand(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: outlineBandStore})
	harness.model.SwitchView(ViewOutline)
	rows := harness.model.Rows()
	homeAt := -1
	for index, row := range rows {
		if strings.Contains(row.Text, "Home") {
			homeAt = index
		}
	}
	if homeAt < 0 {
		t.Fatalf("no Home section:\n%s", rowTexts(harness))
	}
	for _, row := range rows[homeAt+1:] {
		if strings.Contains(row.Text, Band+" later") {
			t.Fatalf("Home was banded even though nothing in it is due:\n%s", rowTexts(harness))
		}
	}
}

// A section that contains sub-sections is already grouped by something its
// author chose; a second grouping cut across it would fight the first.
func TestOutlineDoesNotBandASectionOfSections(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsDormantStore})
	harness.model.SwitchView(ViewOutline)
	if strings.Contains(rowTexts(harness), Band+" later") {
		t.Fatalf("the Projects heading was banded:\n%s", rowTexts(harness))
	}
}

// A parent bands with the most urgent thing anywhere under it, so folding it
// never hides work in a calmer band than the work deserves.
func TestAnOutlineBandTakesTheMostUrgentThingInTheSubtree(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"bbbb0001","parent":"aaaa0001","state":"TODO","title":"calm parent"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"TODO","title":"late child","deadline":"2026-07-01"}
{"type":"task","id":"bbbb0003","parent":"aaaa0001","state":"TODO","title":"undated other"}
{"type":"task","id":"bbbb0004","parent":"aaaa0001","state":"TODO","title":"due today","deadline":"2026-07-14"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewOutline)
	rows := harness.model.Rows()
	overdueAt, parentAt, todayAt := -1, -1, -1
	for index, row := range rows {
		switch {
		case strings.Contains(row.Text, Band+" overdue"):
			overdueAt = index
		case strings.Contains(row.Text, Band+" today"):
			todayAt = index
		case row.Item != nil && row.Item.Title == "calm parent":
			parentAt = index
		}
	}
	if overdueAt < 0 || parentAt < 0 || todayAt < 0 {
		t.Fatalf("missing a landmark:\n%s", rowTexts(harness))
	}
	if !(overdueAt < parentAt && parentAt < todayAt) {
		t.Fatalf("the parent did not band with its overdue child:\n%s", rowTexts(harness))
	}
}

func TestHeaderNamesTheOverdueCountAndOmitsItAtZero(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: outlineBandStore})
	if got := harness.model.OverdueTaskCount(); got != 1 {
		t.Fatalf("OverdueTaskCount = %d, want 1", got)
	}
	if !strings.Contains(harness.model.Header(120), "1 overdue") {
		t.Fatalf("the header hides the overdue count: %q", harness.model.Header(120))
	}
	clean := newModelHarness(t, harnessOptions{live: projectsDormantStore})
	if strings.Contains(clean.model.Header(120), "overdue") {
		t.Fatalf("the header says overdue with none: %q", clean.model.Header(120))
	}
}
