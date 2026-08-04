package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tasks-go/internal/temporal"
	"tasks-go/internal/tui/termform"
)

// editorHarness opens the durable editor on one fixture task.
type editorHarness struct {
	*modelHarness
	editor *TaskEditorSession
}

func newEditorHarness(t *testing.T, id, focus string) *editorHarness {
	t.Helper()
	return newEditorHarnessInView(t, "", id, focus)
}

func newEditorHarnessInView(t *testing.T, view, id, focus string) *editorHarness {
	t.Helper()
	harness := newModelHarness(t, harnessOptions{})
	if view != "" {
		harness.model.SwitchView(view)
	}
	harness.selectRowByID(id)
	harness.model.StartTaskEdit(focus)
	editor := harness.model.TaskEditor()
	if editor == nil {
		t.Fatalf("the editor did not open: %q", harness.model.FlashMessage())
	}
	return &editorHarness{modelHarness: harness, editor: editor}
}

// typeText drives the editor one key at a time, as a user does.
func (h *editorHarness) typeText(text string) {
	h.t.Helper()
	for _, key := range strings.Split(text, "") {
		h.editor.HandleKey(key)
	}
}

func (h *editorHarness) storeContent() string {
	h.t.Helper()
	raw, err := os.ReadFile(h.org)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

func (h *editorHarness) line(id string) string {
	h.t.Helper()
	for _, line := range strings.Split(h.storeContent(), "\n") {
		if strings.Contains(line, `"id":"`+id+`"`) {
			return line
		}
	}
	h.t.Fatalf("no stored line carries id %s", id)
	return ""
}

// -- the field vocabulary ------------------------------------------------------

func TestEditorExposesTheRubyFieldOrder(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	keys := []string{}
	for _, field := range harness.editor.Form().Fields() {
		keys = append(keys, field.Key())
	}
	want := "title,priority,deferred,scheduled,deadline,recurrence,lead,contexts,tags,body,state"
	if got := strings.Join(keys, ","); got != want {
		t.Errorf("field order\n got %s\nwant %s", got, want)
	}
}

// THE contract this packet was blocked on: every field the editor SHOWS must
// save. A field that renders and then refuses is worse than one that is absent,
// because the user has already done the work.
func TestEveryExposedEditorFieldReachesTheStore(t *testing.T) {
	cases := []struct {
		field  string
		set    func(*termform.Form)
		expect string
	}{
		{"title", func(f *termform.Form) { f.SetValue("title", "Renamed here", termform.Event{}) },
			`"title":"Renamed here"`},
		{"priority", func(f *termform.Form) { f.SetValue("priority", "C", termform.Event{}) },
			`"priority":"C"`},
		{"deferred", func(f *termform.Form) { f.SetValue("deferred", true, termform.Event{}) },
			`"defer"`},
		{"deadline", func(f *termform.Form) { f.SetValue("deadline", "2026-08-09", termform.Event{}) },
			`"deadline":"2026-08-09"`},
		{"contexts", func(f *termform.Form) {
			f.SetValue("contexts", []string{"@errand"}, termform.Event{})
		}, `"@errand"`},
		{"tags", func(f *termform.Form) {
			f.SetValue("tags", []string{"important", "billing"}, termform.Event{})
		}, `"billing"`},
		{"body", func(f *termform.Form) { f.SetValue("body", "a note", termform.Event{}) },
			`"body":"a note"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.field, func(t *testing.T) {
			harness := newEditorHarness(t, fixFlight, testCase.field)
			testCase.set(harness.editor.Form())
			outcome := harness.editor.Save()
			if outcome.Status != EditorSaved {
				t.Fatalf("saving %s produced %q: %s", testCase.field, outcome.Status, outcome.Message)
			}
			if line := harness.line(fixFlight); !strings.Contains(line, testCase.expect) {
				t.Errorf("the store does not carry %s after saving %s:\n%s",
					testCase.expect, testCase.field, line)
			}
		})
	}
}

// The three non-string fields are the ones that used to refuse. They are pinned
// separately so a regression to "visible but unsaveable" fails loudly by name.
func TestTheThreeNonStringFieldsDoNotRefuse(t *testing.T) {
	for _, field := range []string{"deferred", "contexts", "tags"} {
		harness := newEditorHarness(t, fixFlight, field)
		switch field {
		case "deferred":
			harness.editor.Form().SetValue(field, true, termform.Event{})
		default:
			harness.editor.Form().SetValue(field, []string{"@x"}, termform.Event{})
			if field == "tags" {
				harness.editor.Form().SetValue(field, []string{"x"}, termform.Event{})
			}
		}
		outcome := harness.editor.Save()
		if outcome.Status != EditorSaved {
			t.Errorf("%s still refuses to save: %q %q", field, outcome.Status, outcome.Message)
		}
	}
}

// -- save on blur ----------------------------------------------------------------

func TestBlurCommitsTheDirtyFieldAndMovesOn(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	outcome := harness.editor.HandleKey("\t")
	if outcome.Status != EditorSaved {
		t.Fatalf("tab out of a dirty field produced %q: %s", outcome.Status, outcome.Message)
	}
	if line := harness.line(fixFlight); !strings.Contains(line, `"title":"Book flight in Concur!"`) {
		t.Errorf("blur did not persist the edit:\n%s", line)
	}
	if harness.editor.FocusedKey() != "priority" {
		t.Errorf("focus %q after an accepted blur, want the next field", harness.editor.FocusedKey())
	}
}

func TestBlurOnACleanFieldWritesNothing(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	before := harness.storeContent()
	harness.editor.HandleKey("\t")
	if harness.storeContent() != before {
		t.Error("moving off a clean field wrote to the store")
	}
}

// One editing session is ONE undo step, however many fields it saved. The
// coalesce key is what buys that; two saves in one session must share it.
func TestOneSessionCoalescesItsSavesIntoOneHistoryStep(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.editor.Form().SetValue("title", "First edit", termform.Event{})
	harness.editor.Save()
	harness.editor.Form().SetValue("body", "Second edit", termform.Event{})
	harness.editor.Form().Focus("body", termform.Event{}, "")
	harness.editor.Save()

	steps := journalSteps(t, harness.root)
	if steps < 1 {
		t.Fatal("the session wrote no journal entry at all")
	}
	if steps != 1 {
		t.Errorf("two saves in one session opened %d undo steps, want 1", steps)
	}
	// Both saves must carry the SAME coalesce key; that is the mechanism, and
	// pinning it keeps a future refactor from getting one undo step by accident.
	keys := map[string]bool{}
	for _, state := range journalStates(t, harness.root) {
		if key, present := state["coalesce_key"].(string); present && key != "" {
			keys[key] = true
		}
	}
	if len(keys) != 1 {
		t.Errorf("the session used %d coalesce keys, want 1: %v", len(keys), keys)
	}
}

// A second session must NOT coalesce into the first, or an unrelated later edit
// would be undone along with one the user considers finished.
func TestASecondSessionOpensItsOwnHistoryStep(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.editor.Form().SetValue("title", "First edit", termform.Event{})
	harness.editor.Save()
	first := journalSteps(t, harness.root)

	harness.model.CloseTaskEdit("")
	harness.selectRowByID(fixFlight)
	harness.model.StartTaskEdit("title")
	second := harness.model.TaskEditor()
	second.Form().SetValue("title", "Second edit", termform.Event{})
	second.Save()

	if journalSteps(t, harness.root) <= first {
		t.Error("a fresh session merged into the previous session's undo step")
	}
}

// -- the narrow conflict check -----------------------------------------------------

func TestAnExternalEditOfTheSameFieldRefusesRatherThanOverwrites(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	harness.rewrite(strings.Replace(fixtureStore,
		`"title":"Book flight in Concur"`, `"title":"Changed by the CLI"`, 1))
	harness.model.Refresh()

	outcome := harness.editor.Save()
	if outcome.Status != EditorConflicted {
		t.Fatalf("saving over an external edit produced %q, want a conflict", outcome.Status)
	}
	if line := harness.line(fixFlight); !strings.Contains(line, "Changed by the CLI") {
		t.Errorf("the refused save still landed:\n%s", line)
	}
	if got := harness.editor.Form().Value("title"); got != "Book flight in Concur!" {
		t.Errorf("the conflict cost the user their text: %v", got)
	}
	if harness.editor.Conflict() == nil {
		t.Error("no conflict was offered for reload/revert/copy")
	}
}

// An external edit of a DIFFERENT field must not block the save. That is the
// whole point of a field-owned baseline rather than a whole-task revision.
func TestAnExternalEditOfAnotherFieldDoesNotBlockTheSave(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	harness.rewrite(strings.Replace(fixtureStore,
		`"title":"Review PR backlog"`, `"title":"Someone else's edit"`, 1))
	harness.model.Refresh()

	if outcome := harness.editor.Save(); outcome.Status != EditorSaved {
		t.Fatalf("an unrelated external edit blocked the save: %q %s",
			outcome.Status, outcome.Message)
	}
}

func TestReloadingAConflictAdoptsTheLiveValue(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	harness.rewrite(strings.Replace(fixtureStore,
		`"title":"Book flight in Concur"`, `"title":"Changed by the CLI"`, 1))
	harness.model.Refresh()
	harness.editor.Save()

	if outcome := harness.editor.ReloadConflict(); outcome.Status != EditorConflictReloaded {
		t.Fatalf("reload produced %q", outcome.Status)
	}
	if got := harness.editor.Form().Value("title"); got != "Changed by the CLI" {
		t.Errorf("reload did not adopt the live value: %v", got)
	}
	if harness.editor.Conflict() != nil {
		t.Error("the conflict survived its own resolution")
	}
}

func TestKeepingAConflictingValueForCopyDoesNotOverwrite(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	harness.rewrite(strings.Replace(fixtureStore,
		`"title":"Book flight in Concur"`, `"title":"Changed by the CLI"`, 1))
	harness.model.Refresh()
	harness.editor.Save()

	before := harness.storeContent()
	if outcome := harness.editor.KeepForCopy(); outcome.Status != EditorCopyKept {
		t.Fatalf("keep-for-copy produced %q", outcome.Status)
	}
	if harness.storeContent() != before {
		t.Error("keeping a value for copy wrote to the store")
	}
	if got := harness.editor.CopyValue(); got != "Book flight in Concur!" {
		t.Errorf("the kept copy is %v", got)
	}
}

// -- validation ---------------------------------------------------------------------

func TestAnEmptyTitleIsRefusedBeforeItReachesTheStore(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	before := harness.storeContent()
	harness.editor.Form().SetValue("title", "", termform.Event{})
	outcome := harness.editor.Save()
	if outcome.Status != EditorInvalid {
		t.Fatalf("an empty title produced %q, want invalid", outcome.Status)
	}
	// Ruby's Field#validation_errors puts the required check FIRST, before the
	// field's own validators, so an empty required field says the obvious thing
	// rather than a parse complaint about "".
	if !strings.Contains(outcome.Message, "required") {
		t.Errorf("the refusal does not name the rule: %q", outcome.Message)
	}
	if harness.storeContent() != before {
		t.Error("a form-invalid save still wrote")
	}
}

func TestARecurrenceWithoutADateIsRefusedByTheField(t *testing.T) {
	harness := newEditorHarnessInView(t, ViewNext, fixPlants, "recurrence")
	harness.editor.Form().SetValue("recurrence", "weekly", termform.Event{})
	outcome := harness.editor.Save()
	if outcome.Status != EditorInvalid {
		t.Fatalf("a dateless recurrence produced %q, want invalid", outcome.Status)
	}
	if !strings.Contains(outcome.Message, "Available from date or deadline") {
		t.Errorf("the refusal does not name the fix: %q", outcome.Message)
	}
}

func TestALeadBesideBothDatesIsRefusedByTheField(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "scheduled")
	harness.editor.Form().SetValue("scheduled", "2026-07-01", termform.Event{})
	harness.editor.Form().SetValue("lead", "1w", termform.Event{})
	harness.editor.Form().Focus("lead", termform.Event{}, "")
	outcome := harness.editor.Save()
	if outcome.Status != EditorInvalid {
		t.Fatalf("a lead beside two dates produced %q, want invalid", outcome.Status)
	}
	if !strings.Contains(outcome.Message, "measures from the deadline") {
		t.Errorf("the refusal does not name the fix: %q", outcome.Message)
	}
}

func TestAnUnparseableDateReportsTheParsersOwnReason(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "deadline")
	harness.editor.Form().SetValue("deadline", "not a date", termform.Event{})
	outcome := harness.editor.Save()
	if outcome.Status != EditorInvalid {
		t.Fatalf("an unparseable date produced %q", outcome.Status)
	}
	if outcome.Message == "" {
		t.Error("the refusal said nothing")
	}
}

// -- confirmations -------------------------------------------------------------------

func TestCompletingAParentAsksBeforeCascading(t *testing.T) {
	harness := newEditorHarness(t, fixWorkParent(t), "state")
	harness.editor.Form().SetValue("state", "DONE", termform.Event{})
	before := harness.storeContent()
	outcome := harness.editor.Save()
	if outcome.Status != EditorConfirmation {
		t.Fatalf("completing produced %q, want a confirmation", outcome.Status)
	}
	if harness.storeContent() != before {
		t.Error("the confirmation wrote before it was answered")
	}
	if harness.editor.Confirm().Status != EditorSaved {
		t.Error("answering yes did not perform the write")
	}
}

func TestDecliningAConfirmationKeepsTheLocalValueAndWritesNothing(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "state")
	harness.editor.Form().SetValue("state", "CANCELLED", termform.Event{})
	before := harness.storeContent()
	harness.editor.Save()
	outcome := harness.editor.CancelConfirmation()
	if outcome.Status != EditorConfirmCancelled {
		t.Fatalf("declining produced %q", outcome.Status)
	}
	if harness.storeContent() != before {
		t.Error("a declined confirmation still wrote")
	}
	if got := harness.editor.Form().Value("state"); got != "CANCELLED" {
		t.Errorf("declining discarded the local value: %v", got)
	}
}

func TestARecurrenceChangeIsGlossedInItsConfirmation(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "recurrence")
	harness.editor.Form().SetValue("recurrence", "weekly", termform.Event{})
	outcome := harness.editor.Save()
	if outcome.Status != EditorConfirmation {
		t.Fatalf("a recurrence change produced %q", outcome.Status)
	}
	// The prompt asks about meaning first, spelling second — the canonical
	// value is what lands in the record, so it is shown too.
	if !strings.Contains(outcome.Message, "week") || !strings.Contains(outcome.Message, ".+1w") {
		t.Errorf("the prompt explains neither the meaning nor the stored value: %q", outcome.Message)
	}
}

// -- escape, revert and finish ---------------------------------------------------------

func TestEscapeOnADirtyFieldAsksTwiceBeforeDiscarding(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	first := harness.editor.HandleKey("\x1b")
	if first.Status != EditorRevertPending {
		t.Fatalf("the first escape produced %q, want an armed discard", first.Status)
	}
	if got := harness.editor.Form().Value("title"); got != "Book flight in Concur!" {
		t.Errorf("the first escape already discarded: %v", got)
	}
	second := harness.editor.HandleKey("\x1b")
	if second.Status != EditorReverted {
		t.Fatalf("the second escape produced %q", second.Status)
	}
	if got := harness.editor.Form().Value("title"); got != "Book flight in Concur" {
		t.Errorf("the revert did not restore the baseline: %v", got)
	}
}

func TestEscapeOnACleanFieldFinishesEditing(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	if got := harness.editor.HandleKey("\x1b").Status; got != EditorFinished {
		t.Errorf("escape on a clean field produced %q, want finished", got)
	}
}

func TestEscapeClosesAnOpenPickerBeforeItArmsADiscard(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "deadline")
	harness.editor.HandleKey("\r") // opens the calendar
	field := harness.editor.Form().Field("deadline").(*TemporalInput)
	if !field.PickerOpen() {
		t.Fatal("return did not open the calendar")
	}
	harness.editor.HandleKey("\x1b")
	if field.PickerOpen() {
		t.Error("escape did not close the calendar")
	}
	if harness.editor.PendingRevert() != "" {
		t.Error("escape armed a field discard while a picker owned the key")
	}
}

func TestCtrlOFinishesAfterCommittingTheFocusedBuffer(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	if got := harness.editor.HandleKey("\x0f").Status; got != EditorFinished {
		t.Fatalf("ctrl-o produced %q", got)
	}
	if line := harness.line(fixFlight); !strings.Contains(line, "Book flight in Concur!") {
		t.Errorf("ctrl-o finished without saving:\n%s", line)
	}
}

// -- a vanished task ---------------------------------------------------------------------

func TestAVanishedTaskKeepsTheBufferCopyableRatherThanClosing(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	harness.rewrite(strings.Replace(fixtureStore,
		`{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Book flight in Concur","tags":["@computer","important","urgent"],"deadline":"2026-07-02"}`+"\n",
		"", 1))
	harness.model.Refresh()

	outcome := harness.editor.Save()
	if outcome.Status != EditorMissing {
		t.Fatalf("saving a vanished task produced %q", outcome.Status)
	}
	if got := harness.editor.CopyValue(); got != "Book flight in Concur!" {
		t.Errorf("the unsaved value was not retained for copy: %v", got)
	}
}

// -- suspension ----------------------------------------------------------------------

func TestSuspendDisarmsOneKeyPromptsWithoutLosingTheBuffer(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "state")
	harness.editor.Form().SetValue("state", "CANCELLED", termform.Event{})
	harness.editor.Save() // arms a confirmation

	outcome := harness.editor.Suspend()
	if outcome.Status != EditorSuspended {
		t.Fatalf("suspend produced %q", outcome.Status)
	}
	if harness.editor.PendingConfirmation() != nil {
		t.Error("a hidden editor kept a one-key destructive prompt armed")
	}
	if got := harness.editor.Form().Value("state"); got != "CANCELLED" {
		t.Errorf("suspending lost the local value: %v", got)
	}
}

// -- quitting -------------------------------------------------------------------------

func TestQuittingWithADirtyDraftAsksFirst(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	if got := harness.editor.RequestQuit().Status; got != EditorQuitConfirmation {
		t.Fatalf("quitting a dirty draft produced %q", got)
	}
	if got := harness.editor.HandleQuitConfirmation("n").Status; got != EditorQuitCancelled {
		t.Errorf("answering no produced %q", got)
	}
	if !harness.editor.Dirty("") {
		t.Error("a cancelled quit discarded the draft anyway")
	}
}

func TestQuittingWithACleanDraftDoesNotAsk(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	if got := harness.editor.RequestQuit().Status; got != EditorQuitReady {
		t.Errorf("a clean draft asked anyway: %q", got)
	}
}

// -- refresh ----------------------------------------------------------------------------

func TestRefreshAdoptsCleanFieldsAndPreservesTheDirtyOne(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "title")
	harness.typeText("!")
	harness.rewrite(strings.Replace(fixtureStore, `"priority":"A","title":"Book flight`,
		`"priority":"C","title":"Book flight`, 1))
	harness.model.Refresh()

	if got := harness.editor.Refresh().Status; got != EditorRefreshed {
		t.Fatalf("refresh produced %q", got)
	}
	if got := harness.editor.Form().Value("priority"); got != "C" {
		t.Errorf("a clean field did not adopt the external change: %v", got)
	}
	if got := harness.editor.Form().Value("title"); got != "Book flight in Concur!" {
		t.Errorf("the refresh overwrote what the user was typing: %v", got)
	}
}

