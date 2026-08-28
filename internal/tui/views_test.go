package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/tui/term/shortcuts"
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

// nextEmptyStore has dated, prioritized, open work and not one NEXT mark — the
// shape every capture/approve/date workflow ends up in.
const nextEmptyStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"cccc0001","parent":"aaaa0003","state":"TODO","priority":"A","title":"Dated but unmarked","deadline":"2026-07-15"}
{"type":"task","id":"cccc0002","parent":"aaaa0003","state":"TODO","title":"Also dated","deadline":"2026-07-20"}
{"type":"task","id":"cccc0003","parent":"aaaa0003","state":"TODO","title":"Undated"}
`

// A Next tab with nothing marked NEXT has to explain itself. Blank is the one
// answer it must not give: dating lands work in TODO, so this list sits beside
// a FULL agenda, and an empty pane reads as a broken tab rather than as work
// nobody has named yet.
func TestNextEmptyStatePointsAtTheAgendaAndSaysHowToMark(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nextEmptyStore})
	for _, useTree := range []bool{true, false} {
		// 72 cells is a narrow-but-ordinary pane: the copy has to survive the
		// meta column's truncation with the command still on it.
		text := strings.Join(rowTextsOf(nextRows(t, harness, useTree, 72)), "\n")
		for _, want := range []string{
			"NEXT",
			"No explicit next actions.",
			"2 dated items are waiting on Agenda.",
			"Mark one with N, or: tasks state <ref> NEXT",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("tree=%v: the empty Next tab never said %q:\n%s", useTree, want, text)
			}
		}
	}
}

// The placeholder is a stand-in for rows, so it obeys the rows' contract: one
// shared right edge, and a body padded to the same width as any other line.
func TestNextEmptyStateKeepsTheSharedRowWidth(t *testing.T) {
	const width = 100
	harness := newModelHarness(t, harnessOptions{live: nextEmptyStore})
	for _, row := range nextRows(t, harness, true, width) {
		want := width
		if row.Chrome {
			want += CursorField
		}
		if got := len([]rune(row.Text)); got != want {
			t.Fatalf("row is %d cells wide, want %d: %q", got, want, row.Text)
		}
	}
}

// Nothing about a POPULATED Next tab changes: the groups are still the
// contexts, and no placeholder prose leaks in beside real actions.
func TestNextWithActionsIsUntouchedByTheEmptyState(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	text := rowTexts(harness)
	for _, unwanted := range []string{"No explicit next actions.", "waiting on Agenda"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("the empty state painted over a populated list:\n%s", text)
		}
	}
	for _, want := range []string{"@computer", "@home", "Book flight", "Water the plants"} {
		if !strings.Contains(text, want) {
			t.Fatalf("a populated Next tab lost %q:\n%s", want, text)
		}
	}
}

// nextRows builds the Next tab in either mode at a known width. The flat mode
// is the one a `/` search drops into, and it needs the same empty state.
func nextRows(t *testing.T, harness *modelHarness, useTree bool, width int) []Row {
	t.Helper()
	harness.model.SwitchView(ViewNext)
	read := harness.model.ReadModel()
	return BuildRows(BuildRequest{
		View: ViewNext, Styler: PlainStyler{}, Queries: read.Queries(),
		Items: read.Items(), Tree: read.Queries().Tree().Roots, UseTree: useTree,
		Collapsed: map[string]bool{}, Width: width,
	})
}

func rowTextsOf(rows []Row) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row.Text)
	}
	return out
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

func TestOutlineShowsEveryLiveOpenRecordAndHidesClosedOnes(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	for _, want := range []string{"Inbox", "Work", "Home", "Book flight in Concur"} {
		if !strings.Contains(text, want) {
			t.Fatalf("outline is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Old finished thing") {
		t.Fatalf("outline showed a closed task before anyone asked:\n%s", text)
	}
}

func TestOutlineTogglesClosedTasksBackInWithTheirClosedStyling(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.model.ToggleClosed()
	// Closed tasks speak the shared dot vocabulary, not a state word: the DONE
	// row is the closed dot with its title kept on the row. The priority letter
	// sits between the dot and the title — see priorityField — so a closed
	// C-priority row reads `● C  title`.
	if text := rowTexts(harness); !strings.Contains(text, DotClosed+" C  Old finished thing") {
		t.Fatalf("the toggle did not restore the closed row in place:\n%s", text)
	}
	harness.model.ToggleClosed()
	if text := rowTexts(harness); strings.Contains(text, "Old finished thing") {
		t.Fatalf("toggling back did not hide the closed row again:\n%s", text)
	}
}

// The `C` key, the `:` palette and `?` help all have to reach the same toggle,
// or the capability exists only for whoever guessed the keystroke.
func TestClosedToggleIsReachableByKeyAndPaletteAndHelp(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.press('C')
	if !harness.model.showClosed {
		t.Fatal("C did not toggle closed rows on")
	}
	if !strings.Contains(rowTexts(harness), "Old finished thing") {
		t.Fatalf("C did not reveal the closed row:\n%s", rowTexts(harness))
	}
	harness.press('C')
	if harness.model.showClosed {
		t.Fatal("a second C did not toggle closed rows back off")
	}

	if !paletteOffers(harness, "toggle_closed_view") {
		t.Fatal("the action palette does not offer the closed-row toggle")
	}
	help := strings.Join(
		HelpContent(harness.model.styler, harness.model.app.DelegationModes()).Lines, "\n")
	if !strings.Contains(help, "show / hide closed tasks") {
		t.Fatalf("the help overlay does not document the closed-row toggle:\n%s", help)
	}
}

// PROPOSED is intake's, whichever way the closed toggle is set: an undecided
// proposal is not finished work, and revealing history must not smuggle the
// approval queue into the outline.
func TestOutlineNeverShowsProposalsEitherWayRoundTheToggle(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: proposalOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	for _, showClosed := range []bool{false, true} {
		harness.model.showClosed = showClosed
		harness.model.RefreshRows()
		if text := rowTexts(harness); strings.Contains(text, "Proposed thing") {
			t.Fatalf("showClosed=%v let a proposal into the outline:\n%s", showClosed, text)
		}
	}
}

// A section whose children are ALL closed must not read as an empty project. It
// keeps its heading and says what it is holding, so the reader can see there is
// history there and one keystroke away.
func TestOutlineSectionOfOnlyClosedWorkKeepsALeftoverMarker(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: closedOnlyOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	heading := headingRowText(harness.model.Rows(), "Archive candidates")
	if heading == "" {
		t.Fatalf("the closed-only section vanished entirely:\n%s", rowTexts(harness))
	}
	if !strings.Contains(heading, "· 2 closed") {
		t.Fatalf("closed-only heading %q does not name its leftovers", heading)
	}
	if strings.Contains(rowTexts(harness), "Swept later") {
		t.Fatalf("a closed row painted while the toggle was off:\n%s", rowTexts(harness))
	}
	// With the toggle on the rows ARE the evidence, so the marker retires
	// rather than double-counting what is now on the screen.
	harness.model.ToggleClosed()
	heading = headingRowText(harness.model.Rows(), "Archive candidates")
	if strings.Contains(heading, "closed") || !strings.HasSuffix(strings.TrimSpace(heading), "2") {
		t.Fatalf("revealed heading %q should badge 2 and drop the leftover marker", heading)
	}
}

// The badge counts what is on the screen. A section badge that silently
// included hidden history would send the reader looking for rows that are not
// there; naming the split keeps the number and the list in agreement.
func TestOutlineSectionBadgeCountsOnlyWhatItShows(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	heading := headingRowText(harness.model.Rows(), "Work")
	if !strings.Contains(heading, "4 · 1 closed") {
		t.Fatalf("Work heading %q, want the shown count and the leftover split", heading)
	}
	harness.model.ToggleClosed()
	heading = headingRowText(harness.model.Rows(), "Work")
	if strings.Contains(heading, "closed") || !strings.HasSuffix(strings.TrimSpace(heading), "5") {
		t.Fatalf("revealed Work heading %q, want a plain badge of 5", heading)
	}
}

// A folded row's dim count is the same promise the badge makes: it stands for
// rows the fold hid, not for rows the filter already removed.
func TestOutlineFoldedCountIgnoresHiddenClosedDescendants(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: closedChildOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	harness.model.collapsed["bbbb0001"] = true
	harness.model.RefreshRows()
	text := rowTexts(harness)
	if !strings.Contains(text, MoreGlyph+" 1") {
		t.Fatalf("folded count should stand for the one shown child:\n%s", text)
	}
	harness.model.ToggleClosed()
	if text := rowTexts(harness); !strings.Contains(text, MoreGlyph+" 2") {
		t.Fatalf("revealing closed work did not widen the folded count:\n%s", text)
	}
}

// A closed parent is transparent, not a wall: hiding it must not take an open
// child down with it. That is the same hoisting anchorRoots does for the other
// tree views, and the outline owes the reader the same guarantee.
func TestOutlineHoistsAnOpenChildOutOfAHiddenClosedParent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: closedParentOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	if !strings.Contains(text, "Still open inside") {
		t.Fatalf("an open child vanished with its hidden closed parent:\n%s", text)
	}
	if strings.Contains(text, "Closed parent") {
		t.Fatalf("the closed parent painted a row of its own:\n%s", text)
	}
}

// Z and C answer different questions — "not yet" and "no longer" — and neither
// may move the other's rows.
//
// The Outline has always painted unavailable work (it is the unfiltered tree;
// that is what makes it the ordering tab), so Z is a no-op HERE and must stay
// one: all four settings show the deferred row, and only C moves the closed one.
func TestClosedAndDeferredTogglesComposeIndependently(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: deferredAndClosedOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	for _, deferred := range []bool{false, true} {
		for _, closed := range []bool{false, true} {
			harness.model.showDeferred = deferred
			harness.model.showClosed = closed
			harness.model.RefreshRows()
			text := rowTexts(harness)
			if got := strings.Contains(text, "Finished work"); got != closed {
				t.Fatalf("Z=%v C=%v: closed row shown = %v, want %v:\n%s",
					deferred, closed, got, closed, text)
			}
			// Neither toggle owns these two, so all four settings keep them.
			for _, want := range []string{"Live work", "Blocked work"} {
				if !strings.Contains(text, want) {
					t.Fatalf("Z=%v C=%v: %q disappeared:\n%s", deferred, closed, want, text)
				}
			}
		}
	}
}

// Z's own view keeps working while C is on, which is the other half of "these
// two compose": the closed toggle must not reach into the tabs Z governs.
func TestClosedRevealDoesNotDisturbTheDeferredToggleElsewhere(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: nestedStore})
	harness.model.SwitchView(ViewNext)
	harness.model.ToggleClosed()
	if strings.Contains(rowTexts(harness), "Buried child") {
		t.Fatalf("revealing closed work leaked a deferred subtree into Next:\n%s", rowTexts(harness))
	}
	harness.model.ToggleDeferred()
	if !strings.Contains(rowTexts(harness), "Buried child") {
		t.Fatalf("Z stopped working while closed rows were revealed:\n%s", rowTexts(harness))
	}
}

// `/` and `@` narrow what the reveal reveals. Turning history on is not a way
// to escape the filter the user is standing in.
func TestClosedRevealStillObeysSearchAndContextFilters(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.model.ToggleClosed()

	harness.model.filter = "finished"
	harness.model.RefreshRows()
	text := rowTexts(harness)
	if !strings.Contains(text, "Old finished thing") {
		t.Fatalf("the search dropped a revealed closed row that matched:\n%s", text)
	}
	if strings.Contains(text, "Water the plants") {
		t.Fatalf("the reveal leaked rows the search excludes:\n%s", text)
	}

	harness.model.filter = ""
	harness.model.contextFilters = []string{"@home"}
	harness.model.RefreshRows()
	if text := rowTexts(harness); strings.Contains(text, "Old finished thing") {
		t.Fatalf("the reveal ignored the @context filter:\n%s", text)
	}
}

// The Outline is where a reader goes to FIND a broken row, so "hide the closed
// ones" must mean exactly DONE and CANCELLED. A state nobody recognizes is a
// defect, not history: it stays on screen and is never counted as closed.
func TestOutlineKeepsMalformedStatesVisibleAndUncounted(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: malformedStateOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	if !strings.Contains(text, "Typo state") {
		t.Fatalf("an unknown-state row vanished from the repair view:\n%s", text)
	}
	if !strings.Contains(text, "Stateless row") {
		t.Fatalf("a row with no state at all vanished from the repair view:\n%s", text)
	}
	heading := headingRowText(harness.model.Rows(), "Work")
	if !strings.Contains(heading, "3 · 1 closed") {
		t.Fatalf("Work heading %q: the malformed rows should count as shown, not as closed", heading)
	}
	if strings.Contains(text, "closed hidden · C shows") &&
		!strings.Contains(text, "1 closed hidden") {
		t.Fatalf("the hidden-work note miscounted the malformed rows:\n%s", text)
	}
}

// A `/` search drops the outline into its flat shape, where there is no section
// row to carry `· N closed`. Without a note of its own, searching for something
// you finished last week comes back empty and says nothing — and "gone" is the
// wrong conclusion to hand a reader about their own data.
func TestOutlineSearchThatMatchesOnlyClosedWorkSaysSo(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.model.filter = "finished"
	harness.model.RefreshRows()
	text := rowTexts(harness)
	if strings.Contains(text, "Old finished thing") {
		t.Fatalf("the search revealed a closed row on its own:\n%s", text)
	}
	if !strings.Contains(text, "1 closed hidden · C shows") {
		t.Fatalf("a search matching only hidden work returned nothing and said nothing:\n%s", text)
	}
	harness.model.ToggleClosed()
	text = rowTexts(harness)
	if !strings.Contains(text, "Old finished thing") {
		t.Fatalf("the note's own key did not produce the row:\n%s", text)
	}
	if strings.Contains(text, "closed hidden") {
		t.Fatalf("the note outstayed the thing it was about:\n%s", text)
	}
}

// A store swept into DONE but not yet archived has nothing open and no section
// to badge. A blank pane there reads as a broken tab rather than as a finished
// list.
func TestOutlineOfNothingButClosedWorkIsNotBlank(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: closedRootsOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	text := rowTexts(harness)
	if strings.TrimSpace(text) == "" {
		t.Fatal("an outline of only closed work painted a blank pane")
	}
	if !strings.Contains(text, "2 closed hidden · C shows") {
		t.Fatalf("the pane does not say what it is holding:\n%s", text)
	}
	harness.model.ToggleClosed()
	if !strings.Contains(rowTexts(harness), "Swept but unarchived") {
		t.Fatalf("the toggle did not produce the rows:\n%s", rowTexts(harness))
	}
}

// The note is absent when nothing is hidden — an outline with no history pays
// nothing for it.
func TestOutlineWithNoClosedWorkCarriesNoNote(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: proposalOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	if strings.Contains(rowTexts(harness), "closed hidden") {
		t.Fatalf("a note appeared with nothing to report:\n%s", rowTexts(harness))
	}
}

// A folded row's count and its chevron are two statements about the same fact.
// `▾ 0` beside an expandable marker is a contradiction — and it happened when
// the count knew only about tasks while the fold also hid a section heading.
func TestOutlineFoldedCountNeverContradictsItsChevron(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: foldedSectionOutlineFixture})
	harness.model.SwitchView(ViewOutline)
	harness.model.collapsed["bbbb0001"] = true
	harness.model.RefreshRows()
	text := rowTexts(harness)
	if strings.Contains(text, MoreGlyph+" 0") {
		t.Fatalf("a fold reported hiding nothing while wearing a chevron:\n%s", text)
	}
	if !strings.Contains(text, MoreGlyph+" 1") {
		t.Fatalf("the fold did not count the section heading it hid:\n%s", text)
	}
	if strings.Contains(text, "Nested") {
		t.Fatalf("the fold did not actually hide the heading:\n%s", text)
	}
}

// The marker and the count come from one fact and must never disagree: an
// expandable row always hides something, a leaf never does.
func TestOutlineFoldMarkersAndCountsAgreeAcrossTheToggle(t *testing.T) {
	for _, live := range []string{
		fixtureStore, foldedSectionOutlineFixture, closedChildOutlineFixture, closedRootsOutlineFixture,
	} {
		for _, showClosed := range []bool{false, true} {
			harness := newModelHarness(t, harnessOptions{live: live})
			harness.model.SwitchView(ViewOutline)
			harness.model.showClosed = showClosed
			harness.model.CollapseAll()
			for _, row := range harness.model.Rows() {
				if row.Node == nil {
					continue
				}
				expandable := outlineRenders(harness.model.treeRequest(), row.Node)
				if expandable != (outlineDescendantCount(harness.model.treeRequest(), row.Node) > 0) {
					t.Fatalf("showClosed=%v: marker and count disagree on %q", showClosed, row.Text)
				}
				if row.HasMarker() != expandable {
					t.Fatalf("showClosed=%v: %q wears the wrong marker", showClosed, row.Text)
				}
			}
		}
	}
}

// A paused draft whose task got COMPLETED is one keystroke from recovery, and
// the panel that exists to rescue it has to name that keystroke.
func TestPausedDraftOnAClosedTargetNamesTheClosedToggle(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("\r", "e", "!")
	harness.model.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	harness.rewrite(strings.Replace(fixtureStore,
		`"state":"NEXT","priority":"A","title":"Book flight`,
		`"state":"DONE","priority":"A","title":"Book flight`, 1))
	harness.model.Refresh()
	panel := harness.model.Panel()
	if panel == nil || panel.Kind != PanelSuspendedTaskEdit {
		t.Fatal("the completed target did not raise a recovery panel")
	}
	lines := strings.Join(panel.Lines, "\n")
	if !strings.Contains(lines, "C in the Outline") {
		t.Fatalf("the recovery panel does not name the toggle that recovers it:\n%s", lines)
	}
	if !strings.Contains(harness.model.FlashMessage(), "C in the Outline") {
		t.Fatalf("the flash does not name the toggle: %q", harness.model.FlashMessage())
	}
}

const malformedStateOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Live work"}
{"type":"task","id":"bbbb0002","parent":"aaaa0003","state":"TODOO","title":"Typo state"}
{"type":"task","id":"bbbb0003","parent":"aaaa0003","title":"Stateless row"}
{"type":"task","id":"bbbb0004","parent":"aaaa0003","state":"DONE","title":"Real history","closed":"2026-06-20"}
`

const closedRootsOutlineFixture = `{"type":"meta","version":2}
{"type":"task","id":"bbbb0001","state":"DONE","title":"Swept but unarchived","closed":"2026-06-20"}
{"type":"task","id":"bbbb0002","state":"CANCELLED","title":"Dropped root","closed":"2026-06-21"}
`

const foldedSectionOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action"}
{"type":"section","id":"bbbb0002","parent":"bbbb0001","title":"Nested heading"}
{"type":"task","id":"bbbb0003","parent":"bbbb0002","state":"DONE","title":"Nested history","closed":"2026-06-20"}
`

// A reveal has to be visible in the chrome, and only where it is in force: a
// header that claimed closed rows were shown on Agenda would describe a list
// that is not there.
func TestClosedRevealIsNamedInTheHeaderOnlyWhereItApplies(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	if strings.Contains(harness.model.headerCount(), "closed shown") {
		t.Fatal("the header claimed closed rows were shown by default")
	}
	harness.model.ToggleClosed()
	if !strings.Contains(harness.model.headerCount(), "closed shown") {
		t.Fatalf("the header did not name the reveal: %q", harness.model.headerCount())
	}
	harness.model.SwitchView(ViewAgenda)
	if strings.Contains(harness.model.headerCount(), "closed shown") {
		t.Fatalf("the header named a reveal on a tab it does not touch: %q",
			harness.model.headerCount())
	}
}

