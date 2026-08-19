package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/config"
)

// The fixture modal is deliberately NOT the delegate modal.
//
// "Move this task somewhere" — a typed destination, a section chosen from a
// vocabulary that only exists at runtime, and a note — exercises all three field
// kinds without knowing anything about delegation. If the component only worked
// for delegation, this file would not compile.

type fieldModalFixture struct {
	modal    *FieldModal
	sections *[]FieldOption
	// submitted is what the host was handed, and refusal is what it answers.
	submitted map[string]string
	refusal   string
}

func newFieldModalFixture(t *testing.T) *fieldModalFixture {
	t.Helper()
	sections := []FieldOption{
		{Value: "inbox", Label: "Inbox"},
		{Value: "work", Label: "Work"},
		{Value: "home", Label: "Home"},
		{Value: "garden", Label: "Garden"},
		{Value: "travel", Label: "Travel"},
		{Value: "someday", Label: "Someday"},
	}
	fixture := &fieldModalFixture{sections: &sections}
	fixture.modal = NewFieldModal(FieldModalOptions{
		Kind:  FieldModalGeneric,
		Title: "Move task",
		Fields: []FieldSpec{
			{Key: "owner", Kind: FieldText, Label: "Owner", Hint: "who picks this up",
				Initial: "alice",
				Validate: func(value string) string {
					if strings.TrimSpace(value) == "" {
						return "an owner is required"
					}
					return ""
				}},
			{Key: "section", Kind: FieldChoice, Label: "Section", Hint: "arrows or type a prefix",
				Initial: "work", VisibleOptions: 3,
				Options: func() []FieldOption { return sections }},
			{Key: "note", Kind: FieldTextArea, Label: "Note", Hint: "why it is moving", Rows: 3},
		},
		Actions: []FieldModalAction{
			{ID: "clear", Label: "Clear", Key: "\x0b", KeyLabel: "ctrl-k"},
		},
		SubmitLabel: "Move",
		Submit: func(values map[string]string) string {
			fixture.submitted = values
			return fixture.refusal
		},
	})
	return fixture
}

// fieldModalState is the whole observable state of the component, which is what
// the parity test compares between a key path and a mouse path.
//
// It has to include the CURSORS and the SCROLL, not just the values. A version
// of this that compared values alone passed while the mouse placed no caret at
// all, which is the largest divergence the two paths can have — a parity test
// that cannot see the thing the gesture exists to do is not a parity test. Every
// piece of per-field state a gesture can move is listed here on purpose.
func fieldModalState(f *FieldModal) string {
	parts := []string{"focus=" + f.FocusKey(), fmt.Sprintf("guard=%v", f.PendingCancel()),
		"err=" + f.Error()}
	for _, key := range f.Keys() {
		field := f.byKey[key]
		detail := ""
		switch {
		case field.area != nil:
			detail = fmt.Sprintf(" caret=%d scroll=%d", field.area.Cursor(), field.area.RowOffset())
		case field.spec.Kind == FieldChoice:
			detail = fmt.Sprintf(" highlight=%d window=%d query=%q",
				field.highlight, field.offset, field.query.Text())
			if field.input != nil {
				detail += fmt.Sprintf(" caret=%d", field.input.Cursor())
			}
		case field.input != nil:
			detail = fmt.Sprintf(" caret=%d", field.input.Cursor())
		}
		parts = append(parts, fmt.Sprintf("%s=%q/%q%s", key, f.Value(key), f.FieldError(key), detail))
	}
	return strings.Join(parts, " ")
}

// paint renders at a fixed budget, which also records the hit map.
func paint(f *FieldModal) FieldModalRender {
	return f.Render(PlainStyler{}, 70, f.Height())
}

// rowOf finds the painted row carrying a target, so no test hard-codes a row
// number that a layout change would silently invalidate.
func rowOf(t *testing.T, f *FieldModal, kind fieldModalTargetKind, key string, match func(fieldModalLine) bool) (int, fieldModalLine) {
	t.Helper()
	for index, line := range f.layout {
		if line.kind != kind || (key != "" && line.key != key) {
			continue
		}
		if match != nil && !match(line) {
			continue
		}
		return index, line
	}
	t.Fatalf("no painted %s row for %q", kind, key)
	return 0, fieldModalLine{}
}

func buttonSpan(t *testing.T, f *FieldModal, id string) (int, fieldModalSpan) {
	t.Helper()
	for index, line := range f.layout {
		for _, span := range line.spans {
			if span.id == id {
				return index, span
			}
		}
	}
	t.Fatalf("no button %q was painted", id)
	return 0, fieldModalSpan{}
}

// -- geometry ------------------------------------------------------------------------

