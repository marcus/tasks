package recur

import (
	"testing"
	"time"
)

func TestParseIntervalCookiesAndFriendlySpellings(t *testing.T) {
	cases := []struct {
		input, prefix, want string
	}{
		{".+1w", "+", ".+1w"},
		{" +2D ", ".+", "+2d"},
		{"weekly", ".+", ".+1w"},
		{"fortnightly", ".+", ".+2w"},
		{"2w", "+", "+2w"},
		{"every\t3 days", ".+", ".+3d"},
		{"every 3 days", ".+", ".+3d"},
		{"month", ".+", ".+1m"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := Parse(tc.input, tc.prefix)
			if got.Error != "" || got.Canonical != tc.want {
				t.Fatalf("Parse(%q) = %#v, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseOffAndRejectsZeroOrGarbage(t *testing.T) {
	for _, input := range []string{"off", "none", "never", "clear", "no", "stop"} {
		if got := Parse(input, ".+"); got.Canonical != "off" || got.Error != "" {
			t.Fatalf("Parse(%q) = %#v, want off", input, got)
		}
	}
	for _, input := range []string{"++0d", "0 weeks", "2 frogs", "", "d", "w:mon"} {
		if got := Parse(input, ".+"); got.Error == "" {
			t.Fatalf("Parse(%q) = %#v, want rejection", input, got)
		}
	}
}

func TestHumanizeAndNextDate(t *testing.T) {
	if got, want := Humanize("++2w"), "every 2 weeks from the scheduled date (catching up)"; got != want {
		t.Fatalf("Humanize = %q, want %q", got, want)
	}
	from := mustDate(t, "2026-01-31")
	if got, err := NextDate("+1m", from, mustDate(t, "2026-02-01")); err != nil || !got.Equal(mustDate(t, "2026-02-28")) {
		t.Fatalf("month next date = %s, %v", got, err)
	}
	if got, err := NextDate("++1w", mustDate(t, "2026-01-01"), mustDate(t, "2026-01-15")); err != nil || !got.Equal(mustDate(t, "2026-01-15")) {
		t.Fatalf("catch-up next date = %s, %v; want today", got, err)
	}
}

func TestStepMonthAndYearClampProperty(t *testing.T) {
	for year := 2024; year <= 2028; year++ {
		for month := time.January; month <= time.December; month++ {
			for day := 1; day <= daysInMonth(year, month); day++ {
				from := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
				got := Step(from, 1, "m")
				if got.Day() > daysInMonth(got.Year(), got.Month()) {
					t.Fatalf("Step(%s, 1m) produced invalid date %s", from, got)
				}
				if got.Day() != min(day, daysInMonth(got.Year(), got.Month())) {
					t.Fatalf("Step(%s, 1m) = %s, want clamped day", from, got)
				}
			}
		}
	}
	if got := Step(mustDate(t, "2024-02-29"), 1, "y"); !got.Equal(mustDate(t, "2025-02-28")) {
		t.Fatalf("leap-year clamp = %s", got)
	}
}

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return date
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
