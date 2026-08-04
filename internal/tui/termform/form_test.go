package termform

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/temporal"
)

// buildForm is the fixture every lifecycle test drives: two groups, a required
// title, a note, and a select.
func buildForm(t *testing.T, focus string) (*Form, *Input, *TextArea, *Select) {
	t.Helper()
	titleBase := NewBase("title", "Title", "Ship it")
	titleBase.RequiredFixed = true
	title := NewInput(titleBase)
	note := NewTextArea(NewBase("note", "Note", "first\nsecond"))
	state := NewSelect(NewBase("state", "State", "TODO"), func(Context) []Option {
		return []Option{NewOption("TODO", ""), NewOption("NEXT", ""), NewOption("DONE", "")}
	}, false)
	form, err := NewForm([]Group{
		NewGroup("basics", "Basics", title, note),
		NewGroup("lifecycle", "Lifecycle", state),
	}, focus, nil)
	if err != nil {
		t.Fatal(err)
	}
	return form, title, note, state
}

func TestFormStartsOnTheRequestedFieldAndFallsBackToTheFirst(t *testing.T) {
	form, _, _, _ := buildForm(t, "state")
	if form.FocusKey() != "state" {
		t.Errorf("focus %q, want state", form.FocusKey())
	}
	form, _, _, _ = buildForm(t, "")
	if form.FocusKey() != "title" {
		t.Errorf("default focus %q, want the first focusable field", form.FocusKey())
	}
}

func TestFormLeavingACleanFieldJustMovesFocus(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	transition := form.HandleKey("\t")
	if !transition.IsFocusChanged() {
		t.Fatalf("tab on a clean field produced %q, want focus_changed", transition.Type)
	}
	if form.FocusKey() != "note" {
		t.Errorf("focus %q, want note", form.FocusKey())
	}
	if form.Pending() {
		t.Error("a clean blur opened a commit the host never has to answer")
	}
}

// THE save-on-blur contract: leaving a dirty field asks the host to persist it
// and HOLDS focus until the host answers. This is the behavior the whole editor
// is built on.
func TestFormLeavingADirtyFieldRequestsACommitAndHoldsFocus(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	form.HandleKey("!")
	transition := form.HandleKey("\t")
	if transition.Type != CommitRequested {
		t.Fatalf("tab on a dirty field produced %q, want commit_requested", transition.Type)
	}
	request := transition.Request
	if request == nil {
		t.Fatal("no commit request rode along")
	}
	if request.FieldKey != "title" || request.IntendedFocus != "note" {
		t.Errorf("request is for %q intending %q, want title -> note",
			request.FieldKey, request.IntendedFocus)
	}
	if request.ProposedValue != "Ship it!" {
		t.Errorf("proposed value %v", request.ProposedValue)
	}
	if request.ExpectedBaseline != "Ship it" {
		t.Errorf("expected baseline %v — the host cannot detect a conflict without it",
			request.ExpectedBaseline)
	}
	if form.FocusKey() != "title" {
		t.Errorf("focus moved to %q before the host answered", form.FocusKey())
	}
}

func TestFormAcceptingACommitAdvancesFocusAndClearsDirty(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	form.HandleKey("!")
	request := form.HandleKey("\t").Request
	if _, err := form.AcceptCommit(map[string]any{"title": "Ship it!"}, request.Token); err != nil {
		t.Fatal(err)
	}
	if form.Dirty("title") {
		t.Error("an accepted field is still dirty")
	}
	if form.FocusKey() != "note" {
		t.Errorf("focus %q after acceptance, want the intended note", form.FocusKey())
	}
	if form.Pending() {
		t.Error("the commit is still pending after acceptance")
	}
}

func TestFormRejectingACommitKeepsTheBufferAndTheFocus(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	form.HandleKey("!")
	request := form.HandleKey("\t").Request
	transition, err := form.RejectCommit(map[string][]string{"title": {"nope"}}, "", request.Token)
	if err != nil {
		t.Fatal(err)
	}
	if transition.Type != CommitRejected {
		t.Fatalf("rejection produced %q", transition.Type)
	}
	if got := form.Value("title"); got != "Ship it!" {
		t.Errorf("a rejected save cost the user their text: %v", got)
	}
	if form.FocusKey() != "title" {
		t.Errorf("focus left the field the user has to fix: %q", form.FocusKey())
	}
	if got := form.Errors()["title"]; len(got) != 1 || got[0] != "nope" {
		t.Errorf("the host's reason did not reach the field: %v", got)
	}
}

func TestFormValidationBlocksACommitAndFocusesTheOffendingField(t *testing.T) {
	form, _, _, _ := buildForm(t, "state")
	form.SetValue("title", "", Event{})
	transition := form.RequestCommit("", "", "state", Event{})
	if !transition.IsInvalid() {
		t.Fatalf("an empty required field produced %q, want invalid", transition.Type)
	}
	if form.FocusKey() != "title" {
		t.Errorf("focus %q, want the field that failed", form.FocusKey())
	}
	if form.Pending() {
		t.Error("a host was asked to write a value the form knows is wrong")
	}
}

