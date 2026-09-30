package api

import (
	"encoding/json"
	"net/url"
	"testing"
)

// The harness clock is Wednesday 2026-07-15 12:00 UTC, in Etc/UTC.

func TestDateParseAnswersWithoutTheStoreOrAWrite(t *testing.T) {
	h := newHarness(t)
	h.writeStore("{not-json\n")

	timed := h.get("/api/v1/dates/parse?input=" + url.QueryEscape("fri 4pm"))
	assertStatus(t, timed, 200)
	if timed.dig("meta") != nil {
		t.Errorf("a parse preview carries a store revision: %s", timed.Body)
	}
	want := `{"data":{"input":"fri 4pm","date":"2026-07-17",` +
		`"time":{"local":"16:00","timezone":null,"fold":0},"human":"Fri 17 Jul, 4:00p"}}`
	if timed.Body != want {
		t.Errorf("fri 4pm:\n got %s\nwant %s", timed.Body, want)
	}

	allDay := h.get("/api/v1/dates/parse?input=" + url.QueryEscape("in 2 weeks"))
	if allDay.dig("data", "date") != "2026-07-29" || allDay.dig("data", "time") != nil {
		t.Errorf("in 2 weeks: %s", allDay.Body)
	}
	if _, present := allDay.data()["time"]; !present {
		t.Errorf("an all-day answer still names time, as null: %s", allDay.Body)
	}

	// The trailing zone and fold words are the TUI's grammar, so a browser
	// field reads them the same way.
	zoned := h.get("/api/v1/dates/parse?input=" + url.QueryEscape("2026-11-01 01:30 America/New_York fold=later"))
	if got, _ := zoned.dig("data", "time").(map[string]any); got["timezone"] != "America/New_York" ||
		got["fold"] != float64(1) || got["local"] != "01:30" {
		t.Errorf("zoned: %s", zoned.Body)
	}

	blursday := h.get("/api/v1/dates/parse?input=blursday")
	assertStatus(t, blursday, 200)
	if blursday.Body != `{"data":{"input":"blursday","error":"unrecognized date: blursday"}}` {
		t.Errorf("blursday: %s", blursday.Body)
	}

	// Only a malformed REQUEST is 4xx.
	assertError(t, h.get("/api/v1/dates/parse"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/dates/parse?input=%20"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/dates/parse?input=fri&today=2026-01-01"), 422, "validation_failed")
}

// The time object is the TaskTimeInput shape exactly, so it goes straight back
// into a write and lands as the value the preview described.
func TestDateParseOutputIsAcceptedVerbatimByAWrite(t *testing.T) {
	h := newHarness(t)
	parsed := h.get("/api/v1/dates/parse?input=" + url.QueryEscape("fri 4pm Europe/Berlin"))
	assertStatus(t, parsed, 200)
	created := h.json("POST", "/api/v1/tasks", `{"title":"Call Berlin","deadline":"`+
		parsed.dig("data", "date").(string)+`","deadline_time":`+jsonOf(t, parsed.dig("data", "time"))+`}`, nil)
	assertStatus(t, created, 201)
	if created.dig("data", "deadline") != "2026-07-17" ||
		created.dig("data", "deadline_time", "timezone") != "Europe/Berlin" ||
		created.dig("data", "deadline_time", "local") != "16:00" {
		t.Errorf("the write did not store the previewed value: %s", created.Body)
	}
}

func TestDateParseHonorsTheConfiguredDateOrder(t *testing.T) {
	mdy := newHarness(t)
	if got := mdy.get("/api/v1/dates/parse?input=10/2").dig("data", "date"); got != "2026-10-02" {
		t.Errorf("mdy 10/2 = %v", got)
	}
	if got := mdy.get("/api/v1/meta").dig("data", "date_order"); got != "mdy" {
		t.Errorf("default date_order = %v", got)
	}

	dmy := newHarnessConfigured(t, func(h *harness) { h.dateOrder = "dmy" })
	if got := dmy.get("/api/v1/dates/parse?input=10/2").dig("data", "date"); got != "2027-02-10" {
		t.Errorf("dmy 10/2 = %v", got)
	}
	if got := dmy.get("/api/v1/meta").dig("data", "date_order"); got != "dmy" {
		t.Errorf("configured date_order = %v", got)
	}
}

// anchor projects from a task's own stamp — "when does THIS task fire next" —
// rather than from today. A from-completion interval (`.+1w`) is measured from
// today whatever the stamp says, exactly as `tasks recur <ref>` projects it.
func TestRecurrenceExplainProjectsFromAnAnchor(t *testing.T) {
	h := newHarness(t)
	explain := func(query string) []string {
		return stringsOf(h.get("/api/v1/recurrence/explain?count=2&"+query).dig("data", "next"))
	}
	assertStrings(t, explain("input=every+mon"), []string{"2026-07-20", "2026-07-27"}, "from today")
	assertStrings(t, explain("input=every+mon&anchor=2026-08-03"), []string{"2026-08-10", "2026-08-17"},
		"calendar schedule from the anchor")
	assertStrings(t, explain("input=%2B1w&anchor=2026-08-03"), []string{"2026-08-10", "2026-08-17"},
		"from-schedule interval from the anchor")
	assertStrings(t, explain("input=weekly&anchor=2026-08-03"), []string{"2026-07-22", "2026-07-29"},
		"from-completion interval ignores the stamp")

	assertError(t, h.get("/api/v1/recurrence/explain?input=weekly&anchor=2026-02-30"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/recurrence/explain?input=weekly&anchor=soon"), 422, "validation_failed")
}

func TestLeadExplainPreviewsTheSpanAndItsWindow(t *testing.T) {
	h := newHarness(t)
	h.writeStore("{not-json\n")

	anchored := h.get("/api/v1/lead/explain?input=" + url.QueryEscape("3 weeks") + "&anchor=2026-11-01")
	assertStatus(t, anchored, 200)
	if want := `{"data":{"input":"3 weeks","canonical":"3w","human":"3 weeks","opens":"2026-10-11"}}`; anchored.Body != want {
		t.Errorf("3 weeks:\n got %s\nwant %s", anchored.Body, want)
	}
	if bare := h.get("/api/v1/lead/explain?input=3w"); bare.dig("data", "opens") != nil ||
		bare.dig("data", "canonical") != "3w" {
		t.Errorf("no anchor: %s", bare.Body)
	}
	if off := h.get("/api/v1/lead/explain?input=off"); off.Body !=
		`{"data":{"input":"off","canonical":null,"human":"no lead time","opens":null}}` {
		t.Errorf("off: %s", off.Body)
	}
	junk := h.get("/api/v1/lead/explain?input=soonish")
	assertStatus(t, junk, 200)
	if junk.dig("data", "error") == nil || junk.dig("data", "canonical") != nil {
		t.Errorf("soonish: %s", junk.Body)
	}

	assertError(t, h.get("/api/v1/lead/explain"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/lead/explain?input=3w&anchor=nope"), 422, "validation_failed")
}

func TestParsePreviewsLogTheirRoute(t *testing.T) {
	for path, route := range map[string]string{
		"/api/v1/dates/parse?input=fri": "/api/v1/dates/parse",
		"/api/v1/lead/explain?input=3w": "/api/v1/lead/explain",
	} {
		if got := routeName(mustPath(t, path)); got != route {
			t.Errorf("routeName(%s) = %q", path, got)
		}
	}
}

func mustPath(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Path
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
