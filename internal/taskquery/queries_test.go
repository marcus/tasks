package taskquery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// buildQueries loads a conformance fixture at the pinned instant the harness
// uses, so these assertions and the corpus are talking about the same day.
//
// The fixture is COPIED first. Reading a store takes its sidecar lock, and
// creating that lock is a real side effect — pointing a test at the fixture
// tree in place would leave `.tasks.jsonl.lock` behind and change the
// fixture's root digest, which every observation records.
func buildQueries(t *testing.T, class, name string) *Queries {
	t.Helper()
	root := copyFixture(t, filepath.Join("..", "..", "testdata", "fixtures", class, name, "store"))
	snapshot, err := store.New(filepath.Join(root, "tasks.jsonl"), filepath.Join(root, "archive.jsonl")).
		ReadSnapshot(true)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	now, err := time.Parse(time.RFC3339, "2026-03-14T15:09:26Z")
	if err != nil {
		t.Fatalf("parse pin: %v", err)
	}
	context, err := temporal.NewContext(now, "UTC", 12)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	return New(snapshot, context)
}

func copyFixture(t *testing.T, source string) string {
	t.Helper()
	destination := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatalf("read fixture file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), content, 0o644); err != nil {
			t.Fatalf("write fixture copy: %v", err)
		}
	}
	return destination
}

func itemByID(t *testing.T, queries *Queries, id string) store.Item {
	t.Helper()
	item, found := queries.FindLive(id)
	if !found {
		t.Fatalf("no live item %q", id)
	}
	return item
}

// A future available-from date is what makes an otherwise ordinary open task
// unavailable — and it is why `list` shows five of small-gtd's six open tasks
// rather than all six.
func TestFutureStartDateHoldsATaskBack(t *testing.T) {
	queries := buildQueries(t, "valid", "small-gtd")
	held := itemByID(t, queries, "1a2b3c04") // scheduled 2026-06-05, pinned now is March
	availability := queries.AvailabilityFor(held)
	if availability.Available() {
		t.Fatalf("task with a June start date reported available at the March pin")
	}
	if availability.Reason != ReasonScheduled {
		t.Fatalf("reason = %q, want %q", availability.Reason, ReasonScheduled)
	}
	if availability.BlockerID != "1a2b3c04" {
		t.Fatalf("blocker = %q, want the task itself", availability.BlockerID)
	}
	free := itemByID(t, queries, "1a2b3c09")
	if !queries.AvailabilityFor(free).Available() {
		t.Fatalf("undated open task reported unavailable")
	}
}

// A lead REPLACES the available-from gate rather than joining it, and a
// `lead_skip` stamped with the current anchor removes the task's own gate
// entirely — otherwise `activate` would be undone by the next read.
func TestLeadGatesAndTheirRelease(t *testing.T) {
	queries := buildQueries(t, "valid", "full-field-matrix")

	gated := itemByID(t, queries, "f0000040") // deadline 2026-08-01, lead 3w
	opens, hasOpens := queries.LeadOpens(gated)
	if !hasOpens {
		t.Fatalf("a 3w lead on an August deadline derived no window")
	}
	if got, want := opens.ISO(), "2026-07-11"; got != want {
		t.Fatalf("lead opens %s, want %s", got, want)
	}
	if queries.AvailabilityFor(gated).Available() {
		t.Fatalf("task hidden until July reported available in March")
	}

	released := itemByID(t, queries, "f0000042") // scheduled 2026-06-22, lead 2d, lead_skip 2026-06-22
	if _, hasWindow := queries.LeadOpens(released); hasWindow {
		t.Fatalf("a released occurrence still reported a lead window")
	}
	if !queries.AvailabilityFor(released).Available() {
		t.Fatalf("a released occurrence is not available; activate would be undone by the next read")
	}
}

// Closed and archived items are `closed`, not `available` — the availability
// question does not apply to work that is already finished, and answering
// "available" would put them back in every actionable list.
func TestClosedAndProposedShortCircuit(t *testing.T) {
	queries := buildQueries(t, "valid", "full-field-matrix")
	if got := queries.AvailabilityFor(itemByID(t, queries, "f0000015")).Reason; got != ReasonClosed {
		t.Fatalf("DONE task reason = %q, want %q", got, ReasonClosed)
	}
	if got := queries.AvailabilityFor(itemByID(t, queries, "f0000010")).Reason; got != ReasonProposed {
		t.Fatalf("PROPOSED task reason = %q, want %q", got, ReasonProposed)
	}
}

// The headline renders tags in STORED order, contexts and plain tags
// interleaved as the record has them. Splitting them for display is a rendering
// choice; the headline's bytes are not.
func TestHeadlineKeepsStoredTagOrder(t *testing.T) {
	queries := buildQueries(t, "valid", "full-field-matrix")
	item := itemByID(t, queries, "f0000013")
	want := "NEXT [#A] Next with priority A and both tags :@computer:important:urgent:"
	if got := Headline(item); got != want {
		t.Fatalf("headline = %q, want %q", got, want)
	}
}

// A task's project for display climbs PAST closed task ancestors, so an open
// subtask of a finished parent still groups under something real.
func TestProjectIsTheNearestOpenAncestorHeadline(t *testing.T) {
	queries := buildQueries(t, "valid", "small-gtd")
	project, found := queries.Project(itemByID(t, queries, "1a2b3c09"))
	if !found || project != "Rebuild the garden shed" {
		t.Fatalf("project = %q (found=%v), want %q", project, found, "Rebuild the garden shed")
	}
}
