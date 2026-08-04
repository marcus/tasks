package lead

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/temporal"
)

func date(t *testing.T, iso string) temporal.Date {
	t.Helper()
	parsed, ok := temporal.ParseDate(iso)
	if !ok {
		t.Fatalf("%q is not a storable date", iso)
	}
	return parsed
}

func context(t *testing.T, zone string, now time.Time) temporal.Context {
	t.Helper()
	built, err := temporal.NewContext(now, zone, 12)
	if err != nil {
		t.Fatalf("NewContext(%q): %v", zone, err)
	}
	return built
}

// denver is test_lead.rb's ZONE: America/Denver, UTC-6 in autumn and UTC-7 in
// summer, so a DST change is always in reach of a fixture.
const denver = "America/Denver"

func TestCanonicalSpansPassThroughAndPhrasesNormalize(t *testing.T) {
	cases := map[string]string{
		"3w": "3w", "2d": "2d", "1m": "1m", "10y": "10y",
		"3 weeks": "3w", "a week": "1w", "the week before": "1w",
		"2 wks": "2w", "1 month": "1m", "6 months": "6m",
		"a quarter": "3m", "fortnight": "2w", "2 yrs": "2y",
		"10 days ahead": "10d", "4 days early": "4d",
		// Spellings Ruby also accepts through the same tables.
		"2wks": "2w", "3 mos": "3m", "1 yr": "1y", " 3W ": "3w",
		"2 weeks prior": "2w", "in 2 weeks": "2w", "a day": "1d",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			result := Parse(input)
			if result.Error != "" || result.Canonical != want {
				t.Fatalf("Parse(%q) = %#v, want %q", input, result, want)
			}
		})
	}
}

func TestOffWordsClearTheLead(t *testing.T) {
	for _, word := range []string{"off", "none", "never", "clear", "no", "stop", "OFF", " never "} {
		result := Parse(word)
		if !result.IsOff() {
			t.Fatalf("Parse(%q) = %#v, want the clear-the-lead outcome", word, result)
		}
		if _, ok := Canonical(word); ok {
			t.Fatalf("Canonical(%q) must report no span", word)
		}
	}
}

func TestZeroAndJunkAreRefusedWithAReason(t *testing.T) {
	cases := map[string]string{
		"0 days":  "at least 1 day",
		"0 weeks": "at least 1 week",
		"soonish": "unrecognized lead time",
		"  ":      "no lead time given",
		"":        "no lead time given",
		"weekly":  "unrecognized lead time",
		"w:mon":   "unrecognized lead time",
		"2 blorp": "unrecognized lead time",
	}
	for input, want := range cases {
		result := Parse(input)
		if result.Error == "" {
			t.Fatalf("Parse(%q) = %#v, want a refusal", input, result)
		}
		if !strings.Contains(result.Error, want) {
			t.Fatalf("Parse(%q) refusal = %q, want it to mention %q", input, result.Error, want)
		}
	}
	for _, notASpan := range []string{"0w", "+2w", "w:mon", "3 weeks", "", "3W"} {
		if Span(notASpan) {
			t.Fatalf("Span(%q) must be false — that is not a canonical stored span", notASpan)
		}
	}
}

// `h` is the one clock unit; `m` must keep meaning months, in the lead grammar
// exactly as in the recurrence grammar it shares its letters with.
func TestHourLeadsParseAndMStillMeansMonths(t *testing.T) {
	for input, want := range map[string]string{
		"5h": "5h", "5 hr": "5h", "12hours": "12h", "an hour": "1h",
		"6 hrs": "6h", "1 h": "1h",
	} {
		if result := Parse(input); result.Error != "" || result.Canonical != want {
			t.Fatalf("Parse(%q) = %#v, want %q", input, result, want)
		}
	}
	if !Clock("5h") || Clock("5m") || Clock("5d") {
		t.Fatal("only the hour unit is a clock span")
	}
	for input, want := range map[string]string{"1m": "1m", "6 months": "6m", "5 min": "", "5m": "5m"} {
		result := Parse(input)
		if want == "" {
			if result.Error == "" {
				t.Fatalf("Parse(%q) = %#v, want a refusal — minutes are not a lead unit", input, result)
			}
			continue
		}
		if result.Canonical != want {
			t.Fatalf("Parse(%q) = %#v, want %q", input, result, want)
		}
	}
	if _, ok := GateDate(date(t, "2026-06-01"), "5h"); ok {
		t.Fatal("a clock gate is an instant no date can express")
	}
	if duration, ok := Duration("5h"); !ok || duration != 5*time.Hour {
		t.Fatalf("Duration(5h) = %s (%v)", duration, ok)
	}
	if _, ok := Duration("5d"); ok {
		t.Fatal("a calendar span has no clock duration")
	}
}

