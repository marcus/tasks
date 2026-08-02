package recur

import (
	"fmt"
	"math/big"
	"testing"
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
	if got, want := Humanize("++2w"), "every 2 weeks from the scheduled date (catching up)"; got == nil || *got != want {
		t.Fatalf("Humanize = %v, want %q", got, want)
	}
	if got := Humanize(" \t "); got != nil {
		t.Fatalf("Humanize(blank) = %q, want nil", *got)
	}
	if got, want := Humanize("w:mon"), "w:mon"; got == nil || *got != want {
		t.Fatalf("Humanize(non-interval) = %v, want %q", got, want)
	}
	from := mustDate("2026-01-31")
	if got, err := NextDate("+1m", from, mustDate("2026-02-01")); err != nil || !got.Equal(mustDate("2026-02-28")) {
		t.Fatalf("month next date = %s, %v", got, err)
	}
	if got, err := NextDate("++1w", mustDate("2026-01-01"), mustDate("2026-01-15")); err != nil || !got.Equal(mustDate("2026-01-15")) {
		t.Fatalf("catch-up next date = %s, %v; want today", got, err)
	}
}

func TestStepMonthAndYearClampProperty(t *testing.T) {
	for year := 2024; year <= 2028; year++ {
		for month := 1; month <= 12; month++ {
			for day := 1; day <= daysInMonth(big.NewInt(int64(year)), month); day++ {
				from := NewCivilDate(int64(year), month, day)
				got := Step(from, big.NewInt(1), "m")
				if got.Day > daysInMonth(got.Year, got.Month) {
					t.Fatalf("Step(%s, 1m) produced invalid date %s", from, got)
				}
				if got.Day != min(day, daysInMonth(got.Year, got.Month)) {
					t.Fatalf("Step(%s, 1m) = %s, want clamped day", from, got)
				}
			}
		}
	}
	if got := Step(mustDate("2024-02-29"), big.NewInt(1), "y"); !got.Equal(mustDate("2025-02-28")) {
		t.Fatalf("leap-year clamp = %s", got)
	}
}

func TestArbitrarySizeCountsPreserveRubyProjection(t *testing.T) {
	const count = "999999999999999999999999999999999"
	if got := Parse(count+" days", ".+"); got.Error != "" || got.Canonical != ".+"+count+"d" {
		t.Fatalf("Parse huge interval = %#v", got)
	}
	if got, want := Humanize(".+"+count+"d"), "every "+count+" days from completion"; got == nil || *got != want {
		t.Fatalf("Humanize huge interval = %v, want %q", got, want)
	}
	if got, err := NextDate("+"+count+"d", mustDate("2026-01-01"), mustDate("2026-01-01")); err != nil || got.String() != "2737907006988507635338165741226-09-01" {
		t.Fatalf("huge day projection = %s, %v", got.String(), err)
	}
	if got, err := NextDate("+"+count+"m", mustDate("2026-01-31"), mustDate("2026-01-31")); err != nil || got.String() != "83333333333333333333333333335359-04-30" {
		t.Fatalf("huge month projection = %s, %v", got.String(), err)
	}
	if got, err := NextDate("+"+count+"y", mustDate("2026-01-31"), mustDate("2026-01-31")); err != nil || got.String() != "1000000000000000000000000000002025-01-31" {
		t.Fatalf("huge year projection = %s, %v", got.String(), err)
	}
}

func mustDate(value string) CivilDate {
	var year int64
	var month, day int
	if _, err := fmt.Sscanf(value, "%d-%d-%d", &year, &month, &day); err != nil {
		panic(err)
	}
	return NewCivilDate(year, month, day)
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
