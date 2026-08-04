package tui

import (
	"strings"
	"testing"

	"tasks-go/internal/tui/term/ansi"
	"tasks-go/internal/tui/term/shortcuts"
)

func pickerOptions(labels ...string) []PickerOption {
	out := make([]PickerOption, 0, len(labels))
	for _, label := range labels {
		out = append(out, PickerOption{ID: label, Label: label})
	}
	return out
}

func resultIDs(options []PickerOption) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		out = append(out, option.ID)
	}
	return out
}

// -- the choice picker ---------------------------------------------------------

// Relevance is what makes typing three letters land on the thing you meant.
// Ties break by declaration order, so the answer is identical on every run.
func TestPickerRanksExactPrefixTokenPrefixThenSubstring(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{
		Title:   "t",
		Options: pickerOptions("unrelated cat", "catalog", "cat", "the cat sat", "concat"),
	})
	for _, key := range strings.Split("cat", "") {
		picker.HandleKey(key)
	}
	got := resultIDs(picker.Results())
	want := []string{"cat", "catalog", "unrelated cat", "the cat sat", "concat"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("ranking\n got %v\nwant %v", got, want)
	}
}

func TestPickerCursorScrollsWithoutReorderingResults(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{
		Title: "t", Options: pickerOptions("a", "b", "c", "d", "e"), MaxVisible: 2,
	})
	before := resultIDs(picker.Results())
	picker.Move(3)
	if picker.CursorIndex() != 3 {
		t.Errorf("cursor %d, want 3", picker.CursorIndex())
	}
	if after := resultIDs(picker.Results()); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Errorf("moving the cursor reordered the rows: %v", after)
	}
	if picker.ViewportStart() == 0 {
		t.Error("the viewport did not follow the cursor past the visible window")
	}
}

func TestPickerCursorClampsAtBothEnds(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{Title: "t", Options: pickerOptions("a", "b")})
	picker.Move(10)
	if picker.CursorIndex() != 1 {
		t.Errorf("cursor ran past the end: %d", picker.CursorIndex())
	}
	picker.Move(-10)
	if picker.CursorIndex() != 0 {
		t.Errorf("cursor ran before the start: %d", picker.CursorIndex())
	}
}

func TestPickerMultipleChoiceStagesUntilAccept(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{
		Title: "t", Options: pickerOptions("a", "b", "c"), Mode: SelectMultiple,
	})
	if got := picker.HandleKey(" ").Kind; got != PickerChanged {
		t.Fatalf("space produced %q, want a staged toggle", got)
	}
	if !picker.Selected("a") {
		t.Error("space did not stage the option under the cursor")
	}
	picker.Move(1)
	picker.HandleKey(" ")
	result := picker.HandleKey("\r")
	if result.Kind != PickerAccepted {
		t.Fatalf("return produced %q", result.Kind)
	}
	if strings.Join(result.IDs, ",") != "a,b" {
		t.Errorf("accepted %v, want the staged pair", result.IDs)
	}
}

func TestPickerSingleChoiceTreatsSpaceAsSearchInput(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{Title: "t", Options: pickerOptions("a b", "c")})
	picker.HandleKey("a")
	if got := picker.HandleKey(" ").Kind; got != PickerChanged {
		t.Fatalf("space produced %q, want typed input", got)
	}
	if picker.Input() != "a " {
		t.Errorf("space did not reach the query: %q", picker.Input())
	}
}

func TestPickerDuplicateAndEmptyIDsAreDropped(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{Title: "t", Options: []PickerOption{
		{ID: "a", Label: "first"}, {ID: "a", Label: "second"}, {ID: "", Label: "nameless"},
	}})
	if got := len(picker.Options()); got != 1 {
		t.Errorf("kept %d options, want the single well-formed one", got)
	}
}

func TestPickerRefreshPreservesQueryAndNeverShrinksTheBox(t *testing.T) {
	styler := testStyler()
	picker := NewChoicePicker(ChoicePickerOptions{
		Title: "t", Options: pickerOptions("alpha", "beta", "gamma"),
	})
	picker.HandleKey("a")
	picker.Popup(styler, 200, 40, func(text string, _ int) string { return text })
	widthBefore := len(picker.Popup(styler, 200, 40, func(text string, _ int) string { return text })[0])

	picker.RefreshOptions(pickerOptions("alpha"), nil)
	if picker.Input() != "a" {
		t.Errorf("refresh dropped the query: %q", picker.Input())
	}
	widthAfter := len(picker.Popup(styler, 200, 40, func(text string, _ int) string { return text })[0])
	if widthAfter < widthBefore {
		t.Errorf("a refresh shrank the box from %d to %d", widthBefore, widthAfter)
	}
}

