package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// These drive the ROOT MODEL with keys, the way a user does, so they check the
// wiring — mode, selection, message and stored bytes — rather than the
// components in isolation.

// pressKeys sends a raw byte sequence through the same path a terminal key
// takes, so a test and the differential harness exercise one dispatcher.
func (h *modelHarness) pressKeys(sequences ...string) {
	h.t.Helper()
	for _, sequence := range sequences {
		h.model.Update(keyMessage(sequence))
	}
}

// keyMessage is the inverse of KeySequence: it turns a byte sequence back into
// the decoded message Bubble Tea would deliver.
func keyMessage(sequence string) tea.KeyMsg {
	for keyType, bytes := range keyTypeSequences {
		if bytes == sequence {
			return tea.KeyMsg{Type: keyType}
		}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(sequence)}
}

func (h *modelHarness) content() string {
	h.t.Helper()
	raw, err := os.ReadFile(h.org)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

// -- the help modal --------------------------------------------------------------

func TestHelpOpensFiltersAndCloses(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.pressKeys("?")
	if harness.model.Mode() != ModeModal {
		t.Fatalf("? left the mode at %s", harness.model.Mode())
	}
	if harness.model.Modal().Kind() != ModalHelp {
		t.Fatalf("? opened %s", harness.model.Modal().Kind())
	}

	// Typing a letter with no modal binding of its own opens the live filter
	// immediately — `/` is only needed to resume editing a committed one.
	harness.pressKeys("v")
	if harness.model.Mode() != ModeModalFilter {
		t.Fatalf("typing in a filterable modal left the mode at %s", harness.model.Mode())
	}
	if harness.model.ModalFilterInput() != "v" {
		t.Errorf("the typed character did not reach the filter: %q", harness.model.ModalFilterInput())
	}
	if len(harness.model.Modal().Lines()) == len(harness.model.Modal().AllLines()) {
		t.Error("the filter matched everything; it is not narrowing")
	}

	// Escape clears the filter and returns to the modal, NOT to the list — one
	// escape undoes one thing.
	harness.pressKeys("\x1b")
	if harness.model.Mode() != ModeModal {
		t.Fatalf("escape from the filter left the mode at %s", harness.model.Mode())
	}
	if harness.model.Modal().Filter() != "" {
		t.Error("escape did not clear the filter")
	}

	harness.pressKeys("\x1b")
	if harness.model.Mode() != ModeList {
		t.Errorf("escape did not close the modal: %s", harness.model.Mode())
	}
}

func TestHelpIsGeneratedFromTheRegistry(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.pressKeys("?")
	joined := strings.Join(harness.model.Modal().AllLines(), "\n")
	for _, want := range []string{"in the task list", "while editing a task", "complete selected task"} {
		if !strings.Contains(joined, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}

func TestModalScrollKeysMoveTheWindow(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.pressKeys("?")
	harness.pressKeys("j", "j")
	if harness.model.Modal().Scroll() != 2 {
		t.Errorf("j scrolled to %d, want 2", harness.model.Modal().Scroll())
	}
	harness.pressKeys("\x15") // ctrl-u
	if harness.model.Modal().Scroll() >= 2 {
		t.Errorf("ctrl-u did not scroll back: %d", harness.model.Modal().Scroll())
	}
}

// -- the palettes ------------------------------------------------------------------

func TestActionPaletteRunsTheChosenAction(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	harness.pressKeys(":")
	if harness.model.Mode() != ModePalette {
		t.Fatalf(": left the mode at %s", harness.model.Mode())
	}
	for _, key := range strings.Split("expand all", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if harness.model.Mode() != ModeList {
		t.Errorf("running an action left the mode at %s", harness.model.Mode())
	}
	if harness.model.ActionPalette() != nil {
		t.Error("the palette outlived the action it ran")
	}
}

func TestActionPaletteEscapeReturnsToTheList(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.pressKeys(":", "\x1b")
	if harness.model.Mode() != ModeList || harness.model.ActionPalette() != nil {
		t.Errorf("escape left mode %s with palette %v",
			harness.model.Mode(), harness.model.ActionPalette())
	}
}

func TestContextPaletteAppliesAFilterAndTheListNarrows(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	before := len(harness.model.Rows())
	harness.pressKeys("@")
	if harness.model.Mode() != ModeContextPalette {
		t.Fatalf("@ left the mode at %s", harness.model.Mode())
	}
	for _, key := range strings.Split("home", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if harness.model.Mode() != ModeList {
		t.Fatalf("applying a filter left the mode at %s", harness.model.Mode())
	}
	if got := harness.model.ContextFilters(); len(got) != 1 || got[0] != "@home" {
		t.Fatalf("filters %v, want @home", got)
	}
	if !strings.Contains(harness.model.FlashMessage(), "@home") {
		t.Errorf("the applied filter was not announced: %q", harness.model.FlashMessage())
	}
	if len(harness.model.Rows()) >= before {
		t.Error("applying a context filter did not narrow the list")
	}
}

func TestEscapeClearsAnAppliedContextFilter(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.ApplyContextFilter([]string{"@home"})
	harness.pressKeys("\x1b")
	if len(harness.model.ContextFilters()) != 0 {
		t.Errorf("escape left filters %v", harness.model.ContextFilters())
	}
}

// -- quick forms that write --------------------------------------------------------

func TestTheDatePopupWritesTheDeadlineAndReportsIt(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	harness.pressKeys("d")
	if harness.model.Mode() != ModeForm {
		t.Fatalf("d left the mode at %s", harness.model.Mode())
	}
	for _, key := range strings.Split("2026-08-09", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if harness.model.Mode() != ModeList {
		t.Fatalf("submitting left the mode at %s: %q",
			harness.model.Mode(), harness.model.Form().Error())
	}
	if !strings.Contains(harness.content(), `"deadline":"2026-08-09"`) {
		t.Error("the date popup did not write the deadline")
	}
	if !strings.Contains(harness.model.FlashMessage(), "2026-08-09") {
		t.Errorf("the write was not reported: %q", harness.model.FlashMessage())
	}
}

func TestAnUnparseableDateKeepsTheFormOpenWithItsReason(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	before := harness.content()
	harness.pressKeys("d")
	for _, key := range strings.Split("nonsense", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if harness.model.Mode() != ModeForm {
		t.Fatalf("a refused submit closed the form; mode is %s", harness.model.Mode())
	}
	if harness.model.Form().Error() == "" {
		t.Error("the refusal said nothing")
	}
	if harness.content() != before {
		t.Error("a refused submit still wrote")
	}
}

func TestTheRecurrencePopupGlossesWhatTheTypedCookieMeans(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	harness.pressKeys("r")
	for _, key := range strings.Split("weekly", "") {
		harness.pressKeys(key)
	}
	if hint := harness.model.Form().Hint(60); !strings.Contains(hint, "week") {
		t.Errorf("the live hint does not explain the cookie: %q", hint)
	}
	harness.pressKeys("\r")
	if !strings.Contains(harness.content(), `"recur":".+1w"`) {
		t.Error("the recurrence popup did not write the canonical cookie")
	}
}

func TestEscapeCancelsAQuickFormWithoutWriting(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	before := harness.content()
	harness.pressKeys("d", "2", "\x1b")
	if harness.model.Mode() != ModeList || harness.model.Form() != nil {
		t.Errorf("escape left mode %s with form %v", harness.model.Mode(), harness.model.Form())
	}
	if harness.content() != before {
		t.Error("a cancelled form wrote")
	}
}

// -- project actions ------------------------------------------------------------------

func TestCompletingAProjectConfirmsFirstThenCloses(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewProjects)
	if !selectFirstProject(harness) {
		t.Skip("the fixture exposes no project header in this view")
	}
	before := harness.content()
	harness.pressKeys("c")
	if harness.model.Mode() != ModeModal ||
		harness.model.Modal().Kind() != ModalProjectCompleteConfirm {
		t.Fatalf("c on a project produced mode %s", harness.model.Mode())
	}
	if harness.content() != before {
		t.Error("the confirmation wrote before it was answered")
	}
	harness.pressKeys("n")
	if harness.model.Mode() != ModeList {
		t.Errorf("declining left the mode at %s", harness.model.Mode())
	}
	if harness.content() != before {
		t.Error("a declined confirmation still wrote")
	}
	if !strings.Contains(harness.model.FlashMessage(), "cancelled") {
		t.Errorf("declining was not reported: %q", harness.model.FlashMessage())
	}
}

func TestRenamingAProjectWritesTheNewTitle(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewProjects)
	if !selectFirstProject(harness) {
		t.Skip("the fixture exposes no project header in this view")
	}
	harness.pressKeys("e")
	if harness.model.Mode() != ModeForm {
		t.Fatalf("e on a project produced mode %s", harness.model.Mode())
	}
	// The form is prefilled with the current title; replace it entirely.
	harness.model.Form().SetText("Renamed project")
	harness.pressKeys("\r")
	if !strings.Contains(harness.content(), "Renamed project") {
		t.Error("the rename did not reach the store")
	}
}

func selectFirstProject(harness *modelHarness) bool {
	for index, row := range harness.model.Rows() {
		if row.Project != nil {
			harness.model.selectRow(index)
			return true
		}
	}
	return false
}

// -- quick actions that write ------------------------------------------------------

func TestCompletingATaskWritesDoneAndSaysWhatHappened(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPR)
	harness.pressKeys("c")
	if !strings.Contains(harness.content(), `"id":"aaaa0005"`) {
		t.Fatal("the fixture task vanished")
	}
	if !strings.Contains(taskLineIn(harness.content(), fixPR), `"state":"DONE"`) {
		t.Error("c did not close the task")
	}
	if !strings.Contains(harness.model.FlashMessage(), "DONE") {
		t.Errorf("the completion was not reported: %q", harness.model.FlashMessage())
	}
}

func TestPriorityKeysWalkTheLadderAndStopAtItsEnds(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight) // starts at A, the top
	before := harness.content()
	harness.pressKeys("K")
	if harness.content() != before {
		t.Error("raising past the top of the ladder wrote")
	}
	harness.pressKeys("J")
	if !strings.Contains(taskLineIn(harness.content(), fixFlight), `"priority":"B"`) {
		t.Error("J did not lower the priority")
	}
}

func TestTheEditorOpensFromTheDetailPanelAndSavesThroughTheFacade(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	harness.pressKeys("\r") // open details
	harness.pressKeys("e")
	if harness.model.Mode() != ModeTaskEdit {
		t.Fatalf("e in the detail panel produced mode %s (%q)",
			harness.model.Mode(), harness.model.FlashMessage())
	}
	harness.pressKeys("!")
	harness.pressKeys("\x0f") // ctrl-o finishes, saving the focused buffer
	if harness.model.Mode() != ModeList {
		t.Fatalf("ctrl-o left the mode at %s", harness.model.Mode())
	}
	if !strings.Contains(harness.content(), "Book flight in Concur!") {
		t.Error("the editor's save did not reach the store")
	}
}

// -- refusals ----------------------------------------------------------------------

// Ruby speaks for exactly two unavailable families — ordering and delegation —
// and consumes the rest in silence. Both halves are pinned: an extra message is
// as much a divergence as a missing one.
func TestUnavailableActionsMatchRubysNarrowExplanations(t *testing.T) {
	spoken := newModelHarness(t, harnessOptions{})
	spoken.model.SwitchView(ViewNext)
	spoken.selectRowByID(fixFlight)
	spoken.pressKeys(">")
	if !strings.Contains(spoken.model.FlashMessage(), "Outline") {
		t.Errorf("> outside Outline said %q", spoken.model.FlashMessage())
	}

	silent := newModelHarness(t, harnessOptions{})
	silent.model.SwitchView(ViewNext)
	silent.selectRowByID(fixFlight) // not a proposal, so `a` cannot approve
	silent.pressKeys("a")
	if got := silent.model.FlashMessage(); got != "" {
		t.Errorf("a on a non-proposal said %q; Ruby consumes it in silence", got)
	}
}

// -- archive, history, ordering and links ------------------------------------------

func TestArchiveSweepPreviewsThenSweepsOnConfirmation(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	before := harness.content()

	harness.pressKeys("x")
	if harness.model.Mode() != ModeModal ||
		harness.model.Modal().Kind() != ModalArchiveConfirm {
		t.Fatalf("x produced mode %s (%q)", harness.model.Mode(), harness.model.FlashMessage())
	}
	if joined := strings.Join(harness.model.Modal().AllLines(), " "); !strings.Contains(joined, "Would move 1 completed root") {
		t.Errorf("the preview does not report the counts: %q", joined)
	}
	if harness.content() != before {
		t.Fatal("the preview wrote")
	}

	harness.pressKeys("y")
	if harness.model.Mode() != ModeList {
		t.Fatalf("confirming left the mode at %s", harness.model.Mode())
	}
	if strings.Contains(harness.content(), `"id":"aaaa0008"`) {
		t.Error("the closed task is still live after the sweep")
	}
	archive, err := os.ReadFile(harness.model.paths.Archive)
	if err != nil || !strings.Contains(string(archive), `"id":"aaaa0008"`) {
		t.Errorf("the closed task did not reach the archive: %v %s", err, archive)
	}
	if !strings.Contains(harness.model.FlashMessage(), "archived 1 root") {
		t.Errorf("the sweep was not reported: %q", harness.model.FlashMessage())
	}
}

func TestArchiveSweepCancelWritesNothing(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	before := harness.content()
	harness.pressKeys("x", "n")
	if harness.content() != before {
		t.Error("cancelling still archived")
	}
	if !strings.Contains(harness.model.FlashMessage(), "archive cancelled") {
		t.Errorf("cancelling said %q", harness.model.FlashMessage())
	}
}

// The pin is the safety property: a list that changed while the modal was open
// must refuse rather than archive a set the user never saw.
func TestArchiveSweepRefusesAStalePreview(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.pressKeys("x")
	harness.rewrite(strings.Replace(fixtureStore,
		`{"type":"task","id":"aaaa000a","parent":"aaaa0009","state":"NEXT","title":"Water the plants","tags":["@home"]}`,
		`{"type":"task","id":"aaaa000a","parent":"aaaa0009","state":"CANCELLED","title":"Water the plants","tags":["@home"],"closed":"2026-07-01"}`, 1))
	harness.pressKeys("y")
	if !strings.Contains(harness.model.FlashMessage(), "press x to review") {
		t.Errorf("a stale preview produced %q", harness.model.FlashMessage())
	}
	if strings.Contains(harness.content(), "archive.jsonl") {
		t.Error("a refused sweep wrote")
	}
}

func TestArchiveSweepReportsAnEmptyPreviewWithoutOpeningAModal(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{
		live: strings.Replace(fixtureStore,
			`{"type":"task","id":"aaaa0008","parent":"aaaa0003","state":"DONE","priority":"C","title":"Old finished thing","tags":["@computer"],"closed":"2026-06-20"}`+"\n", "", 1),
	})
	harness.pressKeys("x")
	if harness.model.Mode() != ModeList {
		t.Fatalf("an empty preview opened %s", harness.model.Mode())
	}
	if !strings.Contains(harness.model.FlashMessage(), "nothing to archive") {
		t.Errorf("an empty preview said %q", harness.model.FlashMessage())
	}
}

// A closed root with open work inside is BLOCKED, not confirmable — there is
// nothing to say yes to, so it opens a different modal that names the work.
func TestArchiveSweepBlocksOnOpenDescendants(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: blockedArchiveFixture})
	harness.pressKeys("x")
	if harness.model.Modal() == nil || harness.model.Modal().Kind() != ModalArchiveBlocked {
		t.Fatalf("x on a blocked list produced %v", harness.model.Modal())
	}
	joined := strings.Join(harness.model.Modal().AllLines(), " ")
	if !strings.Contains(joined, "Cannot archive") || !strings.Contains(joined, "Still open") {
		t.Errorf("the blocked modal does not name the blocking work: %q", joined)
	}
	// The blocked modal answers no confirmation key: `y` must not sweep.
	before := harness.content()
	harness.pressKeys("y")
	if harness.content() != before {
		t.Error("y on the blocked modal archived anyway")
	}
}

