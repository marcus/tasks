package recur

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/temporal"
)

func dateList(t *testing.T, isos ...string) []CivilDate {
	t.Helper()
	dates := make([]CivilDate, len(isos))
	for index, iso := range isos {
		dates[index] = day(t, iso)
	}
	return dates
}

func assertOccurrences(t *testing.T, value, from, today string, want []CivilDate) {
	t.Helper()
	got, err := Occurrences(value, day(t, from), day(t, today), len(want))
	if err != nil {
		t.Fatalf("Occurrences(%q): %v", value, err)
	}
	if len(got) != len(want) {
		t.Fatalf("Occurrences(%q) = %v, want %v", value, got, want)
	}
	for index := range want {
		if !got[index].Equal(want[index]) {
			t.Fatalf("Occurrences(%q) = %v, want %v", value, got, want)
		}
	}
}

func assertNextDate(t *testing.T, value, from, today, want string) {
	t.Helper()
	got, err := NextDate(value, day(t, from), day(t, today))
	if err != nil {
		t.Fatalf("NextDate(%q): %v", value, err)
	}
	if got.String() != want {
		t.Fatalf("NextDate(%q, from %s, today %s) = %s, want %s", value, from, today, got, want)
	}
}

// -- interval cookies -------------------------------------------------------

// recurToday is test_recur.rb's TODAY: 2026-07-04.
const recurToday = "2026-07-04"

func TestFromCompletionAnchorsOnToday(t *testing.T) {
	assertNextDate(t, ".+1w", "2020-01-01", recurToday, "2026-07-11")
	assertNextDate(t, ".+2d", "2026-07-01", recurToday, "2026-07-06")
}

// "+" adds exactly one interval to the stored date, and may still land in the
// past — that is the point of the one-hop prefix.
func TestFixedIsASingleHopFromStoredDate(t *testing.T) {
	assertNextDate(t, "+1w", "2026-07-02", recurToday, "2026-07-09")
	assertNextDate(t, "+1w", "2020-01-01", recurToday, "2020-01-08")
}

func TestCatchUpWalksTheSeriesUpToToday(t *testing.T) {
	assertNextDate(t, "++1w", "2026-06-01", recurToday, "2026-07-06")
	// An already-future stored date still advances at least once.
	assertNextDate(t, "++1w", "2026-07-13", recurToday, "2026-07-20")
}

// The date-only projection stops where the completion path's own fast-forward
// stops — at today, not past it. An all-day stamp landing on the completion day
// is still ahead by its end-of-day boundary.
func TestCatchUpProjectionCanLandOnToday(t *testing.T) {
	assertNextDate(t, "++1d", "2026-07-03", recurToday, recurToday)
	assertNextDate(t, "++1w", "2026-06-27", recurToday, recurToday)
	assertNextDate(t, "++1w", "2026-06-06", recurToday, recurToday)
	assertNextDate(t, "++1m", "2026-06-04", recurToday, recurToday)
	// Projections chain from each landing, so the series keeps stepping.
	assertOccurrences(t, "++1w", "2026-06-27", recurToday,
		dateList(t, recurToday, "2026-07-11", "2026-07-18"))
}

func TestUnitsDaysWeeksMonthsYears(t *testing.T) {
	assertNextDate(t, "+3d", "2026-03-10", recurToday, "2026-03-13")
	assertNextDate(t, "+2w", "2026-03-10", recurToday, "2026-03-24")
	assertNextDate(t, "+2m", "2026-03-10", recurToday, "2026-05-10")
	assertNextDate(t, "+2y", "2026-03-10", recurToday, "2028-03-10")
}

func TestMonthStepClampsOverflowingDay(t *testing.T) {
	assertNextDate(t, "+1m", "2026-01-31", recurToday, "2026-02-28")
}

func TestYearStepFromLeapDayClamps(t *testing.T) {
	assertNextDate(t, "+1y", "2028-02-29", recurToday, "2029-02-28")
}

func TestNextDateRejectsANonStoredForm(t *testing.T) {
	for _, value := range []string{"weekly", "every monday", "w:funday", "W:MON", "w:monday", ""} {
		if _, err := NextDate(value, day(t, recurToday), day(t, recurToday)); err == nil {
			t.Fatalf("NextDate(%q) must refuse a value that is not a stored form", value)
		}
	}
}

