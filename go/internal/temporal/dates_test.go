package temporal

import (
	"testing"
	"time"
)

// datesToday is test_dates.rb's TODAY: Wednesday 2026-07-01.
var datesToday = mustDate(2026, 7, 1)

func mustDate(year int, month time.Month, day int) Date {
	date, ok := NewDate(year, month, day)
	if !ok {
		panic("test fixture names an impossible date")
	}
	return date
}

func parseOn(t *testing.T, today Date, input string, order Order) (Date, bool) {
	t.Helper()
	return ParseWhen(input, today, order)
}

func assertParses(t *testing.T, input string, want Date) {
	t.Helper()
	got, ok := parseOn(t, datesToday, input, MDY)
	if !ok {
		t.Fatalf("ParseWhen(%q) = not understood, want %s", input, want.ISO())
	}
	if got != want {
		t.Fatalf("ParseWhen(%q) = %s, want %s", input, got.ISO(), want.ISO())
	}
}

func assertRejects(t *testing.T, input string) {
	t.Helper()
	if got, ok := parseOn(t, datesToday, input, MDY); ok {
		t.Fatalf("ParseWhen(%q) = %s, want a refusal", input, got.ISO())
	}
}

func TestTodayAndTomorrow(t *testing.T) {
	assertParses(t, "today", datesToday)
	assertParses(t, "tomorrow", mustDate(2026, 7, 2))
}

func TestPlusDays(t *testing.T) {
	assertParses(t, "+3", mustDate(2026, 7, 4))
	assertParses(t, "+14", mustDate(2026, 7, 15))
}

func TestWeekdayNames(t *testing.T) {
	assertParses(t, "fri", mustDate(2026, 7, 3))
	assertParses(t, "friday", mustDate(2026, 7, 3))
	assertParses(t, "mon", mustDate(2026, 7, 6))
}

func TestSameWeekdayMeansNextWeek(t *testing.T) {
	assertParses(t, "wed", mustDate(2026, 7, 8))
}

func TestMonthDay(t *testing.T) {
	assertParses(t, "07-15", mustDate(2026, 7, 15))
	assertParses(t, "7/15", mustDate(2026, 7, 15))
}

func TestPastMonthDayRollsToNextYear(t *testing.T) {
	assertParses(t, "02-01", mustDate(2027, 2, 1))
}

func TestFullISODate(t *testing.T) {
	assertParses(t, "2026-08-01", mustDate(2026, 8, 1))
}

func TestGarbageReturnsNil(t *testing.T) {
	for _, input := range []string{"", "someday", "13-45", "2026-99-99", "   "} {
		assertRejects(t, input)
	}
}

func TestTwoLetterWeekdayNotMatched(t *testing.T) {
	assertRejects(t, "fr")
}

func TestNextWeekMonthYear(t *testing.T) {
	assertParses(t, "next week", mustDate(2026, 7, 8))
	assertParses(t, "next month", mustDate(2026, 8, 1))
	assertParses(t, "next year", mustDate(2027, 7, 1))
}

func TestNextMonthClampsShortMonth(t *testing.T) {
	got, ok := parseOn(t, mustDate(2026, 1, 31), "next month", MDY)
	if !ok || got != mustDate(2026, 2, 28) {
		t.Fatalf("next month from 2026-01-31 = %s (%v), want 2026-02-28", got.ISO(), ok)
	}
}

func TestNextYearClampsLeapDay(t *testing.T) {
	got, ok := parseOn(t, mustDate(2028, 2, 29), "next year", MDY)
	if !ok || got != mustDate(2029, 2, 28) {
		t.Fatalf("next year from 2028-02-29 = %s (%v), want 2029-02-28", got.ISO(), ok)
	}
}

func TestInNUnits(t *testing.T) {
	cases := map[string]Date{
		"in 3 days":   mustDate(2026, 7, 4),
		"in 1 day":    mustDate(2026, 7, 2),
		"in 2 weeks":  mustDate(2026, 7, 15),
		"in a week":   mustDate(2026, 7, 8),
		"in 6 months": mustDate(2027, 1, 1),
		"in 2 years":  mustDate(2028, 7, 1),
		"in a month":  mustDate(2026, 8, 1),
		"in an year":  mustDate(2027, 7, 1),
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) { assertParses(t, input, want) })
	}
}