func TestHumanizeReadsAsASpan(t *testing.T) {
	cases := map[string]string{
		"3w": "3 weeks", "1d": "1 day", "1m": "1 month", "2y": "2 years",
		"1h": "1 hour", "5h": "5 hours", "nonsense": "nonsense",
	}
	for span, want := range cases {
		if got, ok := Humanize(span); !ok || got != want {
			t.Fatalf("Humanize(%q) = %q (%v), want %q", span, got, ok, want)
		}
	}
	if _, ok := Humanize(""); ok {
		t.Fatal("a blank span renders as nothing")
	}
	if got, ok := Describe("3w"); !ok || got != "3 weeks before" {
		t.Fatalf("Describe(3w) = %q (%v)", got, ok)
	}
	if _, ok := Describe(""); ok {
		t.Fatal("a blank span has nothing to describe")
	}
}

func TestMonthAndYearLeadsClampLikeRecurrenceIntervals(t *testing.T) {
	cases := []struct {
		anchor, span, want string
	}{
		{"2026-03-31", "1m", "2026-02-28"},
		{"2024-03-31", "1m", "2024-02-29"},
		{"2026-11-01", "1y", "2025-11-01"},
		{"2026-11-01", "3w", "2026-10-11"},
		{"2026-01-15", "2m", "2025-11-15"},
		{"2024-02-29", "1y", "2023-02-28"},
		{"2026-03-01", "1d", "2026-02-28"},
		{"2026-01-01", "10y", "2016-01-01"},
	}
	for _, tc := range cases {
		got, ok := GateDate(date(t, tc.anchor), tc.span)
		if !ok || got.ISO() != tc.want {
			t.Fatalf("GateDate(%s, %s) = %s (%v), want %s", tc.anchor, tc.span, got.ISO(), ok, tc.want)
		}
	}
	// A reader derives no gate rather than raising on data Check will report.
	for _, span := range []string{"0w", "3 weeks", "", "+2w"} {
		if _, ok := GateDate(date(t, "2026-11-01"), span); ok {
			t.Fatalf("GateDate must refuse the uncanonical span %q", span)
		}
	}
}

func TestAnchorPrefersTheDeadline(t *testing.T) {
	deadline := date(t, "2026-11-01")
	scheduled := date(t, "2026-10-01")
	if got, ok := AnchorDate(deadline, scheduled); !ok || got != deadline {
		t.Fatalf("AnchorDate = %s (%v), want the deadline", got.ISO(), ok)
	}
	if got, ok := AnchorDate(temporal.Date{}, scheduled); !ok || got != scheduled {
		t.Fatalf("AnchorDate = %s (%v), want the available-from date", got.ISO(), ok)
	}
	if _, ok := AnchorDate(temporal.Date{}, temporal.Date{}); ok {
		t.Fatal("a task with no dates has no anchor")
	}
}

// A calendar lead holds its WALL date across a DST change and simply releases
// an hour earlier or later in UTC. US DST ends 2026-11-01 and begins
// 2026-03-08.
func TestACalendarLeadHoldsItsWallDateAcrossADSTChange(t *testing.T) {
	cases := []struct {
		anchor, span, wantDate, wantInstant string
	}{
		{"2026-11-10", "2w", "2026-10-27", "2026-10-27T06:00:00Z"},
		{"2026-03-20", "2w", "2026-03-06", "2026-03-06T07:00:00Z"},
		{"2026-11-01", "3w", "2026-10-11", "2026-10-11T06:00:00Z"},
	}
	for _, tc := range cases {
		gate, ok := GateDate(date(t, tc.anchor), tc.span)
		if !ok || gate.ISO() != tc.wantDate {
			t.Fatalf("GateDate(%s, %s) = %s, want %s", tc.anchor, tc.span, gate.ISO(), tc.wantDate)
		}
		value, err := temporal.NewValue(gate, "", "", 0, true)
		if err != nil {
			t.Fatalf("NewValue: %v", err)
		}
		instant, err := value.Instant(context(t, denver, time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)))
		if err != nil {
			t.Fatalf("Instant: %v", err)
		}
		if got := instant.UTC().Format(time.RFC3339); got != tc.wantInstant {
			t.Fatalf("the window for %s opens at %s, want %s", tc.anchor, got, tc.wantInstant)
		}
	}
}