// The toggle is an errand, not a preference: the next session opens on the work.
func TestClosedToggleIsNotPersistedInTheSession(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.model.ToggleClosed()
	if !harness.model.showClosed {
		t.Fatal("the toggle did not turn on")
	}
	harness.model.Save()

	revived := newModelHarness(t, harnessOptions{})
	if revived.model.showClosed {
		t.Fatal("the closed-row toggle survived a restart")
	}
	revived.model.SwitchView(ViewOutline)
	if strings.Contains(rowTexts(revived), "Old finished thing") {
		t.Fatalf("a restarted outline opened on closed rows:\n%s", rowTexts(revived))
	}
}

// headingRowText is one outline heading's whole row, badge included.
func headingRowText(rows []Row, label string) string {
	for _, row := range rows {
		if row.Item != nil {
			continue
		}
		text := strings.TrimSpace(row.Text)
		text = strings.TrimPrefix(strings.TrimPrefix(text, MarkExpanded), MarkCollapsed)
		if strings.HasPrefix(text, label+" ") {
			return row.Text
		}
	}
	return ""
}

func paletteOffers(harness *modelHarness, handler string) bool {
	for _, entry := range shortcuts.PaletteEntries(shortcuts.List, harness.model.availability) {
		if entry.Handler == handler {
			return true
		}
	}
	return false
}

const proposalOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Live work"}
{"type":"task","id":"bbbb0002","parent":"aaaa0003","state":"PROPOSED","title":"Proposed thing"}
`

const closedOnlyOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Archive candidates"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"DONE","title":"Swept later","closed":"2026-06-20"}
{"type":"task","id":"bbbb0002","parent":"aaaa0003","state":"CANCELLED","title":"Dropped idea","closed":"2026-06-21"}
`

const closedChildOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Parent action"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"Open child"}
{"type":"task","id":"bbbb0003","parent":"bbbb0001","state":"DONE","title":"Closed child","closed":"2026-06-20"}
`

const closedParentOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"DONE","title":"Closed parent","closed":"2026-06-20"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"Still open inside"}
`

const deferredAndClosedOutlineFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"bbbb0001","parent":"aaaa0003","state":"NEXT","title":"Live work"}
{"type":"task","id":"bbbb0002","parent":"aaaa0003","state":"TODO","title":"Blocked work","scheduled":"2027-01-01"}
{"type":"task","id":"bbbb0003","parent":"aaaa0003","state":"DONE","title":"Finished work","closed":"2026-06-20"}
`

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
	// The badge is the fold's whole point: it survives the fold and still says
	// what the section holds — the four it is showing, plus the closed row the
	// toggle is holding back.
	if heading := headingRowText(harness.model.Rows(), "Work"); !strings.Contains(heading, "4 · 1 closed") {
		t.Fatalf("folded Work badge %q, want the full shown count and its leftovers:\n%s", heading, text)
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

// -- issue #19: Projects shows closed work behind the same C toggle --------

// projectsClosedStore is one project holding open work, a closed parent with an
// open child under it, and a second project whose only live child is CANCELLED.
// The Inbox holds finished work of its own, which Projects owes nobody.
const projectsClosedStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"NEXT","title":"pick a generator"}
{"type":"task","id":"bbbb0002","parent":"aaaa0002","state":"DONE","title":"buy the domain"}
{"type":"task","id":"bbbb0003","parent":"bbbb0002","state":"TODO","title":"renew it yearly"}
{"type":"section","id":"aaaa0003","parent":"aaaa0001","title":"Mid-year reviews"}
{"type":"task","id":"bbbb0004","parent":"aaaa0003","state":"CANCELLED","title":"draft the memo"}
{"type":"section","id":"aaaa0004","parent":"aaaa0001","title":"RaaS consolidation"}
{"type":"section","id":"aaaa0005","title":"Inbox"}
{"type":"task","id":"bbbb0005","parent":"aaaa0005","state":"DONE","title":"unfiled leftover"}
`