func TestUndoAndRedoWalkTheJournalAndSayTheLabel(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("J") // lower priority: one journal step
	after := harness.content()

	harness.pressKeys("u")
	if !strings.Contains(harness.model.FlashMessage(), "undid: priority") {
		t.Errorf("undo said %q", harness.model.FlashMessage())
	}
	if harness.content() == after {
		t.Error("undo did not change the file")
	}
	if !strings.Contains(taskLineIn(harness.content(), fixFlight), `"priority":"A"`) {
		t.Error("undo did not restore the priority")
	}

	harness.pressKeys("\x12") // ctrl-r
	if !strings.Contains(harness.model.FlashMessage(), "redid: priority") {
		t.Errorf("redo said %q", harness.model.FlashMessage())
	}
	if harness.content() != after {
		t.Error("redo did not reapply the change")
	}
}

func TestUndoWithNothingToUndoSaysSo(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.pressKeys("u")
	if harness.model.FlashMessage() != "nothing to undo" {
		t.Errorf("undo on a fresh store said %q", harness.model.FlashMessage())
	}
}

func TestUndoRefusesWhenTheFileChangedExternally(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("J")
	harness.rewrite(strings.Replace(fixtureStore, "Water the plants", "Renamed elsewhere", 1))
	harness.pressKeys("u")
	if !strings.Contains(harness.model.FlashMessage(), "changed externally") {
		t.Errorf("undo over an external write said %q", harness.model.FlashMessage())
	}
}

