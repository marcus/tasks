package taskquery

import (
	"testing"

	"github.com/marcus/tasks/internal/temporal"
)

// AvailabilityAfter is what a dry-run asks: "if this write landed, could I work
// on this?" It exists so the preview and the write share ONE derivation — the
// ancestor walk, the hold precedence and the lead gate all stay in the read
// model rather than being approximated a second time in the CLI.

const previewFixture = `{"type":"meta","version":2}
{"type":"section","id":"bb000001","title":"Work"}
{"type":"task","id":"bb000002","parent":"bb000001","state":"NEXT","title":"Free","deadline":"2026-08-01"}
{"type":"task","id":"bb000003","parent":"bb000001","state":"NEXT","title":"Held","tags":["defer"]}
{"type":"task","id":"bb000004","parent":"bb000001","state":"NEXT","title":"Blocking parent","scheduled":"2026-09-01"}
{"type":"task","id":"bb000005","parent":"bb000004","state":"NEXT","title":"Blocked child"}
`

func previewOf(t *testing.T, queries *Queries, id string, override Override) Availability {
	t.Helper()
	item, found := queries.FindLive(id)
	if !found {
		t.Fatalf("no live item %q", id)
	}
	return queries.AvailabilityAfter(item, override)
}

func TestAvailabilityAfterWithNoOverrideIsThePlainAnswer(t *testing.T) {
	queries := queriesAt(t, previewFixture, "2026-07-20T12:00:00Z")
	if got := previewOf(t, queries, "bb000002", Override{}); got.Reason != ReasonAvailable {
		t.Errorf("reason = %s", got.Reason)
	}
	if got := previewOf(t, queries, "bb000003", Override{}); got.Reason != ReasonOnHold {
		t.Errorf("reason = %s", got.Reason)
	}
}

func TestAvailabilityAfterSubstitutesTheOwnHoldMarker(t *testing.T) {
	queries := queriesAt(t, previewFixture, "2026-07-20T12:00:00Z")
	held := true
	if got := previewOf(t, queries, "bb000002", Override{Deferred: &held}); got.Reason != ReasonOnHold {
		t.Errorf("previewing `someday` must read as on hold, got %s", got.Reason)
	}
	released := false
	if got := previewOf(t, queries, "bb000003", Override{Deferred: &released}); got.Reason != ReasonAvailable {
		t.Errorf("previewing `activate` must read as available, got %s", got.Reason)
	}
}

func TestAvailabilityAfterSubstitutesTheOwnAvailableFromDate(t *testing.T) {
	queries := queriesAt(t, previewFixture, "2026-07-20T12:00:00Z")
	date, _ := temporal.NewDate(2026, 12, 1)
	future := temporal.Value{Date: date}
	got := previewOf(t, queries, "bb000002",
		Override{Deferred: boolValue(false), Scheduled: &future, ScheduledSet: true})
	if got.Reason != ReasonScheduled || got.Value == nil || got.Value.Date.ISO() != "2026-12-01" {
		t.Errorf("timed defer preview = %+v", got)
	}

	// Clearing the date is a DIFFERENT override from not naming one, which is
	// the whole reason Scheduled is a pointer beside a boolean.
	cleared := previewOf(t, queries, "bb000004",
		Override{Deferred: boolValue(false), ScheduledSet: true})
	if cleared.Reason != ReasonAvailable {
		t.Errorf("activate preview = %+v", cleared)
	}
}

func TestAvailabilityAfterKeepsAncestorPrecedenceOutOfTheOverride(t *testing.T) {
	queries := queriesAt(t, previewFixture, "2026-07-20T12:00:00Z")
	// Releasing the CHILD's own gate does not release its parent's: a preview
	// that reported "available now" here would be promising work the user
	// cannot start.
	got := previewOf(t, queries, "bb000005",
		Override{Deferred: boolValue(false), ScheduledSet: true})
	if got.Reason != ReasonAncestorScheduled || got.BlockerID != "bb000004" {
		t.Errorf("child preview = %+v", got)
	}
}

func TestAvailabilityAfterIgnoresAReleaseStampALeadWriteWouldRetire(t *testing.T) {
	// `activate` stamps lead_skip to release the current occurrence. A lead
	// write CLEARS that stamp, so previewing a lead must ignore it — otherwise
	// the preview promises "available now" for a window about to re-arm.
	const released = `{"type":"meta","version":2}
{"type":"section","id":"cc000001","title":"Work"}
{"type":"task","id":"cc000002","parent":"cc000001","state":"NEXT","title":"Released","deadline":"2026-09-01","lead":"3w","lead_skip":"2026-09-01"}
`
	queries := queriesAt(t, released, "2026-07-20T12:00:00Z")
	if got := previewOf(t, queries, "cc000002", Override{}); got.Reason != ReasonAvailable {
		t.Fatalf("the stamp must release the current occurrence, got %s", got.Reason)
	}
	span := "6w"
	got := previewOf(t, queries, "cc000002", Override{Lead: &span})
	if got.Reason != ReasonScheduled || got.Value == nil || got.Value.Date.ISO() != "2026-07-21" {
		t.Errorf("lead preview = %+v", got)
	}

	// Clearing the lead leaves no window at all, and no available-from date to
	// fall back to.
	off := ""
	if cleared := previewOf(t, queries, "cc000002", Override{Lead: &off}); cleared.Reason != ReasonAvailable {
		t.Errorf("lead off preview = %+v", cleared)
	}
}

func boolValue(value bool) *bool { return &value }