// The landing page does not change. Projects opens on what is moving, closed
// rows stay out, the dormant tail still rolls up the quiet projects, and the
// open child of a completed parent is still hoisted into its place.
func TestProjectsStillOpensOnOpenWorkOnly(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	text := rowTexts(harness)
	for _, gone := range []string{"buy the domain", "draft the memo", "unfiled leftover"} {
		if strings.Contains(text, gone) {
			t.Fatalf("Projects showed closed row %q before anyone asked:\n%s", gone, text)
		}
	}
	if !strings.Contains(text, "pick a generator") {
		t.Fatalf("Projects lost its open work:\n%s", text)
	}
	// Hoisting: the open child survives its completed parent's pruning.
	if !strings.Contains(text, "renew it yearly") {
		t.Fatalf("the open child of a closed parent was pruned with it:\n%s", text)
	}
	// Mid-year reviews has only a CANCELLED child, so it is still tail, not
	// heading; RaaS has nothing at all and keeps it company.
	if !strings.Contains(text, "Mid-year reviews · RaaS consolidation") {
		t.Fatalf("the dormant tail changed shape:\n%s", text)
	}
}

// The reveal is the whole point of the tab for a finished commitment: DONE and
// CANCELLED rows arrive under the project they were filed in, wearing the same
// dot and the same `· cancelled` suffix the Outline gives them.
func TestProjectsRevealsClosedWorkUnderItsProject(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.model.ToggleClosed()
	text := rowTexts(harness)
	if !strings.Contains(text, DotClosed+"    buy the domain") {
		t.Fatalf("the DONE row did not arrive in the Outline's vocabulary:\n%s", text)
	}
	if !strings.Contains(text, DotClosed+"    draft the memo · cancelled") {
		t.Fatalf("the CANCELLED row lost its reason word:\n%s", text)
	}
	harness.model.ToggleClosed()
	if text := rowTexts(harness); strings.Contains(text, "buy the domain") {
		t.Fatalf("toggling back did not hide the closed rows again:\n%s", text)
	}
}