func TestOrderingMovesASubtreeAndReportsIt(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixPR)
	harness.pressKeys("\x1b[1;3A") // alt-up
	if !strings.Contains(harness.model.FlashMessage(), "move up") {
		t.Fatalf("alt-up said %q", harness.model.FlashMessage())
	}
	lines := strings.Split(harness.content(), "\n")
	flightAt, prAt := -1, -1
	for index, line := range lines {
		if strings.Contains(line, `"id":"`+fixFlight+`"`) {
			flightAt = index
		}
		if strings.Contains(line, `"id":"`+fixPR+`"`) {
			prAt = index
		}
	}
	if prAt > flightAt {
		t.Errorf("the task did not move above its sibling (%d vs %d)", prAt, flightAt)
	}
	if harness.model.SelectedID() != fixPR {
		t.Errorf("the cursor did not follow the moved task: %q", harness.model.SelectedID())
	}
}

func TestOrderingReportsItsBoundariesWithoutWriting(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.selectRowByID(fixFlight) // first among its siblings
	before := harness.content()
	harness.pressKeys("\x1b[1;3A")
	if !strings.Contains(harness.model.FlashMessage(), "already first among siblings") {
		t.Errorf("moving the first sibling up said %q", harness.model.FlashMessage())
	}
	if harness.content() != before {
		t.Error("a boundary notice still wrote")
	}
	harness.pressKeys(">")
	if !strings.Contains(harness.model.FlashMessage(), "without a preceding sibling") {
		t.Errorf("indenting the first sibling said %q", harness.model.FlashMessage())
	}
	if harness.content() != before {
		t.Error("a refused indent still wrote")
	}
}

