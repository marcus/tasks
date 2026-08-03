package check

import (
	"reflect"
	"strings"
	"testing"
)

// Every expectation below is transcribed from the captured Ruby CLI output in
// porting/evidence/check-task-fields/ruby, not from what this package printed.
// The evidence directory's compare.rb re-derives them from that capture on
// every run; these tests keep `go test` alone enough to catch a regression.

var ownedTaskErrors = []string{
	"invalid state ", "invalid priority ", "task has no title", "title must be a string",
	" is not a YYYY-MM-DD date", " is not a real date", "invalid recur cookie ",
	"closed date on an open task", "closed date on a proposed task",
	"tags must be an array", "tags must all be strings",
	" is not an RFC3339 UTC timestamp with device slug",
}

var ownedTaskWarnings = []string{"unknown key ", "unknown delegation key ", "duplicate open title "}

func ownedEntries(entries []Entry, owned []string) []Entry {
	kept := []Entry{}
	for _, entry := range entries {
		for _, fragment := range owned {
			if strings.Contains(entry.Message, fragment) {
				kept = append(kept, entry)
				break
			}
		}
	}
	return kept
}

func TestTaskFieldDiagnosticsMatchRubyOracle(t *testing.T) {
	got := ownedEntries(Check(fixture("malformed", "wrong-types", "tasks.jsonl")).Errors, ownedTaskErrors)
	want := []Entry{
		{4, `invalid state "STARTED" (expected PROPOSED/INBOX/TODO/NEXT/WAITING/DONE/CANCELLED)`},
		{5, `invalid priority "Z" (expected A, B, or C)`},
		{6, "title must be a string"},
		{7, "tags must be an array"},
		{8, "tags must all be strings"},
		{9, "scheduled 2026-02-30 is not a real date"},
		{10, `deadline "14/06/2026" is not a YYYY-MM-DD date`},
		{11, "closed date on an open task (TODO)"},
		{12, `invalid recur cookie "every week" (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)`},
		{17, `updated "2026-06-01T10:00:00Z" is not an RFC3339 UTC timestamp with device slug`},
		{22, "task has no title"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
}

func TestNonCanonicalRecurCookiesAreAllRejectedWithOneMessage(t *testing.T) {
	// All 27 rows collapse to one message shape: Check calls the boolean
	// Recur.cookie? and discards every reason Recur produced.
	values := []string{
		".+w:mon", "++m:15", "1w:mon", "0w:mon", "w:monday", "w:wed,mon", "w:mon,mon", "w:", "w:mon,",
		"w:xyz", "m:0", "m:32", "m:01", "m:15,1", "m:6tue", "m:2tues", "m:", "y:13-01", "y:02-30",
		"y:7-04", "y:07-4", "y:11:6thu", " w:mon", "w:mon ", "+0d", "+1W",
	}
	got := ownedEntries(Check(fixture("malformed", "recur-non-canonical", "tasks.jsonl")).Errors, ownedTaskErrors)
	want := make([]Entry, 0, len(values)+1)
	for index, value := range values {
		want = append(want, Entry{index + 3, `invalid recur cookie "` + value +
			`" (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)`})
	}
	// The last row is the integer 7: a non-string value is reported, never raised.
	want = append(want, Entry{29, "invalid recur cookie 7 (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
}

func TestCanonicalStoredRecurValuesAreAccepted(t *testing.T) {
	result := Check(fixture("valid", "recur-calendar-grammar", "tasks.jsonl"))
	if got := ownedEntries(result.Errors, ownedTaskErrors); len(got) != 0 {
		t.Fatalf("canonical grammar rejected: %#v", got)
	}
	if got := ownedEntries(Check(fixture("valid", "full-field-matrix", "tasks.jsonl")).Errors, ownedTaskErrors); len(got) != 0 {
		t.Fatalf("full field matrix rejected: %#v", got)
	}
}

func TestUnknownKeysWarnOnBothChannelsOfTheRecord(t *testing.T) {
	result := Check(fixture("compat", "forward-compat-unknown-keys", "tasks.jsonl"))
	want := []Entry{
		{3, `unknown key "energy"`},
		{3, `unknown delegation key "budget_tokens"`},
		{4, `unknown key "review_after"`},
	}
	if got := ownedEntries(result.Warnings, ownedTaskWarnings); !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %#v, want %#v", got, want)
	}
	// An unknown key is a hazard, not a defect: the file still checks clean.
	if got := ownedEntries(result.Errors, ownedTaskErrors); len(got) != 0 {
		t.Fatalf("unknown keys reached the error channel: %#v", got)
	}
}

func TestDuplicateTitlesWarnOnlyForOpenStates(t *testing.T) {
	open := Check(fixture("malformed", "duplicate-open-titles", "tasks.jsonl"))
	want := []Entry{{8, `duplicate open title "replace the bathroom bulb" (lines 3, 8) — fuzzy refs will be ambiguous`}}
	if got := ownedEntries(open.Warnings, ownedTaskWarnings); !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %#v, want %#v", got, want)
	}
	if !open.OK() {
		t.Fatalf("a duplicate open title is a warning, not an error: %#v", open.Errors)
	}
	closed := Check(fixture("valid", "duplicate-closed-titles", "tasks.jsonl"))
	if got := ownedEntries(closed.Warnings, ownedTaskWarnings); len(got) != 0 {
		t.Fatalf("closed and proposed titles warned: %#v", got)
	}
}

// The vocabularies, exhaustively: every accepted value and its near misses.
func TestStateAndPriorityVocabularyProperty(t *testing.T) {
	for _, state := range []string{"PROPOSED", "INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"} {
		if got := taskErrors(t, `"state":`+quote(state)+`,"title":"t"`); len(got) != 0 {
			t.Fatalf("state %q rejected: %#v", state, got)
		}
	}
	for _, state := range []string{"todo", "STARTED", "", " TODO", "TODO ", "DONE\n"} {
		if got := taskErrors(t, `"state":`+quote(state)+`,"title":"t"`); len(got) != 1 {
			t.Fatalf("state %q yielded %#v, want one invalid-state error", state, got)
		}
	}
	for _, priority := range []string{"A", "B", "C"} {
		if got := taskErrors(t, `"state":"TODO","title":"t","priority":`+quote(priority)); len(got) != 0 {
			t.Fatalf("priority %q rejected: %#v", priority, got)
		}
	}
	for _, priority := range []string{"a", "D", "Z", "AA", " A"} {
		if got := taskErrors(t, `"state":"TODO","title":"t","priority":`+quote(priority)); len(got) != 1 {
			t.Fatalf("priority %q yielded %#v, want one invalid-priority error", priority, got)
		}
	}
	// A falsey priority is absent as far as Ruby's `if r["priority"]` is concerned.
	for _, absent := range []string{"null", "false"} {
		if got := taskErrors(t, `"state":"TODO","title":"t","priority":`+absent); len(got) != 0 {
			t.Fatalf("priority %s judged: %#v", absent, got)
		}
	}
}

func TestDateVocabularyProperty(t *testing.T) {
	valid := []string{"2026-01-01", "2024-02-29", "2000-02-29", "1970-12-31", "2026-06-30"}
	invalid := []string{"2026-02-30", "2023-02-29", "2026-13-01", "2026-00-10", "2026-04-31", "1900-02-29"}
	malformed := []string{"14/06/2026", "2026-6-1", "2026-01-01 ", " 2026-01-01", "20260101", ""}
	for _, key := range []string{"scheduled", "deadline", "archived"} {
		for _, date := range valid {
			if got := taskErrors(t, `"state":"TODO","title":"t","`+key+`":`+quote(date)); len(got) != 0 {
				t.Fatalf("%s %q rejected: %#v", key, date, got)
			}
		}
		for _, date := range invalid {
			want := key + " " + date + " is not a real date"
			if got := taskErrors(t, `"state":"TODO","title":"t","`+key+`":`+quote(date)); len(got) != 1 || got[0].Message != want {
				t.Fatalf("%s %q yielded %#v, want %q", key, date, got, want)
			}
		}
		for _, date := range malformed {
			want := key + " " + quote(date) + " is not a YYYY-MM-DD date"
			if got := taskErrors(t, `"state":"TODO","title":"t","`+key+`":`+quote(date)); len(got) != 1 || got[0].Message != want {
				t.Fatalf("%s %q yielded %#v, want %q", key, date, got, want)
			}
		}
	}
}

func TestRecurCookieVocabularyProperty(t *testing.T) {
	accepted := []string{
		".+1w", "++1m", "+2d", "+10y", "w:mon", "w:mon,wed,fri", "+w:sun", "2w:tue", "+3w:mon,sat",
		"m:1", "m:15", "m:31", "m:last", "m:1,15,last", "m:2tue", "m:lastfri", "m:1,last,3wed",
		"y:07-04", "y:02-29", "y:11:3thu", "y:12:lastsun", "2y:01-01",
	}
	for _, value := range accepted {
		if !recurCookie(value) {
			t.Fatalf("canonical cookie %q rejected", value)
		}
	}
	rejected := []string{
		"", "1w", "+1", "+1x", "+1W", "+0d", "+01d", ".+w:mon", "++m:15", "1w:mon", "0w:mon",
		"w:monday", "w:wed,mon", "w:mon,mon", "w:", "w:mon,", "w:xyz", "m:0", "m:32", "m:01",
		"m:15,1", "m:6tue", "m:2tues", "m:", "m:0tue", "y:13-01", "y:02-30", "y:7-04", "y:07-4",
		"y:11:6thu", "y:00-01", "W:MON", "every week", "off", "m:1,3wed,last", "m:last,1",
	}
	for _, value := range rejected {
		if recurCookie(value) {
			t.Fatalf("non-canonical value %q accepted", value)
		}
	}
	// Recur.cookie? strips; check_task's own guard is what refuses padding.
	for _, padded := range []string{" w:mon", "w:mon ", "\t+1w", ".+1w\n"} {
		if !recurCookie(padded) {
			t.Fatalf("Recur.cookie? should strip %q", padded)
		}
		want := "invalid recur cookie " + quote(padded) + " (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)"
		if got := taskErrors(t, `"state":"TODO","title":"t","recur":`+quote(padded)); len(got) != 1 || got[0].Message != want {
			t.Fatalf("padded %q yielded %#v, want %q", padded, got, want)
		}
	}
}

func TestUpdateStampVocabularyProperty(t *testing.T) {
	// Ruby validates the timestamp half with Time.iso8601, which range-checks
	// components without demanding a real calendar date.
	accepted := []string{
		"2026-06-01T10:00:00Z#marcus", "2026-02-30T10:00:00Z#a", "2026-06-31T00:00:00Z#a",
		"2016-12-31T23:59:60Z#a", "2026-06-01T24:00:00Z#a", "0000-01-01T00:00:00Z#a",
	}
	rejected := []string{
		"2026-13-01T00:00:00Z#a", "2026-00-01T10:00:00Z#a", "2026-06-00T10:00:00Z#a",
		"2026-06-32T10:00:00Z#a", "2026-06-01T25:00:00Z#a", "2026-06-01T10:60:00Z#a",
		"2026-06-01T10:00:61Z#a", "2026-06-01T24:30:00Z#a", "2026-06-01T24:00:01Z#a",
		"2026-06-01T10:00:00Z#", "2026-06-01T10:00:00Z#A1", "2026-06-01T10:00:00Z", "",
	}
	for _, value := range accepted {
		if got := taskErrors(t, `"state":"TODO","title":"t","updated":`+quote(value)); len(got) != 0 {
			t.Fatalf("stamp %q rejected: %#v", value, got)
		}
	}
	for _, value := range rejected {
		want := "updated " + quote(value) + " is not an RFC3339 UTC timestamp with device slug"
		if got := taskErrors(t, `"state":"TODO","title":"t","updated":`+quote(value)); len(got) != 1 || got[0].Message != want {
			t.Fatalf("stamp %q yielded %#v, want %q", value, got, want)
		}
	}
	// The rule is keyed on presence, so an explicit null is reported as nil.
	want := "updated nil is not an RFC3339 UTC timestamp with device slug"
	if got := taskErrors(t, `"state":"TODO","title":"t","updated":null`); len(got) != 1 || got[0].Message != want {
		t.Fatalf("null stamp yielded %#v, want %q", got, want)
	}
}

func taskErrors(t *testing.T, fields string) []Entry {
	t.Helper()
	text := "{\"type\":\"meta\",\"version\":2}\n{\"type\":\"task\",\"id\":\"c0000001\"," + fields + "}"
	return ownedEntries(CheckText([]byte(text)).Errors, ownedTaskErrors)
}

func quote(value string) string {
	return rubyInspectString(value)
}
