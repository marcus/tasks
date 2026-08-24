package termform

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/temporal"
)

// -- text fields -------------------------------------------------------------------

func TestInputEditsByGraphemeNotByByte(t *testing.T) {
	field := NewInput(NewBase("value", "Value", ""))
	for _, key := range []string{"界", "é", "🙂"} {
		field.HandleRawKey(key)
	}
	if field.Cursor() != 3 {
		t.Fatalf("cursor %d after three clusters, want 3", field.Cursor())
	}
	field.HandleRawKey("\x7f")
	if field.Text() != "界é" {
		t.Errorf("backspace removed part of a cluster: %q", field.Text())
	}
}

func TestInputSanitizesPastedLineBreaks(t *testing.T) {
	field := NewInput(NewBase("value", "Value", ""))
	field.Paste("one\ntwo\tthree")
	if field.Text() != "one two three" {
		t.Errorf("a single-line field kept a line break: %q", field.Text())
	}
}

func TestInputScrollsHorizontallyToKeepTheCaretVisible(t *testing.T) {
	field := NewInput(NewBase("value", "Value", strings.Repeat("ab", 20)))
	view := field.Render(10)
	if len(view.Lines) != 1 || len(view.Lines[0]) == 0 {
		t.Fatalf("render produced %v", view.Lines)
	}
	if view.CursorColumn < 0 || view.CursorColumn > 10 {
		t.Errorf("the caret is off screen at column %d", view.CursorColumn)
	}
	if view.VirtualCursorColumn != 40 {
		t.Errorf("the virtual caret is at %d, want the end of the value", view.VirtualCursorColumn)
	}
}

func TestInputRenderNeverSplitsAWideGrapheme(t *testing.T) {
	field := NewInput(NewBase("value", "Value", "界界界界"))
	for width := 1; width <= 10; width++ {
		view := field.Render(width)
		if got := cellWidth(view.Lines[0]); got > width {
			t.Errorf("width %d produced a %d-cell line %q", width, got, view.Lines[0])
		}
	}
}

func TestTextAreaKeepsNewlinesAndMovesVertically(t *testing.T) {
	field := NewTextArea(NewBase("note", "Note", "alpha\nbeta"))
	field.Render(20, 4)
	field.editor.SetCursor(0)
	if result := field.HandleEvent(KeyEvent("down"), "alpha\nbeta", Context{}); result == nil {
		t.Fatal("down did not move in a text area")
	}
	if field.Cursor() != 6 {
		t.Errorf("down landed on offset %d, want the start of the second line", field.Cursor())
	}
}

func TestTextAreaReturnInsertsANewlineRatherThanCommitting(t *testing.T) {
	field := NewTextArea(NewBase("note", "Note", "alpha"))
	result := field.HandleEvent(Event{Type: EventCommit}, "alpha", Context{})
	if result == nil || result.Status != Changed {
		t.Fatalf("return produced %v", result)
	}
	if !strings.Contains(field.Text(), "\n") {
		t.Errorf("return did not insert a newline: %q", field.Text())
	}
}

// Entering a note shows it from the TOP. The cursor after a sync sits at
// end-of-buffer, which would otherwise scroll a long note so only its last
// lines are visible.
func TestTextAreaFocusShowsALongValueFromTheTop(t *testing.T) {
	field := NewTextArea(NewBase("note", "Note", strings.Repeat("line\n", 30)))
	field.FocusGained(nil, Context{})
	if field.Cursor() != 0 {
		t.Errorf("focus parked the caret at %d, want the top", field.Cursor())
	}
}

// A deliberately placed mid-buffer caret survives a blur and refocus, because
// SyncValue leaves an unchanged buffer untouched.
func TestTextAreaKeepsAMidBufferCaretAcrossAnUnchangedSync(t *testing.T) {
	field := NewTextArea(NewBase("note", "Note", "alpha\nbeta"))
	field.editor.SetCursor(3)
	field.SyncValue("alpha\nbeta")
	if field.Cursor() != 3 {
		t.Errorf("an unchanged sync moved the caret to %d", field.Cursor())
	}
}

