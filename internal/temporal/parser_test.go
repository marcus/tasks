package temporal

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/timezones"
)

func testContext(t *testing.T, zone string, now time.Time) Context {
	t.Helper()
	context, err := NewContext(now, zone, 12)
	if err != nil {
		t.Fatalf("NewContext(%q): %v", zone, err)
	}
	return context
}

// losAngelesAt is test_temporal.rb's default context: America/Los_Angeles,
// with 2026-07-20 16:00Z as "now" unless a case says otherwise.
func losAngelesAt(t *testing.T, now time.Time) Context {
	t.Helper()
	return testContext(t, "America/Los_Angeles", now)
}

func mustParse(t *testing.T, expression string, options ParseOptions) Value {
	t.Helper()
	value, err := ParseExpression(expression, options)
	if err != nil {
		t.Fatalf("ParseExpression(%q): %v", expression, err)
	}
	return value
}

func parserOptions() ParseOptions { return ParseOptions{Today: datesToday, Order: MDY} }

func TestParserPreservesAllDayAndAcceptsBoundedTimes(t *testing.T) {
	allDay := mustParse(t, "tomorrow", parserOptions())
	if !allDay.AllDay() {
		t.Fatal(`"tomorrow" must stay all-day`)
	}
	if allDay.Date != mustDate(2026, 7, 2) {
		t.Fatalf(`"tomorrow" = %s, want 2026-07-02`, allDay.Date.ISO())
	}

	cases := map[string]string{
		"today 5pm":              "17:00",
		"tomorrow at 09:30":      "09:30",
		"fri noon":               "12:00",
		"2026-07-20T17:00":       "17:00",
		"2026-07-20 midnight":    "00:00",
		"2026-07-20 at 11:45 pm": "", // "pm" detached by a space is not a time token
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			value, err := ParseExpression(input, parserOptions())
			if want == "" {
				if err == nil {
					t.Fatalf("ParseExpression(%q) = %#v, want a refusal", input, value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseExpression(%q): %v", input, err)
			}
			if value.LocalTime != want {
				t.Fatalf("ParseExpression(%q).LocalTime = %q, want %q", input, value.LocalTime, want)
			}
		})
	}
}

// "fri 5" must not silently become 05:00; minutes may be omitted only when a
// meridiem disambiguates.
func TestParserRejectsABareDigitAsATime(t *testing.T) {
	for _, input := range []string{"tomorrow 5", "fri 17"} {
		if _, err := ParseExpression(input, parserOptions()); !errors.Is(err, ErrNotADate) {
			t.Fatalf("ParseExpression(%q) error = %v, want ErrNotADate", input, err)
		}
	}
	for _, input := range []string{"fri 5pm", "fri 17:00"} {
		if value := mustParse(t, input, parserOptions()); value.LocalTime != "17:00" {
			t.Fatalf("ParseExpression(%q).LocalTime = %q, want 17:00", input, value.LocalTime)
		}
	}
}

func TestParserThreadsDateOrderAndNewDateGrammarWithATime(t *testing.T) {
	options := parserOptions()
	mdy := mustParse(t, "8/1/2026 5pm", options)
	if mdy.Date != mustDate(2026, 8, 1) || mdy.LocalTime != "17:00" {
		t.Fatalf("mdy = %s %s, want 2026-08-01 17:00", mdy.Date.ISO(), mdy.LocalTime)
	}

	options.Order = DMY
	dmy := mustParse(t, "8/1/2026 5pm", options)
	if dmy.Date != mustDate(2026, 1, 8) || dmy.LocalTime != "17:00" {
		t.Fatalf("dmy = %s %s, want 2026-01-08 17:00", dmy.Date.ISO(), dmy.LocalTime)
	}

	nextMonth := mustParse(t, "next month at 9am", parserOptions())
	if nextMonth.Date != mustDate(2026, 8, 1) || nextMonth.LocalTime != "09:00" {
		t.Fatalf("next month at 9am = %s %s, want 2026-08-01 09:00", nextMonth.Date.ISO(), nextMonth.LocalTime)
	}
}

func TestParserAcceptsClockRelativePhrasesAtMinutePrecision(t *testing.T) {
	context := testContext(t, "Etc/UTC", time.Date(2026, 7, 20, 12, 0, 30, 0, time.UTC))
	options := ParseOptions{Today: context.LocalDate(), Order: MDY, Context: &context}
	cases := map[string]Value{
		"two seconds":           {Date: mustDate(2026, 7, 20), LocalTime: "12:01"},
		"in two minutes":        {Date: mustDate(2026, 7, 20), LocalTime: "12:03"},
		"two hours from now":    {Date: mustDate(2026, 7, 20), LocalTime: "14:01"},
		"in one thousand hours": {Date: mustDate(2026, 8, 31), LocalTime: "04:01"},
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got := mustParse(t, input, options)
			if !got.Equal(want) {
				t.Fatalf("ParseExpression(%q) = %#v, want %#v", input, got, want)
			}
		})
	}
}

