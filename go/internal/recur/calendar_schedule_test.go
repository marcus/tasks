package recur

import (
	"regexp"
	"strings"
	"testing"
)

// day is a stored-spelling date, which is all any fixture in this file needs.
func day(t *testing.T, iso string) CivilDate {
	t.Helper()
	parsed, ok := parseStoredDate(iso)
	if !ok {
		t.Fatalf("%q is not a storable date", iso)
	}
	return parsed
}

func parseTable(t *testing.T, table map[string]string) {
	t.Helper()
	for input, want := range table {
		t.Run(input, func(t *testing.T) {
			got := Parse(input, ".+")
			if got.Error != "" || got.Canonical != want {
				t.Fatalf("Parse(%q) = %#v, want %q", input, got, want)
			}
		})
	}
}

// -- grammar productions ----------------------------------------------------

func TestParsesEveryCanonicalProduction(t *testing.T) {
	parseTable(t, map[string]string{
		// weekly := [N] "w:" dayset
		"w:mon": "w:mon", "w:mon,wed,fri": "w:mon,wed,fri", "w:sat,sun": "w:sat,sun",
		"2w:mon": "2w:mon", "4w:tue,thu": "4w:tue,thu",
		// monthly := [N] "m:" mspec ("," mspec)*
		"m:1": "m:1", "m:31": "m:31", "m:1,15": "m:1,15", "m:last": "m:last",
		"m:2tue": "m:2tue", "m:5fri": "m:5fri", "m:lastfri": "m:lastfri",
		"m:15,lastfri": "m:15,lastfri", "3m:last": "3m:last",
		// yearly := [N] "y:" MM "-" DD | [N] "y:" MM ":" ordday
		"y:07-04": "y:07-04", "y:02-29": "y:02-29", "y:11:3thu": "y:11:3thu",
		"y:05:lastmon": "y:05:lastmon", "2y:07-04": "2y:07-04",
		// prefix := "+"
		"+w:mon": "+w:mon", "+m:15": "+m:15", "+2w:mon,thu": "+2w:mon,thu",
		"+y:07-04": "+y:07-04",
	})
}

func TestParsesNaturalPhrases(t *testing.T) {
	parseTable(t, map[string]string{
		"every monday":                 "w:mon",
		"every Monday":                 "w:mon",
		"mondays":                      "w:mon",
		"every mon wed fri":            "w:mon,wed,fri",
		"every mon, wed, fri":          "w:mon,wed,fri",
		"weekdays":                     "w:mon,tue,wed,thu,fri",
		"every weekday":                "w:mon,tue,wed,thu,fri",
		"weekends":                     "w:sat,sun",
		"every week on monday":         "w:mon",
		"every 2 weeks on monday":      "2w:mon",
		"every 2 weeks on mon and thu": "2w:mon,thu",
		"monthly on the 2nd tuesday":   "m:2tue",
		"2nd tuesday of the month":     "m:2tue",
		// An explicit monthly scope disambiguates a bare cardinal.
		"2 tuesdays of the month":       "m:2tue",
		"monthly on the 15th":           "m:15",
		"1st of the month":              "m:1",
		"the 1st and 15th of the month": "m:1,15",
		"last day of the month":         "m:last",
		"2nd tuesday":                   "m:2tue",
		"second tuesday of the month":   "m:2tue",
		"last friday of the month":      "m:lastfri",
		"every 2 months on the 15th":    "2m:15",
		"every july 4":                  "y:07-04",
		"every july 4th":                "y:07-04",
		"4 july":                        "y:07-04",
		"3rd thursday of november":      "y:11:3thu",
		"every 2 years on july 4":       "2y:07-04",
	})
}

// The bare-interval path is untouched by the calendar grammar.
func TestNaturalIntervalPhrasesStillProduceCookies(t *testing.T) {
	parseTable(t, map[string]string{
		"weekly": ".+1w", "daily": ".+1d", "every day": ".+1d", "every month": ".+1m",
		"every 2 weeks": ".+2w", "biweekly": ".+2w", "quarterly": ".+3m",
	})
}

// -- normalization ----------------------------------------------------------

func TestSynonymousInputsShareOneCanonicalSpelling(t *testing.T) {
	groups := []struct {
		canonical string
		inputs    []string
	}{
		{"w:mon", []string{"W:MON", "1w:mon", "w:monday", "w:mondays", "every monday", " every Monday "}},
		{"w:mon,wed,fri", []string{"w:fri,mon,wed", "w:wed,mon,fri,mon", "every fri wed mon"}},
		{"w:mon,tue,wed,thu,fri", []string{"weekdays", "every weekday", "w:tue,mon,fri,thu,wed"}},
		{"2w:mon", []string{"2w:monday", "every 2 weeks on monday", "2W:MON"}},
		{"m:1,15", []string{"m:15,1", "m:15,1,15", "the 1st and 15th of the month"}},
		{"m:2tue", []string{"m:2tue", "2nd tuesday", "second tuesday", "m:2tuesday"}},
		{"m:lastfri", []string{"m:lastfri", "last friday of the month", "m:lastfriday"}},
		{"y:07-04", []string{"y:7-4", "y:07-4", "every july 4", "every July 4th"}},
		{"m:15,lastfri", []string{"m:lastfri,15"}},
	}
	for _, group := range groups {
		for _, input := range group.inputs {
			if got := Parse(input, ".+"); got.Error != "" || got.Canonical != group.canonical {
				t.Fatalf("Parse(%q) = %#v, want %q", input, got, group.canonical)
			}
		}
	}
}