func TestTextAreaWrapsAndReportsTheCaretRow(t *testing.T) {
	field := NewTextArea(NewBase("note", "Note", "aaaaaaaaaa bbbbbbbbbb"))
	view := field.Render(10, 4)
	if len(view.Lines) != 4 {
		t.Fatalf("render produced %d lines, want the requested height", len(view.Lines))
	}
	if view.VirtualCursorRow == 0 {
		t.Error("the caret at the end of a wrapped value is still reported on row 0")
	}
}

// -- choice fields ------------------------------------------------------------------

func selectField(searchable bool) *Select {
	return NewSelect(NewBase("state", "State", "TODO"), func(Context) []Option {
		return []Option{NewOption("TODO", ""), NewOption("NEXT", ""), NewOption("DONE", "")}
	}, searchable)
}

func TestSelectReturnOpensThenPicks(t *testing.T) {
	field := selectField(false)
	if result := field.HandleEvent(Event{Type: EventCommit}, "TODO", Context{}); result == nil ||
		result.Status != Handled {
		t.Fatalf("the first return produced %v", result)
	}
	if !field.Open() {
		t.Fatal("return did not open the option list")
	}
	field.HandleEvent(KeyEvent("down"), "TODO", Context{})
	result := field.HandleEvent(Event{Type: EventCommit}, "TODO", Context{})
	if result == nil || result.Status != Changed || result.Value != "NEXT" {
		t.Fatalf("the second return produced %v", result)
	}
	if field.Open() {
		t.Error("picking did not close the list")
	}
}

func TestSelectEscapeClosesWithoutChanging(t *testing.T) {
	field := selectField(false)
	field.HandleEvent(Event{Type: EventCommit}, "TODO", Context{})
	result := field.HandleEvent(Event{Type: EventCancel}, "TODO", Context{})
	if result == nil || result.Status != Handled || result.Value != "TODO" {
		t.Fatalf("escape produced %v", result)
	}
	if field.Open() {
		t.Error("escape did not close the list")
	}
}

func TestSelectHighlightWraps(t *testing.T) {
	field := selectField(false)
	field.HandleEvent(KeyEvent("up"), "TODO", Context{})
	if field.HighlightIndex() != 2 {
		t.Errorf("up from the top landed on %d, want the last option", field.HighlightIndex())
	}
}

func TestSelectValidationCatchesAVanishedSelection(t *testing.T) {
	field := NewSelect(NewBase("state", "State", "GONE"), func(Context) []Option {
		return []Option{NewOption("TODO", "")}
	}, false)
	errs := field.ValidationErrors("GONE", Context{Values: map[string]any{"state": "GONE"}})
	if len(errs) == 0 || !strings.Contains(errs[0], "no longer available") {
		t.Errorf("a vanished selection produced %v", errs)
	}
}

func multiField(creatable bool) *MultiSelect {
	return NewMultiSelect(NewBase("contexts", "Contexts", []string{"@home"}),
		func(Context) []Option { return []Option{NewOption("@home", ""), NewOption("@work", "")} },
		true, creatable, func(token string) string {
			token = strings.TrimSpace(token)
			if token == "" {
				return ""
			}
			if !strings.HasPrefix(token, "@") {
				return "@" + token
			}
			return token
		})
}

func TestMultiSelectNormalizesTrimsAndDeduplicates(t *testing.T) {
	field := multiField(true)
	got, _ := field.NormalizeValue([]string{"home", "@home", " ", "work"}).([]string)
	if strings.Join(got, ",") != "@home,@work" {
		t.Errorf("normalized to %v", got)
	}
}