func TestClockRelativePhraseUsesRequestedZone(t *testing.T) {
	context := testContext(t, "Etc/UTC", time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	options := ParseOptions{Today: context.LocalDate(), Order: MDY, Context: &context, Timezone: "Europe/London"}
	value := mustParse(t, "in two minutes", options)
	if value.Date != mustDate(2026, 7, 20) || value.LocalTime != "13:02" || value.Timezone != "Europe/London" {
		t.Fatalf("London relative value = %#v", value)
	}
}

func TestClockRelativePhraseKeepsTheExactSideOfADSTFold(t *testing.T) {
	context := testContext(t, "America/Los_Angeles", time.Date(2026, 11, 1, 8, 29, 0, 0, time.UTC))
	options := ParseOptions{Today: context.LocalDate(), Order: MDY, Context: &context}
	value := mustParse(t, "in sixty-two minutes", options)
	if value.Date != mustDate(2026, 11, 1) || value.LocalTime != "01:31" || value.Fold != 1 {
		t.Fatalf("fold-crossing relative value = %#v", value)
	}
	instant, err := value.Instant(context)
	if err != nil || !instant.Equal(time.Date(2026, 11, 1, 9, 31, 0, 0, time.UTC)) {
		t.Fatalf("fold-crossing instant = %s (%v)", instant, err)
	}
}

func TestClockRelativePhraseRequiresNow(t *testing.T) {
	if _, err := ParseExpression("in two minutes", parserOptions()); err == nil || !strings.Contains(err.Error(), "current time context") {
		t.Fatalf("missing-context error = %v", err)
	}
}

func TestClockRelativePhraseRejectsAFoldModifier(t *testing.T) {
	context := testContext(t, "America/Los_Angeles", time.Date(2026, 11, 1, 8, 20, 0, 0, time.UTC))
	options := ParseOptions{Today: context.LocalDate(), Order: MDY, Context: &context, Fold: 1, FoldSpecified: true}
	if _, err := ParseExpression("in ten minutes", options); err == nil || !strings.Contains(err.Error(), "already selects an exact instant") {
		t.Fatalf("clock-relative fold error = %v", err)
	}
}

func TestClockRelativePhraseValidatesFoldAndStoredDateRange(t *testing.T) {
	context := testContext(t, "Etc/UTC", time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	badFold := ParseOptions{Today: context.LocalDate(), Order: MDY, Context: &context, Fold: 2}
	if _, err := ParseExpression("in ten minutes", badFold); err == nil || !strings.Contains(err.Error(), "fold must be 0 or 1") {
		t.Fatalf("bad-fold error = %v", err)
	}

	endContext := testContext(t, "Etc/UTC", time.Date(9999, 12, 31, 23, 59, 0, 0, time.UTC))
	outOfRange := ParseOptions{Today: endContext.LocalDate(), Order: MDY, Context: &endContext}
	if _, err := ParseExpression("in two minutes", outOfRange); err == nil || !strings.Contains(err.Error(), "supported date range") {
		t.Fatalf("out-of-range error = %v", err)
	}
}

func TestParserRequiresATimeForFloatingFlag(t *testing.T) {
	options := parserOptions()
	options.Floating = true
	if _, err := ParseExpression("2026-07-20", options); err == nil {
		t.Fatal("a floating value with no time must be refused")
	}
	zoned := parserOptions()
	zoned.Timezone = "Etc/UTC"
	if _, err := ParseExpression("2026-07-20", zoned); err == nil {
		t.Fatal("a zoned value with no time must be refused")
	}
	folded := parserOptions()
	folded.Fold = 1
	if _, err := ParseExpression("2026-07-20", folded); err == nil {
		t.Fatal("a folded value using the legacy Fold convention with no time must be refused")
	}
	earlier := parserOptions()
	earlier.FoldSpecified = true
	if _, err := ParseExpression("2026-07-20", earlier); err == nil {
		t.Fatal("an explicitly earlier fold with no time must be refused")
	}
}

func TestParserRejectsBareTimeAndMutuallyExclusiveModes(t *testing.T) {
	if _, err := ParseExpression("9am", parserOptions()); !errors.Is(err, ErrNotADate) {
		t.Fatal("a bare time names no date")
	}
	options := parserOptions()
	options.Timezone = "Etc/UTC"
	options.Floating = true
	if _, err := ParseExpression("tomorrow 9am", options); err == nil {
		t.Fatal("--timezone and --floating must be mutually exclusive")
	}
}

func TestFloatingValueFollowsEvaluationZone(t *testing.T) {
	value := mustParse(t, "2026-07-20 9am", parserOptions())
	if !value.Floating() {
		t.Fatal("a time with no zone is floating")
	}
	cases := map[string]time.Time{
		"America/Los_Angeles": time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC),
		"Europe/London":       time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC),
	}
	for zone, want := range cases {
		instant, err := value.Instant(testContext(t, zone, want))
		if err != nil {
			t.Fatalf("Instant in %s: %v", zone, err)
		}
		if !instant.Equal(want) {
			t.Fatalf("Instant in %s = %s, want %s", zone, instant, want)
		}
	}
}