// A project whose only live children are closed is a finished commitment, not
// an empty one. With the reveal on it earns its heading back — and stops being
// warned about as `stuck`, which would be a false alarm about work that is over.
func TestAClosedOnlyProjectBecomesARealHeadingWhenRevealed(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.model.ToggleClosed()
	text := rowTexts(harness)
	if !strings.Contains(text, MarkExpanded+"Mid-year reviews") {
		t.Fatalf("a closed-only project stayed on the dormant tail:\n%s", text)
	}
	if strings.Contains(text, "Mid-year reviews  ⚠ stuck") {
		t.Fatalf("a finished project was warned about as stalled:\n%s", text)
	}
	// It folds like any other heading, because it has something to fold.
	harness.selectRowByID("aaaa0003")
	harness.model.CollapseSelected()
	if text := rowTexts(harness); strings.Contains(text, "draft the memo") {
		t.Fatalf("the revealed project heading would not fold:\n%s", text)
	}
	// A project with nothing at all under it is still tail — the reveal widens
	// what counts as live, it does not abolish the tail.
	if !strings.Contains(rowTexts(harness), MarkCollapsed+"RaaS consolidation") {
		t.Fatalf("a genuinely empty project left the dormant tail:\n%s", rowTexts(harness))
	}
}

// Hoisting is the open-only mode's rule and stays there. With history revealed
// the tab shows the tree the FILE holds, or a reader reviewing what a project
// did would be told the wrong story about which step produced which.
func TestRevealedProjectsShowTheStoredTreeRatherThanHoisting(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.model.ToggleClosed()
	rows := harness.model.Rows()
	parentAt, childAt := -1, -1
	for index, row := range rows {
		if row.Item == nil {
			continue
		}
		switch row.Item.ID {
		case "bbbb0002":
			parentAt = index
		case "bbbb0003":
			childAt = index
		}
	}
	if parentAt < 0 || childAt < 0 {
		t.Fatalf("missing the parent or the child:\n%s", rowTexts(harness))
	}
	if childAt != parentAt+1 {
		t.Fatalf("the child did not land directly under its parent (%d, %d):\n%s",
			parentAt, childAt, rowTexts(harness))
	}
	// Nesting, not adjacency. A descendant wears the dim thread the subtree
	// walker drops at every level below its anchor; an anchor does not. That
	// glyph IS the difference between "shown under it" and "hoisted beside it".
	if !strings.Contains(rows[childAt].Text, "│") {
		t.Fatalf("the child anchored in its own right instead of nesting:\n%s", rowTexts(harness))
	}
	if strings.Contains(rows[parentAt].Text, "│") {
		t.Fatalf("the closed parent is not the anchor it should be:\n%s", rowTexts(harness))
	}
	// The closed parent is a container now, so it offers the fold its children
	// justify — a chevron that opens onto something.
	if rows[parentAt].MarkerBegin == rows[parentAt].MarkerEnd {
		t.Fatalf("the revealed closed parent has no fold marker:\n%s", rowTexts(harness))
	}

	// The contrast: with the reveal off, the same child is hoisted into the
	// closed parent's place and threads off nothing.
	harness.model.ToggleClosed()
	for _, row := range harness.model.Rows() {
		if row.Item != nil && row.Item.ID == "bbbb0003" && strings.Contains(row.Text, "│") {
			t.Fatalf("open-only mode stopped hoisting:\n%s", rowTexts(harness))
		}
	}
}

// Projects is a listing of commitments. An unfiled capture has no project to
// sit beneath, so it is out of the tab whatever the toggle says — otherwise
// "show me what this project finished" would quietly include the Inbox.
func TestProjectsKeepsUnfiledClosedWorkOut(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.model.ToggleClosed()
	if text := rowTexts(harness); strings.Contains(text, "unfiled leftover") {
		t.Fatalf("the reveal dragged unfiled closed work into Projects:\n%s", text)
	}
	// And the note must not promise it either: it counts what C would paint.
	harness.model.ToggleClosed()
	if text := rowTexts(harness); !strings.Contains(text, "2 closed hidden · C shows") {
		t.Fatalf("the hidden-work note counted the unfiled row:\n%s", text)
	}
}

// `x` sweeps closed rows into archive.jsonl; this toggle is about the ones
// still in the live file. Widening to the archive under the same key would make
// "show closed" and "show the archive" the same surprise.
func TestProjectsRevealNeverReachesTheArchive(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"NEXT","title":"pick a generator"}
`
	archived := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"cccc0001","parent":"aaaa0002","state":"DONE","title":"swept away"}
`
	harness := newModelHarness(t, harnessOptions{live: live, archive: archived})
	harness.model.SwitchView(ViewProjects)
	harness.model.ToggleClosed()
	if text := rowTexts(harness); strings.Contains(text, "swept away") {
		t.Fatalf("the reveal reached into archive.jsonl:\n%s", text)
	}
}