// Backspace on an empty query pops the last token. It is the one gesture that
// makes a token list feel like text rather than a form control.
func TestMultiSelectBackspacePopsTheLastToken(t *testing.T) {
	field := multiField(true)
	result := field.HandleEvent(KeyEvent("backspace"), []string{"@home", "@work"}, Context{})
	if result == nil || result.Status != Changed {
		t.Fatalf("backspace produced %v", result)
	}
	got, _ := result.Value.([]string)
	if strings.Join(got, ",") != "@home" {
		t.Errorf("backspace left %v", got)
	}
}

func TestMultiSelectCreatesANewTokenFromTheQuery(t *testing.T) {
	field := multiField(true)
	for _, key := range strings.Split("errand", "") {
		field.HandleEvent(Event{Type: EventInput, Text: key}, []string{}, Context{})
	}
	result := field.HandleEvent(Event{Type: EventCommit}, []string{}, Context{})
	if result == nil || result.Status != Changed {
		t.Fatalf("return produced %v", result)
	}
	got, _ := result.Value.([]string)
	if strings.Join(got, ",") != "@errand" {
		t.Errorf("creating produced %v", got)
	}
}

func TestMultiSelectRefusesAnUnavailableTokenWhenNotCreatable(t *testing.T) {
	field := multiField(false)
	errs := field.ValidationErrors([]string{"@nowhere"}, Context{})
	if len(errs) == 0 || !strings.Contains(errs[0], "no longer available") {
		t.Errorf("an unknown token produced %v", errs)
	}
}

func TestConfirmAcceptsTheObviousKeys(t *testing.T) {
	field := NewConfirm(NewBase("deferred", "On hold", false), "Yes", "No")
	for _, key := range []string{"y", "\x1b[C"} {
		result := field.HandleEvent(Event{Type: EventKey, Key: key, Raw: key}, false, Context{})
		if result == nil || result.Value != true {
			t.Errorf("%q produced %v", key, result)
		}
	}
	if result := field.HandleEvent(Event{Type: EventKey, Key: "n", Raw: "n"}, true, Context{}); result == nil ||
		result.Value != false {
		t.Errorf("n produced %v", result)
	}
	if result := field.HandleEvent(Event{Type: EventCommit}, false, Context{}); result == nil ||
		result.Value != true {
		t.Errorf("return did not toggle: %v", result)
	}
}

// -- the date field -----------------------------------------------------------------

func isoHooks() DateHooks {
	return DateHooks{
		Parse: func(text string, _ temporal.Date) (any, error) {
			date, ok := temporal.ParseDate(text)
			if !ok {
				return nil, errors.New("could not understand that date")
			}
			return &date, nil
		},
		Format: func(value any) string {
			date, ok := value.(*temporal.Date)
			if !ok || date == nil {
				return ""
			}
			return date.ISO()
		},
		Parsed: func(value any) bool {
			date, ok := value.(*temporal.Date)
			return ok && date != nil
		},
		DateOf: func(value any) (temporal.Date, bool) {
			if date, ok := value.(*temporal.Date); ok && date != nil {
				return *date, true
			}
			return temporal.Date{}, false
		},
		WithDate: func(date temporal.Date, _ any) any { return &date },
	}
}

func dateField(value any) *DateInput {
	today := func() temporal.Date { return temporal.Date{Year: 2026, Month: 7, Day: 14} }
	return NewDateInput(NewBase("deadline", "Deadline", value), isoHooks(), today, nil, true)
}

func TestDateInputParsesTypedTextIntoAValue(t *testing.T) {
	field := dateField(nil)
	for _, key := range strings.Split("2026-08-09", "") {
		field.HandleEvent(Event{Type: EventInput, Text: key}, nil, Context{})
	}
	result := field.HandleEvent(Event{Type: EventInput, Text: ""}, nil, Context{})
	_ = result
	value := field.NormalizeValue(field.Text())
	if !isoHooks().Parsed(value) {
		t.Fatalf("the typed date did not parse: %v", value)
	}
}