func TestFieldModalBoxNeverResizesOrMovesWhileItIsUsed(t *testing.T) {
	fixture := newFieldModalFixture(t)
	modal := fixture.modal
	first := paint(modal)

	// Everything that could plausibly change the size: a much longer value, a
	// filtered vocabulary, an inline validation failure, a host refusal, and the
	// armed discard latch.
	modal.SetValue("owner", strings.Repeat("a very long owner name ", 6))
	modal.SetFocus("section")
	modal.HandleKey("z")
	modal.SetFieldError("section", "no such section")
	modal.SetError("the file changed underneath — try again")
	modal.SetFocus("owner")
	modal.HandleKey("\x1b")

	after := paint(modal)
	if len(after.Lines) != len(first.Lines) || after.Width != first.Width {
		t.Fatalf("box moved: %dx%d became %dx%d",
			first.Width, len(first.Lines), after.Width, len(after.Lines))
	}
	for index, line := range after.Lines {
		if got, want := (PlainStyler{}).Width(line), first.Width; got != want {
			t.Fatalf("row %d is %d cells, want %d", index, got, want)
		}
	}
}

func TestFieldModalDegenerateTerminalStillRendersSomethingUsable(t *testing.T) {
	fixture := newFieldModalFixture(t)
	for _, size := range [][2]int{{20, 5}, {12, 3}, {8, 3}} {
		render := fixture.modal.Render(PlainStyler{}, size[0], size[1])
		if len(render.Lines) == 0 {
			t.Fatalf("%dx%d rendered nothing", size[0], size[1])
		}
		if len(render.Lines) > size[1] {
			t.Fatalf("%dx%d rendered %d rows", size[0], size[1], len(render.Lines))
		}
		for _, line := range render.Lines {
			if got := (PlainStyler{}).Width(line); got > size[0] {
				t.Fatalf("%dx%d painted a %d-cell row", size[0], size[1], got)
			}
		}
	}
}

func TestFieldModalInlineErrorReplacesTheHintInPlace(t *testing.T) {
	fixture := newFieldModalFixture(t)
	before := paint(fixture.modal)
	fixture.modal.SetFieldError("owner", "an owner is required")
	after := paint(fixture.modal)
	if len(before.Lines) != len(after.Lines) {
		t.Fatal("an inline error changed the row count")
	}
	joined := strings.Join(after.Lines, "\n")
	if !strings.Contains(joined, "an owner is required") {
		t.Fatalf("the error is not painted:\n%s", joined)
	}
	if strings.Contains(joined, "who picks this up") {
		t.Fatal("the hint is still painted beside its own error")
	}
}

// -- keyboard -------------------------------------------------------------------------

func TestFieldModalTabWalksTheFieldsAndWraps(t *testing.T) {
	fixture := newFieldModalFixture(t)
	want := []string{"section", "note", "owner"}
	for _, key := range want {
		fixture.modal.HandleKey("\t")
		if got := fixture.modal.FocusKey(); got != key {
			t.Fatalf("tab landed on %q, want %q", got, key)
		}
	}
	for _, key := range []string{"note", "section", "owner"} {
		fixture.modal.HandleKey("\x1b[Z")
		if got := fixture.modal.FocusKey(); got != key {
			t.Fatalf("shift-tab landed on %q, want %q", got, key)
		}
	}
}

func TestFieldModalEnterSubmitsFromASingleLineFieldAndCtrlSFromANote(t *testing.T) {
	fixture := newFieldModalFixture(t)
	if outcome := fixture.modal.HandleKey("\r"); outcome.Result != FieldModalSubmitted {
		t.Fatalf("enter gave %s", outcome.Result)
	}
	if fixture.submitted["owner"] != "alice" || fixture.submitted["section"] != "work" {
		t.Fatalf("submitted %v", fixture.submitted)
	}

	// The documented exception: in a note, Return is text.
	fixture = newFieldModalFixture(t)
	fixture.modal.SetFocus("note")
	fixture.modal.HandleKey("a")
	if outcome := fixture.modal.HandleKey("\r"); outcome.Result != FieldModalChanged {
		t.Fatalf("enter in a note gave %s", outcome.Result)
	}
	fixture.modal.HandleKey("b")
	if got := fixture.modal.Value("note"); got != "a\nb" {
		t.Fatalf("note is %q, want a newline in it", got)
	}
	if outcome := fixture.modal.HandleKey("\x13"); outcome.Result != FieldModalSubmitted {
		t.Fatalf("ctrl-s in a note gave %s", outcome.Result)
	}
	if fixture.submitted["note"] != "a\nb" {
		t.Fatalf("submitted note %q", fixture.submitted["note"])
	}
}

