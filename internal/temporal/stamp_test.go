package temporal

import (
	"testing"
	"time"
)

func stampContext(t *testing.T, zone string, timeFormat int) Context {
	t.Helper()
	now := time.Date(2026, time.August, 28, 0, 46, 44, 0, time.UTC)
	context, err := NewContext(now, zone, timeFormat)
	if err != nil {
		t.Fatalf("NewContext(%s): %v", zone, err)
	}
	return context
}

// One instant, four readers. The stored spelling never moves; the rendering is
// entirely a property of who is looking.
func TestStampLabelProjectsOneInstantForEveryReader(t *testing.T) {
	const stored = "2026-08-28T00:46:44Z"
	for _, testCase := range []struct {
		zone       string
		timeFormat int
		want       string
	}{
		{"America/Los_Angeles", 12, "thu 08-27 5:46p"},
		{"America/Los_Angeles", 24, "thu 08-27 17:46"},
		{"Asia/Tokyo", 12, "fri 08-28 9:46a"},
		{"Etc/UTC", 24, "fri 08-28 00:46"},
	} {
		context := stampContext(t, testCase.zone, testCase.timeFormat)
		if got := context.StampLabel(stored); got != testCase.want {
			t.Errorf("StampLabel in %s/%d = %q, want %q",
				testCase.zone, testCase.timeFormat, got, testCase.want)
		}
	}
}

// Noon and midnight are where a 12-hour clock goes wrong, so they are asserted
// rather than assumed.
func TestStampLabelSpellsNoonAndMidnight(t *testing.T) {
	context := stampContext(t, "Etc/UTC", 12)
	if got := context.StampLabel("2026-08-28T12:00:00Z"); got != "fri 08-28 12:00p" {
		t.Errorf("noon = %q", got)
	}
	if got := context.StampLabel("2026-08-28T00:00:00Z"); got != "fri 08-28 12:00a" {
		t.Errorf("midnight = %q", got)
	}
}

// A stamp this build cannot read is still the record's answer to "when", so it
// is printed as stored rather than dropped or blanked.
func TestStampLabelFallsBackToTheStoredSpelling(t *testing.T) {
	context := stampContext(t, "America/Los_Angeles", 12)
	for _, stored := range []string{"", "not a stamp", "2026-13-45T99:99:99Z", "2026-08-28"} {
		if got := context.StampLabel(stored); got != stored {
			t.Errorf("StampLabel(%q) = %q, want the stored spelling back", stored, got)
		}
	}
}

// A zero context is what a surface holds when the configured zone could not be
// loaded at all. It must still render the field.
func TestStampLabelWithoutAZoneReturnsTheStoredSpelling(t *testing.T) {
	var context Context
	const stored = "2026-08-28T00:46:44Z"
	if got := context.StampLabel(stored); got != stored {
		t.Errorf("StampLabel with no zone = %q, want %q", got, stored)
	}
}

// A stamp from another year says so. A rendering that made a 2023 handoff read
// exactly like yesterday's would not be terse, it would be wrong.
func TestStampLabelNamesTheYearOnlyWhenItIsNotTheReadersOwn(t *testing.T) {
	context := stampContext(t, "America/Los_Angeles", 12)
	if got := context.StampLabel("2026-08-28T00:46:44Z"); got != "thu 08-27 5:46p" {
		t.Errorf("a stamp in the reader's own year = %q, want it terse", got)
	}
	if got := context.StampLabel("2023-08-28T00:46:44Z"); got != "sun 2023-08-27 5:46p" {
		t.Errorf("a prior-year stamp = %q, want its year named", got)
	}
	if got := context.StampLabel("2027-01-01T08:00:00Z"); got != "fri 2027-01-01 12:00a" {
		t.Errorf("a next-year stamp = %q, want its year named", got)
	}
}

// The stored spelling is UTC with a literal Z, and ONLY that. An offset form is
// a record `check` rejects, so rendering it as a healthy local time would hide
// exactly the fault the reader needs to see.
func TestParseStampAcceptsOnlyTheStoredSpelling(t *testing.T) {
	if _, ok := ParseStamp("2026-08-28T00:46:44Z"); !ok {
		t.Fatalf("the canonical spelling was refused")
	}
	for _, stored := range []string{
		"2026-08-27T17:46:44-07:00",
		"2026-08-28T00:46:44+00:00",
		"2026-08-28T00:46:44.500Z",
		"2026-8-28T00:46:44Z",
		"28 Aug 2026",
	} {
		if _, ok := ParseStamp(stored); ok {
			t.Errorf("ParseStamp(%q) accepted a spelling the store never writes", stored)
		}
	}
}

// …and a spelling ParseStamp refuses reaches the reader untouched.
func TestStampLabelPrintsAnOffsetSpellingAsStored(t *testing.T) {
	context := stampContext(t, "America/Los_Angeles", 12)
	const stored = "2026-08-27T17:46:44-07:00"
	if got := context.StampLabel(stored); got != stored {
		t.Errorf("StampLabel(%q) = %q, want the stored spelling back", stored, got)
	}
}

func TestClockLabelHonoursTheConfiguredFormat(t *testing.T) {
	for _, testCase := range []struct {
		hour, minute, format int
		want                 string
	}{
		{9, 5, 12, "9:05a"},
		{9, 5, 24, "09:05"},
		{13, 0, 12, "1:00p"},
		{13, 0, 24, "13:00"},
		{0, 30, 12, "12:30a"},
		{12, 30, 12, "12:30p"},
	} {
		got := ClockLabel(testCase.hour, testCase.minute, testCase.format)
		if got != testCase.want {
			t.Errorf("ClockLabel(%d, %d, %d) = %q, want %q",
				testCase.hour, testCase.minute, testCase.format, got, testCase.want)
		}
	}
}
