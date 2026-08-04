package tui

import (
	"strings"
	"testing"
	"time"

	"tasks-go/internal/temporal"
	"tasks-go/internal/tui/termform"
)

// The structured control is the affordance a typed value cannot replace: it is
// the only surface that SHOWS a user that a mode, a zone and a fold exist.

func berlinContext(t *testing.T) temporal.Context {
	t.Helper()
	context, err := temporal.NewContext(
		time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), "Europe/Berlin", 24)
	if err != nil {
		t.Fatal(err)
	}
	return context
}

func temporalField(t *testing.T, value any, context temporal.Context) *TemporalInput {
	t.Helper()
	today := func() temporal.Date { return temporal.Date{Year: 2026, Month: 7, Day: 14} }
	edit := &TaskEditForm{today: today, context: context}
	field := edit.temporalField("deadline", "Deadline", value).(*TemporalInput)
	return field
}

func openControl(t *testing.T, field *TemporalInput, value any) {
	t.Helper()
	field.HandleEvent(termform.Event{Type: termform.EventCommit}, value, termform.Context{})
	if !field.ControlOpen() {
		t.Fatal("return did not open the structured control")
	}
}

func TestControlOpensOnReturnAndClosesOnEscape(t *testing.T) {
	field := temporalField(t, nil, berlinContext(t))
	openControl(t, field, nil)
	if field.CursorFor(nil, termform.Context{}) != nil {
		t.Error("the text caret is still shown while the control owns the keyboard")
	}
	field.HandleEvent(termform.Event{Type: termform.EventCancel}, nil, termform.Context{})
	if field.ControlOpen() {
		t.Error("escape did not close the control")
	}
}

func TestControlRowsAppearOnlyWhenTheyApply(t *testing.T) {
	context := berlinContext(t)

	allDay := temporalField(t, nil, context)
	openControl(t, allDay, nil)
	if rows := allDay.visibleRows(allDay.Value()); len(rows) != 3 {
		t.Errorf("an all-day value shows %v; zone and fold do not apply", rows)
	}

}

func TestTemporalInputValueReturnsADefensiveCopy(t *testing.T) {
	value := &temporal.Value{Date: temporal.Date{Year: 2026, Month: 7, Day: 14}}
	field := temporalField(t, value, berlinContext(t))
	openControl(t, field, value)
	first := field.Value()
	first.Date.Day = 1
	if got := field.Value().Date.Day; got != 14 {
		t.Fatalf("mutating Value() changed the field-owned date to %d", got)
	}
}

func TestControlZoneRowAppearsOnlyForAFixedValue(t *testing.T) {
	context := berlinContext(t)
	field := temporalField(t, nil, context)
	openControl(t, field, nil)

	// all-day → floating → fixed
	field.adjust(temporalRowMode, 1, nil)
	if hasRow(field, temporalRowZone) {
		t.Error("a floating value offers a zone row it does not have")
	}
	field.adjust(temporalRowMode, 1, nil)
	if !hasRow(field, temporalRowZone) {
		t.Error("a fixed value does not offer its zone row")
	}
	if got := field.Value().Timezone; got != "Europe/Berlin" {
		t.Errorf("fixing adopted zone %q, want the configured one", got)
	}
}

// The fold row exists only where a wall time is genuinely ambiguous. Offering it
// otherwise would offer a choice with no second option.
func TestControlFoldRowAppearsOnlyForAnAmbiguousLocalTime(t *testing.T) {
	context := berlinContext(t)

	// 2026-10-25 02:30 in Berlin happens twice: the DST fall-back repeats it.
	ambiguous := &temporal.Value{
		Date:      temporal.Date{Year: 2026, Month: 10, Day: 25},
		LocalTime: "02:30", Timezone: "Europe/Berlin",
	}
	field := temporalField(t, ambiguous, context)
	openControl(t, field, ambiguous)
	if !hasRow(field, temporalRowFold) {
		t.Fatalf("an ambiguous time offers no fold row: %v", field.visibleRows(field.Value()))
	}

	plain := &temporal.Value{
		Date:      temporal.Date{Year: 2026, Month: 7, Day: 14},
		LocalTime: "09:00", Timezone: "Europe/Berlin",
	}
	unambiguous := temporalField(t, plain, context)
	openControl(t, unambiguous, plain)
	if hasRow(unambiguous, temporalRowFold) {
		t.Error("an unambiguous time offers a fold row with nothing to choose")
	}
}