func TestFieldModalEscapeGuardsUnsavedChanges(t *testing.T) {
	fixture := newFieldModalFixture(t)
	if outcome := fixture.modal.HandleKey("\x1b"); outcome.Result != FieldModalCancelled {
		t.Fatalf("a clean modal answered esc with %s", outcome.Result)
	}

	fixture = newFieldModalFixture(t)
	fixture.modal.HandleKey("x")
	if outcome := fixture.modal.HandleKey("\x1b"); outcome.Result != FieldModalGuarded {
		t.Fatalf("a dirty modal answered the first esc with %s", outcome.Result)
	}
	if !fixture.modal.PendingCancel() {
		t.Fatal("the latch did not arm")
	}
	// Carrying on typing disarms it, exactly as the task-draft confirmation does.
	fixture.modal.HandleKey("y")
	if fixture.modal.PendingCancel() {
		t.Fatal("typing left the latch armed")
	}
	fixture.modal.HandleKey("\x1b")
	if outcome := fixture.modal.HandleKey("\x1b"); outcome.Result != FieldModalCancelled {
		t.Fatalf("the second esc gave %s", outcome.Result)
	}
}

func TestFieldModalValidationRefusesTheSubmitAndFocusesTheOffender(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetValue("owner", "")
	fixture.modal.SetFocus("note")
	outcome := fixture.modal.HandleKey("\x13")
	if outcome.Result != FieldModalError {
		t.Fatalf("an invalid submit gave %s", outcome.Result)
	}
	if fixture.submitted != nil {
		t.Fatal("an invalid submit reached the host")
	}
	if fixture.modal.FocusKey() != "owner" {
		t.Fatalf("focus is on %q, want the invalid field", fixture.modal.FocusKey())
	}
	if fixture.modal.FieldError("owner") == "" {
		t.Fatal("no inline error was posted")
	}
	// Typing an answer clears the complaint.
	fixture.modal.HandleKey("b")
	if fixture.modal.FieldError("owner") != "" {
		t.Fatal("the error survived the fix")
	}
}

func TestFieldModalHostRefusalStaysInTheBox(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.refusal = "that section is not configured"
	if outcome := fixture.modal.HandleKey("\r"); outcome.Result != FieldModalError {
		t.Fatalf("a refused submit gave %s", outcome.Result)
	}
	if fixture.modal.Error() != "that section is not configured" {
		t.Fatalf("refusal is %q", fixture.modal.Error())
	}
	if !strings.Contains(strings.Join(paint(fixture.modal).Lines, "\n"), "not configured") {
		t.Fatal("the refusal is not painted")
	}
}

// -- the choice field ------------------------------------------------------------------

func TestFieldModalChoiceVocabularyIsReadAtRenderTimeNotBuildTime(t *testing.T) {
	fixture := newFieldModalFixture(t)
	*fixture.sections = append(*fixture.sections, FieldOption{Value: "reading", Label: "Reading"})
	joined := strings.Join(paint(fixture.modal).Lines, "\n")
	if !strings.Contains(joined, "Inbox") {
		t.Fatalf("the vocabulary is not painted:\n%s", joined)
	}
	found := false
	for _, option := range fixture.modal.Options("section") {
		if option.Value == "reading" {
			found = true
		}
	}
	if !found {
		t.Fatal("an option added after construction is invisible to the field")
	}
}

func TestFieldModalChoiceArrowsAndPrefixTypingSelect(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	fixture.modal.HandleKey("\x1b[B")
	if got := fixture.modal.Value("section"); got != "home" {
		t.Fatalf("down arrow selected %q, want home", got)
	}
	fixture.modal.HandleKey("\x1b[A")
	if got := fixture.modal.Value("section"); got != "work" {
		t.Fatalf("up arrow selected %q", got)
	}
	// Prefix typing: "gar" reaches an option three past the window without an
	// arrow key, which is the whole point of a vocabulary that may be long.
	for _, key := range []string{"g", "a", "r"} {
		fixture.modal.HandleKey(key)
	}
	if got := fixture.modal.Value("section"); got != "garden" {
		t.Fatalf("prefix typing selected %q, want garden", got)
	}
}

func TestFieldModalChoiceListScrollsWhenTheHighlightLeavesTheWindow(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	paint(fixture.modal)
	before := fixture.modal.byKey["section"].offset
	for count := 0; count < 5; count++ {
		fixture.modal.HandleKey("\x1b[B")
	}
	paint(fixture.modal)
	if fixture.modal.byKey["section"].offset <= before {
		t.Fatal("arrowing past the window did not scroll the list")
	}
}

func TestFieldModalFreeTextChoiceKeepsWhatWasTyped(t *testing.T) {
	people := []FieldOption{{Value: "alice@example.com", Label: "Alice"}}
	modal := NewFieldModal(FieldModalOptions{
		Title: "Hand off",
		Fields: []FieldSpec{{Key: "to", Kind: FieldChoice, Label: "To", FreeText: true,
			Options: func() []FieldOption { return people }}},
	})
	for _, key := range strings.Split("zed@example.com", "") {
		modal.HandleKey(key)
	}
	if got := modal.Value("to"); got != "zed@example.com" {
		t.Fatalf("free text became %q", got)
	}
	// An arrow still adopts an offered option, replacing what was typed.
	modal.SetValue("to", "")
	modal.HandleKey("\x1b[B")
	if got := modal.Value("to"); got != "alice@example.com" {
		t.Fatalf("the arrow selected %q", got)
	}
}