// -- edge-date rules --------------------------------------------------------

// A numeric day the month lacks clamps to the month end, which makes m:31 a
// synonym for m:last in short months, by design.
func TestNumericDayAMonthLacksClampsToTheMonthEnd(t *testing.T) {
	assertOccurrences(t, "m:31", "2026-03-01", "2026-03-01",
		dateList(t, "2026-03-31", "2026-04-30", "2026-05-31", "2026-06-30"))
	assertNextDate(t, "m:31", "2026-04-01", "2026-04-01", "2026-04-30")
}

// February, March and April 2026 have only four Fridays; they are SKIPPED, not
// clamped.
func TestOrdinalWeekdayAMonthLacksSkipsToAMonthThatHasOne(t *testing.T) {
	assertOccurrences(t, "m:5fri", "2026-01-01", "2026-01-01",
		dateList(t, "2026-01-30", "2026-05-29", "2026-07-31", "2026-10-30"))
}

func TestLastWeekdayAndLastDayAreDistinctRules(t *testing.T) {
	assertNextDate(t, "m:last", "2026-01-01", "2026-01-01", "2026-01-31")
	assertNextDate(t, "m:lastfri", "2026-01-01", "2026-01-01", "2026-01-30")
}

func TestFeb29YearlyClampsInNonLeapYears(t *testing.T) {
	assertOccurrences(t, "y:02-29", "2024-03-01", "2024-03-01",
		dateList(t, "2025-02-28", "2026-02-28", "2027-02-28", "2028-02-29"))
}

// A February with five Fridays needs 29 days starting on a Friday, so only a
// leap year has one — an odd anchor year with a two-year interval never fires.
// Satisfiability is a property of the schedule AND its anchor, so it cannot be
// decided at parse time.
func TestAnAnchorDependentDeadScheduleParsesButCannotProject(t *testing.T) {
	const unsatisfiable = "2y:02:5fri"
	if got := Parse("every 2 years on the 5th friday of february", ".+"); got.Canonical != unsatisfiable {
		t.Fatalf("Parse = %#v, want %q", got, unsatisfiable)
	}
	_, err := NextDate(unsatisfiable, day(t, "2027-01-01"), day(t, "2027-01-01"))
	if err == nil {
		t.Fatal("an unreachable schedule must report that it cannot project")
	}
	for _, fragment := range []string{`no occurrence of "2y:02:5fri"`, "from 2027-01-01",
		"may never fire for this anchor"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want it to mention %q", err, fragment)
		}
	}
}

func TestTheSameScheduleProjectsFineFromALeapParityAnchor(t *testing.T) {
	assertOccurrences(t, "2y:02:5fri", "2028-01-01", "2028-01-01",
		dateList(t, "2036-02-29", "2064-02-29"))
}

// -- advance semantics ------------------------------------------------------

// calendarToday is test_recur_calendar.rb's TODAY: Tuesday 2026-07-28, whose
// ISO week starts Monday 2026-07-27.
const calendarToday = "2026-07-28"

func TestCatchUpLandsStrictlyAfterToday(t *testing.T) {
	assertNextDate(t, "w:mon", "2026-01-05", calendarToday, "2026-08-03")
	assertNextDate(t, "m:1", "2025-11-01", calendarToday, "2026-08-01")
	assertNextDate(t, "y:07-04", "2020-07-04", calendarToday, "2027-07-04")
}

// The stamp IS the current occurrence, so a roll always moves past it.
func TestCatchUpStillAdvancesWhenTheStampIsAlreadyInTheFuture(t *testing.T) {
	assertNextDate(t, "w:mon", "2026-08-10", calendarToday, "2026-08-17")
}

func TestOneHopAdvancesFromTheStoredDateAndMayStayInThePast(t *testing.T) {
	assertNextDate(t, "+w:mon", "2026-01-05", calendarToday, "2026-01-12")
	assertNextDate(t, "+m:1", "2025-11-01", calendarToday, "2025-12-01")
	result, err := NextDate("+w:mon", day(t, "2026-01-05"), day(t, calendarToday))
	if err != nil || !result.Before(day(t, calendarToday)) {
		t.Fatalf("a one-hop roll may stay in the past: %s (%v)", result, err)
	}
}

