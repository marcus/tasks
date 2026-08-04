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

func TestArchiveSweepNamesTheSeamItNeeds(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.selectRowByID(fixFlight)
	harness.pressKeys("x")
	if !strings.Contains(harness.model.FlashMessage(), "archive seam") {
		t.Errorf("x said %q, which does not name the missing capability",
			harness.model.FlashMessage())
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