// -- pointer, against the recorded hit map ------------------------------------------

func TestFieldModalClickFocusesAFieldAndPlacesTheCaret(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetValue("owner", "alice cooper")
	fixture.modal.SetFocus("note")
	paint(fixture.modal)

	row, line := rowOf(t, fixture.modal, fieldModalValue, "owner", nil)
	fixture.modal.Click(row, line.valueCol+5)
	if fixture.modal.FocusKey() != "owner" {
		t.Fatalf("the click focused %q", fixture.modal.FocusKey())
	}
	if got := fixture.modal.byKey["owner"].input.Cursor(); got != 5 {
		t.Fatalf("the caret landed at %d, want 5", got)
	}
	// A click past the end of the value lands at the end of the value.
	fixture.modal.Click(row, line.valueCol+40)
	if got := fixture.modal.byKey["owner"].input.Cursor(); got != len("alice cooper") {
		t.Fatalf("the trailing click landed at %d", got)
	}
}

func TestFieldModalClickPlacesTheCaretInsideANote(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetValue("note", "first line\nsecond line")
	fixture.modal.SetFocus("note")
	paint(fixture.modal)

	row, line := rowOf(t, fixture.modal, fieldModalValue, "note", func(line fieldModalLine) bool {
		return line.valueRow == 1
	})
	fixture.modal.Click(row, line.valueCol+3)
	if got := fixture.modal.byKey["note"].area.Cursor(); got != len("first line\nsec") {
		t.Fatalf("the caret landed at %d", got)
	}
}

func TestFieldModalClickSelectsAnOption(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	paint(fixture.modal)
	row, _ := rowOf(t, fixture.modal, fieldModalOption, "section", func(line fieldModalLine) bool {
		return line.optionIndex == 2
	})
	if outcome := fixture.modal.Click(row, 4); outcome.Result != FieldModalChanged {
		t.Fatalf("clicking an option gave %s", outcome.Result)
	}
	if got := fixture.modal.Value("section"); got != "home" {
		t.Fatalf("the click selected %q, want home", got)
	}
}

func TestFieldModalWheelScrollsTheChoiceListWithoutSelecting(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	paint(fixture.modal)
	row, _ := rowOf(t, fixture.modal, fieldModalOption, "section", nil)
	selected := fixture.modal.Value("section")
	fixture.modal.Wheel(row, 4, 1)
	fixture.modal.Wheel(row, 4, 1)
	if fixture.modal.byKey["section"].offset != 2 {
		t.Fatalf("the wheel scrolled to %d", fixture.modal.byKey["section"].offset)
	}
	if fixture.modal.Value("section") != selected {
		t.Fatal("a wheel tick changed the selection")
	}
	paint(fixture.modal)
	if !strings.Contains(strings.Join(paint(fixture.modal).Lines, "\n"), "Travel") {
		t.Fatal("the scrolled window does not show the later options")
	}
}

func TestFieldModalWheelScrollsANote(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetValue("note", "one\ntwo\nthree\nfour\nfive\nsix")
	fixture.modal.SetFocus("note")
	paint(fixture.modal)
	row, _ := rowOf(t, fixture.modal, fieldModalValue, "note", func(line fieldModalLine) bool {
		return line.valueRow == 0
	})
	before := fixture.modal.byKey["note"].area.Cursor()
	fixture.modal.Wheel(row, 4, 1)
	fixture.modal.Wheel(row, 4, 1)
	if fixture.modal.byKey["note"].area.Cursor() == before {
		t.Fatal("the wheel did not move through the note")
	}
}

func TestFieldModalButtonsAreHittable(t *testing.T) {
	fixture := newFieldModalFixture(t)
	paint(fixture.modal)
	row, span := buttonSpan(t, fixture.modal, fieldModalSubmitID)
	if outcome := fixture.modal.Click(row, span.begin); outcome.Result != FieldModalSubmitted {
		t.Fatalf("clicking Move gave %s", outcome.Result)
	}

	fixture = newFieldModalFixture(t)
	paint(fixture.modal)
	row, span = buttonSpan(t, fixture.modal, "clear")
	outcome := fixture.modal.Click(row, span.end-1)
	if outcome.Result != FieldModalActioned || outcome.ActionID != "clear" {
		t.Fatalf("clicking the action gave %s/%s", outcome.Result, outcome.ActionID)
	}

	// The cancel button carries the same latch the escape key does.
	fixture = newFieldModalFixture(t)
	fixture.modal.HandleKey("x")
	paint(fixture.modal)
	row, span = buttonSpan(t, fixture.modal, fieldModalCancelID)
	if outcome := fixture.modal.Click(row, span.begin+1); outcome.Result != FieldModalGuarded {
		t.Fatalf("the first cancel click gave %s", outcome.Result)
	}
	if outcome := fixture.modal.Click(row, span.begin+1); outcome.Result != FieldModalCancelled {
		t.Fatalf("the second cancel click gave %s", outcome.Result)
	}
}