func TestPickerNoMatchesKeepGeometryAndRecoverToTheTop(t *testing.T) {
	picker := NewChoicePicker(ChoicePickerOptions{Title: "t", Options: pickerOptions("a", "b")})
	picker.Move(1)
	picker.HandleKey("z")
	if len(picker.Results()) != 0 {
		t.Fatal("an unmatched query still matched")
	}
	if picker.CursorIndex() != 0 {
		t.Errorf("the cursor did not recover to the top: %d", picker.CursorIndex())
	}
	if got := picker.HandleKey("\r").Kind; got != PickerHandled {
		t.Errorf("return on an empty result set produced %q, want a no-op", got)
	}
}

func TestPickerHitSelectsTheOptionRowThePaintProduced(t *testing.T) {
	styler := testStyler()
	picker := NewChoicePicker(ChoicePickerOptions{Title: "t", Options: pickerOptions("a", "b", "c")})
	picker.Popup(styler, 60, 20, func(text string, _ int) string { return text })
	// Options start on row 3: top border, query, blank, then the first option.
	result := picker.Hit(4)
	if result.Kind != PickerAccepted || len(result.IDs) != 1 || result.IDs[0] != "b" {
		t.Errorf("clicking the second option row produced %v", result)
	}
}

func TestPickerPopupAdaptsToEveryNarrowRectangle(t *testing.T) {
	styler := testStyler()
	for width := 1; width <= 60; width += 7 {
		for height := 1; height <= 12; height++ {
			picker := NewChoicePicker(ChoicePickerOptions{Title: "t", Options: pickerOptions("a", "b")})
			lines := picker.Popup(styler, width, height, func(text string, _ int) string { return text })
			if len(lines) > height {
				t.Fatalf("%dx%d produced %d lines", width, height, len(lines))
			}
			for _, line := range lines {
				if styler.Width(line) > width {
					t.Fatalf("%dx%d produced a %d-cell line: %q",
						width, height, styler.Width(line), ansi.Strip(line))
				}
			}
		}
	}
}

// -- the action palette ------------------------------------------------------------

func actionEntries() []shortcuts.Entry {
	entries, _ := shortcuts.Entries(shortcuts.List, false)
	kept := []shortcuts.Entry{}
	for _, entry := range entries {
		if entry.Handler != "" {
			kept = append(kept, entry)
		}
	}
	return kept
}

func TestActionPaletteSearchesDescriptionKeyAndHandler(t *testing.T) {
	palette := NewActionPalette(PlainStyler{}, actionEntries(), ReturnList, "")
	for _, key := range strings.Split("archive", "") {
		palette.HandleKey(key)
	}
	results := palette.Picker().Results()
	if len(results) == 0 {
		t.Fatal("searching a description matched nothing")
	}
	if results[0].ID != "archive_sweep" {
		t.Errorf("the best match is %q, want archive_sweep", results[0].ID)
	}
}

// The `@` in a description or key must NOT be normalized away here: that
// normalization belongs to the CONTEXT palette, where the sigil is noise.
func TestActionPaletteDoesNotNormalizeAtSignQueries(t *testing.T) {
	palette := NewActionPalette(PlainStyler{}, actionEntries(), ReturnList, "")
	palette.HandleKey("@")
	for _, option := range palette.Picker().Results() {
		if !strings.Contains(strings.ToLower(option.Label), "@") &&
			!strings.Contains(option.ID, "@") {
			t.Errorf("an @ query matched %q, which contains no @", option.ID)
		}
	}
}

func TestActionPaletteReturnExecutesAndEscapeCancels(t *testing.T) {
	palette := NewActionPalette(PlainStyler{}, actionEntries(), ReturnList, "task-1")
	outcome := palette.HandleKey("\r")
	if !outcome.Execute || outcome.Entry.Handler == "" {
		t.Fatalf("return produced %v", outcome)
	}
	if got := palette.HandleKey("\x1b").Kind; got != PickerCancelled {
		t.Errorf("escape produced %q", got)
	}
}

