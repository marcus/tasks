package tui

import (
	"strings"
	"testing"

	"tasks-go/internal/tui/term/ansi"
	"tasks-go/internal/tui/termform"
)

func demoForm(t *testing.T, focus string) *termform.Form {
	t.Helper()
	titleBase := termform.NewBase("title", "Title", "Book flight")
	titleBase.RequiredFixed = true
	note := termform.NewTextArea(termform.NewBase("note", "Notes", "line one\nline two"))
	state := termform.NewSelect(termform.NewBase("state", "State", "TODO"),
		func(termform.Context) []termform.Option {
			return []termform.Option{termform.NewOption("TODO", ""), termform.NewOption("DONE", "")}
		}, false)
	form, err := termform.NewForm([]termform.Group{
		termform.NewGroup("basics", "Basics", termform.NewInput(titleBase)),
		termform.NewGroup("notes", "Notes", note),
		termform.NewGroup("lifecycle", "Lifecycle", state),
	}, focus, nil)
	if err != nil {
		t.Fatal(err)
	}
	return form
}

func renderedText(render FormRender) string {
	lines := make([]string, 0, len(render.Lines))
	for _, line := range render.Lines {
		lines = append(lines, ansi.Strip(line))
	}
	return strings.Join(lines, "\n")
}

// -- the shared shape --------------------------------------------------------------

func TestFormRenderShowsGroupsLabelsValuesAndTheHint(t *testing.T) {
	render := RenderForm(testStyler(), FormRenderRequest{
		Model: demoForm(t, "title").RenderModel(), Width: 60, Height: 14,
		Title: "edit task", Hint: "tab saves and moves",
	})
	text := renderedText(render)
	for _, want := range []string{"edit task", "Basics", "Title*", "Book flight", "tab saves and moves"} {
		if !strings.Contains(text, want) {
			t.Errorf("the rendered form is missing %q:\n%s", want, text)
		}
	}
}

func TestFormRenderMarksFocusDirtyAndError(t *testing.T) {
	form := demoForm(t, "title")
	form.SetValue("title", "Book flight!", termform.Event{})
	render := RenderForm(testStyler(), FormRenderRequest{
		Model: form.RenderModel(), Width: 60, Height: 14, Title: "t",
	})
	text := renderedText(render)
	if !strings.Contains(text, "›") {
		t.Error("the focused row carries no focus marker")
	}
	if !strings.Contains(text, "*") {
		t.Error("the dirty row carries no unsaved marker")
	}

	form.SetValue("title", "", termform.Event{})
	form.Validate()
	errored := renderedText(RenderForm(testStyler(), FormRenderRequest{
		Model: form.RenderModel(), Width: 60, Height: 14, Title: "t",
	}))
	if !strings.Contains(errored, "!") || !strings.Contains(errored, "required") {
		t.Errorf("an invalid field is not marked and explained:\n%s", errored)
	}
}

// A host-level failure — a rejected save — outranks the form's own validation
// message, because it is the newer news.
func TestFormRenderPrefersTheHostErrorOverAFieldError(t *testing.T) {
	form := demoForm(t, "title")
	form.SetValue("title", "", termform.Event{})
	form.Validate()
	text := renderedText(RenderForm(testStyler(), FormRenderRequest{
		Model: form.RenderModel(), Width: 60, Height: 14, Title: "t",
		Error: "the store refused this",
	}))
	if !strings.Contains(text, "the store refused this") {
		t.Errorf("the host's refusal is not shown:\n%s", text)
	}
}

func TestFormRenderShowsAChoiceFieldsOptions(t *testing.T) {
	form := demoForm(t, "state")
	form.HandleKey("\r") // opens the option list
	text := renderedText(RenderForm(testStyler(), FormRenderRequest{
		Model: form.RenderModel(), Width: 60, Height: 18, Title: "t",
	}))
	if !strings.Contains(text, "[x]") || !strings.Contains(text, "DONE") {
		t.Errorf("the open select shows no options:\n%s", text)
	}
}

func TestFormRenderWrapsANoteAcrossRows(t *testing.T) {
	form := demoForm(t, "note")
	text := renderedText(RenderForm(testStyler(), FormRenderRequest{
		Model: form.RenderModel(), Width: 40, Height: 16, Title: "t",
	}))
	if !strings.Contains(text, "line one") || !strings.Contains(text, "line two") {
		t.Errorf("the note's second line is missing:\n%s", text)
	}
}