func TestControlFoldRowPicksTheSecondInstant(t *testing.T) {
	context := berlinContext(t)
	ambiguous := &temporal.Value{
		Date:      temporal.Date{Year: 2026, Month: 10, Day: 25},
		LocalTime: "02:30", Timezone: "Europe/Berlin",
	}
	field := temporalField(t, ambiguous, context)
	openControl(t, field, ambiguous)
	field.adjust(temporalRowFold, 1, ambiguous)
	if field.Value().Fold != 1 {
		t.Errorf("the fold row did not select the later instant: %d", field.Value().Fold)
	}
	if !strings.Contains(field.Text(), "fold=later") {
		t.Errorf("the buffer does not carry the fold: %q", field.Text())
	}
}

// Stepping the clock FORWARD into a spring-forward gap lands on the first valid
// local time after it, because the clock in that zone genuinely skips.
func TestControlTimeStepJumpsTheSpringForwardGap(t *testing.T) {
	context := berlinContext(t)
	// Berlin springs forward on 2026-03-29: 02:00 → 03:00, so 02:00–02:59 does
	// not exist. Stepping up from 01:45 must not land on 02:00.
	before := &temporal.Value{
		Date:      temporal.Date{Year: 2026, Month: 3, Day: 29},
		LocalTime: "01:45", Timezone: "Europe/Berlin",
	}
	field := temporalField(t, before, context)
	openControl(t, field, before)
	field.adjust(temporalRowTime, 1, before)
	got := field.Value().LocalTime
	if got == "02:00" {
		t.Fatal("the step landed inside the gap, on a time that does not exist")
	}
	if got != "03:00" {
		t.Errorf("the step landed on %q, want the first valid local time after the gap", got)
	}
}

func TestControlTimeStepRefusesToWalkBackwardsIntoTheGap(t *testing.T) {
	context := berlinContext(t)
	after := &temporal.Value{
		Date:      temporal.Date{Year: 2026, Month: 3, Day: 29},
		LocalTime: "03:00", Timezone: "Europe/Berlin",
	}
	field := temporalField(t, after, context)
	openControl(t, field, after)
	field.adjust(temporalRowTime, -1, after)
	if got := field.Value().LocalTime; got == "02:45" {
		t.Error("a backward step landed inside the gap")
	}
	if field.parseError == "" {
		t.Error("the refused step said nothing")
	}
}

func TestControlTimeStepsByFifteenMinutesAndWrapsTheDay(t *testing.T) {
	context := berlinContext(t)
	value := &temporal.Value{
		Date: temporal.Date{Year: 2026, Month: 7, Day: 14}, LocalTime: "09:00",
	}
	field := temporalField(t, value, context)
	openControl(t, field, value)
	field.adjust(temporalRowTime, 1, value)
	if got := field.Value().LocalTime; got != "09:15" {
		t.Errorf("one step produced %q", got)
	}
	for range 4 * 24 {
		field.adjust(temporalRowTime, 1, field.Value())
	}
	if got := field.Value().LocalTime; got != "09:15" {
		t.Errorf("a full day of steps produced %q, want a wrap back", got)
	}
}

func TestControlDateRowStepsAndOpensTheCalendar(t *testing.T) {
	field := temporalField(t, nil, berlinContext(t))
	openControl(t, field, nil)
	start := field.Value().Date
	field.adjust(temporalRowDate, 1, nil)
	if got := field.Value().Date; got != start.AddDays(1) {
		t.Errorf("the date row stepped to %v", got)
	}
	field.activate(temporalRowDate, nil)
	if field.CalendarDate() == nil {
		t.Fatal("return on the date row did not open the calendar")
	}
	field.HandleEvent(termform.Event{Type: termform.EventKey, Key: "\x1b[C", Raw: "\x1b[C"},
		nil, termform.Context{})
	field.HandleEvent(termform.Event{Type: termform.EventCommit}, nil, termform.Context{})
	if field.CalendarDate() != nil {
		t.Error("picking did not close the calendar")
	}
	if got := field.Value().Date; got != start.AddDays(2) {
		t.Errorf("the calendar pick landed on %v", got)
	}
}