func TestActionPaletteRecordsAFailureWithoutLosingState(t *testing.T) {
	palette := NewActionPalette(PlainStyler{}, actionEntries(), ReturnList, "")
	palette.HandleKey("a")
	palette.Fail("that action failed")
	if palette.Picker().Error() != "that action failed" {
		t.Errorf("the failure did not stick: %q", palette.Picker().Error())
	}
	if palette.Picker().Input() != "a" {
		t.Errorf("the failure cleared the query: %q", palette.Picker().Input())
	}
}

// -- the context palette --------------------------------------------------------------

func TestContextPaletteClearRowIsFirstAndContextsAreNormalized(t *testing.T) {
	palette := NewContextPalette([]string{"home", "@work", "@home", " "}, nil)
	options := palette.Picker().Options()
	if options[0].ID != clearContextsID {
		t.Fatalf("the first row is %q, want the clear command", options[0].ID)
	}
	got := resultIDs(options[1:])
	if strings.Join(got, ",") != "@home,@work" {
		t.Errorf("contexts %v, want normalized, unique and sorted", got)
	}
}

func TestContextPaletteMarksActiveContextsAndParksTheCursorOnTheFirst(t *testing.T) {
	palette := NewContextPalette([]string{"@home", "@work"}, []string{"@work"})
	if !palette.Picker().Selected("@work") {
		t.Error("the active context is not checked")
	}
	if current := palette.Picker().Current(); current == nil || current.ID != "@work" {
		t.Errorf("the cursor is on %v, want the first active context", current)
	}
}

// Typing a query and pressing Return applies JUST the context under the cursor.
// Toggling with space first — an explicit multi-select gesture — keeps the
// staged set instead.
func TestContextPaletteOneKeyApplyVersusStagedApply(t *testing.T) {
	palette := NewContextPalette([]string{"@home", "@work"}, nil)
	palette.HandleKey("h")
	outcome := palette.HandleKey("\r")
	if !outcome.Apply || strings.Join(outcome.Contexts, ",") != "@home" {
		t.Fatalf("a searched Return applied %v", outcome.Contexts)
	}

	// The clear command is row 0, so the two contexts are rows 1 and 2.
	staged := NewContextPalette([]string{"@home", "@work"}, nil)
	staged.Move(1)
	staged.HandleKey(" ")
	staged.Move(1)
	staged.HandleKey(" ")
	stagedOutcome := staged.HandleKey("\r")
	if strings.Join(stagedOutcome.Contexts, ",") != "@home,@work" {
		t.Errorf("a staged Return applied %v", stagedOutcome.Contexts)
	}
}

func TestContextPaletteClearCommandStagesEmptyAndAppliesEmpty(t *testing.T) {
	palette := NewContextPalette([]string{"@home"}, []string{"@home"})
	// The clear row is first, so the cursor has to reach it explicitly.
	palette.Move(-10)
	palette.HandleKey(" ")
	if len(palette.Picker().Staged()) != 0 {
		t.Fatalf("the clear command left %v staged", palette.Picker().Staged())
	}
	outcome := palette.HandleKey("\r")
	if !outcome.Apply || len(outcome.Contexts) != 0 {
		t.Errorf("clearing applied %v, want nothing", outcome.Contexts)
	}
}

func TestContextPaletteSearchIgnoresTheSigil(t *testing.T) {
	palette := NewContextPalette([]string{"@home", "@work"}, nil)
	palette.HandleKey("h")
	results := palette.Picker().Results()
	if len(results) == 0 || results[0].ID != "@home" {
		t.Errorf("searching \"h\" ranked %v first", results)
	}
}

func TestContextPaletteRefreshPreservesStagedChoices(t *testing.T) {
	palette := NewContextPalette([]string{"@home", "@work"}, nil)
	palette.HandleKey(" ")
	staged := palette.Picker().Staged()
	palette.RefreshOptions([]string{"@home", "@work", "@errand"}, nil)
	if strings.Join(palette.Picker().Staged(), ",") != strings.Join(staged, ",") {
		t.Errorf("an external reload lost the staged set: %v", palette.Picker().Staged())
	}
}

func TestContextPaletteEmptyResultsRefuseReturn(t *testing.T) {
	palette := NewContextPalette([]string{"@home"}, nil)
	for _, key := range strings.Split("zzz", "") {
		palette.HandleKey(key)
	}
	if got := palette.HandleKey("\r"); got.Apply {
		t.Errorf("Return on an empty result set applied %v", got.Contexts)
	}
}