func TestFieldModalClickOutsideTheBoxIsInertOnBothAxes(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.HandleKey("x")
	render := paint(fixture.modal)
	before := fieldModalState(fixture.modal)

	cells := [][2]int{}
	for _, row := range []int{-4, len(fixture.modal.layout) + 3} {
		cells = append(cells, [2]int{row, 2})
	}
	// The column axis matters as much as the row: a click to the LEFT of the
	// border, or past the right-hand border onto the list behind the modal, is
	// outside the box and must do nothing.
	for row := range fixture.modal.layout {
		cells = append(cells, [2]int{row, -30}, [2]int{row, -1},
			[2]int{row, render.Width}, [2]int{row, render.Width + 12})
	}
	for _, cell := range cells {
		if outcome := fixture.modal.Click(cell[0], cell[1]); outcome.Result != FieldModalHandled {
			t.Fatalf("a click at row %d column %d gave %s", cell[0], cell[1], outcome.Result)
		}
		if outcome := fixture.modal.Wheel(cell[0], cell[1], 1); outcome.Result != FieldModalHandled {
			t.Fatalf("a wheel tick at row %d column %d gave %s", cell[0], cell[1], outcome.Result)
		}
	}
	if after := fieldModalState(fixture.modal); after != before {
		t.Fatalf("an outside click changed the modal\nbefore: %s\nafter:  %s", before, after)
	}
}

// A narrow box truncates its button row, but the spans were recorded against
// the UNtruncated text. Without a column bound the `clear` action sat at columns
// 36-54 of a 24-cell box — a dozen columns out on the list behind it — and
// clicking there invoked it.
func TestFieldModalATruncatedButtonCannotBeClickedOffTheBox(t *testing.T) {
	fixture := newFieldModalFixture(t)
	render := fixture.modal.Render(PlainStyler{}, 24, fixture.modal.Height())
	if render.Width != 24 {
		t.Fatalf("the box painted %d cells, want the clamped 24", render.Width)
	}
	before := fieldModalState(fixture.modal)
	row, span := buttonSpan(t, fixture.modal, "clear")
	if span.begin < render.Width {
		t.Fatalf("the clear button starts at %d, inside a %d-cell box — pick a narrower one",
			span.begin, render.Width)
	}
	for column := render.Width; column < span.end+4; column++ {
		outcome := fixture.modal.Click(row, column)
		if outcome.Result != FieldModalHandled || outcome.ActionID != "" {
			t.Fatalf("column %d outside a %d-cell box gave %s/%s",
				column, render.Width, outcome.Result, outcome.ActionID)
		}
	}
	if after := fieldModalState(fixture.modal); after != before {
		t.Fatal("a click past the border changed the modal")
	}
}

// The vocabulary is a runtime func, so it can reorder between the paint and the
// click. What was painted is what must be selected.
func TestFieldModalAnOptionIsResolvedByValueNotByPosition(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	paint(fixture.modal)
	row, line := rowOf(t, fixture.modal, fieldModalOption, "section", func(line fieldModalLine) bool {
		return line.optionIndex == 2
	})
	painted := line.optionValue

	// The list reverses underneath the pointer, between the paint and the click.
	reversed := []FieldOption{}
	for index := len(*fixture.sections) - 1; index >= 0; index-- {
		reversed = append(reversed, (*fixture.sections)[index])
	}
	*fixture.sections = reversed

	fixture.modal.Click(row, 4)
	if got := fixture.modal.Value("section"); got != painted {
		t.Fatalf("the click selected %q, but the row said %q", got, painted)
	}

	// An option that has gone entirely selects nothing rather than a neighbour.
	fixture = newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	paint(fixture.modal)
	row, _ = rowOf(t, fixture.modal, fieldModalOption, "section", func(line fieldModalLine) bool {
		return line.optionIndex == 2
	})
	before := fixture.modal.Value("section")
	*fixture.sections = []FieldOption{{Value: "only", Label: "Only"}}
	if outcome := fixture.modal.Click(row, 4); outcome.Result != FieldModalHandled {
		t.Fatalf("clicking a vanished option gave %s", outcome.Result)
	}
	if got := fixture.modal.Value("section"); got != before {
		t.Fatalf("clicking a vanished option selected %q", got)
	}
}

