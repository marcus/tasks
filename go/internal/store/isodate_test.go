package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// The table is the recorded Ruby oracle, transcribed from
// porting/evidence/store-snapshot-items/ruby/date-iso8601-grammar.json, which
// was captured under today = 2026-08-02. An empty want means Ruby's to_date
// returned nil.
func TestISODateMatchesTheRecordedRubyGrammar(t *testing.T) {
	today := time.Date(2026, time.August, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		input string
		want  string
	}{
		{input: "2026-08-02", want: "2026-08-02"},
		{input: "20260802", want: "2026-08-02"},
		{input: "-2026-08-02", want: "-2026-08-02"},
		{input: "+2026-08-02", want: "2026-08-02"},
		{input: "2026-8-2", want: ""},
		{input: "2026-08-2", want: ""},
		{input: "2026-08", want: "2026-08-01"},
		{input: "2026", want: ""},
		{input: "202608", want: ""},
		{input: "2026-215", want: "2026-08-03"},
		{input: "2026215", want: "2026-08-03"},
		{input: "2026-000", want: ""},
		{input: "2026-366", want: ""},
		{input: "2024-366", want: "2024-12-31"},
		{input: "2025-366", want: ""},
		{input: "2026-W31-7", want: "2026-08-02"},
		{input: "2026W317", want: "2026-08-02"},
		{input: "2026-W01-1", want: "2025-12-29"},
		{input: "2026W011", want: "2025-12-29"},
		{input: "2026-W31", want: ""},
		{input: "2026-W00-1", want: ""},
		{input: "2026-W54-1", want: ""},
		{input: "2026-W31-0", want: ""},
		{input: "2026-W31-8", want: ""},
		{input: "2026-13-01", want: ""},
		{input: "2026-00-01", want: ""},
		{input: "2026-02-30", want: ""},
		{input: "2024-02-29", want: "2024-02-29"},
		{input: "2025-02-29", want: ""},
		{input: "2026-08-02T10:11:12", want: "2026-08-02"},
		{input: "2026-08-02T10:11:12.5", want: "2026-08-02"},
		{input: "2026-08-02T10:11:12Z", want: "2026-08-02"},
		{input: "2026-08-02T10:11:12+05:00", want: "2026-08-02"},
		{input: "20260802T101112Z", want: "2026-08-02"},
		{input: "2026-215T10:11", want: "2026-08-03"},
		{input: "2026-W31-7T10:11", want: "2026-08-02"},
		{input: "2026-08-02T25:00:00", want: "2026-08-02"},
		{input: "2026-08-02T10", want: ""},
		{input: "2026-08-02Z", want: ""},
		{input: "--08-02", want: "2026-08-02"},
		{input: "--0802", want: "2026-08-02"},
		{input: "---02", want: "2026-08-02"},
		{input: "-08-02", want: "1992-02-01"},
		{input: "-W31-7", want: "2026-08-02"},
		{input: "-W31", want: ""},
		{input: "-215", want: "2026-08-03"},
		{input: "08-02", want: "2008-02-01"},
		{input: "", want: ""},
		{input: " ", want: ""},
		{input: "2026-08-02 ", want: "2026-08-02"},
		{input: " 2026-08-02", want: "2026-08-02"},
		{input: "\t2026-08-02\n", want: "2026-08-02"},
		{input: "2026-08-02\n", want: "2026-08-02"},
		{input: "2026-08-02 10:11:12", want: ""},
		{input: "2026-08-02\u00a0", want: ""},
		{input: "02-08-2026", want: ""},
		{input: "2026/08/02", want: ""},
		{input: "x", want: ""},
		{input: "2026-08-02x", want: ""},
		{input: "x2026-08-02", want: ""},
	}

	for _, testCase := range cases {
		raw, err := json.Marshal(testCase.input)
		if err != nil {
			t.Fatalf("marshal %q: %v", testCase.input, err)
		}
		parsed := isoDate(raw, today)
		got := ""
		if parsed != nil {
			got = rubyDateString(*parsed)
		}
		if got != testCase.want {
			t.Errorf("isoDate(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

// Ruby's Date.iso8601 parses under Date::ITALY, so October 5th through 14th of
// 1582 do not exist and pre-reform dates are Julian. The accept and reject sets
// are Ruby's; the one place the port cannot follow is a Julian-only leap day,
// which time.Time normalizes because it is proleptic Gregorian (td-f2665e).
func TestISODateFollowsTheItalianCalendarReform(t *testing.T) {
	today := time.Date(2026, time.August, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		input string
		want  string
	}{
		{input: "1582-10-04", want: "1582-10-04"},
		{input: "1582-10-05", want: ""},
		{input: "1582-10-14", want: ""},
		{input: "1582-10-15", want: "1582-10-15"},
		{input: "1900-02-29", want: ""},
		{input: "1500-02-28", want: "1500-02-28"},
		{input: "1500-02-29", want: "1500-03-01"}, // Ruby: 1500-02-29 — td-f2665e
	}

	for _, testCase := range cases {
		raw, err := json.Marshal(testCase.input)
		if err != nil {
			t.Fatalf("marshal %q: %v", testCase.input, err)
		}
		parsed := isoDate(raw, today)
		got := ""
		if parsed != nil {
			got = rubyDateString(*parsed)
		}
		if got != testCase.want {
			t.Errorf("isoDate(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

// TestISODateCompletesTruncatedFormsFromTheSuppliedToday proves the injected
// clock is the only source of today, so a read is reproducible.
//
// Provenance of the expectations: the completion rule is the recorded oracle's
// (elements more significant than the first supplied one come from today), and
// the 2026-08-02 rows are Ruby's own captured output. The other rows are that
// rule applied by hand — Date.today is answered in C and cannot be stubbed from
// Ruby, and no faketime is available here, so they are calendar arithmetic, not
// a Go result copied into an expectation: 1999-01-04 was a Monday, so ISO week
// 31 of 1999 begins 1999-08-02 and its Sunday is 1999-08-08; day 215 of the
// non-leap year 1999 is 1999-08-03.
func TestISODateCompletesTruncatedFormsFromTheSuppliedToday(t *testing.T) {
	cases := []struct {
		today string
		input string
		want  string
	}{
		{today: "2026-08-02", input: "---02", want: "2026-08-02"},
		{today: "1999-03-17", input: "---02", want: "1999-03-02"},
		{today: "1999-03-17", input: "--08-02", want: "1999-08-02"},
		{today: "1999-03-17", input: "-215", want: "1999-08-03"},
		{today: "1999-03-17", input: "-W31-7", want: "1999-08-08"},
		{today: "2000-02-29", input: "---29", want: "2000-02-29"},
	}

	for _, testCase := range cases {
		today, err := time.Parse("2006-01-02", testCase.today)
		if err != nil {
			t.Fatalf("today %q: %v", testCase.today, err)
		}
		raw, err := json.Marshal(testCase.input)
		if err != nil {
			t.Fatalf("marshal %q: %v", testCase.input, err)
		}
		parsed := isoDate(raw, today)
		if parsed == nil {
			t.Fatalf("isoDate(%q) under %s = nil, want %s", testCase.input, testCase.today, testCase.want)
		}
		if got := rubyDateString(*parsed); got != testCase.want {
			t.Errorf("isoDate(%q) under %s = %q, want %q", testCase.input, testCase.today, got, testCase.want)
		}
	}
}

// rubyDateString spells a date the way Ruby's Date#iso8601 does.
func rubyDateString(value time.Time) string {
	year, sign := value.Year(), ""
	if year < 0 {
		sign, year = "-", -year
	}
	return fmt.Sprintf("%s%04d-%02d-%02d", sign, year, int(value.Month()), value.Day())
}