// Z and C answer different questions — "what can I not do yet" and "what do I
// no longer have to" — and each has to keep working while the other is on.
// A Z-hidden parent still takes its subtree with it, history included, so
// revealing finished work cannot hoist it out of the parked parent it belongs
// to and drop it at the top of the project.
func TestProjectsClosedAndDeferredTogglesCompose(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"TODO","title":"parked parent","scheduled":"2099-01-01"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"DONE","title":"finished under park"}
{"type":"task","id":"bbbb0003","parent":"aaaa0002","state":"DONE","title":"plain finished"}
`
	for _, want := range []struct {
		deferred, closed             bool
		parked, underPark, plainDone bool
	}{
		{false, false, false, false, false},
		{true, false, true, false, false},
		{false, true, false, false, true},
		{true, true, true, true, true},
	} {
		harness := newModelHarness(t, harnessOptions{live: live})
		harness.model.SwitchView(ViewProjects)
		if want.deferred {
			harness.model.ToggleDeferred()
		}
		if want.closed {
			harness.model.ToggleClosed()
		}
		text := rowTexts(harness)
		for _, check := range []struct {
			title string
			want  bool
		}{
			{"parked parent", want.parked},
			{"finished under park", want.underPark},
			{"plain finished", want.plainDone},
		} {
			if got := strings.Contains(text, check.title); got != check.want {
				t.Fatalf("Z=%v C=%v: %q shown = %v, want %v:\n%s",
					want.deferred, want.closed, check.title, got, check.want, text)
			}
		}
	}
}

// Projects is a commitment listing, not the repair view. A state nobody
// recognizes is neither commitment nor history: it stays out of the tab both
// ways round, and `C` never claims it as something the reader finished. The
// Outline is where a broken row is meant to surface, and still does.
func TestProjectsNeverTreatsAMalformedStateAsClosed(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"NEXT","title":"pick a generator"}
{"type":"task","id":"bbbb0002","parent":"aaaa0002","state":"TODOO","title":"typo state row"}
{"type":"task","id":"bbbb0003","parent":"aaaa0002","state":"DONE","title":"genuinely finished"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewProjects)
	if text := rowTexts(harness); !strings.Contains(text, "1 closed hidden · C shows") {
		t.Fatalf("the malformed row was counted as history:\n%s", text)
	}
	harness.model.ToggleClosed()
	text := rowTexts(harness)
	if strings.Contains(text, "typo state row") {
		t.Fatalf("C claimed a malformed row as finished work:\n%s", text)
	}
	if !strings.Contains(text, "genuinely finished") {
		t.Fatalf("C missed the row that really is finished:\n%s", text)
	}
	harness.model.SwitchView(ViewOutline)
	if text := rowTexts(harness); !strings.Contains(text, "typo state row") {
		t.Fatalf("the repair view lost the malformed row:\n%s", text)
	}
}

// Badges count what is on the screen, and what is being held back is named
// beside them rather than folded into them — the Outline's `4 · 1 closed`
// vocabulary, where `· N closed` always means work that is NOT painted.
func TestProjectsBadgesCountWhatIsShownAndNameWhatIsHeldBack(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	text := rowTexts(harness)
	if !strings.Contains(text, "Launch the site  · 1 closed") {
		t.Fatalf("the project row hid the fact that it holds history:\n%s", text)
	}
	if !strings.Contains(text, "1/3 open") {
		t.Fatalf("the section badge no longer names the live share:\n%s", text)
	}
	harness.model.ToggleClosed()
	text = rowTexts(harness)
	if strings.Contains(text, "closed") && !strings.Contains(text, "· cancelled") {
		t.Fatalf("a leftover marker survived the thing it was about:\n%s", text)
	}
	// Two of the three projects now have rows, and neither is "open" — one is
	// finished. The word follows the number.
	if !strings.Contains(text, "2/3 shown") {
		t.Fatalf("the section badge called a finished project open:\n%s", text)
	}
}

// A `/` search drops Projects into its flat shape, where a project whose only
// matches are closed produces no group at all — the same blind spot the Outline
// answers with one muted line, for the same reason.
func TestProjectsSearchThatMatchesOnlyClosedWorkSaysSo(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.model.filter = "memo"
	harness.model.RefreshRows()
	text := rowTexts(harness)
	if strings.Contains(text, "draft the memo") {
		t.Fatalf("the search revealed a closed row on its own:\n%s", text)
	}
	if !strings.Contains(text, "1 closed hidden · C shows") {
		t.Fatalf("a search matching only hidden work said nothing:\n%s", text)
	}
	harness.model.ToggleClosed()
	text = rowTexts(harness)
	if !strings.Contains(text, "draft the memo") {
		t.Fatalf("the note's own key did not produce the row:\n%s", text)
	}
	if strings.Contains(text, "closed hidden") {
		t.Fatalf("the note outstayed the thing it was about:\n%s", text)
	}
	// The flat heading counts OPEN rows, so its number stays true beneath a
	// group the reveal has added finished rows to.
	if !strings.Contains(text, "Mid-year reviews  0 open") {
		t.Fatalf("the flat heading counted a closed row as open:\n%s", text)
	}
}

// The toggle is an errand, not a preference: nothing about it is written to the
// session file, so the next launch opens on the work.
func TestProjectsClosedRevealIsNotPersisted(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.press('C')
	if !harness.model.showClosed {
		t.Fatal("C did not reveal closed rows on the Projects tab")
	}
	harness.model.Save()

	revived := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	if revived.model.showClosed {
		t.Fatal("the closed reveal survived a restart")
	}
	revived.model.SwitchView(ViewProjects)
	if strings.Contains(rowTexts(revived), "buy the domain") {
		t.Fatalf("a restarted Projects tab opened on the history:\n%s", rowTexts(revived))
	}
}

// An AREA is defined as a top-level list that currently holds open work, so one
// whose work is all finished is not an area any more and leaves the listing
// entirely — today, before any toggle. That definition lives in the shared core
// (Queries.Projects, which `tasks projects` and GET /api/v1/projects both read),
// so the TUI does not widen it for a keystroke. What the tab must not do is
// PROMISE the row anyway: the hidden-work note counts only what C would paint.
func TestTheClosedRevealDoesNotResurrectAFinishedArea(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Launch the site"}
{"type":"task","id":"bbbb0001","parent":"aaaa0002","state":"NEXT","title":"pick a generator"}
{"type":"section","id":"aaaa0010","title":"Home"}
{"type":"task","id":"bbbb0010","parent":"aaaa0010","state":"DONE","title":"finished chore"}
{"type":"section","id":"aaaa0011","title":"Garden"}
{"type":"task","id":"bbbb0011","parent":"aaaa0011","state":"TODO","title":"live chore"}
{"type":"task","id":"bbbb0012","parent":"aaaa0011","state":"DONE","title":"done chore"}
`
	harness := newModelHarness(t, harnessOptions{live: live})
	harness.model.SwitchView(ViewProjects)
	// One hidden row, not two: the finished area's row is not on offer, so the
	// note must not count it.
	if text := rowTexts(harness); !strings.Contains(text, "1 closed hidden · C shows") {
		t.Fatalf("the note promised a row the toggle cannot produce:\n%s", text)
	}
	harness.model.ToggleClosed()
	text := rowTexts(harness)
	if strings.Contains(text, "finished chore") || strings.Contains(text, "Home") {
		t.Fatalf("the reveal resurrected an area the shared listing drops:\n%s", text)
	}
	// An area that still holds open work keeps its history, like any project.
	if !strings.Contains(text, "done chore") {
		t.Fatalf("a living area lost its finished work:\n%s", text)
	}
}