// -- responsive bounds ---------------------------------------------------------------

// Every rectangle has to produce lines that FIT. A form is rendered into a
// panel whose width the user controls with ctrl-k/ctrl-l, so "it looks fine at
// 80 columns" is not a contract.
func TestFormRenderFitsEveryRectangle(t *testing.T) {
	styler := testStyler()
	form := demoForm(t, "title")
	for width := 0; width <= 64; width += 3 {
		for height := 0; height <= 18; height += 2 {
			render := RenderForm(styler, FormRenderRequest{
				Model: form.RenderModel(), Width: width, Height: height,
				Title: "edit task", Hint: "a hint that is quite long indeed",
			})
			if height > 0 && len(render.Lines) > height {
				t.Fatalf("%dx%d produced %d lines", width, height, len(render.Lines))
			}
			for _, line := range render.Lines {
				if styler.Width(line) > width {
					t.Fatalf("%dx%d produced a %d-cell line: %q",
						width, height, styler.Width(line), ansi.Strip(line))
				}
			}
		}
	}
}

func TestFormRenderEmitsNothingAtAZeroBudget(t *testing.T) {
	render := RenderForm(testStyler(), FormRenderRequest{
		Model: demoForm(t, "title").RenderModel(), Width: 0, Height: 6, Title: "t",
	})
	if len(render.Lines) != 0 {
		t.Errorf("a zero-width budget produced %d lines", len(render.Lines))
	}
}

// A tall form has to scroll to the focused field, keeping a couple of rows of
// context above it so navigation reads as scrolling rather than jumping.
func TestFormRenderScrollsToTheFocusedField(t *testing.T) {
	form := demoForm(t, "state")
	render := RenderForm(testStyler(), FormRenderRequest{
		Model: form.RenderModel(), Width: 60, Height: 6, Title: "t",
	})
	if !strings.Contains(renderedText(render), "State") {
		t.Errorf("the focused field scrolled out of view:\n%s", renderedText(render))
	}
	if render.FocusedContentRow < 0 {
		t.Error("the caret row was not reported")
	}
}

// -- the quick form ---------------------------------------------------------------------

func quickForm(t *testing.T, initial string, submit func(string) string) *QuickForm {
	t.Helper()
	form := NewQuickForm(QuickFormOptions{
		Kind: QuickFormDate, Title: "example", Prompt: "value",
		Hint: "enter a value", MinWidth: 32, ReturnMode: ReturnList,
		Initial: initial, Submit: submit,
	})
	if form == nil {
		t.Fatal("the quick form did not build")
	}
	return form
}

func TestQuickFormEditPasteAndUnicodeAreOwnedByOneInput(t *testing.T) {
	form := quickForm(t, "", func(string) string { return "" })
	if got := form.HandleKey("界"); got != QuickFormChanged {
		t.Fatalf("typing produced %q", got)
	}
	if got := form.Paste("🙂e\u0301\nnext"); got != QuickFormChanged {
		t.Fatalf("pasting produced %q", got)
	}
	if form.Text() != "界🙂e\u0301 next" {
		t.Errorf("one input did not own both edits: %q", form.Text())
	}
}

func TestQuickFormValidationErrorStaysUntilTheContentChanges(t *testing.T) {
	form := quickForm(t, "", func(raw string) string {
		if raw == "ok" {
			return ""
		}
		return "not valid"
	})
	if got := form.HandleKey("\r"); got != QuickFormError {
		t.Fatalf("submitting produced %q", got)
	}
	if form.Error() != "not valid" {
		t.Fatalf("the refusal is %q", form.Error())
	}
	// A cursor move is not a fix; the message stays.
	form.HandleKey("\x02")
	if form.Error() != "not valid" {
		t.Error("a cursor move cleared the refusal")
	}
	form.HandleKey("o")
	if form.Error() != "" {
		t.Errorf("typing did not clear the refusal: %q", form.Error())
	}
}