// Two stamps one week apart produce opposite-parity Monday series.
func TestEveryNthWeekParityIsAnchoredOnTheStoredDatesISOWeek(t *testing.T) {
	assertOccurrences(t, "2w:mon", calendarToday, calendarToday,
		dateList(t, "2026-08-10", "2026-08-24", "2026-09-07"))
	assertOccurrences(t, "2w:mon", "2026-07-21", calendarToday,
		dateList(t, "2026-08-03", "2026-08-17", "2026-08-31"))
}

// Stamp 2026-01-05 (a Monday) anchors the odd-week series; catching up in July
// must land on THAT series, not on the nearest Monday.
func TestParitySurvivesACatchUpRollFromAStaleStamp(t *testing.T) {
	assertNextDate(t, "2w:mon", "2026-01-05", calendarToday, "2026-08-03")
	landing := day(t, "2026-08-03")
	anchor := day(t, "2026-01-05")
	elapsed := new(civilDays).between(landing, anchor)
	if elapsed%14 != 0 {
		t.Fatalf("the landing is %d days from the anchor, which is off-parity", elapsed)
	}
}

// civilDays measures whole days between two civil dates, for a parity check.
type civilDays struct{}

func (civilDays) between(later, earlier CivilDate) int64 {
	difference := daysFromCivil(later)
	difference.Sub(difference, daysFromCivil(earlier))
	return difference.Int64()
}

func TestEveryNthMonthAndYearParityAnchorOnTheStoredDate(t *testing.T) {
	assertOccurrences(t, "3m:15", "2026-01-15", "2026-07-01",
		dateList(t, "2026-07-15", "2026-10-15", "2027-01-15"))
	assertOccurrences(t, "2y:07-04", "2025-07-04", calendarToday,
		dateList(t, "2027-07-04", "2029-07-04"))
}

func TestMultiDayWeeklyWalksTheDaySetInOrder(t *testing.T) {
	assertOccurrences(t, "w:mon,wed,fri", calendarToday, calendarToday,
		dateList(t, "2026-07-29", "2026-07-31", "2026-08-03", "2026-08-05"))
}

// -- humanize ---------------------------------------------------------------

func TestHumanizeEveryStoredShape(t *testing.T) {
	cases := map[string]string{
		// interval cookies
		".+1w": "every week from completion",
		".+2w": "every 2 weeks from completion",
		"+1m":  "every month from the scheduled date",
		"++3d": "every 3 days from the scheduled date (catching up)",
		// calendar schedules
		"w:mon":                  "every Monday",
		"w:mon,wed,fri":          "every Mon, Wed, Fri",
		"w:mon,tue,wed,thu,fri":  "every weekday",
		"w:sat,sun":              "every weekend",
		"2w:mon":                 "every 2 weeks on Monday",
		"2w:mon,thu":             "every 2 weeks on Mon, Thu",
		"2w:mon,tue,wed,thu,fri": "every 2 weeks on weekdays",
		"3w:sat,sun":             "every 3 weeks on weekends",
		"m:15":                   "monthly on the 15th",
		"m:1,15":                 "monthly on the 1st and 15th",
		"m:1,15,22":              "monthly on the 1st, 15th and 22nd",
		"m:last":                 "monthly on the last day",
		"m:2tue":                 "monthly on the 2nd Tuesday",
		"m:3wed":                 "monthly on the 3rd Wednesday",
		"m:lastfri":              "monthly on the last Friday",
		"2m:15":                  "every 2 months on the 15th",
		"y:07-04":                "yearly on July 4",
		"y:11:3thu":              "yearly on the 3rd Thursday of November",
		"2y:07-04":               "every 2 years on July 4",
		"+w:mon":                 "every Monday (one hop)",
		"+m:15":                  "monthly on the 15th (one hop)",
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			got := Humanize(value)
			if got == nil || *got != want {
				t.Fatalf("Humanize(%q) = %v, want %q", value, got, want)
			}
		})
	}
}

func TestHumanizeEdges(t *testing.T) {
	if got := Humanize(""); got != nil {
		t.Fatalf("Humanize(blank) = %q, want nothing", *got)
	}
	// Unparsable stored values echo through rather than disappearing.
	if got := Humanize("junk"); got == nil || *got != "junk" {
		t.Fatalf("Humanize(junk) = %v", got)
	}
}