func TestOrderingIndentsUnderThePrecedingSiblingAndUnfoldsIt(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	harness.model.collapsed[fixFlight] = true
	harness.selectRowByID(fixPR)
	harness.pressKeys(">")
	if !strings.Contains(harness.model.FlashMessage(), "indent") {
		t.Fatalf("indent said %q", harness.model.FlashMessage())
	}
	if !strings.Contains(taskLineIn(harness.content(), fixPR), `"parent":"`+fixFlight+`"`) {
		t.Errorf("the task did not land under its preceding sibling:\n%s",
			taskLineIn(harness.content(), fixPR))
	}
	if harness.model.collapsed[fixFlight] {
		t.Error("the new parent stayed folded, hiding the task the user just moved")
	}
}

func TestOrderingIsRefusedOutsideTheUnfilteredOutline(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	before := harness.content()
	harness.pressKeys("\x1b[1;3B")
	if !strings.Contains(harness.model.FlashMessage(), "Outline") {
		t.Errorf("ordering outside Outline said %q", harness.model.FlashMessage())
	}
	if harness.content() != before {
		t.Error("a refused move wrote")
	}
}

// fakeOpener records what it was asked to launch. No test may open a browser.
type fakeOpener struct {
	opened []string
	fail   bool
}

func (f *fakeOpener) Open(url string) bool {
	f.opened = append(f.opened, url)
	return !f.fail
}