func TestInNUnitsRejectsGarbage(t *testing.T) {
	for _, input := range []string{"in days", "in 3 fortnights", "in -1 days"} {
		assertRejects(t, input)
	}
}

func TestNextWeekdayAlias(t *testing.T) {
	assertParses(t, "next fri", mustDate(2026, 7, 3))
	assertParses(t, "next friday", mustDate(2026, 7, 3))
}

func TestMonthNameAndDay(t *testing.T) {
	for _, input := range []string{"aug 1", "august 1", "aug 1st", "aug. 1", "AUG 1"} {
		t.Run(input, func(t *testing.T) { assertParses(t, input, mustDate(2026, 8, 1)) })
	}
	assertParses(t, "aug 22nd", mustDate(2026, 8, 22))
}

func TestMonthNameDayFirst(t *testing.T) {
	assertParses(t, "1 aug", mustDate(2026, 8, 1))
	assertParses(t, "1st august", mustDate(2026, 8, 1))
}

func TestMonthNameExplicitYear(t *testing.T) {
	assertParses(t, "aug 1 2026", mustDate(2026, 8, 1))
	assertParses(t, "aug 1, 2026", mustDate(2026, 8, 1))
	assertParses(t, "aug 1,2026", mustDate(2026, 8, 1))
	// An explicit past year is respected rather than rolled forward.
	assertParses(t, "aug 1 2025", mustDate(2025, 8, 1))
}

func TestMonthNameBarePastRollsToNextYear(t *testing.T) {
	assertParses(t, "jan 15", mustDate(2027, 1, 15))
}

func TestMonthNameRequiresUnambiguousAbbreviation(t *testing.T) {
	assertRejects(t, "ju 1") // june or july
	assertRejects(t, "nonmonth 1")
}

func TestISODateAcceptsSlashes(t *testing.T) {
	assertParses(t, "2026/08/01", mustDate(2026, 8, 1))
}

func TestNumericDateWithYearMDYDefault(t *testing.T) {
	assertParses(t, "8/1/2026", mustDate(2026, 8, 1))
	assertParses(t, "08-01-2026", mustDate(2026, 8, 1))
}

func TestNumericDateTwoDigitYear(t *testing.T) {
	assertParses(t, "8/1/26", mustDate(2026, 8, 1))
}

func TestDateOrderDMYFlipsBareAndYearForms(t *testing.T) {
	if got, ok := parseOn(t, datesToday, "8/1/2026", DMY); !ok || got != mustDate(2026, 1, 8) {
		t.Fatalf("dmy 8/1/2026 = %s (%v), want 2026-01-08", got.ISO(), ok)
	}
	if got, ok := parseOn(t, datesToday, "15-07", DMY); !ok || got != mustDate(2026, 7, 15) {
		t.Fatalf("dmy 15-07 = %s (%v), want 2026-07-15", got.ISO(), ok)
	}
}

// Ruby installs the order in a process-wide global (Dates.configure!) and falls
// back to :mdy for a value that is neither. Go threads the order instead, so
// the equivalent degrade lives in OrderNamed.
func TestOrderNamedFallsBackOnInvalidValue(t *testing.T) {
	cases := map[string]Order{"dmy": DMY, "DMY": DMY, " dmy ": DMY, "mdy": MDY, "nonsense": MDY, "": MDY}
	for name, want := range cases {
		if got := OrderNamed(name); got != want {
			t.Fatalf("OrderNamed(%q) = %v, want %v", name, got, want)
		}
	}
	if MDY.String() != "mdy" || DMY.String() != "dmy" {
		t.Fatal("Order.String must round-trip the config spelling")
	}
}

func TestGarbageMonthNameReturnsNil(t *testing.T) {
	assertRejects(t, "aug 45")    // no such day
	assertRejects(t, "aug 1 abc") // trailing junk is not a year
}

// A dropped digit ("2026" typo'd as "202") must not silently become the year
// 202 — that is a real-data footgun, not a fuzzy convenience.
func TestThreeDigitYearIsRejectedNotTruncated(t *testing.T) {
	assertRejects(t, "8/1/202")
	assertRejects(t, "8-1-202")
}