// The header names a reveal only where it changes something. Projects is now
// one of the two tabs where it does, and the Agenda is still not.
func TestProjectsHeaderNamesTheClosedReveal(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: projectsClosedStore})
	harness.model.SwitchView(ViewProjects)
	harness.model.ToggleClosed()
	if !strings.Contains(harness.model.headerCount(), "closed shown") {
		t.Fatalf("the Projects header hid the reveal: %q", harness.model.headerCount())
	}
	harness.model.SwitchView(ViewAgenda)
	if strings.Contains(harness.model.headerCount(), "closed shown") {
		t.Fatalf("the header named a reveal on a tab it does not touch: %q",
			harness.model.headerCount())
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

// -- the Approvals queue's triage order (issue #8) --------------------------

// triageStore and triageOrder are the fixture and the expected order the CLI and
// API triage tests use, restated here because the three packages cannot import
// each other's tests. Keep the three copies identical: they are the assertion
// that all three surfaces rank the proposed queue the same way.
const triageStore = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Intake"}
{"type":"task","id":"bbbb0001","parent":"aaaa0001","state":"PROPOSED","title":"C tomorrow","priority":"C","deadline":"2026-07-21"}
{"type":"task","id":"bbbb0002","parent":"aaaa0001","state":"PROPOSED","title":"undated A","priority":"A"}
{"type":"task","id":"bbbb0003","parent":"aaaa0001","state":"PROPOSED","title":"unranked dated","deadline":"2026-07-26"}
{"type":"task","id":"bbbb0004","parent":"aaaa0001","state":"PROPOSED","title":"B far","priority":"B","deadline":"2026-09-01"}
{"type":"task","id":"bbbb0005","parent":"aaaa0001","state":"PROPOSED","title":"A overdue","priority":"A","deadline":"2026-07-01"}
{"type":"task","id":"bbbb0006","parent":"aaaa0001","state":"PROPOSED","title":"B soon","priority":"B","deadline":"2026-07-22"}
{"type":"task","id":"bbbb0007","parent":"aaaa0001","state":"PROPOSED","title":"unranked undated"}
{"type":"task","id":"bbbb0008","parent":"aaaa0001","state":"NEXT","title":"accepted work"}
`

var triageOrder = []string{
	"A overdue", "undated A", "B soon", "B far", "C tomorrow",
	"unranked dated", "unranked undated",
}

func TestInboxApprovalsAreRankedByPriorityThenDue(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: triageStore})
	harness.model.SwitchView(ViewInbox)

	approvals := []string{}
	for _, row := range harness.model.Rows() {
		if row.Item != nil && row.Item.State == "PROPOSED" {
			approvals = append(approvals, row.Item.Title)
		}
	}
	if strings.Join(approvals, "|") != strings.Join(triageOrder, "|") {
		t.Fatalf("approvals = %v\nwant        %v\n\n%s", approvals, triageOrder, rowTexts(harness))
	}
}

// The ordering has to be READABLE from the rows, or it looks arbitrary: the
// priority letter leads the body and the shared date column carries the
// deadline, so a reader can see why row two follows row one.
func TestApprovalRowsShowThePriorityAndDueTheOrderReadsBy(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: triageStore})
	harness.model.SwitchView(ViewInbox)
	for _, row := range harness.model.Rows() {
		if row.Item == nil || row.Item.Title != "A overdue" {
			continue
		}
		if !strings.Contains(row.Text, "A") || !strings.Contains(row.Text, "A overdue") {
			t.Fatalf("the approval row dropped its priority letter: %q", row.Text)
		}
		if !strings.Contains(rowTexts(harness), "d ago") {
			t.Fatalf("the approvals section shows no due information:\n%s", rowTexts(harness))
		}
		return
	}
	t.Fatalf("no approval row for the overdue A:\n%s", rowTexts(harness))
}