func TestDateInputLeavesUnparseableTextAsTextAndSaysSo(t *testing.T) {
	field := dateField(nil)
	value := field.NormalizeValue("not a date")
	if _, isText := value.(string); !isText {
		t.Fatalf("unparseable input became %T, want the text the user typed", value)
	}
	errs := field.ValidationErrors(value, Context{})
	if len(errs) == 0 {
		t.Error("unparseable input passed validation")
	}
}

func TestDateInputReturnOpensACalendarAndEscapeClosesIt(t *testing.T) {
	field := dateField(nil)
	field.HandleEvent(Event{Type: EventCommit}, nil, Context{})
	if !field.PickerOpen() {
		t.Fatal("return did not open the calendar")
	}
	if field.CursorFor(nil, Context{}) != nil {
		t.Error("the text caret is still shown while the calendar owns the keyboard")
	}
	field.HandleEvent(Event{Type: EventCancel}, nil, Context{})
	if field.PickerOpen() {
		t.Error("escape did not close the calendar")
	}
}

func TestCalendarArrowsMoveByDayAndWeekAndPagesByMonth(t *testing.T) {
	field := dateField(nil)
	field.HandleEvent(Event{Type: EventCommit}, nil, Context{})
	start, _ := field.PickerDate()

	field.HandleEvent(Event{Type: EventKey, Key: "\x1b[C", Raw: "\x1b[C"}, nil, Context{})
	if got, _ := field.PickerDate(); got != start.AddDays(1) {
		t.Errorf("right moved to %v, want one day on", got)
	}
	field.HandleEvent(Event{Type: EventKey, Key: "\x1b[B", Raw: "\x1b[B"}, nil, Context{})
	if got, _ := field.PickerDate(); got != start.AddDays(8) {
		t.Errorf("down moved to %v, want one week on", got)
	}
	field.HandleEvent(Event{Type: EventKey, Key: "\x1b[6~", Raw: "\x1b[6~"}, nil, Context{})
	if got, _ := field.PickerDate(); got.Month != start.Month+1 {
		t.Errorf("page-down moved to %v, want the next month", got)
	}
}

func TestCalendarReturnAdoptsTheHighlightedDay(t *testing.T) {
	field := dateField(nil)
	field.HandleEvent(Event{Type: EventCommit}, nil, Context{})
	field.HandleEvent(Event{Type: EventKey, Key: "\x1b[C", Raw: "\x1b[C"}, nil, Context{})
	result := field.HandleEvent(Event{Type: EventCommit}, nil, Context{})
	if result == nil || result.Status != Changed {
		t.Fatalf("return in the calendar produced %v", result)
	}
	if field.Text() != "2026-07-15" {
		t.Errorf("the buffer reads %q after picking", field.Text())
	}
}

func TestCalendarGridStartsOnMondayAndCoversSixWeeks(t *testing.T) {
	calendar := CalendarFor(temporal.Date{Year: 2026, Month: 7, Day: 14})
	if len(calendar.Weeks) != 6 {
		t.Fatalf("the grid has %d weeks, want 6", len(calendar.Weeks))
	}
	if got := calendar.Weeks[0][0].Weekday().String(); got != "Monday" {
		t.Errorf("the grid starts on %s", got)
	}
	if calendar.WeekdayLabels[0] != "Mo" {
		t.Errorf("the labels start with %q", calendar.WeekdayLabels[0])
	}
}

// -- the required boundary ------------------------------------------------------------

// "is required" comes FIRST, before the field's own validators, so an empty
// required field says the obvious thing rather than a parse complaint about "".
func TestRequiredIsReportedBeforeAFieldsOwnValidators(t *testing.T) {
	base := NewBase("title", "Title", "")
	base.RequiredFixed = true
	base.Validate = []func(any, Context) string{
		func(any, Context) string { return "and also this" },
	}
	field := NewInput(base)
	errs := field.ValidationErrors("", Context{})
	if len(errs) != 2 || errs[0] != "is required" {
		t.Errorf("errors %v, want the required check first", errs)
	}
}