func TestFieldModalWheelDisarmsTheDiscardLatch(t *testing.T) {
	fixture := newFieldModalFixture(t)
	fixture.modal.SetFocus("section")
	fixture.modal.HandleKey("\x1b[B")
	paint(fixture.modal)
	if fixture.modal.HandleKey("\x1b").Result != FieldModalGuarded {
		t.Fatal("the latch did not arm")
	}
	row, _ := rowOf(t, fixture.modal, fieldModalOption, "section", nil)
	fixture.modal.Wheel(row, 4, 1)
	if fixture.modal.PendingCancel() {
		t.Fatal("a wheel tick left the latch armed")
	}
	if outcome := fixture.modal.HandleKey("\x1b"); outcome.Result != FieldModalGuarded {
		t.Fatalf("the escape after a wheel tick gave %s, want a fresh arm", outcome.Result)
	}
}

// -- parity ----------------------------------------------------------------------------

// TestFieldModalEveryActionHasAKeyPathAndAMousePath is the modal binding parity
// rule restated for a component that has no shortcut registry of its own: each
// action is driven twice, once from the keyboard and once from the pointer, and
// the two must leave the modal in the same state.
func TestFieldModalEveryActionHasAKeyPathAndAMousePath(t *testing.T) {
	type action struct {
		name  string
		setup func(*FieldModal)
		key   func(*FieldModal) FieldModalOutcome
		mouse func(*testing.T, *FieldModal) FieldModalOutcome
	}
	actions := []action{
		{
			name: "focus a field",
			key:  func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\t") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, line := rowOf(t, f, fieldModalValue, "section", nil)
				return f.Click(row, line.valueCol)
			},
		},
		{
			name:  "place the caret",
			setup: func(f *FieldModal) { f.SetValue("owner", "alice cooper") },
			key: func(f *FieldModal) FieldModalOutcome {
				f.HandleKey("\x01")
				for count := 0; count < 5; count++ {
					f.HandleKey("\x1b[C")
				}
				return fieldModalHandled()
			},
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, line := rowOf(t, f, fieldModalValue, "owner", nil)
				return f.Click(row, line.valueCol+5)
			},
		},
		{
			// The single-line case above exercises Input.OffsetAt. This one
			// exercises TextArea.OffsetAt, the 2D inverse — the harder of the
			// two, and mouse-only, so nothing else in this suite would notice it
			// drifting a row.
			name: "place the caret inside a note",
			setup: func(f *FieldModal) {
				f.SetValue("note", "first line\nsecond line")
				f.SetFocus("note")
			},
			key: func(f *FieldModal) FieldModalOutcome {
				f.HandleKey("\x1b[B")
				for count := 0; count < 3; count++ {
					f.HandleKey("\x1b[C")
				}
				return fieldModalHandled()
			},
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, line := rowOf(t, f, fieldModalValue, "note", func(line fieldModalLine) bool {
					return line.valueRow == 1
				})
				return f.Click(row, line.valueCol+3)
			},
		},
		{
			name:  "choose an option",
			setup: func(f *FieldModal) { f.SetFocus("section") },
			key:   func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\x1b[B") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, _ := rowOf(t, f, fieldModalOption, "section", func(line fieldModalLine) bool {
					return line.optionIndex == 2
				})
				return f.Click(row, 5)
			},
		},
		{
			name:  "scroll a note",
			setup: func(f *FieldModal) { f.SetValue("note", "one\ntwo\nthree\nfour"); f.SetFocus("note") },
			key:   func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\x1b[B") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, _ := rowOf(t, f, fieldModalValue, "note", func(line fieldModalLine) bool {
					return line.valueRow == 0
				})
				return f.Wheel(row, 4, 1)
			},
		},
		{
			name: "submit",
			key:  func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\r") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, span := buttonSpan(t, f, fieldModalSubmitID)
				return f.Click(row, span.begin)
			},
		},
		{
			name: "cancel a clean modal",
			key:  func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\x1b") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, span := buttonSpan(t, f, fieldModalCancelID)
				return f.Click(row, span.begin)
			},
		},
		{
			name:  "cancel a dirty modal arms the latch",
			setup: func(f *FieldModal) { f.SetValue("owner", "bob") },
			key:   func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\x1b") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, span := buttonSpan(t, f, fieldModalCancelID)
				return f.Click(row, span.begin)
			},
		},
		{
			name: "invoke an action",
			key:  func(f *FieldModal) FieldModalOutcome { return f.HandleKey("\x0b") },
			mouse: func(t *testing.T, f *FieldModal) FieldModalOutcome {
				row, span := buttonSpan(t, f, "clear")
				return f.Click(row, span.begin)
			},
		},
	}

	for _, subject := range actions {
		t.Run(subject.name, func(t *testing.T) {
			byKey, byMouse := newFieldModalFixture(t), newFieldModalFixture(t)
			for _, fixture := range []*fieldModalFixture{byKey, byMouse} {
				if subject.setup != nil {
					subject.setup(fixture.modal)
				}
				paint(fixture.modal)
			}
			keyOutcome := subject.key(byKey.modal)
			mouseOutcome := subject.mouse(t, byMouse.modal)
			if keyOutcome.ActionID != mouseOutcome.ActionID {
				t.Fatalf("action id %q by key, %q by mouse", keyOutcome.ActionID, mouseOutcome.ActionID)
			}
			if keyOutcome.Result != mouseOutcome.Result {
				t.Fatalf("result %s by key, %s by mouse", keyOutcome.Result, mouseOutcome.Result)
			}
			if got, want := fieldModalState(byMouse.modal), fieldModalState(byKey.modal); got != want {
				t.Fatalf("state differs\nkey:   %s\nmouse: %s", want, got)
			}
			if got, want := fmt.Sprint(byMouse.submitted), fmt.Sprint(byKey.submitted); got != want {
				t.Fatalf("the host saw different values\nkey:   %s\nmouse: %s", want, got)
			}
		})
	}
}