func TestDaySetsSortIntoWeekOrderNotAlphabetical(t *testing.T) {
	if got := Parse("w:sun,sat,fri,thu,wed,tue,mon", ".+"); got.Canonical != "w:mon,tue,wed,thu,fri,sat,sun" {
		t.Fatalf("day set = %#v", got)
	}
}

func TestMonthlySpecsSortNumericThenLastThenOrdinalWeekdays(t *testing.T) {
	if got := Parse("m:lastfri,2tue,last,15,1", ".+"); got.Canonical != "m:1,15,last,2tue,lastfri" {
		t.Fatalf("monthly specs = %#v", got)
	}
}

// -- rejection with reasons -------------------------------------------------

func TestRejectsWithReasons(t *testing.T) {
	cases := map[string]string{
		".+w:mon":                   `interval prefix`,
		".+m:15":                    `interval prefix`,
		"++w:mon":                   `interval prefix`,
		"++y:07-04":                 `interval prefix`,
		"0w:mon":                    `interval must be at least 1`,
		"w:funday":                  `unknown day of week`,
		"w:mon,funday":              `unknown day of week`,
		"w:mon,":                    `at least one day`,
		"m:0":                       `day of month must be`,
		"m:32":                      `day of month must be`,
		"m:6fri":                    `ordinal weekdays run from 1 to 5`,
		"m:0fri":                    `ordinal weekdays run from 1 to 5`,
		"m:bananas":                 `unrecognized monthly rule`,
		"y:13-01":                   `invalid yearly date`,
		"y:02-30":                   `invalid yearly date`,
		"y:04-31":                   `invalid yearly date`,
		"y:11:6thu":                 `ordinal weekdays run from 1 to 5`,
		"y:bananas":                 `unrecognized yearly rule`,
		"monthly on monday":         `weekdays needs a weekly schedule`,
		"every 2 weeks on the 15th": `monthly schedule`,
		"every 2 years on monday":   `weekly schedule`,
		"every 3 days on monday":    `daily schedule`,
		"every 2 months on july 4":  `yearly schedule`,
		"every february 30":         `February has no day 30`,
		"":                          `no schedule given`,
		"bananas":                   `unrecognized schedule`,
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got := Parse(input, ".+")
			if got.Error == "" {
				t.Fatalf("Parse(%q) = %#v, want a refusal", input, got)
			}
			if !strings.Contains(got.Error, want) {
				t.Fatalf("Parse(%q) refusal = %q, want it to mention %q", input, got.Error, want)
			}
		})
	}
}

// Parsing lowercases, but a rejection is read by the person who typed it, so it
// must quote their spelling — not the parser's working copy.
func TestRejectionsEchoTheInputVerbatim(t *testing.T) {
	cases := map[string]string{
		"Pay Rent":       `"Pay Rent"`,
		"2nd Tuesdayish": `"2nd Tuesdayish"`,
		// A sub-token the caller still typed as written.
		"W:FUNDAY": `"funday"`,
	}
	for input, quoted := range cases {
		reason := Parse(input, ".+").Error
		if reason == "" {
			t.Fatalf("Parse(%q) was expected to be rejected", input)
		}
		if !strings.Contains(strings.ToLower(reason), strings.ToLower(quoted)) {
			t.Fatalf("Parse(%q) refusal = %q, want it to quote %s", input, reason, quoted)
		}
	}
	if got := Explain("Pay Rent", day(t, "2026-07-28"), 5, ""); got.Input != "Pay Rent" || got.HasCanonical {
		t.Fatalf("Explain of an unparsable input = %#v", got)
	}
}

// "every 2 tuesdays" reads as a cadence, not as "the 2nd Tuesday" — and the
// ordinal already has its own spelling, so the input is declined.
func TestBareCardinalBeforeAWeekdayIsAmbiguousAndRejected(t *testing.T) {
	cadence := regexp.MustCompile(`every \d+ weeks on \w+`)
	monthly := regexp.MustCompile(`\w+ of the month`)
	for _, input := range []string{"every 2 tuesdays", "2 tuesdays", "every 3 mondays", "3 mon"} {
		reason := Parse(input, ".+").Error
		if reason == "" {
			t.Fatalf("Parse(%q) was expected to be rejected", input)
		}
		if !strings.Contains(reason, "is ambiguous") {
			t.Fatalf("Parse(%q) refusal = %q", input, reason)
		}
		if !cadence.MatchString(reason) || !monthly.MatchString(reason) {
			t.Fatalf("Parse(%q) refusal = %q, want both spellings offered", input, reason)
		}
	}
}