func TestOpenLinkLaunchesTheFirstLinkThroughTheInjectedOpener(t *testing.T) {
	opener := &fakeOpener{}
	harness := newModelHarness(t, harnessOptions{live: linkedFixture, opener: opener})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("o")
	if len(opener.opened) != 1 || !strings.Contains(opener.opened[0], "example.com") {
		t.Fatalf("the opener received %v", opener.opened)
	}
	if !strings.Contains(harness.model.FlashMessage(), "opened") {
		t.Errorf("opening said %q", harness.model.FlashMessage())
	}
}

func TestOpenLinkSaysWhenThereIsNothingToOpenOrNoLauncher(t *testing.T) {
	bare := newModelHarness(t, harnessOptions{opener: &fakeOpener{}})
	bare.model.SwitchView(ViewNext)
	bare.selectRowByID(fixFlight)
	bare.pressKeys("o")
	if bare.model.FlashMessage() != "no links on this task" {
		t.Errorf("a linkless task said %q", bare.model.FlashMessage())
	}

	broken := newModelHarness(t, harnessOptions{live: linkedFixture, opener: &fakeOpener{fail: true}})
	broken.model.SwitchView(ViewNext)
	broken.selectRowByID(fixFlight)
	broken.pressKeys("o")
	if !strings.Contains(broken.model.FlashMessage(), "no browser launcher") {
		t.Errorf("a failing launcher said %q", broken.model.FlashMessage())
	}
}

