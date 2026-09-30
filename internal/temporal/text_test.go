package temporal

import (
	"testing"
	"time"
)

func TestParseTextPeelsTheZoneAndFoldWordsOffTheEnd(t *testing.T) {
	context := losAngelesAt(t, time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC))
	options := ParseOptions{Today: context.LocalDate(), Order: MDY, Context: &context}
	cases := []struct {
		input string
		want  Value
	}{
		{"fri 4pm", Value{Date: Date{2026, 7, 24}, LocalTime: "16:00"}},
		{"fri 4pm Europe/Berlin", Value{Date: Date{2026, 7, 24}, LocalTime: "16:00", Timezone: "Europe/Berlin"}},
		{"fri 4pm floating", Value{Date: Date{2026, 7, 24}, LocalTime: "16:00"}},
		{"fri 4pm UTC", Value{Date: Date{2026, 7, 24}, LocalTime: "16:00", Timezone: "UTC"}},
		{"2026-11-01 01:30 America/New_York fold=later",
			Value{Date: Date{2026, 11, 1}, LocalTime: "01:30", Timezone: "America/New_York", Fold: 1}},
		{"7/15", Value{Date: Date{2027, 7, 15}}},
		{"2026/08/09", Value{Date: Date{2026, 8, 9}}},
	}
	for _, c := range cases {
		got, err := ParseText(c.input, options)
		if err != nil {
			t.Fatalf("ParseText(%q): %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("ParseText(%q) = %+v, want %+v", c.input, got, c.want)
		}
	}
}

func TestParseTextHonorsTheDateOrder(t *testing.T) {
	today := Date{2026, 7, 20}
	mdy, err := ParseText("10/2", ParseOptions{Today: today, Order: MDY})
	if err != nil || mdy.Date != (Date{2026, 10, 2}) {
		t.Fatalf("mdy: %+v, %v", mdy, err)
	}
	dmy, err := ParseText("10/2", ParseOptions{Today: today, Order: DMY})
	if err != nil || dmy.Date != (Date{2027, 2, 10}) {
		t.Fatalf("dmy: %+v, %v", dmy, err)
	}
}

func TestExplainRendersAValueOrAReason(t *testing.T) {
	context := losAngelesAt(t, time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC))
	options := ParseOptions{Today: context.LocalDate(), Order: MDY}

	timed := Explain("fri 4pm", options, context)
	if !timed.HasValue || timed.Error != "" {
		t.Fatalf("fri 4pm: %+v", timed)
	}
	if timed.Value != (Value{Date: Date{2026, 10, 2}, LocalTime: "16:00"}) {
		t.Errorf("fri 4pm value: %+v", timed.Value)
	}
	if timed.Human != "Fri 2 Oct, 4:00p" {
		t.Errorf("fri 4pm human: %q", timed.Human)
	}

	nextYear := Explain("2027-01-04", options, context)
	if nextYear.Human != "Mon 4 Jan 2027" {
		t.Errorf("another year's date must carry its year: %q", nextYear.Human)
	}

	junk := Explain("blursday", options, context)
	if junk.HasValue || junk.Error != "unrecognized date: blursday" || junk.Input != "blursday" {
		t.Errorf("blursday: %+v", junk)
	}

	gap := Explain("2027-03-14 02:30 America/Los_Angeles", options, context)
	if gap.HasValue || gap.Error == "" {
		t.Errorf("a DST-gap wall time must be refused with the engine's reason: %+v", gap)
	}
}

func TestValueLabelFollowsTheTimeFormatAndMarksTheLaterFold(t *testing.T) {
	context := testContext(t, "Etc/UTC", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	context.TimeFormat = 24
	value := Value{Date: Date{2026, 11, 1}, LocalTime: "01:30", Timezone: "America/New_York", Fold: 1}
	if got := context.ValueLabel(value); got != "Sun 1 Nov, 01:30 America/New_York (later fold)" {
		t.Errorf("label: %q", got)
	}
}