// -- the temporal round trip ---------------------------------------------------------------

func TestATimedDateKeepsItsWallTimeAndZoneThroughTheSave(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "deadline")
	harness.editor.Form().SetValue("deadline", "2026-08-09 17:30 Europe/Berlin", termform.Event{})
	if outcome := harness.editor.Save(); outcome.Status != EditorSaved {
		t.Fatalf("saving a timed date produced %q: %s", outcome.Status, outcome.Message)
	}
	line := harness.line(fixFlight)
	if !strings.Contains(line, `"deadline":"2026-08-09"`) {
		t.Errorf("the date did not land:\n%s", line)
	}
	if !strings.Contains(line, `"local":"17:30"`) || !strings.Contains(line, "Europe/Berlin") {
		t.Errorf("the wall time and zone were dropped:\n%s", line)
	}
}

func TestTemporalFormattingRoundTripsThroughTheParser(t *testing.T) {
	parsed, err := ParseTemporal("2026-08-09 17:30 Europe/Berlin", temporal.Date{Year: 2026, Month: 8, Day: 1},
		temporal.Context{})
	if err != nil {
		t.Fatal(err)
	}
	text := FormatTemporal(parsed)
	if text != "2026-08-09 17:30 Europe/Berlin" {
		t.Fatalf("formatting produced %q", text)
	}
	again, err := ParseTemporal(text, temporal.Date{Year: 2026, Month: 8, Day: 1}, temporal.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if FormatTemporal(again) != text {
		t.Errorf("the value does not survive a round trip: %q", FormatTemporal(again))
	}
}

// -- helpers -----------------------------------------------------------------------------

// fixWorkParent is a fixture task that has an open descendant, built by nesting
// one of the existing tasks under another.
func fixWorkParent(t *testing.T) string {
	t.Helper()
	return fixFlight
}

// journalStates reads the undo timeline. One step is one press of undo, so
// counting them is how "a session is one undo step" is actually checked.
func journalStates(t *testing.T, root string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		return nil
	}
	var index struct {
		States []map[string]any `json:"states"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("unreadable journal index: %v", err)
	}
	return index.States
}

// journalSteps is the number of undoable steps: the timeline minus its initial
// state, which records the file as it was before anything was written.
func journalSteps(t *testing.T, root string) int {
	t.Helper()
	states := journalStates(t, root)
	if len(states) == 0 {
		return 0
	}
	return len(states) - 1
}