func taskLineIn(content, id string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, `"id":"`+id+`"`) {
			return line
		}
	}
	return ""
}

// -- delegation, work reference and availability ------------------------------------

func TestDelegateToAPersonComposesTheWaitingDefault(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("D")
	if harness.model.Mode() != ModeForm || harness.model.Form().Kind != QuickFormDelegate {
		t.Fatalf("D produced mode %s (%q)", harness.model.Mode(), harness.model.FlashMessage())
	}
	for _, key := range strings.Split("pat@example.com", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	line := taskLineIn(harness.content(), fixFlight)
	if !strings.Contains(line, `"assignee":"pat@example.com"`) {
		t.Errorf("the delegation did not land:\n%s", line)
	}
	if !strings.Contains(line, `"state":"WAITING"`) {
		t.Errorf("the WAITING default was not composed:\n%s", line)
	}
	if !strings.Contains(harness.model.FlashMessage(), "delegated → pat@example.com") {
		t.Errorf("the write was not reported: %q", harness.model.FlashMessage())
	}
}

// One stray character must not perform the widest or most destructive action in
// the grammar: `re` matches both release and refine.
func TestDelegateRefusesAnAmbiguousOrTooShortPrefix(t *testing.T) {
	for _, input := range []string{"re", "r", "i", "o"} {
		harness := newModelHarness(t, harnessOptions{})
		harness.model.SwitchView(ViewNext)
		harness.selectRowByID(fixFlight)
		before := harness.content()
		harness.pressKeys("D")
		for _, key := range strings.Split(input, "") {
			harness.pressKeys(key)
		}
		harness.pressKeys("\r")
		if harness.model.Mode() != ModeForm {
			t.Fatalf("%q closed the form", input)
		}
		if harness.model.Form().Error() == "" {
			t.Errorf("%q was accepted silently", input)
		}
		if harness.content() != before {
			t.Errorf("%q wrote", input)
		}
	}
}

func TestDelegateNamesANearMissAddressAsATypo(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("D")
	for _, key := range strings.Split("pat@", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if !strings.Contains(harness.model.Form().Error(), "isn't an email address") {
		t.Errorf("a near-miss address said %q", harness.model.Form().Error())
	}
}

func TestWorkRefRefusesBeforeADelegationExists(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("W")
	if harness.model.Mode() == ModeForm {
		t.Fatal("W opened a prompt whose every input must fail")
	}
	if !strings.Contains(harness.model.FlashMessage(), "delegate the task first") {
		t.Errorf("W on an undelegated task said %q", harness.model.FlashMessage())
	}
}

func TestWorkRefRecordsThenClearsTheReference(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("D")
	for _, key := range strings.Split("refine", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")

	harness.pressKeys("W")
	for _, key := range strings.Split("https://x.test/1", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if !strings.Contains(taskLineIn(harness.content(), fixFlight), `"work_ref":"https://x.test/1"`) {
		t.Errorf("the reference did not land:\n%s", taskLineIn(harness.content(), fixFlight))
	}

	harness.pressKeys("W", "\x15")
	for _, key := range strings.Split("off", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if strings.Contains(taskLineIn(harness.content(), fixFlight), "work_ref") {
		t.Errorf("off did not clear the reference:\n%s", taskLineIn(harness.content(), fixFlight))
	}
	if !strings.Contains(harness.model.FlashMessage(), "work ref cleared") {
		t.Errorf("clearing said %q", harness.model.FlashMessage())
	}
}

func TestDeferUntilADateSetsTheGateAndClearsTheOwnHold(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPR)
	harness.pressKeys("z")
	if harness.model.Mode() != ModeForm || harness.model.Form().Kind != QuickFormDeferUntil {
		t.Fatalf("z produced mode %s (%q)", harness.model.Mode(), harness.model.FlashMessage())
	}
	for _, key := range strings.Split("2026-09-01", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	line := taskLineIn(harness.content(), fixPR)
	if !strings.Contains(line, `"scheduled":"2026-09-01"`) {
		t.Errorf("the available-from date did not land:\n%s", line)
	}
	if strings.Contains(line, `"defer"`) {
		t.Errorf("the task's own hold was not cleared:\n%s", line)
	}
	if !strings.Contains(harness.model.FlashMessage(), "unavailable until") {
		t.Errorf("the new availability was not reported: %q", harness.model.FlashMessage())
	}
}

func TestDeferSomedayAndNowWalkTheIndefiniteHold(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPR)
	harness.pressKeys("z")
	for _, key := range strings.Split("someday", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if !strings.Contains(taskLineIn(harness.content(), fixPR), `"defer"`) {
		t.Fatalf("someday did not add the marker:\n%s", taskLineIn(harness.content(), fixPR))
	}
	if !strings.Contains(harness.model.FlashMessage(), "on hold") {
		t.Errorf("someday said %q", harness.model.FlashMessage())
	}

	// The task left the Next view, so it has to be reached deliberately.
	harness.model.showDeferred = true
	harness.model.RefreshRows()
	harness.selectRowByID(fixPR)
	harness.pressKeys("z")
	for _, key := range strings.Split("now", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if strings.Contains(taskLineIn(harness.content(), fixPR), `"defer"`) {
		t.Errorf("now did not release the hold:\n%s", taskLineIn(harness.content(), fixPR))
	}
	if !strings.Contains(harness.model.FlashMessage(), "available now") {
		t.Errorf("now said %q", harness.model.FlashMessage())
	}
}

// A one-off date would fight a lead window, which already owns "hide until".
func TestDeferRefusesADateBesideALeadWindow(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: strings.Replace(fixtureStore,
		`"deadline":"2026-07-02"}`, `"deadline":"2026-07-02","lead":"1w"}`, 1)})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	before := harness.content()
	harness.pressKeys("z")
	for _, key := range strings.Split("2026-09-01", "") {
		harness.pressKeys(key)
	}
	harness.pressKeys("\r")
	if !strings.Contains(harness.model.Form().Error(), "already hides until") {
		t.Errorf("a date beside a lead said %q", harness.model.Form().Error())
	}
	if harness.content() != before {
		t.Error("a refused defer wrote")
	}
}
