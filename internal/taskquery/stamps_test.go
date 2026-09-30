package taskquery

import (
	"testing"
	"time"
)

const stampsFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"Captured","body":"Captured [2026-07-01].\nmore","updated":"2026-07-10T08:30:00Z#home"}
{"type":"task","id":"aaaa0003","parent":"aaaa0001","state":"TODO","title":"Note moved down","body":"First a note.\nCaptured [2026-06-30 Tue 09:15]"}
{"type":"task","id":"aaaa0004","parent":"aaaa0001","state":"TODO","title":"Impossible day","body":"Captured [2026-02-30].","updated":"yesterday"}
{"type":"task","id":"aaaa0005","parent":"aaaa0001","state":"TODO","title":"Quoted","body":"He said Captured [2026-07-01]. once"}
{"type":"task","id":"aaaa0006","parent":"aaaa0001","state":"TODO","priority":"A","title":"Due soon","deadline":"2026-07-25"}
{"type":"task","id":"aaaa0007","parent":"aaaa0001","state":"DONE","priority":"A","title":"Finished","closed":"2026-07-01"}
`

func TestCreatedReadsTheCaptureNote(t *testing.T) {
	queries := queriesFrom(t, stampsFixture)
	for _, tc := range []struct {
		id, want string
		ok       bool
	}{
		{"aaaa0002", "2026-07-01", true},
		{"aaaa0003", "2026-06-30", true},
		{"aaaa0004", "", false},
		{"aaaa0005", "", false},
		{"aaaa0006", "", false},
	} {
		got, ok := queries.Created(itemByID(t, queries, tc.id))
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: Created = %q/%v, want %q/%v", tc.id, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUpdatedAtIsTheStampInstant(t *testing.T) {
	queries := queriesFrom(t, stampsFixture)
	got, ok := queries.UpdatedAt(itemByID(t, queries, "aaaa0002"))
	if want := time.Date(2026, 7, 10, 8, 30, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("UpdatedAt = %v/%v, want %v", got, ok, want)
	}
	for _, id := range []string{"aaaa0003", "aaaa0004"} {
		if _, ok := queries.UpdatedAt(itemByID(t, queries, id)); ok {
			t.Errorf("%s: a missing or malformed stamp dated the task", id)
		}
	}
}

// QuadrantFor honours the configured window, and a closed task has none.
func TestQuadrantForUsesTheConfiguredWindow(t *testing.T) {
	// Pinned today is 2026-07-20, so the deadline is five days out.
	defaults := queriesFrom(t, stampsFixture)
	if got, _ := defaults.QuadrantFor(itemByID(t, defaults, "aaaa0006")); got != "Q2" {
		t.Errorf("default window quadrant = %q, want Q2", got)
	}
	widened := New(defaults.Snapshot(), defaults.Context(), WithUrgentDays(7))
	if got, _ := widened.QuadrantFor(itemByID(t, widened, "aaaa0006")); got != "Q1" {
		t.Errorf("7-day window quadrant = %q, want Q1", got)
	}
	if got, ok := widened.QuadrantFor(itemByID(t, widened, "aaaa0007")); ok {
		t.Errorf("a DONE task has quadrant %q", got)
	}
}