func TestFormRefreshKeepsADirtyBufferAndMovesEveryBaseline(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	form.HandleKey("!")
	if _, err := form.Refresh(map[string]any{
		"title": "Renamed elsewhere", "state": "NEXT",
	}); err != nil {
		t.Fatal(err)
	}
	if got := form.Value("title"); got != "Ship it!" {
		t.Errorf("an external refresh overwrote what the user was typing: %v", got)
	}
	if got := form.Baseline("title"); got != "Renamed elsewhere" {
		t.Errorf("the baseline did not move: %v", got)
	}
	if got := form.Value("state"); got != "NEXT" {
		t.Errorf("a clean field did not adopt the external value: %v", got)
	}
	if !form.Dirty("title") {
		t.Error("the dirty field stopped being dirty against its new baseline")
	}
}

func TestFormAPendingCommitOwnsFocusUntilTheHostAnswers(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	form.HandleKey("!")
	form.HandleKey("\t")
	transition := form.HandleKey("\t")
	if transition.Type != CommitPending {
		t.Fatalf("a second blur while pending produced %q, want commit_pending", transition.Type)
	}
	if form.FocusKey() != "title" {
		t.Errorf("focus escaped the pending field: %q", form.FocusKey())
	}
}

func TestFormRenderModelKeepsThePendingFieldVisible(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	form.HandleKey("!")
	form.HandleKey("\t")
	model := form.RenderModel()
	found := false
	for _, row := range model.Rows {
		if row.Key == "title" && row.Pending {
			found = true
		}
	}
	if !found {
		t.Error("the field awaiting an answer is not marked pending; the user cannot resolve it")
	}
}

func TestFormChangedKeysAreStableAcrossRuns(t *testing.T) {
	// A Go map iterates randomly. Two runs over identical data must still
	// produce identical commit requests, or the differential harness would see
	// a difference that is not one.
	for range 20 {
		form, _, _, _ := buildForm(t, "title")
		form.SetValue("title", "a", Event{})
		form.SetValue("note", "b", Event{})
		if got := strings.Join(form.ChangedKeys(), ","); got != "note,title" {
			t.Fatalf("changed keys %q are not in a stable order", got)
		}
	}
}

func TestFormCancelIsReportedRatherThanActedOn(t *testing.T) {
	form, _, _, _ := buildForm(t, "title")
	if got := form.HandleKey("\x1b").Type; got != CancelRequested {
		t.Errorf("escape produced %q, want cancel_requested — the host decides what it means", got)
	}
}

func TestFormDuplicateKeysAreRefusedAtConstruction(t *testing.T) {
	first := NewInput(NewBase("dup", "One", ""))
	second := NewInput(NewBase("dup", "Two", ""))
	if _, err := NewForm([]Group{NewGroup("g", "G", first, second)}, "", nil); err == nil {
		t.Error("two fields sharing a key were accepted; one would shadow the other")
	}
}

func TestTemporalValuesNeverAliasThroughFormReadOrCommitSurfaces(t *testing.T) {
	value := &temporal.Value{Date: temporal.Date{Year: 2026, Month: 7, Day: 14}}
	baseline := &temporal.Value{Date: temporal.Date{Year: 2026, Month: 7, Day: 13}}
	base := NewBase("when", "When", value)
	base.Baseline, base.BaselineSet = baseline, true
	form, err := NewForm([]Group{NewGroup("g", "G", &base)}, "when", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Construction clones the host-owned pointers.
	value.Date.Day, baseline.Date.Day = 1, 2
	if form.Value("when").(*temporal.Value).Date.Day != 14 ||
		form.Baseline("when").(*temporal.Value).Date.Day != 13 {
		t.Fatal("constructor retained host temporal pointers")
	}
	// Every read accessor returns another clone.
	form.Value("when").(*temporal.Value).Date.Day = 3
	form.Baseline("when").(*temporal.Value).Date.Day = 4
	form.Values()["when"].(*temporal.Value).Date.Day = 5
	if form.Value("when").(*temporal.Value).Date.Day != 14 ||
		form.Baseline("when").(*temporal.Value).Date.Day != 13 {
		t.Fatal("Value/Baseline/Values exposed form-owned pointers")
	}
	proposed := &temporal.Value{Date: temporal.Date{Year: 2026, Month: 7, Day: 15}}
	form.SetValue("when", proposed, Event{})
	request := form.RequestCommit("", "", "when", Event{}).Request
	if request == nil {
		t.Fatal("dirty temporal field produced no commit request")
	}
	request.ProposedValue.(*temporal.Value).Date.Day = 6
	request.ExpectedBaseline.(*temporal.Value).Date.Day = 7
	request.Values["when"].(*temporal.Value).Date.Day = 8
	if form.Value("when").(*temporal.Value).Date.Day != 15 ||
		form.Baseline("when").(*temporal.Value).Date.Day != 13 {
		t.Fatal("commit payload exposed form-owned temporal pointers")
	}
}
