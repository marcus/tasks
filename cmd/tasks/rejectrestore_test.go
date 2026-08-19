package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/determinism"
)

// declineDay is the day every run below happens on. The shared runCLI helper
// leaves the clock alone, and a decline marker is a DATE, so these tests pin the
// instant themselves rather than asserting against whatever today happens to be.
const declineDay = "2026-07-20"

func runPinned(t *testing.T, dir string, argv ...string) cliResult {
	t.Helper()
	previous := env
	env = determinism.Env{
		"TASKS_FILE":      filepath.Join(dir, "tasks.jsonl"),
		"TASKS_ARCHIVE":   filepath.Join(dir, "archive.jsonl"),
		"XDG_STATE_HOME":  filepath.Join(dir, "state"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "cfg"),
		"TASKS_PIN_NOW":   declineDay + "T12:00:00Z",
		"TZ":              "UTC",
	}
	defer func() { env = previous }()
	stdout, stderr := captureOutput(t, func() int { return run(argv) })
	return cliResult{stdout: stdout.text, stderr: stderr.text, status: stdout.status}
}

// The whole loop as a caller sees it: reject, find the decline again, restore it,
// and undo/redo both writes. Everything here is argv in and stdout/exit out —
// the level at which "the default views stayed clean" is actually observable.
//
// The clock is pinned to declineDay, so a decline made in the run is today's and
// always inside the documented 30-day window.
func TestRejectThenListRejectedThenUnreject(t *testing.T) {
	dir := seedStore(t, mutationFixture)

	if result := runPinned(t, dir, "reject", "A proposal", "--note", "superseded"); result.status != 0 {
		t.Fatalf("reject: exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0006")["rejected"]; got != declineDay {
		t.Errorf("rejected marker = %v, want today's date", got)
	}

	// The decline is reachable by scope, and by nothing else.
	listed := runPinned(t, dir, "list", "--rejected")
	if listed.status != 0 {
		t.Fatalf("list --rejected: exit %d, stderr %q", listed.status, listed.stderr)
	}
	if !strings.Contains(listed.stdout, "A proposal") ||
		!strings.Contains(listed.stdout, declineDay) {
		t.Errorf("list --rejected = %q", listed.stdout)
	}
	if !strings.Contains(listed.stdout, "tasks unreject") {
		t.Errorf("the scope must say how to restore a row: %q", listed.stdout)
	}
	for _, argv := range [][]string{
		{"list"}, {"list", "--proposed"}, {"agenda"}, {"next"}, {"inbox"},
	} {
		view := runPinned(t, dir, argv...)
		if strings.Contains(view.stdout, "A proposal") {
			t.Errorf("%v showed a declined proposal: %q", argv, view.stdout)
		}
	}

	// Restore returns the SAME row to PROPOSED.
	restored := runPinned(t, dir, "unreject", "A proposal")
	if restored.status != 0 {
		t.Fatalf("unreject: exit %d, stderr %q", restored.status, restored.stderr)
	}
	if !strings.Contains(restored.stdout, "restored → PROPOSED") {
		t.Errorf("unreject said %q", restored.stdout)
	}
	row := recordFor(t, dir, "dddd0006")
	if row["state"] != "PROPOSED" {
		t.Errorf("state = %v, want PROPOSED", row["state"])
	}
	if row["id"] != "dddd0006" {
		t.Errorf("id = %v — a restore must never mint a new one", row["id"])
	}
	if row["closed"] != nil || row["rejected"] != nil {
		t.Errorf("closed = %v, rejected = %v — both belong to the undone decision",
			row["closed"], row["rejected"])
	}
	if body, _ := row["body"].(string); !strings.Contains(body, "superseded") {
		t.Errorf("body = %v — the withdrawal note is history", row["body"])
	}
	if empty := runPinned(t, dir, "list", "--rejected"); !strings.Contains(empty.stdout, "No matching tasks.") {
		t.Errorf("after a restore the decline list is empty, got %q", empty.stdout)
	}
	if proposed := runPinned(t, dir, "list", "--proposed"); !strings.Contains(proposed.stdout, "A proposal") {
		t.Errorf("the restored proposal is back in review: %q", proposed.stdout)
	}

	// Both writes are ordinary journal steps.
	if undone := runPinned(t, dir, "undo"); undone.status != 0 {
		t.Fatalf("undo: exit %d, stderr %q", undone.status, undone.stderr)
	}
	back := recordFor(t, dir, "dddd0006")
	if back["state"] != "CANCELLED" || back["rejected"] != declineDay {
		t.Errorf("undo of a restore = %v/%v, want the decline back", back["state"], back["rejected"])
	}
	if redone := runPinned(t, dir, "redo"); redone.status != 0 {
		t.Fatalf("redo: exit %d, stderr %q", redone.status, redone.stderr)
	}
	if again := recordFor(t, dir, "dddd0006"); again["state"] != "PROPOSED" {
		t.Errorf("redo of a restore = %v", again["state"])
	}
	if undoneReject := runPinned(t, dir, "undo"); undoneReject.status != 0 {
		t.Fatalf("undo: exit %d, stderr %q", undoneReject.status, undoneReject.stderr)
	}
	if second := runPinned(t, dir, "undo"); second.status != 0 {
		t.Fatalf("undo of the reject: exit %d, stderr %q", second.status, second.stderr)
	}
	if original := recordFor(t, dir, "dddd0006"); original["state"] != "PROPOSED" ||
		original["rejected"] != nil {
		t.Errorf("undoing back past the reject = %v/%v", original["state"], original["rejected"])
	}
}

func TestRejectedScopeJSONShape(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runPinned(t, dir, "reject", "A proposal"); result.status != 0 {
		t.Fatalf("reject: exit %d, stderr %q", result.status, result.stderr)
	}
	result := runPinned(t, dir, "list", "--rejected", "--json")
	if result.status != 0 {
		t.Fatalf("list --rejected --json: exit %d, stderr %q", result.status, result.stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &rows); err != nil {
		t.Fatalf("parse %q: %v", result.stdout, err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: %q", len(rows), result.stdout)
	}
	if rows[0]["state"] != "CANCELLED" || rows[0]["rejected"] != declineDay ||
		rows[0]["title"] != "A proposal" || rows[0]["source"] != "live" {
		t.Errorf("row = %v", rows[0])
	}
	// The member is part of the shared shape, so every read reports it — null on
	// a task that was never declined.
	open := runPinned(t, dir, "list", "--json")
	var openRows []map[string]any
	if err := json.Unmarshal([]byte(open.stdout), &openRows); err != nil {
		t.Fatalf("parse %q: %v", open.stdout, err)
	}
	if len(openRows) == 0 {
		t.Fatal("the open list is empty")
	}
	for _, row := range openRows {
		if value, present := row["rejected"]; !present || value != nil {
			t.Errorf("open row rejected = %v, want an explicit null", value)
		}
	}
}

func TestUnrejectRefusesWhatIsNotADeclinedProposal(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)

	// An open task.
	if result := runPinned(t, dir, "unreject", "Ship the release"); result.status == 0 {
		t.Error("unreject of an open task must refuse")
	}
	// An ordinary cancellation was never a review decision.
	if result := runPinned(t, dir, "cancel", "Both stamps"); result.status != 0 {
		t.Fatalf("cancel: exit %d, stderr %q", result.status, result.stderr)
	}
	cancelled := runPinned(t, dir, "unreject", "Both stamps")
	if cancelled.status == 0 {
		t.Error("unreject of a plain cancellation must refuse")
	}
	if !strings.Contains(cancelled.stderr, "not a rejected proposal") {
		t.Errorf("refusal = %q", cancelled.stderr)
	}
	// Usage.
	if result := runPinned(t, dir, "unreject"); result.status == 0 ||
		!strings.Contains(result.stderr, "usage: tasks unreject") {
		t.Errorf("bare unreject = %d %q", result.status, result.stderr)
	}
	// Only the cancel above wrote anything.
	if storeBytes(t, dir) == before {
		t.Fatal("the cancel step did not write, so this test proves nothing")
	}
}

// An archived decline is still printed by `list --rejected`, so the verb that
// list points at must explain why it cannot restore it. `resolveRef` never
// looks in the archive, so without a check ahead of it the answer is "no
// match" — a row the user is looking at, reported as nonexistent.
func TestUnrejectOfAnArchivedDeclineSaysItIsArchived(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runPinned(t, dir, "reject", "A proposal"); result.status != 0 {
		t.Fatalf("reject: exit %d, stderr %q", result.status, result.stderr)
	}
	if result := runPinned(t, dir, "archive"); result.status != 0 {
		t.Fatalf("archive: exit %d, stderr %q", result.status, result.stderr)
	}
	listed := runPinned(t, dir, "list", "--rejected")
	if !strings.Contains(listed.stdout, "A proposal") ||
		!strings.Contains(listed.stdout, "(archived)") {
		t.Fatalf("the archived decline is not listed: %q", listed.stdout)
	}
	for _, ref := range []string{"A proposal", "dddd0006"} {
		result := runPinned(t, dir, "unreject", ref)
		if result.status == 0 {
			t.Errorf("unreject %q must refuse an archived decline", ref)
		}
		if !strings.Contains(result.stderr, "archived") {
			t.Errorf("unreject %q said %q, want the archived refusal", ref, result.stderr)
		}
	}
	// A ref that names nothing at all is still an ordinary no-match.
	if missing := runPinned(t, dir, "unreject", "no such task"); !strings.Contains(
		missing.stderr, "no match") {
		t.Errorf("missing ref said %q", missing.stderr)
	}
}

// `approve --done` is the CLI half of the TUI's `c` on a proposal: one write,
// one undo step, and PROPOSED restored exactly.
func TestApproveDoneCompletesInOneUndoableStep(t *testing.T) {
	dir := seedStore(t, mutationFixture)

	result := runPinned(t, dir, "approve", "A proposal", "--done")
	if result.status != 0 {
		t.Fatalf("approve --done: exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stdout, "approved + completed \u2192 DONE") {
		t.Errorf("stdout = %q", result.stdout)
	}
	row := recordFor(t, dir, "dddd0006")
	if row["state"] != "DONE" || row["closed"] != declineDay {
		t.Fatalf("row = %v, want a closed DONE task", row)
	}

	if undone := runPinned(t, dir, "undo"); undone.status != 0 {
		t.Fatalf("undo: exit %d, stderr %q", undone.status, undone.stderr)
	}
	restored := recordFor(t, dir, "dddd0006")
	if restored["state"] != "PROPOSED" || restored["closed"] != nil {
		t.Errorf("after undo = %v, want PROPOSED", restored)
	}
}

func TestApproveDoneReportsJSON(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runPinned(t, dir, "approve", "A proposal", "--done", "--json")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %q", result.stdout)
	}
	touched, ok := payload["touched"].([]any)
	if !ok || len(touched) != 1 {
		t.Fatalf("touched = %v", payload["touched"])
	}
	task, _ := touched[0].(map[string]any)
	if task["state"] != "DONE" {
		t.Errorf("touched task = %v", task)
	}
}

// `--done` belongs to approve alone: reject already closes the row, and a
// declined proposal is not completed work.
func TestRejectHasNoDoneFlag(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runPinned(t, dir, "reject", "A proposal", "--done")
	if result.status == 0 || !strings.Contains(result.stderr, "unknown flag: --done") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}