func TestOrdinalMarkedWeekdaysStillParse(t *testing.T) {
	for _, input := range []string{"2nd tuesday", "second tuesday", "2nd tuesday of the month",
		"monthly on the 2nd tuesday", "2 tuesdays of the month"} {
		if got := Parse(input, ".+"); got.Canonical != "m:2tue" {
			t.Fatalf("Parse(%q) = %#v, want m:2tue", input, got)
		}
	}
}

func TestDotPlusPrefixOnACalendarFormExplainsTheDefault(t *testing.T) {
	reason := Parse(".+w:mon", ".+").Error
	if !strings.Contains(reason, "already advances to the next occurrence after today") {
		t.Fatalf("refusal = %q", reason)
	}
	if !strings.Contains(reason, `"+"`) {
		t.Fatalf("refusal = %q, want it to offer the one-hop prefix", reason)
	}
}

// Every surface goes through Parse, so interval and calendar input reach the
// store through the same entry point and normalize the same way.
func TestOneParserReadsBothShapes(t *testing.T) {
	for _, input := range []string{"every monday", "w:mon", "weekdays", "m:15", "+y:07-04"} {
		if got := Parse(input, ".+"); got.Error != "" {
			t.Fatalf("Parse(%q) = %#v", input, got)
		}
	}
	if got := Parse("weekly", ".+"); got.Canonical != ".+1w" {
		t.Fatalf("Parse(weekly) = %#v", got)
	}
	if got := Parse("2w", "+"); got.Canonical != "+2w" {
		t.Fatalf("Parse(2w) = %#v", got)
	}
	if got := Parse("off", ".+"); got.Canonical != "off" {
		t.Fatalf("Parse(off) = %#v", got)
	}
}

// Bare calendar input is catch-up regardless of the interval default prefix.
func TestCalendarInputIgnoresTheIntervalDefaultPrefix(t *testing.T) {
	if got := Parse("every monday", "+"); got.Canonical != "w:mon" {
		t.Fatalf("Parse = %#v", got)
	}
	if got := Parse("w:mon", ".+"); got.Canonical != "w:mon" {
		t.Fatalf("Parse = %#v", got)
	}
}

func TestIntervalRejectionsAreUnchanged(t *testing.T) {
	for _, input := range []string{"", "bananas", "1", "w", "2x", "+0d", ".+0w", "1.5w", "-2w"} {
		if got := Parse(input, ".+"); got.Error == "" {
			t.Fatalf("Parse(%q) = %#v, want a refusal", input, got)
		}
	}
}

// -- stored-form validation -------------------------------------------------

func TestCookiePredicateAcceptsCanonicalCalendarForms(t *testing.T) {
	for _, stored := range []string{"w:mon", "w:mon,wed,fri", "2w:mon", "m:15", "m:last", "m:2tue",
		"m:lastfri", "y:07-04", "y:11:3thu", "+w:mon", "3m:1,15", ".+1w", "++2d"} {
		if !Cookie(stored) {
			t.Fatalf("Cookie(%q) must accept a stored value", stored)
		}
	}
}

// Cookie validates what is already on disk, so it is strict: anything the
// parser would rewrite is not a stored form.
func TestCookiePredicateRejectsNonCanonicalSpellings(t *testing.T) {
	for _, input := range []string{"weekly", "2w", "1w:mon", "W:MON", "w:monday", "w:wed,mon",
		"m:15,1", "y:7-4", ".+w:mon", "w:", "m:32"} {
		if Cookie(input) {
			t.Fatalf("Cookie(%q) must refuse a non-canonical spelling", input)
		}
	}
}

func TestParseOutputAlwaysSatisfiesCookiePredicate(t *testing.T) {
	for _, input := range []string{"every monday", "weekdays", "every 2 weeks on mon and thu",
		"last day of the month", "2nd tuesday", "every july 4", "3rd thursday of november",
		"weekly", "2w", "w:sun,mon", "+m:31", "m:lastfri,15"} {
		got := Parse(input, ".+")
		if got.Error != "" {
			t.Fatalf("Parse(%q) = %#v", input, got)
		}
		if !Cookie(got.Canonical) {
			t.Fatalf("Parse(%q) = %q, which Cookie refuses", input, got.Canonical)
		}
	}
}

func TestCalendarPredicateSeparatesTheTwoShapes(t *testing.T) {
	if !Calendar("w:mon") || !Calendar("+2m:1,15") {
		t.Fatal("a canonical calendar schedule is a calendar schedule")
	}
	if Calendar(".+1w") || Calendar("weekly") {
		t.Fatal("an interval cookie is not a calendar schedule")
	}
}