// -- explain ----------------------------------------------------------------

func TestExplainPayload(t *testing.T) {
	result := Explain("every 2 weeks on monday", day(t, calendarToday), 3, "")
	if result.Input != "every 2 weeks on monday" || result.Canonical != "2w:mon" ||
		result.Human != "every 2 weeks on Monday" || result.Error != "" {
		t.Fatalf("Explain = %#v", result)
	}
	want := dateList(t, "2026-08-10", "2026-08-24", "2026-09-07")
	for index := range want {
		if !result.Next[index].Equal(want[index]) {
			t.Fatalf("Explain next = %v, want %v", result.Next, want)
		}
	}
}

func TestExplainProjectsFromASuppliedStamp(t *testing.T) {
	result := Explain("2w:mon", day(t, calendarToday), 2, "2026-07-21")
	want := dateList(t, "2026-08-03", "2026-08-17")
	if len(result.Next) != 2 || !result.Next[0].Equal(want[0]) || !result.Next[1].Equal(want[1]) {
		t.Fatalf("Explain next = %v, want %v", result.Next, want)
	}
}

func TestExplainHandlesIntervalCookiesToo(t *testing.T) {
	result := Explain("every 2 weeks", day(t, calendarToday), 3, "")
	if result.Canonical != ".+2w" || result.Human != "every 2 weeks from completion" {
		t.Fatalf("Explain = %#v", result)
	}
	want := dateList(t, "2026-08-11", "2026-08-25", "2026-09-08")
	for index := range want {
		if !result.Next[index].Equal(want[index]) {
			t.Fatalf("Explain next = %v, want %v", result.Next, want)
		}
	}
}

func TestExplainReturnsAStructuredErrorWithTheReason(t *testing.T) {
	result := Explain(".+w:mon", day(t, calendarToday), 5, "")
	if result.Input != ".+w:mon" || !strings.Contains(result.Error, "interval prefix") {
		t.Fatalf("Explain = %#v", result)
	}
	if result.HasCanonical || result.HasNext {
		t.Fatal("a parse failure carries neither a canonical form nor a projection")
	}
}

// A projection failure must not masquerade as a parse error: the canonical form
// and human rendering stay, Next is empty, and the reason is distinct.
func TestExplainKeepsIdentifyingAScheduleItCannotProject(t *testing.T) {
	result := Explain("every 2 years on the 5th friday of february", day(t, calendarToday), 5, "2027-01-01")
	if result.Canonical != "2y:02:5fri" ||
		result.Human != "every 2 years on the 5th Friday of February" {
		t.Fatalf("Explain = %#v", result)
	}
	if len(result.Next) != 0 {
		t.Fatalf("Explain next = %v, want empty", result.Next)
	}
	if !strings.Contains(result.Error, "may never fire for this anchor") ||
		strings.Contains(result.Error, "unrecognized") {
		t.Fatalf("Explain error = %q", result.Error)
	}
}

func TestExplainRejectsAnUnparsableStampWithoutProjecting(t *testing.T) {
	for _, stamp := range []string{"not-a-date", "2026-02-30", "2026-7-21", "tomorrow"} {
		result := Explain("w:mon", day(t, calendarToday), 5, stamp)
		if !strings.Contains(result.Error, "must be a real YYYY-MM-DD date") {
			t.Fatalf("Explain(from %q) = %#v", stamp, result)
		}
		if result.HasCanonical {
			t.Fatal("an unusable stamp is reported before anything is projected")
		}
	}
}

func TestExplainOfOffReportsNoRecurrence(t *testing.T) {
	result := Explain("off", day(t, calendarToday), 5, "")
	if result.Canonical != "" || result.Human != "no recurrence" || len(result.Next) != 0 {
		t.Fatalf("Explain(off) = %#v", result)
	}
}

func TestExplainCountDefaultsToFiveAndClamps(t *testing.T) {
	cases := map[int]int{0: 0, 3: 3, 5: 5, 999: 50, -1: 0}
	for count, want := range cases {
		result := Explain("w:mon", day(t, calendarToday), count, "")
		if len(result.Next) != want {
			t.Fatalf("Explain(count %d) projected %d dates, want %d", count, len(result.Next), want)
		}
	}
}