func TestControlZoneSearchFiltersAndSelects(t *testing.T) {
	context := berlinContext(t)
	value := &temporal.Value{
		Date:      temporal.Date{Year: 2026, Month: 7, Day: 14},
		LocalTime: "09:00", Timezone: "Europe/Berlin",
	}
	field := temporalField(t, value, context)
	openControl(t, field, value)
	field.activate(temporalRowZone, value)
	if field.ZoneSearch() == nil {
		t.Fatal("return on the zone row did not open the search")
	}
	for _, key := range strings.Split("Tokyo", "") {
		field.HandleEvent(termform.Event{Type: termform.EventKey, Key: key, Raw: key},
			value, termform.Context{})
	}
	matches := field.zoneMatches()
	if len(matches) == 0 || !strings.Contains(matches[0], "Tokyo") {
		t.Fatalf("the search matched %v", matches)
	}
	field.HandleEvent(termform.Event{Type: termform.EventCommit}, value, termform.Context{})
	if field.ZoneSearch() != nil {
		t.Error("selecting did not close the search")
	}
	if got := field.Value().Timezone; got != "Asia/Tokyo" {
		t.Errorf("the search selected %q", got)
	}
	if !strings.Contains(field.Text(), "Asia/Tokyo") {
		t.Errorf("the buffer does not carry the new zone: %q", field.Text())
	}
}

// Only identifiers the store will accept are offered: a slashless link like
// "Japan" would fail validation on save, so offering it offers a dead end.
func TestZoneIdentifiersAreAllStorable(t *testing.T) {
	identifiers := ZoneIdentifiers()
	if len(identifiers) < 5 {
		t.Fatalf("only %d zones were enumerated", len(identifiers))
	}
	for _, identifier := range identifiers {
		if identifier != "UTC" && !strings.Contains(identifier, "/") {
			t.Errorf("%q is not an Area/Location identifier", identifier)
		}
	}
}

// Escape closes the OVERLAY only. Arrow adjustments have already edited the
// field, so discarding the whole draft is the editor-level Escape's job.
func TestControlEscapeKeepsTheAdjustedValue(t *testing.T) {
	field := temporalField(t, nil, berlinContext(t))
	openControl(t, field, nil)
	field.adjust(temporalRowDate, 1, nil)
	adjusted := field.Text()
	field.HandleEvent(termform.Event{Type: termform.EventCancel}, nil, termform.Context{})
	if field.Text() != adjusted {
		t.Errorf("escape discarded the adjustment: %q, want %q", field.Text(), adjusted)
	}
}

// The whole point of the control is that it produces a value the STORE keeps.
func TestControlAdjustmentsReachTheStore(t *testing.T) {
	harness := newEditorHarness(t, fixFlight, "deadline")
	field := harness.editor.Form().Field("deadline").(*TemporalInput)
	harness.editor.HandleKey("\r") // opens the control
	if !field.ControlOpen() {
		t.Fatal("the control did not open")
	}
	// date +1, then mode → floating, then mode → fixed.
	harness.editor.HandleKey("\x1b[C")
	harness.editor.HandleKey("\x1b[B")
	harness.editor.HandleKey("\x1b[B")
	harness.editor.HandleKey("\x1b[C")
	harness.editor.HandleKey("\x1b[C")
	harness.editor.HandleKey("\x1b") // close the control, keeping the value
	if outcome := harness.editor.Save(); outcome.Status != EditorSaved {
		t.Fatalf("saving the stepped value produced %q: %s", outcome.Status, outcome.Message)
	}
	line := harness.line(fixFlight)
	if !strings.Contains(line, `"deadline":"2026-07-03"`) {
		t.Errorf("the stepped date did not land:\n%s", line)
	}
	if !strings.Contains(line, `"local":"09:00"`) {
		t.Errorf("the stepped wall time did not land:\n%s", line)
	}
}

func hasRow(field *TemporalInput, wanted temporalRow) bool {
	for _, row := range field.visibleRows(field.Value()) {
		if row == wanted {
			return true
		}
	}
	return false
}