// An all-day anchor resolves to the first instant of its date, so a clock lead
// opens partway through the previous evening.
func TestAClockLeadOnAnAllDayAnchorOpensTheEveningBefore(t *testing.T) {
	anchor, err := temporal.NewValue(date(t, "2026-06-01"), "", "", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	reader := context(t, denver, time.Date(2026, 5, 31, 18, 0, 0, 0, time.UTC))
	instant, ok := GateInstant(anchor, "5h", reader)
	if !ok {
		t.Fatal("a clock lead on an all-day anchor still has a gate")
	}
	// 19:00 on May 31 in Denver (MDT, UTC-6).
	if got := instant.UTC().Format(time.RFC3339); got != "2026-06-01T01:00:00Z" {
		t.Fatalf("GateInstant = %s, want 2026-06-01T01:00:00Z", got)
	}
	if got := temporal.DateOf(instant.In(reader.Timezone)).ISO(); got != "2026-05-31" {
		t.Fatalf("the window opens on %s, want 2026-05-31", got)
	}
}

func TestAClockLeadMeasuresFromATimedAnchorsOwnInstant(t *testing.T) {
	anchor, err := temporal.NewValue(date(t, "2026-06-01"), "09:00", denver, 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	reader := context(t, denver, time.Date(2026, 5, 31, 18, 0, 0, 0, time.UTC))
	instant, ok := GateInstant(anchor, "3h", reader)
	if !ok {
		t.Fatal("a timed anchor has a clock gate")
	}
	// 06:00 local, three hours before a 09:00 deadline.
	if got := instant.UTC().Format(time.RFC3339); got != "2026-06-01T12:00:00Z" {
		t.Fatalf("GateInstant = %s, want 2026-06-01T12:00:00Z", got)
	}
}

// Either side of local midnight: the window opens on the previous day when the
// duration reaches back past 00:00, and on the anchor's own day when it does not.
func TestAClockLeadLandsOnTheRightSideOfLocalMidnight(t *testing.T) {
	cases := []struct {
		local, span, want string
	}{
		{"00:30", "1h", "2026-05-31"},
		{"01:30", "1h", "2026-06-01"},
	}
	reader := context(t, denver, time.Date(2026, 5, 30, 18, 0, 0, 0, time.UTC))
	for _, tc := range cases {
		anchor, err := temporal.NewValue(date(t, "2026-06-01"), tc.local, denver, 0, true)
		if err != nil {
			t.Fatalf("NewValue: %v", err)
		}
		instant, ok := GateInstant(anchor, tc.span, reader)
		if !ok {
			t.Fatal("a timed anchor has a clock gate")
		}
		if got := temporal.DateOf(instant.In(reader.Timezone)).ISO(); got != tc.want {
			t.Fatalf("%s minus %s opens on %s, want %s", tc.local, tc.span, got, tc.want)
		}
	}
}

// A clock lead is a real duration, so a DST change inside the window MOVES the
// wall time — the opposite of a calendar lead, which holds its wall date.
func TestAClockLeadCrossingDSTKeepsItsDurationNotItsWallTime(t *testing.T) {
	cases := []struct {
		anchorDate, local, span, want, why string
	}{
		{"2026-11-01", "12:00", "5h", "2026-11-01T14:00:00Z",
			"12:00 MST is 19:00Z; five hours earlier is 08:00 MDT, an hour further back in wall terms"},
		{"2026-03-08", "06:00", "5h", "2026-03-08T07:00:00Z",
			"06:00 MDT is 12:00Z; five real hours earlier is 00:00 MST, a six-hour wall step"},
	}
	reader := context(t, denver, time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC))
	for _, tc := range cases {
		anchor, err := temporal.NewValue(date(t, tc.anchorDate), tc.local, denver, 0, true)
		if err != nil {
			t.Fatalf("NewValue: %v", err)
		}
		instant, ok := GateInstant(anchor, tc.span, reader)
		if !ok {
			t.Fatal("a timed anchor has a clock gate")
		}
		if got := instant.UTC().Format(time.RFC3339); got != tc.want {
			t.Fatalf("%s: GateInstant = %s, want %s (%s)", tc.anchorDate, got, tc.want, tc.why)
		}
	}
}

// The storable-range guard needs a date even for a clock span, and it must
// never claim a window opens LATER than it really can.
func TestDateBoundNeverOverstatesWhenAWindowOpens(t *testing.T) {
	anchor := date(t, "2026-06-01")
	cases := map[string]string{
		"3w": "2026-05-11", // identical to the gate date for a calendar span
		"1m": "2026-05-01",
		// A clock span rounds its duration UP to whole days and then takes one
		// more, so the bound is safe in every zone on earth.
		"5h":  "2026-05-30",
		"30h": "2026-05-29",
		"48h": "2026-05-29",
	}
	for span, want := range cases {
		got, ok := DateBound(anchor, span)
		if !ok || got.ISO() != want {
			t.Fatalf("DateBound(%s) = %s (%v), want %s", span, got.ISO(), ok, want)
		}
	}
	if _, ok := DateBound(anchor, "0w"); ok {
		t.Fatal("an uncanonical span has no bound")
	}

	// The bound must sit at or before the real gate for every zone on earth,
	// which is the property the write-path guard depends on.
	reader := context(t, "Pacific/Kiritimati", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	value, err := temporal.NewValue(anchor, "", "", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	for _, span := range []string{"1h", "5h", "23h", "24h", "48h"} {
		bound, _ := DateBound(anchor, span)
		instant, ok := GateInstant(value, span, reader)
		if !ok {
			t.Fatalf("GateInstant(%s): no gate", span)
		}
		actual := temporal.DateOf(instant.In(reader.Timezone))
		if actual.Before(bound) {
			t.Fatalf("DateBound(%s) = %s but the window really opens on %s",
				span, bound.ISO(), actual.ISO())
		}
	}
}

func TestDisplayRendersTheSpanBesideTheDateItDerives(t *testing.T) {
	anchor := date(t, "2026-11-01")
	cases := []struct {
		span      string
		hasAnchor bool
		want      string
	}{
		{"3w", true, "3 weeks before — opens 2026-10-11"},
		{"1d", true, "1 day before — opens 2026-10-31"},
		{"3w", false, "3 weeks before"},
		{"5h", true, "5 hours before"}, // a clock gate needs a zone, not a date
	}
	for _, tc := range cases {
		got, ok := DisplayDate(tc.span, anchor, tc.hasAnchor)
		if !ok || got != tc.want {
			t.Fatalf("DisplayDate(%q, anchor=%v) = %q (%v), want %q",
				tc.span, tc.hasAnchor, got, ok, tc.want)
		}
	}
	if _, ok := DisplayDate("", anchor, true); ok {
		t.Fatal("a blank span renders as nothing")
	}

	value, err := temporal.NewValue(date(t, "2026-06-01"), "", "", 0, true)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	reader := context(t, denver, time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC))
	got, ok := DisplayInstant("5h", value, reader)
	if !ok || got != "5 hours before — opens 2026-05-31 19:00" {
		t.Fatalf("DisplayInstant = %q (%v)", got, ok)
	}
	// A calendar span has no instant to render, so it falls back to the span.
	if got, ok := DisplayInstant("3w", value, reader); !ok || got != "3 weeks before" {
		t.Fatalf("DisplayInstant(3w) = %q (%v)", got, ok)
	}
}

// Every span Parse produces must satisfy Span, or the write path would store a
// value its own reader refuses.
func TestParseOutputAlwaysSatisfiesTheStoredGuard(t *testing.T) {
	inputs := []string{"3 weeks", "a week", "2 wks", "a quarter", "fortnight",
		"10 days ahead", "an hour", "12hours", "5h", "1m", "10y", "2 yrs", "3 mos"}
	for _, input := range inputs {
		canonical, ok := Canonical(input)
		if !ok {
			t.Fatalf("Parse(%q) was expected to yield a span", input)
		}
		if !Span(canonical) {
			t.Fatalf("Parse(%q) = %q, which Span refuses", input, canonical)
		}
		// And it is a fixed point: re-parsing a canonical span changes nothing.
		if again, ok := Canonical(canonical); !ok || again != canonical {
			t.Fatalf("Parse(%q) = %q, want it unchanged", canonical, again)
		}
	}
}