func TestQuickFormSubmitCancelAndSuccessAreDeterministic(t *testing.T) {
	if got := quickForm(t, "", func(string) string { return "" }).HandleKey("\x1b"); got != QuickFormCancelled {
		t.Errorf("escape produced %q", got)
	}
	submitted := ""
	form := quickForm(t, "ready", func(raw string) string {
		submitted = raw
		return ""
	})
	if got := form.HandleKey("\n"); got != QuickFormSubmitted {
		t.Errorf("return produced %q", got)
	}
	if submitted != "ready" {
		t.Errorf("the callback received %q", submitted)
	}
}

func TestQuickFormPopupShowsThePromptValueAndRefusal(t *testing.T) {
	form := quickForm(t, "界", func(string) string { return "bad value" })
	form.Submit()
	text := renderedText(form.Popup(testStyler(), 80, 10))
	for _, want := range []string{"example", "value", "界", "bad value"} {
		if !strings.Contains(text, want) {
			t.Errorf("the popup is missing %q:\n%s", want, text)
		}
	}
}

// A callable hint turns the hint line into a live preview of what the typed
// value means — the recurrence popup renders its schedule explanation this way.
func TestQuickFormHintResolvesFromTheCurrentInput(t *testing.T) {
	form := NewQuickForm(QuickFormOptions{
		Kind: QuickFormRecurrence, Title: "t", Prompt: "every", MinWidth: 20,
		HintFunc: func(text string, width int) string {
			return "you typed " + text + " in " + itoa(width) + " cells"
		},
		Submit: func(string) string { return "" },
	})
	form.HandleKey("w")
	if got := form.Hint(24); got != "you typed w in 20 cells" {
		t.Errorf("the hint resolved to %q", got)
	}
}

func TestQuickFormPopupAdaptsToEveryNarrowRectangle(t *testing.T) {
	styler := testStyler()
	for width := 1; width <= 60; width += 5 {
		for height := 1; height <= 8; height++ {
			form := quickForm(t, "some value", func(string) string { return "" })
			render := form.Popup(styler, width, height)
			if len(render.Lines) > height {
				t.Fatalf("%dx%d produced %d lines", width, height, len(render.Lines))
			}
			for _, line := range render.Lines {
				if styler.Width(line) > width {
					t.Fatalf("%dx%d produced a %d-cell line: %q",
						width, height, styler.Width(line), ansi.Strip(line))
				}
			}
		}
	}
}

// -- the fixed-size render proof ----------------------------------------------------

// A whole frame at a pinned size, with the editor open, compared byte for byte
// against a recorded fixture.
//
// This is the proof that the overlay compositing, the panel geometry and the
// form renderer agree. Every other test in this file checks one of the three;
// only this one catches the case where each is individually right and the
// composite is one column off.
func TestFixedSizeFrameWithTheEditorOpen(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.styler = testStyler()
	harness.model.width, harness.model.height = 96, 24
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()
	harness.model.StartTaskEdit("title")
	if harness.model.TaskEditor() == nil {
		t.Fatalf("the editor did not open: %q", harness.model.FlashMessage())
	}
	assertFrameFixture(t, "editor_open", harness.model.Render())
}

func TestFixedSizeFrameWithTheHelpModalOpen(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.styler = testStyler()
	harness.model.width, harness.model.height = 96, 24
	harness.model.SwitchView(ViewNext)
	harness.model.OpenHelp()
	assertFrameFixture(t, "help_modal", harness.model.Render())
}

func TestFixedSizeFrameWithTheActionPaletteOpen(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.styler = testStyler()
	harness.model.width, harness.model.height = 96, 24
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenActionPalette()
	assertFrameFixture(t, "action_palette", harness.model.Render())
}

// assertFrameFixture compares a rendered frame against its recorded bytes, and
// also re-checks the invariant a golden alone would not: every line is exactly
// as wide as the frame claims to be.
func assertFrameFixture(t *testing.T, name, frame string) {
	t.Helper()
	styler := testStyler()
	lines := strings.Split(frame, "\n")
	for index, line := range lines {
		if got := styler.Width(line); got != 96 {
			t.Errorf("%s line %d is %d cells wide, want 96: %q",
				name, index, got, ansi.Strip(line))
		}
	}
	if len(lines) != 24 {
		t.Errorf("%s rendered %d lines, want 24", name, len(lines))
	}
	assertGolden(t, "frame_"+name, stripFrame(frame))
}

func stripFrame(frame string) string {
	lines := strings.Split(frame, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, ansi.Strip(line))
	}
	return strings.Join(out, "\n") + "\n"
}