// -- the temporal roll ------------------------------------------------------

func rollContext(t *testing.T, zone string, now time.Time) temporal.Context {
	t.Helper()
	context, err := temporal.NewContext(now, zone, 12)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context
}

func stamp(t *testing.T, iso, local, zone string) temporal.Value {
	t.Helper()
	parsed, ok := temporal.ParseDate(iso)
	if !ok {
		t.Fatalf("%q is not a storable date", iso)
	}
	// validate=false: some fixtures deliberately name a local time that does
	// not exist, which is exactly the case the roll must skip.
	value, err := temporal.NewValue(parsed, local, zone, 0, false)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	return value
}

func assertRoll(t *testing.T, value string, from temporal.Value, kind Kind,
	context temporal.Context, want string) temporal.Date {
	t.Helper()
	got, err := NextTemporalDate(value, from, kind, context, nil)
	if err != nil {
		t.Fatalf("NextTemporalDate(%q): %v", value, err)
	}
	if got.ISO() != want {
		t.Fatalf("NextTemporalDate(%q) = %s, want %s", value, got.ISO(), want)
	}
	return got
}

// The date-only projection and the temporal write must agree for an all-day
// stamp; the timed case is the documented exception.
func TestCatchUpProjectionAgreesWithTheTemporalRoll(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC))
	for _, value := range []string{"++1d", "++1w", "++1m"} {
		from := stamp(t, "2026-06-06", "", "")
		rolled, err := NextTemporalDate(value, from, Deadline, context, nil)
		if err != nil {
			t.Fatalf("NextTemporalDate(%q): %v", value, err)
		}
		projected, err := NextDate(value, day(t, "2026-06-06"), day(t, recurToday))
		if err != nil {
			t.Fatalf("NextDate(%q): %v", value, err)
		}
		if rolled.ISO() != projected.String() {
			t.Fatalf("%s: preview says %s but the roll says %s", value, projected, rolled.ISO())
		}
	}
}

// A catch-up roll walks its series to today with plain date math before the
// civil-time loop runs, so a stamp thousands of hops stale still lands.
func TestCatchUpRollsFromAStampThousandsOfHopsStale(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-01-31", "", "") // 1,582 daily hops behind
	assertRoll(t, "++1d", from, Deadline, context, "2030-06-01")
	// 2026-01-31 and 2030-06-01 are both Saturdays, so the weekly series lands
	// exactly on the completion day — still ahead by its end-of-day boundary.
	assertRoll(t, "++1w", from, Deadline, context, "2030-06-01")
	// Monthly hops clamp to the 28th at the first short month and stay there.
	assertRoll(t, "++1m", from, Deadline, context, "2030-06-28")
}

// The fast-forward stops AT today, never past it: an all-day stamp on the
// completion day is still ahead by its end-of-day boundary, and a timed one is
// judged by its local time.
func TestCatchUpStillOffersACandidateLandingOnToday(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 7, 20, 0, 30, 0, 0, time.UTC))
	assertRoll(t, "++1d", stamp(t, "2026-07-19", "23:00", "Etc/UTC"), Deadline, context, "2026-07-20")
	// A candidate already past by its local time advances again.
	assertRoll(t, "++1d", stamp(t, "2026-07-19", "00:05", "Etc/UTC"), Deadline, context, "2026-07-21")
}

func TestCatchUpFromAStaleStampStillHonorsAVeto(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC))
	got, err := NextTemporalDate("++1d", stamp(t, "2026-01-31", "", ""), Deadline, context,
		func(candidate temporal.Date) bool { return candidate.Day > 4 })
	if err != nil || got.ISO() != "2030-06-05" {
		t.Fatalf("veto walk = %s (%v), want 2030-06-05", got.ISO(), err)
	}
}

// 2026-03-08 is the US spring-forward; 02:30 does not exist that morning, so
// the candidate is skipped rather than moved.
func TestDSTGapCandidateSkipsToTheNextOccurrence(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-03-01", "02:30", "America/Los_Angeles")
	assertRoll(t, "w:sun", from, Scheduled, context, "2026-03-15")
	assertRoll(t, "+w:sun", from, Scheduled, context, "2026-03-15")
	// The interval form skips the same occurrence.
	assertRoll(t, "+1w", from, Scheduled, context, "2026-03-15")
}