func TestFixedValueKeepsInstantWhenDisplayZoneChanges(t *testing.T) {
	options := parserOptions()
	options.Timezone = "Europe/London"
	value := mustParse(t, "2026-07-20 5pm", options)
	if !value.Fixed() {
		t.Fatal("a value naming a zone is fixed")
	}
	want := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	for _, zone := range []string{"Etc/UTC", "America/Los_Angeles"} {
		instant, err := value.Instant(testContext(t, zone, want))
		if err != nil {
			t.Fatalf("Instant read from %s: %v", zone, err)
		}
		if !instant.Equal(want) {
			t.Fatalf("Instant read from %s = %s, want %s", zone, instant, want)
		}
	}

	projection, err := value.Projected(losAngelesAt(t, want))
	if err != nil {
		t.Fatalf("Projected: %v", err)
	}
	if projection.Date != mustDate(2026, 7, 20) || projection.Local != "09:00" ||
		projection.TimezoneID != "America/Los_Angeles" {
		t.Fatalf("Projected = %+v, want 2026-07-20 09:00 America/Los_Angeles", projection)
	}
}

func TestAllDayDueBoundaryIsNextLocalDateNotStoredMidnight(t *testing.T) {
	value, err := NewValue(mustDate(2026, 7, 20), "", "", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	cases := []struct {
		now  time.Time
		want bool
	}{
		{time.Date(2026, 7, 21, 6, 59, 0, 0, time.UTC), false},
		{time.Date(2026, 7, 21, 7, 0, 0, 0, time.UTC), true},
		{time.Date(2026, 7, 21, 7, 1, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		overdue, err := value.Overdue(losAngelesAt(t, tc.now))
		if err != nil {
			t.Fatalf("Overdue at %s: %v", tc.now, err)
		}
		if overdue != tc.want {
			t.Fatalf("Overdue at %s = %v, want %v", tc.now, overdue, tc.want)
		}
	}
	if _, _, _, ok := value.TimeMetadata(); ok {
		t.Fatal("an all-day value carries no time metadata")
	}
}

func TestTimedDeadlineBecomesOverdueImmediatelyAfterItsExactMinute(t *testing.T) {
	value, err := NewValue(mustDate(2026, 7, 20), "09:00", "America/Los_Angeles", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	cases := []struct {
		now  time.Time
		want bool
	}{
		{time.Date(2026, 7, 20, 15, 59, 0, 0, time.UTC), false},
		{time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC), false},
		{time.Date(2026, 7, 20, 16, 0, 1, 0, time.UTC), true},
	}
	for _, tc := range cases {
		overdue, err := value.Overdue(losAngelesAt(t, tc.now))
		if err != nil {
			t.Fatalf("Overdue at %s: %v", tc.now, err)
		}
		if overdue != tc.want {
			t.Fatalf("Overdue at %s = %v, want %v", tc.now, overdue, tc.want)
		}
	}
}

// Released is the mirror of Overdue for an available-from stamp: true AT the
// instant, where a deadline is still on time at its own.
func TestTimedReleaseIsInclusiveOfItsInstant(t *testing.T) {
	value, err := NewValue(mustDate(2026, 7, 20), "09:00", "", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	cases := []struct {
		now  time.Time
		want bool
	}{
		{time.Date(2026, 7, 20, 15, 59, 59, 0, time.UTC), false},
		{time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		released, err := value.Released(losAngelesAt(t, tc.now))
		if err != nil {
			t.Fatalf("Released at %s: %v", tc.now, err)
		}
		if released != tc.want {
			t.Fatalf("Released at %s = %v, want %v", tc.now, released, tc.want)
		}
	}
}

func TestGapIsRejectedAndFoldRoundTripsBothInstants(t *testing.T) {
	// 2026-03-08 is the US spring-forward; 02:30 does not exist that morning.
	_, err := NewValue(mustDate(2026, 3, 8), "02:30", "America/Los_Angeles", 0, true)
	var gap *timezones.NonexistentLocalTime
	if !errors.As(err, &gap) {
		t.Fatalf("a DST gap must be refused, got %v", err)
	}

	context := losAngelesAt(t, time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC))
	earlier, err := NewValue(mustDate(2026, 11, 1), "01:30", "America/Los_Angeles", 0, true)
	if err != nil {
		t.Fatalf("fold 0: %v", err)
	}
	later, err := NewValue(mustDate(2026, 11, 1), "01:30", "America/Los_Angeles", 1, true)
	if err != nil {
		t.Fatalf("fold 1: %v", err)
	}
	first, err := earlier.Instant(context)
	if err != nil {
		t.Fatalf("earlier instant: %v", err)
	}
	second, err := later.Instant(context)
	if err != nil {
		t.Fatalf("later instant: %v", err)
	}
	if second.Sub(first) != time.Hour {
		t.Fatalf("the two instants of an ambiguous local time differ by %s, want 1h", second.Sub(first))
	}
	if _, _, fold, ok := later.TimeMetadata(); !ok || fold != 1 {
		t.Fatalf("fold metadata = %d (%v), want 1", fold, ok)
	}
	if _, _, fold, _ := earlier.TimeMetadata(); fold != 0 {
		t.Fatal("an unfolded value must not claim fold 1")
	}
}

func TestUnknownZoneAndAbbreviationAreRejected(t *testing.T) {
	for _, zone := range []string{"Mars/Olympus", "PST", "", "  "} {
		if _, err := timezones.Get(zone); err == nil {
			t.Fatalf("timezones.Get(%q) must be refused", zone)
		}
	}
}

func TestNonHourOffsetZone(t *testing.T) {
	value, err := NewValue(mustDate(2026, 7, 20), "09:00", "Asia/Kathmandu", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	want := time.Date(2026, 7, 20, 3, 15, 0, 0, time.UTC)
	instant, err := value.Instant(losAngelesAt(t, want))
	if err != nil {
		t.Fatalf("Instant: %v", err)
	}
	if !instant.Equal(want) {
		t.Fatalf("Instant = %s, want %s", instant, want)
	}
}

func TestValueValidationRefusesUnusableCombinations(t *testing.T) {
	cases := []struct {
		name      string
		local     string
		timezone  string
		fold      int
		wantError string
	}{
		{"seconds precision", "09:00:00", "", 0, "minute precision"},
		{"single digit hour", "9:00", "", 0, "minute precision"},
		{"hour out of range", "24:00", "", 0, "minute precision"},
		{"zone with no time", "", "Etc/UTC", 0, "requires a local time"},
		{"fold out of range", "09:00", "", 2, "fold must be 0 or 1"},
		{"unknown zone", "09:00", "Mars/Olympus", 0, "unknown IANA time zone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewValue(mustDate(2026, 7, 20), tc.local, tc.timezone, tc.fold, true)
			if err == nil {
				t.Fatalf("want a refusal mentioning %q", tc.wantError)
			}
		})
	}
}

// NormalizeTime is the whole clock grammar, including the readings a fuzzy
// parser must still refuse.
func TestNormalizeTime(t *testing.T) {
	accepted := map[string]string{
		"noon": "12:00", "MIDNIGHT": "00:00", "5pm": "17:00", "12am": "00:00",
		"12pm": "12:00", "1am": "01:00", "09:30": "09:30", "23:59": "23:59",
		"00:00": "00:00", "9:30pm": "21:30", "9:30AM": "09:30",
	}
	for input, want := range accepted {
		got, err := NormalizeTime(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeTime(%q) = %q (%v), want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"13pm", "0pm", "24:00", "9:60", "abc", "", "5"} {
		if got, err := NormalizeTime(input); err == nil && input != "5" {
			t.Fatalf("NormalizeTime(%q) = %q, want a refusal", input, got)
		}
	}
	// A bare digit normalizes on its own but never reaches here from an
	// expression, because TIME_TOKEN refuses to split it off.
	if got, err := NormalizeTime("5"); err != nil || got != "05:00" {
		t.Fatalf("NormalizeTime(%q) = %q (%v)", "5", got, err)
	}
}

func TestShiftAndWithDateKeepTheWallTimeAndZone(t *testing.T) {
	value, err := NewValue(mustDate(2026, 7, 20), "09:00", "America/Los_Angeles", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	shifted, err := value.Shift(7)
	if err != nil {
		t.Fatalf("Shift: %v", err)
	}
	if shifted.Date != mustDate(2026, 7, 27) || shifted.LocalTime != "09:00" ||
		shifted.Timezone != "America/Los_Angeles" {
		t.Fatalf("Shift(7) = %+v", shifted)
	}
	redated, err := value.WithDate(mustDate(2026, 12, 1))
	if err != nil {
		t.Fatalf("WithDate: %v", err)
	}
	if redated.Date != mustDate(2026, 12, 1) || redated.LocalTime != "09:00" {
		t.Fatalf("WithDate = %+v", redated)
	}
	if value.Equal(shifted) || !value.Equal(value) {
		t.Fatal("Equal compares the four stored fields")
	}
	// Re-dating ONTO a DST gap is refused rather than silently moved, which is
	// what makes the recurrence roll skip such an occurrence.
	gapProne, err := NewValue(mustDate(2026, 7, 20), "02:30", "America/Los_Angeles", 0, true)
	if err != nil {
		t.Fatalf("02:30 is an ordinary time in July: %v", err)
	}
	if _, err := gapProne.WithDate(mustDate(2026, 3, 8)); err == nil {
		t.Fatal("re-dating onto a DST gap must be refused")
	}
}

func TestFromRecordDegradesToTheDateWhenTheTimeIsUnusable(t *testing.T) {
	cases := []struct {
		name         string
		date         string
		local        string
		zone         string
		fold         int
		wantOK       bool
		wantAllDay   bool
		wantTimezone string
	}{
		{name: "all day", date: "2026-07-20", wantOK: true, wantAllDay: true},
		{name: "floating", date: "2026-07-20", local: "09:00", wantOK: true},
		{name: "fixed", date: "2026-07-20", local: "09:00", zone: "Europe/London",
			wantOK: true, wantTimezone: "Europe/London"},
		{name: "unusable time degrades", date: "2026-07-20", local: "9am",
			wantOK: true, wantAllDay: true},
		{name: "unusable zone degrades", date: "2026-07-20", local: "09:00", zone: "PST",
			wantOK: true, wantAllDay: true},
		{name: "impossible date is no value", date: "2026-02-30", wantOK: false},
		{name: "unstored spelling is no value", date: "2026-7-20", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, ok := FromRecord(tc.date, tc.local, tc.zone, tc.fold, true)
			if ok != tc.wantOK {
				t.Fatalf("FromRecord ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if value.AllDay() != tc.wantAllDay {
				t.Fatalf("AllDay = %v, want %v (%+v)", value.AllDay(), tc.wantAllDay, value)
			}
			if value.Timezone != tc.wantTimezone {
				t.Fatalf("Timezone = %q, want %q", value.Timezone, tc.wantTimezone)
			}
		})
	}
}

// Not covered by Ruby: a context renders the same stored value differently for
// two readers, which is the whole reason value and context are separate types.
func TestContextProjectsOneValueForTwoReaders(t *testing.T) {
	value, err := NewValue(mustDate(2026, 7, 20), "23:30", "Europe/London", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	london, err := value.Projected(testContext(t, "Europe/London", now))
	if err != nil {
		t.Fatalf("Projected London: %v", err)
	}
	tokyo, err := value.Projected(testContext(t, "Asia/Tokyo", now))
	if err != nil {
		t.Fatalf("Projected Tokyo: %v", err)
	}
	if london.Date.ISO() != "2026-07-20" || london.Local != "23:30" {
		t.Fatalf("London projection = %+v", london)
	}
	if tokyo.Date.ISO() != "2026-07-21" || tokyo.Local != "07:30" {
		t.Fatalf("Tokyo projection = %+v", tokyo)
	}
}

// Not covered by Ruby: the reader's own calendar day comes from the zone, not
// from UTC, which is what every "is it due today" answer rests on.
func TestContextLocalDateFollowsTheReadersZone(t *testing.T) {
	now := time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC)
	if got := losAngelesAt(t, now).LocalDate().ISO(); got != "2026-07-20" {
		t.Fatalf("Los Angeles local date = %s, want 2026-07-20", got)
	}
	if got := testContext(t, "Asia/Tokyo", now).LocalDate().ISO(); got != "2026-07-21" {
		t.Fatalf("Tokyo local date = %s, want 2026-07-21", got)
	}
}
