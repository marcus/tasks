package check

import (
	"testing"

	"github.com/marcus/tasks/internal/recur"
)

// The vocabulary properties salvaged from port/check-task-fields. The branch's
// production code was superseded by validate.go, but these tests characterize
// the vocabularies as ranges rather than as the handful of examples the fixture
// oracles happen to contain, which is coverage validate.go did not otherwise
// have.

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
		if !recur.Cookie(value) {
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
		if recur.Cookie(value) {
			t.Fatalf("non-canonical value %q accepted", value)
		}
	}
	// Recur.cookie? strips; checkTask's own guard is what refuses the padding,
	// so a padded cookie is one diagnostic rather than none.
	for _, padded := range []string{" w:mon", "w:mon ", "\t+1w", ".+1w\n"} {
		if !recur.Cookie(padded) {
			t.Fatalf("recur.Cookie should strip %q", padded)
		}
		want := "invalid recur cookie " + quote(padded) + " (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)"
		if got := taskErrors(t, `"state":"TODO","title":"t","recur":`+quote(padded)); len(got) != 1 || got[0].Message != want {
			t.Fatalf("padded %q yielded %#v, want %q", padded, got, want)
		}
	}
}

// TestUpdateStampVocabularyProperty covers the stamps Ruby and Go agree on.
// The shapes they disagree on are their own test below.
func TestUpdateStampVocabularyProperty(t *testing.T) {
	accepted := []string{"2026-06-01T10:00:00Z#marcus", "2026-06-01T00:00:00Z#a", "0000-01-01T00:00:00Z#a"}
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

// TestUpdateStampRejectsInstantsRubyAccepts pins the one place this package
// knowingly differs from the oracle. Ruby validates the timestamp half with
// Time.iso8601, which range-checks the components without demanding a real
// calendar date: 2026-02-30 rolls over, hour 24 is legal at exactly 24:00:00,
// and second 60 is allowed for a leap second. Go's time.Parse demands a real
// instant and refuses all three.
//
// No store either implementation writes can contain one of these: both format
// stamps from a real clock reading. The difference is reachable only by hand
// edit. It is recorded here directly beside the regression test
// because only Marcus accepts a difference — this test states what Go does
// today so the choice is visible rather than silent.
func TestUpdateStampRejectsInstantsRubyAccepts(t *testing.T) {
	for _, value := range []string{
		"2026-02-30T10:00:00Z#a", // Ruby: rolls over to March 2
		"2026-06-31T00:00:00Z#a", // Ruby: rolls over to July 1
		"2016-12-31T23:59:60Z#a", // Ruby: leap second
		"2026-06-01T24:00:00Z#a", // Ruby: midnight-ending spelling
	} {
		want := "updated " + quote(value) + " is not an RFC3339 UTC timestamp with device slug"
		if got := taskErrors(t, `"state":"TODO","title":"t","updated":`+quote(value)); len(got) != 1 || got[0].Message != want {
			t.Fatalf("stamp %q yielded %#v, want %q", value, got, want)
		}
	}
}

func taskErrors(t *testing.T, fields string) []Entry {
	t.Helper()
	text := "{\"type\":\"meta\",\"version\":2}\n{\"type\":\"task\",\"id\":\"c0000001\"," + fields + "}"
	return CheckText([]byte(text)).Errors
}

func quote(value string) string {
	return rubyInspectString(value)
}