// Anchor Sunday 2026-02-22 puts the every-other-Sunday series on 2026-03-08 —
// the spring-forward. The skip must land on the next SAME-PARITY Sunday
// (03-22), not on the next Sunday (03-15).
func TestNextTemporalDateKeepsNwParityWhenACandidateHitsADSTGap(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 2, 23, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-02-22", "02:30", "America/Los_Angeles")
	result := assertRoll(t, "2w:sun", from, Scheduled, context, "2026-03-22")
	if result.Sub(from.Date)%14 != 0 {
		t.Fatal("the skip left the anchor's parity")
	}
}

func TestValidationBlockCanVetoACalendarCandidate(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-07-28", "", "")
	seen := []string{}
	got, err := NextTemporalDate("w:mon", from, Deadline, context, func(candidate temporal.Date) bool {
		seen = append(seen, candidate.ISO())
		return candidate.After(mustTemporalDate(t, "2026-08-10"))
	})
	if err != nil || got.ISO() != "2026-08-17" {
		t.Fatalf("veto walk = %s (%v), want 2026-08-17", got.ISO(), err)
	}
	want := []string{"2026-08-03", "2026-08-10", "2026-08-17"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("the veto saw %v, want %v", seen, want)
	}
}

func TestNextTemporalDateCatchUpAndOneHopForAllDayStamps(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-01-05", "", "")
	assertRoll(t, "w:mon", from, Deadline, context, "2026-08-03")
	assertRoll(t, "+w:mon", from, Deadline, context, "2026-01-12")
}

// Catch-up jumps to the current cycle by arithmetic, so a stamp years stale
// costs no more search than a fresh one — and every-Nth parity survives it.
func TestNextTemporalDateCatchesUpFromAStampYearsStale(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2019-01-07", "", "")
	assertRoll(t, "w:mon", from, Deadline, context, "2030-06-03")
	parity := assertRoll(t, "2w:mon", from, Deadline, context, "2030-06-10")
	if parity.Sub(from.Date)%14 != 0 {
		t.Fatal("the catch-up jump left the anchor's parity")
	}
	assertRoll(t, "m:15", from, Deadline, context, "2030-06-15")
}

// February, March and April 2026 have no fifth Friday; the roll skips them.
func TestNextTemporalDateSkipsMonthsWithoutAnOrdinalWeekday(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-01-01", "", "")
	assertRoll(t, "m:5fri", from, Deadline, context, "2026-05-29")
	// One-hop walks the same series, just from the stored date.
	assertRoll(t, "+m:5fri", from, Deadline, context, "2026-01-30")
}

func TestNextTemporalDateRejectsANonStoredForm(t *testing.T) {
	context := rollContext(t, "Etc/UTC", time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))
	from := stamp(t, "2026-07-28", "", "")
	for _, value := range []string{"every monday", "weekly", "w:monday", ""} {
		if _, err := NextTemporalDate(value, from, Deadline, context, nil); err == nil {
			t.Fatalf("NextTemporalDate(%q) must refuse a value that is not a stored form", value)
		}
	}
}

// A fixed stamp rolls by its OWN zone's calendar day, not the reader's — the
// two disagree for several hours every day, and the wrong one moves an
// occurrence by a whole cycle.
func TestFromCompletionRollsByTheStampsOwnZone(t *testing.T) {
	// 2026-07-21 11:00Z is still the 20th in Los Angeles and already the 21st
	// in Tokyo.
	now := time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC)
	reader := rollContext(t, "Asia/Tokyo", now)
	losAngeles := stamp(t, "2026-07-01", "09:00", "America/Los_Angeles")
	got, err := NextTemporalDate(".+1d", losAngeles, Scheduled, reader, nil)
	if err != nil {
		t.Fatalf("NextTemporalDate: %v", err)
	}
	if got.ISO() != "2026-07-21" {
		t.Fatalf("roll = %s, want 2026-07-21 — the stamp's own zone is still on the 20th", got.ISO())
	}
}

func mustTemporalDate(t *testing.T, iso string) temporal.Date {
	t.Helper()
	parsed, ok := temporal.ParseDate(iso)
	if !ok {
		t.Fatalf("%q is not a storable date", iso)
	}
	return parsed
}