func TestRequiredTreatsTheEmptyShapesAsBlank(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"empty string", ""},
		{"empty list", []string{}},
	}
	for _, testCase := range cases {
		base := NewBase("f", "F", testCase.value)
		base.RequiredFixed = true
		field := NewInput(base)
		if errs := field.ValidationErrors(testCase.value, Context{}); len(errs) == 0 {
			t.Errorf("%s passed a required check", testCase.name)
		}
	}
}

func TestRequiredAcceptsAWhitespaceOnlyValueSoTheFieldCanRefuseItSelf(t *testing.T) {
	// Ruby's `blank?` is `nil? || empty?` — it does NOT strip. A field that
	// wants to reject "   " says so with its own validator, which is what the
	// task title does. Pinning it here keeps the two layers from both
	// half-owning the rule.
	base := NewBase("title", "Title", " ")
	base.RequiredFixed = true
	field := NewInput(base)
	if errs := field.ValidationErrors(" ", Context{}); len(errs) != 0 {
		t.Errorf("the required check stripped: %v", errs)
	}
}

// A hidden or disabled field is deliberately NOT validated: refusing a save
// because of a rule the user cannot see or reach is a dead end.
func TestValidationSkipsUnreachableFields(t *testing.T) {
	visible := NewInput(NewBase("visible", "Visible", "ok"))
	hiddenBase := NewBase("hidden", "Hidden", "")
	hiddenBase.RequiredFixed = true
	hiddenBase.VisibleFixed = false
	hidden := NewInput(hiddenBase)
	form, err := NewForm([]Group{NewGroup("g", "G", visible, hidden)}, "visible", nil)
	if err != nil {
		t.Fatal(err)
	}
	if errs := form.Validate(); len(errs) != 0 {
		t.Errorf("an unreachable required field blocked the form: %v", errs)
	}
}

func cellWidth(text string) int {
	total := 0
	for _, gc := range Graphemes(text) {
		total += ClusterWidth(gc)
	}
	return total
}

// An absolute scroll has to land on the row it was given in BOTH directions.
// Scrolling in a text area is caret motion, and Render derives the window from
// whichever edge the caret crossed, so parking the caret on the bottom edge
// unconditionally landed every upward jump height-1 rows low — and landed there
// permanently, because the next identical gesture then computed a delta of
// zero. The note's scrollbar could not reach the top of a note.
func TestTextAreaScrollToRowLandsExactlyInBothDirections(t *testing.T) {
	const width, height = 24, 4
	text := strings.TrimRight(strings.Repeat("a wrapped line of text\n", 12), "\n")

	area := NewTextArea(Base{FieldKey: "note", Value: text})
	area.Render(width, height)
	area.ScrollLines(len(strings.Split(text, "\n")) * 2) // park at the bottom
	area.Render(width, height)
	bottom := area.rowOffset
	if bottom == 0 {
		t.Fatal("the fixture never scrolled, so nothing below is being tested")
	}

	// Upward, to the very top, and idempotent once there.
	for attempt := 1; attempt <= 3; attempt++ {
		area.ScrollToRow(0)
		area.Render(width, height)
		if area.rowOffset != 0 {
			t.Fatalf("attempt %d: ScrollToRow(0) from %d landed at %d, want 0",
				attempt, bottom, area.rowOffset)
		}
	}

	// Downward still lands exactly.
	area.ScrollToRow(5)
	area.Render(width, height)
	if area.rowOffset != 5 {
		t.Fatalf("downward ScrollToRow(5) landed at %d, want 5", area.rowOffset)
	}

	// And upward again from a mid-buffer position, not just from the bottom.
	area.ScrollToRow(2)
	area.Render(width, height)
	if area.rowOffset != 2 {
		t.Fatalf("upward ScrollToRow(2) from 5 landed at %d, want 2", area.rowOffset)
	}
}