// -- the model host ----------------------------------------------------------------------

func fieldModalHarness(t *testing.T, mouse bool) (*modelHarness, *fieldModalFixture) {
	t.Helper()
	harness := newModelHarness(t, harnessOptions{
		paths: func(paths *config.Paths) { paths.Mouse = mouse },
	})
	fixture := newFieldModalFixture(t)
	if !harness.model.OpenFieldModal(fixture.modal) {
		t.Fatal("the modal did not open")
	}
	return harness, fixture
}

func TestFieldModalOpensAsItsOwnModeAndPaintsOverTheList(t *testing.T) {
	harness, _ := fieldModalHarness(t, false)
	if harness.model.Mode() != ModeFieldModal {
		t.Fatalf("mode is %s", harness.model.Mode())
	}
	if harness.model.FocusContext() != "field_modal" {
		t.Fatalf("focus context is %s", harness.model.FocusContext())
	}
	frame := fmt.Sprint(harness.model.View().Content)
	if !strings.Contains(frame, "Move task") || !strings.Contains(frame, "Section") {
		t.Fatalf("the box is not painted over the list:\n%s", frame)
	}
	harness.pressKeys("\x1b")
	if harness.model.Mode() != ModeList || harness.model.FieldModal() != nil {
		t.Fatal("escape did not close a clean modal")
	}
}

func TestFieldModalKeyboardRunsEndToEndThroughTheModel(t *testing.T) {
	harness, fixture := fieldModalHarness(t, false)
	harness.pressKeys("\t", "\x1b[B", "\t", "d", "o", "n", "e", "\x13")
	if harness.model.Mode() != ModeList {
		t.Fatalf("the modal is still open in %s", harness.model.Mode())
	}
	if fixture.submitted["section"] != "home" || fixture.submitted["note"] != "done" {
		t.Fatalf("submitted %v", fixture.submitted)
	}
}

func TestFieldModalMouseRunsEndToEndThroughTheModel(t *testing.T) {
	harness, fixture := fieldModalHarness(t, true)
	box := harness.model.Overlay()
	if box == nil {
		t.Fatal("no overlay was painted")
	}
	modal := harness.model.FieldModal()
	row, _ := rowOf(t, modal, fieldModalOption, "section", func(line fieldModalLine) bool {
		return line.optionIndex == 2
	})
	if !harness.model.HandleMouse(tea.MouseClickMsg{X: box.Col + 5, Y: box.Row + row, Button: tea.MouseLeft}) {
		t.Fatal("the option click was not consumed")
	}
	if modal.Value("section") != "home" {
		t.Fatalf("the click selected %q", modal.Value("section"))
	}
	submitRow, span := buttonSpan(t, modal, fieldModalSubmitID)
	if !harness.model.HandleMouse(tea.MouseClickMsg{
		X: box.Col + span.begin, Y: box.Row + submitRow, Button: tea.MouseLeft}) {
		t.Fatal("the submit click was not consumed")
	}
	if harness.model.Mode() != ModeList || fixture.submitted["section"] != "home" {
		t.Fatalf("mode=%s submitted=%v", harness.model.Mode(), fixture.submitted)
	}
}

func TestFieldModalIgnoresThePointerWhenTheConfigTurnedTheMouseOff(t *testing.T) {
	harness, _ := fieldModalHarness(t, false)
	modal := harness.model.FieldModal()
	box := harness.model.Overlay()
	before := fieldModalState(modal)
	row, span := buttonSpan(t, modal, fieldModalSubmitID)
	if harness.model.HandleMouse(tea.MouseClickMsg{
		X: box.Col + span.begin, Y: box.Row + row, Button: tea.MouseLeft}) {
		t.Fatal("a click was consumed with the mouse disabled")
	}
	if harness.model.Mode() != ModeFieldModal || fieldModalState(modal) != before {
		t.Fatal("a click acted on the modal with the mouse disabled")
	}
}