// 2025 is not a leap year, so rolling "the next Feb 29" forward from mid-2024
// lands on a day that does not exist. Both spellings must refuse it; neither
// may quietly clamp to Feb 28, a day the user did not type.
func TestBareAndNamedLeapDayRolloverAgree(t *testing.T) {
	today := mustDate(2024, 6, 1)
	if got, ok := parseOn(t, today, "2/29", MDY); ok {
		t.Fatalf(`ParseWhen("2/29") = %s, want a refusal`, got.ISO())
	}
	if got, ok := parseOn(t, today, "feb 29", MDY); ok {
		t.Fatalf(`ParseWhen("feb 29") = %s, want a refusal`, got.ISO())
	}
	// The same day IS reachable when the roll lands on a leap year.
	if got, ok := parseOn(t, mustDate(2023, 6, 1), "feb 29", MDY); !ok || got != mustDate(2024, 2, 29) {
		t.Fatalf(`ParseWhen("feb 29") from 2023 = %s (%v), want 2024-02-29`, got.ISO(), ok)
	}
}

// INTENTIONAL DIFFERENCE. Ruby refuses a year-less "feb 29" typed in any
// non-leap year, because it builds this year's date before deciding to roll and
// lets the Date::Error fall out as nil. Go rolls to the next real February 29
// within one year instead. The refuse-don't-clamp rule is unchanged, and so is
// the one-year horizon, which is why the leap-year-after case below still says
// no rather than reaching four years out.
func TestYearLessLeapDayRollsToTheNextRealFebruary29(t *testing.T) {
	cases := []struct {
		today string
		input string
		want  string // "" means refused
	}{
		{"2023-06-01", "feb 29", "2024-02-29"}, // Ruby: nil
		{"2023-06-01", "2/29", "2024-02-29"},   // Ruby: nil
		{"2024-01-10", "feb 29", "2024-02-29"}, // agrees with Ruby
		{"2024-06-01", "feb 29", ""},           // agrees with Ruby: four years is not a roll
		{"2024-06-01", "2/29", ""},
		{"2026-06-01", "feb 30", ""}, // never a real date, in any year
	}
	for _, tc := range cases {
		today, _ := ParseDate(tc.today)
		got, ok := ParseWhen(tc.input, today, MDY)
		switch {
		case tc.want == "" && ok:
			t.Fatalf("ParseWhen(%q) on %s = %s, want a refusal", tc.input, tc.today, got.ISO())
		case tc.want != "" && (!ok || got.ISO() != tc.want):
			t.Fatalf("ParseWhen(%q) on %s = %s (%v), want %s", tc.input, tc.today, got.ISO(), ok, tc.want)
		}
	}
}

// Not covered by Ruby: the month-end clamp of the calendar step, in both
// directions and across a leap boundary.
func TestAddMonthsClampsToMonthEnd(t *testing.T) {
	cases := []struct {
		from   Date
		months int
		want   Date
	}{
		{mustDate(2026, 1, 31), 1, mustDate(2026, 2, 28)},
		{mustDate(2024, 1, 31), 1, mustDate(2024, 2, 29)},
		{mustDate(2026, 3, 31), -1, mustDate(2026, 2, 28)},
		{mustDate(2026, 5, 31), 1, mustDate(2026, 6, 30)},
		{mustDate(2026, 8, 31), 12, mustDate(2027, 8, 31)},
		{mustDate(2026, 1, 15), -13, mustDate(2024, 12, 15)},
		{mustDate(2028, 2, 29), 12, mustDate(2029, 2, 28)},
		{mustDate(2026, 12, 31), 2, mustDate(2027, 2, 28)},
	}
	for _, tc := range cases {
		if got := tc.from.AddMonths(tc.months); got != tc.want {
			t.Fatalf("%s.AddMonths(%d) = %s, want %s", tc.from.ISO(), tc.months, got.ISO(), tc.want.ISO())
		}
	}
}

// Not covered by Ruby: every AddMonths result is a real date whose day never
// exceeds the source day, which is what "clamp, never overflow" means.
func TestAddMonthsNeverOverflowsIntoTheNextMonth(t *testing.T) {
	start := mustDate(2024, 1, 31)
	for months := -36; months <= 36; months++ {
		got := start.AddMonths(months)
		if _, ok := NewDate(got.Year, got.Month, got.Day); !ok {
			t.Fatalf("AddMonths(%d) produced the impossible date %s", months, got.ISO())
		}
		if got.Day > start.Day {
			t.Fatalf("AddMonths(%d) = %s, which overflowed past day %d", months, got.ISO(), start.Day)
		}
	}
}
