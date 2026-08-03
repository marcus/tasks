package recur

import (
	"errors"
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
		{"a week", ".+", ".+1w"},
		{"each 2 weeks", ".+", ".+2w"},
		{"2,weeks", ".+", ".+2w"},
		{"2/weeks", ".+", ".+2w"},
		{"in 3 days", ".+", ".+3d"},
		{"every3days", ".+", ".+3d"},
		{"month", ".+", ".+1m"},
		{"weekly", "wat", "wat1w"},
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

func TestParseFriendlyIntervalUsesRubyTokenization(t *testing.T) {
	for _, input := range []string{"2 3 days", "2 3days", "2and3days", "2,3days"} {
		if got := Parse(input, ".+"); got.Error == "" {
			t.Fatalf("Parse(%q) = %#v, want rejection", input, got)
		}
	}
}

func TestParseFriendlyIntervalUsesRubyASCIIWhitespace(t *testing.T) {
	for _, input := range []string{
		"2\u00a0weeks",
		"2\u3000weeks",
		"2\u0085weeks",
		"\u00a0weekly",
		"weekly\u00a0",
		"2\u1680weeks",
	} {
		if got := Parse(input, ".+"); got.Error == "" {
			t.Fatalf("Parse(%q) = %#v, want Ruby-compatible rejection", input, got)
		}
	}
	for _, input := range []string{"2 weeks", "2\tweeks", "2\rweeks", "2\nweeks", "2\fweeks", "2\vweeks"} {
		if got := Parse(input, ".+"); got.Error != "" || got.Canonical != ".+2w" {
			t.Fatalf("Parse(%q) = %#v, want .+2w", input, got)
		}
	}
}

func TestParseUsesRubyFullUnicodeDowncase(t *testing.T) {
	if got := Parse("DA\u0130LY", ".+"); got.Error == "" {
		t.Fatalf("Parse(dotted I) = %#v, want Ruby-compatible rejection", got)
	}
	if got := Parse("wee\u212Aly", ".+"); got.Error != "" || got.Canonical != ".+1w" {
		t.Fatalf("Parse(Kelvin sign) = %#v, want .+1w", got)
	}
	if got := Parse("\u212AEEKLY", ".+"); got.Error == "" {
		t.Fatalf("Parse(leading Kelvin sign) = %#v, want Ruby-compatible rejection", got)
	}
}

func TestParseOffAndRejectsZeroOrGarbage(t *testing.T) {
	for _, input := range []string{"off", "none", "never", "clear", "no", "stop"} {
		if got := Parse(input, ".+"); got.Canonical != "off" || got.Error != "" {
			t.Fatalf("Parse(%q) = %#v, want off", input, got)
		}
	}
	for _, input := range []string{"++0d", "0 weeks", "2 frogs", "2 3 days", "2 3days", "", "d"} {
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
	// Calendar schedules now render through the same one renderer, so a value
	// that used to echo verbatim is glossed. An UNPARSEABLE value still echoes.
	for _, tc := range []struct{ cookie, want string }{
		{"w:mon", "every Monday"},
		{"w:mon,wed", "every Mon, Wed"},
		{"m:15", "monthly on the 15th"},
		{"m:last,2tue", "monthly on the last day and 2nd Tuesday"},
		{"y:07-04", "yearly on July 4"},
		{"y:11:3thu", "yearly on the 3rd Thursday of November"},
		{"2w:sat", "every 2 weeks on Saturday"},
		{"+w:fri", "every Friday (one hop)"},
		{"w:monday", "w:monday"},
	} {
		if got := Humanize(tc.cookie); got == nil || *got != tc.want {
			t.Fatalf("Humanize(%q) = %v, want %q", tc.cookie, got, tc.want)
		}
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

func TestStepPreservesRubyItalyReformCalendar(t *testing.T) {
	if got := Step(mustDate("1582-10-04"), big.NewInt(1), "d"); !got.Equal(mustDate("1582-10-15")) {
		t.Fatalf("Italy reform day step = %s, want 1582-10-15", got)
	}
	if got := Step(mustDate("1500-02-28"), big.NewInt(1), "d"); !got.Equal(mustDate("1500-02-29")) {
		t.Fatalf("Julian leap-day step = %s, want 1500-02-29", got)
	}
	if got := Step(mustDate("1582-09-10"), big.NewInt(1), "m"); !got.Equal(mustDate("1582-10-04")) {
		t.Fatalf("month step into reform gap = %s, want 1582-10-04", got)
	}
	if got, err := NextDate("+1d", mustDate("1582-10-04"), mustDate("1582-10-04")); err != nil || !got.Equal(mustDate("1582-10-15")) {
		t.Fatalf("reform next date = %s, %v; want 1582-10-15", got, err)
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

func TestCivilDateStringPreservesRubySignedYearPadding(t *testing.T) {
	if got, want := NewCivilDate(-1, 1, 1).String(), "-0001-01-01"; got != want {
		t.Fatalf("negative year string = %q, want %q", got, want)
	}
	got, err := NextDate("+1d", NewCivilDate(-1, 1, 1), NewCivilDate(-1, 1, 1))
	if err != nil || got.String() != "-0001-01-02" {
		t.Fatalf("negative year projection = %s, %v", got, err)
	}
}

// Expectations transcribed from Ruby Date.iso8601 / Date.new under the default
// Date::ITALY calendar; see porting/evidence/recur-interval-cookies/README.md.
func TestNewCheckedCivilDateMatchesRubyDateValidity(t *testing.T) {
	cases := []struct {
		year  int64
		month int
		day   int
		valid bool
	}{
		{2026, 1, 1, true},
		{2026, 13, 1, false},
		{2026, 0, 10, false},
		{2026, 1, 0, false},
		{2026, 4, 31, false},
		{2026, 2, 29, false},
		{2024, 2, 29, true},
		{1500, 2, 29, true}, // Julian leap rule before the reform
		{-44, 2, 29, true},
		{-45, 2, 29, false},
		{1582, 10, 4, true},
		{1582, 10, 5, false}, // the ten omitted reform days
		{1582, 10, 14, false},
		{1582, 10, 15, true},
	}
	for _, test := range cases {
		got, err := NewCheckedCivilDate(big.NewInt(test.year), test.month, test.day)
		if test.valid {
			if err != nil {
				t.Errorf("%d-%02d-%02d rejected: %v", test.year, test.month, test.day, err)
				continue
			}
			if want := NewCivilDate(test.year, test.month, test.day); !got.Equal(want) {
				t.Errorf("%s != %s", got, want)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidDate) {
			t.Errorf("%d-%02d-%02d accepted as %s (err %v), want ErrInvalidDate", test.year, test.month, test.day, got, err)
		}
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