func TestFieldModalClickOutsideTheBoxDoesNotReachTheListBehindIt(t *testing.T) {
	harness, _ := fieldModalHarness(t, true)
	selected := harness.model.Selected()
	box := harness.model.Overlay()
	if !harness.model.HandleMouse(tea.MouseClickMsg{X: 1, Y: box.Row - 1, Button: tea.MouseLeft}) {
		t.Fatal("an outside click was not consumed by the overlay")
	}
	if harness.model.Mode() != ModeFieldModal {
		t.Fatal("an outside click closed the modal")
	}
	if harness.model.Selected() != selected {
		t.Fatal("an outside click moved the list selection underneath")
	}
}

// Ctrl-C reaches through every mode, which made it the one route that discarded
// a filled-in modal in a single keystroke, behind the back of the escape latch.
// A dirty modal now raises the same confirmation a dirty task draft does.
func TestFieldModalCtrlCOnADirtyModalConfirmsBeforeDiscarding(t *testing.T) {
	harness, _ := fieldModalHarness(t, false)
	harness.pressKeys("z")
	harness.pressKeys("\x03")
	if harness.model.quitting {
		t.Fatal("ctrl-c quit with unsaved changes")
	}
	if harness.model.Modal() == nil || harness.model.Modal().Kind() != ModalFieldModalQuitConfirm {
		t.Fatalf("no confirmation was raised: %v", harness.model.Modal())
	}
	if available, err := harness.model.CommandAvailable("quit"); err != nil || available {
		t.Fatalf("quit is still advertised during the confirmation (available=%v err=%v)", available, err)
	}

	// Ctrl-C cannot answer its own prompt.
	harness.pressKeys("\x03")
	if harness.model.quitting || harness.model.Modal() == nil {
		t.Fatal("a second ctrl-c answered the confirmation")
	}
	// Declining restores the modal with its work intact.
	harness.pressKeys("n")
	if harness.model.Mode() != ModeFieldModal || harness.model.FieldModal() == nil {
		t.Fatalf("declining did not restore the modal (mode=%s)", harness.model.Mode())
	}
	if !harness.model.FieldModal().Dirty() {
		t.Fatal("the restored modal lost its changes")
	}

	// Accepting discards and quits.
	harness.pressKeys("\x03", "y")
	if !harness.model.quitting {
		t.Fatal("confirming did not quit")
	}
}

// The two latches must not collide. Arming the discard latch and then being
// interrupted by the quit confirmation left the arm standing, so declining the
// quit — which says "unsaved changes kept" — was followed by a single escape
// that discarded them. Both latches guard the same work; neither may cancel the
// other out.
func TestFieldModalDecliningTheQuitConfirmationDisarmsTheDiscardLatch(t *testing.T) {
	harness, _ := fieldModalHarness(t, false)
	harness.pressKeys("z")
	harness.pressKeys("\x1b")
	if !harness.model.FieldModal().PendingCancel() {
		t.Fatal("the discard latch did not arm")
	}
	harness.pressKeys("\x03")
	if harness.model.Modal() == nil || harness.model.Modal().Kind() != ModalFieldModalQuitConfirm {
		t.Fatal("ctrl-c did not raise the quit confirmation")
	}
	harness.pressKeys("n")
	if harness.model.Mode() != ModeFieldModal || harness.model.FieldModal() == nil {
		t.Fatalf("declining did not restore the modal (mode=%s)", harness.model.Mode())
	}
	if harness.model.FieldModal().PendingCancel() {
		t.Fatal("the discard latch survived the interruption")
	}

	// The next escape therefore arms again rather than discarding, and the
	// banner the restored box paints agrees with what the flash promised.
	harness.pressKeys("\x1b")
	if harness.model.Mode() != ModeFieldModal || harness.model.FieldModal() == nil {
		t.Fatal("one escape after declining discarded the work")
	}
	if !harness.model.FieldModal().Dirty() {
		t.Fatal("the changes were lost")
	}
	harness.pressKeys("\x1b")
	if harness.model.Mode() != ModeList || harness.model.FieldModal() != nil {
		t.Fatal("the second escape did not discard")
	}
}

func TestFieldModalCtrlCOnACleanModalQuitsImmediately(t *testing.T) {
	harness, _ := fieldModalHarness(t, false)
	harness.pressKeys("\x03")
	if !harness.model.quitting {
		t.Fatal("ctrl-c on a clean modal raised a confirmation instead of quitting")
	}
}

func TestFieldModalGuardFlashesBeforeItDiscards(t *testing.T) {
	harness, _ := fieldModalHarness(t, false)
	harness.pressKeys("z", "\x1b")
	if harness.model.Mode() != ModeFieldModal {
		t.Fatal("the first escape discarded a dirty modal")
	}
	if !strings.Contains(harness.model.FlashMessage(), "esc again discards") {
		t.Fatalf("flash is %q", harness.model.FlashMessage())
	}
	harness.pressKeys("\x1b")
	if harness.model.Mode() != ModeList || harness.model.FieldModal() != nil {
		t.Fatal("the second escape did not discard")
	}
}
