package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marcus/tasks/internal/determinism"
)

// The journal is what makes undo trustworthy across processes: the CLI and the
// TUI share one linear history for one store file, and it has to survive a cold
// start, a hand edit, a vanished blob, and a second binary in the other
// language reading the same directory.

const scope = "test-scope"

func writable(t *testing.T, dir, org string, limit int) *Journal {
	t.Helper()
	return Open(dir, org).Writable(limit, scope)
}

func text(value string) *string { return &value }

func snapshot(org, archive string) Snapshot { return Snapshot{Org: text(org), Archive: text(archive)} }

func orgOnly(org string) Snapshot { return Snapshot{Org: text(org)} }

// seed lays down an org file so Canonical resolves the same way it will later.
func seed(t *testing.T) (dir, org string) {
	t.Helper()
	home := t.TempDir()
	org = filepath.Join(home, "tasks.jsonl")
	if err := os.WriteFile(org, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, "journal"), org
}

func mustPlan(t *testing.T, j *Journal, delta int) Step {
	t.Helper()
	step, ok := j.Plan(delta)
	if !ok {
		t.Fatalf("no step available at delta %d", delta)
	}
	return step
}

// -- persistence across instances --------------------------------------------

// A brand-new Journal over the same directory — a fresh process, in effect —
// can still undo an edit the previous one recorded. That is the whole reason
// the history is on disk rather than in the TUI's memory.
func TestUndoSurvivesANewJournalInstance(t *testing.T) {
	dir, org := seed(t)
	before := orgOnly("first")
	after := orgOnly("second")
	if !writable(t, dir, org, 50).Record("state → DONE: Book flight", before, after, "", false) {
		t.Fatal("record failed")
	}

	step := mustPlan(t, Open(dir, org), -1)
	if step.Label != "state → DONE: Book flight" {
		t.Fatalf("label = %q", step.Label)
	}
	if !step.Expect.Equal(after) {
		t.Fatalf("expect = %#v", step.Expect)
	}
	if !step.Target.Equal(before) {
		t.Fatalf("target = %#v", step.Target)
	}
}

func TestRedoSurvivesANewJournalInstance(t *testing.T) {
	dir, org := seed(t)
	first := writable(t, dir, org, 50)
	first.Record("priority → C", orgOnly("first"), orgOnly("second"), "", false)
	undo := mustPlan(t, first, -1)
	if !first.Commit(undo.To) {
		t.Fatal("commit failed")
	}

	step := mustPlan(t, Open(dir, org), 1)
	if !step.Target.Equal(orgOnly("second")) {
		t.Fatalf("redo target = %#v", step.Target)
	}
	if step.Label != "priority → C" {
		t.Fatalf("label = %q", step.Label)
	}
}

func TestTwoJournalsShareOneHistory(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)

	other := writable(t, dir, org, 50)
	step := mustPlan(t, other, -1)
	if !other.Commit(step.To) {
		t.Fatal("commit failed")
	}
	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("the other instance's commit must move the shared cursor")
	}
}

// A journal keyed to one org path must not replay onto a different file. The
// guard is the index's own `org` field, so even a deliberately shared directory
// refuses.
func TestAJournalNeverReplaysOntoADifferentOrg(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)

	otherHome := t.TempDir()
	other := filepath.Join(otherHome, "tasks.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(dir, other).Plan(-1); ok {
		t.Fatal("a foreign org must see no history")
	}
}

// A symlink and its target are the same file, so an edit through one spelling
// must be undoable through the other: one canonical identity, one history.
func TestSharedHistoryAcrossTwoPathSpellings(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real.jsonl")
	link := filepath.Join(home, "tasks.jsonl")
	if err := os.WriteFile(real, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "journal")

	writable(t, dir, link, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)

	if _, ok := Open(dir, real).Plan(-1); !ok {
		t.Fatal("the other spelling must share the history")
	}
}

func TestDirForNamespacesByCanonicalPath(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real.jsonl")
	link := filepath.Join(home, "tasks.jsonl")
	if err := os.WriteFile(real, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	env := determinism.Env{"XDG_STATE_HOME": filepath.Join(home, "state")}

	if DirFor(link, env) != DirFor(real, env) {
		t.Fatal("two spellings of one file must resolve to one journal directory")
	}
	elsewhere := filepath.Join(t.TempDir(), "tasks.jsonl")
	if DirFor(elsewhere, env) == DirFor(real, env) {
		t.Fatal("distinct task files must never share a history")
	}
}

// -- capping and garbage collection -------------------------------------------

func TestCappingKeepsOnlyRecentHistoryAcrossInstances(t *testing.T) {
	dir, org := seed(t)
	const limit = 5
	previous := orgOnly("state 0")
	for step := 1; step <= limit+5; step++ {
		next := orgOnly(fmt.Sprintf("state %d", step))
		// A fresh Journal per step, as separate CLI invocations would be.
		if !writable(t, dir, org, limit).Record(fmt.Sprintf("edit %d", step), previous, next, "", false) {
			t.Fatalf("record %d failed", step)
		}
		previous = next
	}

	journal := writable(t, dir, org, limit)
	undone := 0
	for {
		step, ok := journal.Plan(-1)
		if !ok {
			break
		}
		if !journal.Commit(step.To) {
			t.Fatal("commit failed")
		}
		undone++
	}
	if undone != limit {
		t.Fatalf("undid %d steps, want the %d-step limit", undone, limit)
	}
}

func TestBlobsAreGarbageCollectedWhenHistoryIsCapped(t *testing.T) {
	dir, org := seed(t)
	const limit = 4
	previous := orgOnly("state 0")
	for step := 1; step <= 20; step++ {
		next := orgOnly(fmt.Sprintf("state %d", step))
		writable(t, dir, org, limit).Record(fmt.Sprintf("edit %d", step), previous, next, "", false)
		previous = next
	}

	entries, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	// Capped to limit+1 states, and blobs deduplicate, so the count is bounded
	// and nowhere near the twenty mutations.
	if len(entries) > limit+2 {
		t.Fatalf("%d blobs retained for a %d-step history", len(entries), limit)
	}
}

// An ordinary edit writes ONE new blob: the untouched archive deduplicates to
// the digest already on disk. That is what keeps a mutation cheap rather than a
// re-serialization of the whole history.
func TestAnUnchangedFileDeduplicatesToOneBlob(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("edit", snapshot("live one", "archive"), snapshot("live two", "archive"), "", false)

	entries, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("blobs = %v, want the two live versions plus one shared archive", names)
	}
}

// -- resilience ---------------------------------------------------------------

// A mutation must still succeed after a corrupt index: the journal degrades to
// a fresh baseline rather than crashing the write it is only bookkeeping for.
func TestACorruptCursorDegradesToEmptyAndTheNextRecordRebuilds(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)

	indexPath := filepath.Join(dir, "index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["cursor"] = 999
	edited, _ := json.Marshal(document)
	if err := os.WriteFile(indexPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("an out-of-range cursor must read as no history")
	}
	if !writable(t, dir, org, 50).Record("edit", orgOnly("b"), orgOnly("c"), "", false) {
		t.Fatal("a fresh mutation must still record")
	}
	if _, ok := Open(dir, org).Plan(-1); !ok {
		t.Fatal("the rebuilt history must be undoable")
	}
}

func TestACorruptTopLevelIndexShapeDegradesToEmpty(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("a non-object index must read as no history")
	}
}

func TestAMissingBlobDegradesToEmptyNotACrash(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)
	entries, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(dir, "blobs", entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("a vanished blob must read as no history")
	}
}

// A directory or a symlink squatting where a blob belongs makes the plan
// refuse, and the NEXT record repairs the history rather than leaving it broken
// forever. The symlink is never followed: a journal that wrote through one
// would clobber whatever it pointed at.
func TestASquattedBlobRefusesThePlanAndTheNextRecordRepairsIt(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, org := seed(t)
			writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)
			blob := currentOrgBlob(t, dir)
			sentinel := filepath.Join(t.TempDir(), "sentinel")
			if err := os.WriteFile(sentinel, []byte("do not read or replace me"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(blob); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(blob, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(sentinel, blob); err != nil {
				t.Fatal(err)
			}

			if _, ok := Open(dir, org).Plan(-1); ok {
				t.Fatalf("a %s at a blob path must refuse the plan", kind)
			}

			if !writable(t, dir, org, 50).Record("edit", orgOnly("b"), orgOnly("c"), "", false) {
				t.Fatal("the next record must repair the history")
			}
			info, err := os.Lstat(blob)
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("blob was not replaced by a regular file: %v", err)
			}
			if kind == "symlink" {
				content, err := os.ReadFile(sentinel)
				if err != nil || string(content) != "do not read or replace me" {
					t.Fatalf("the symlink target was written through: %q %v", content, err)
				}
			}
			if _, ok := Open(dir, org).Plan(-1); !ok {
				t.Fatal("the repaired history must be undoable")
			}
		})
	}
}

func TestADirectoryAtTheIndexDegradesAndTheNextRecordRebuilds(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("edit", orgOnly("a"), orgOnly("b"), "", false)
	indexPath := filepath.Join(dir, "index.json")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("a directory at index.json must read as no history")
	}
	if !writable(t, dir, org, 50).Record("edit", orgOnly("b"), orgOnly("c"), "", false) {
		t.Fatal("the next record must rebuild the index")
	}
	info, err := os.Lstat(indexPath)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("index.json was not rebuilt as a regular file: %v", err)
	}
}

// An out-of-band edit between two mutations makes the recorded chain unsafe to
// replay: undoing across it would clobber the edit nobody journaled. The chain
// is discarded and a fresh baseline starts at the state actually on disk.
func TestAnOutOfBandEditStartsAFreshBaseline(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("first", orgOnly("a"), orgOnly("b"), "", false)
	journal.Record("second", orgOnly("edited by hand"), orgOnly("c"), "", false)

	reader := Open(dir, org)
	step := mustPlan(t, reader, -1)
	if !step.Target.Equal(orgOnly("edited by hand")) {
		t.Fatalf("the new baseline must be the state on disk, got %#v", step.Target)
	}
	if !reader.Commit(step.To) {
		t.Fatal("commit failed")
	}
	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("the stale chain must be gone, not merely shadowed")
	}
}

// A mutation made after an undo drops the now-unreachable tail — the familiar
// "a new edit clears redo".
func TestANewEditAfterAnUndoDropsTheRedoTail(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("first", orgOnly("a"), orgOnly("b"), "", false)
	journal.Record("second", orgOnly("b"), orgOnly("c"), "", false)
	step := mustPlan(t, journal, -1)
	journal.Commit(step.To)
	if _, ok := Open(dir, org).Plan(1); !ok {
		t.Fatal("a redo must be available before the new edit")
	}

	journal.Record("third", orgOnly("b"), orgOnly("d"), "", false)

	if _, ok := Open(dir, org).Plan(1); ok {
		t.Fatal("a new edit must clear the redo tail")
	}
	if got := mustPlan(t, Open(dir, org), -1); got.Label != "third" {
		t.Fatalf("label = %q", got.Label)
	}
}

func TestBarrierMakesTheCurrentFilesTheOnlyBaseline(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("first", orgOnly("a"), orgOnly("b"), "", false)

	if !journal.Barrier(orgOnly("b")) {
		t.Fatal("barrier failed")
	}
	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatal("an ordinary undo must never cross a schema barrier")
	}
}

// -- coalescing ---------------------------------------------------------------

// Keyed steps from one scope collapse into a single undo step: an editor
// session that typed a title one keystroke at a time is one thing the user
// wants back, not forty.
func TestKeyedStepsFromOneScopeCoalesce(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("baseline", orgOnly("a"), orgOnly("b"), "", false)
	journal.Record("title → T", orgOnly("b"), orgOnly("c"), "edit-session", false)
	journal.Record("title → Ti", orgOnly("c"), orgOnly("d"), "edit-session", false)

	reader := Open(dir, org)
	step := mustPlan(t, reader, -1)
	if !step.Target.Equal(orgOnly("b")) {
		t.Fatalf("the coalesced step must undo the whole session, got %#v", step.Target)
	}
	if step.Label != "title → Ti" {
		t.Fatalf("label = %q", step.Label)
	}
}

// Coalescing is local to one scope owner: an unrelated process must not extend
// a step this one started, or its edit would vanish with the other's undo.
func TestAnotherScopeNeverExtendsAKeyedStep(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("baseline", orgOnly("a"), orgOnly("b"), "", false)
	writable(t, dir, org, 50).Record("title → T", orgOnly("b"), orgOnly("c"), "edit-session", false)
	Open(dir, org).Writable(50, "another-process").
		Record("title → Ti", orgOnly("c"), orgOnly("d"), "edit-session", false)

	step := mustPlan(t, Open(dir, org), -1)
	if !step.Target.Equal(orgOnly("c")) {
		t.Fatalf("the other process's edit must be its own step, got %#v", step.Target)
	}
}

// A coalesced follow-up replaces the step's content but keeps its before-state,
// so a repair exemption must survive the overwrite — otherwise undoing the
// coalesced step wrongly refuses to restore the malformed bytes the user
// deliberately asked to fix.
func TestCoalescingOntoARepairStepKeepsTheUndoExemption(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("baseline", orgOnly("a"), orgOnly("invalid bytes"), "", false)
	journal.Record("repair", orgOnly("invalid bytes"), orgOnly("repaired"), "edit-session", true)
	journal.Record("follow-up", orgOnly("repaired"), orgOnly("repaired twice"), "edit-session", false)

	step := mustPlan(t, Open(dir, org), -1)
	if !step.Repair {
		t.Fatal("the repair exemption must survive the coalesced overwrite")
	}
	if !step.Target.Equal(orgOnly("invalid bytes")) {
		t.Fatalf("target = %#v", step.Target)
	}
}

// Undo and redo are explicit history boundaries. Even a redo back to the exact
// former tip must not resume coalescing with the editor session that preceded
// the history move.
func TestCommitStripsSegmentMetadata(t *testing.T) {
	dir, org := seed(t)
	journal := writable(t, dir, org, 50)
	journal.Record("baseline", orgOnly("a"), orgOnly("b"), "", false)
	journal.Record("title → T", orgOnly("b"), orgOnly("c"), "edit-session", false)

	undo := mustPlan(t, journal, -1)
	journal.Commit(undo.To)
	redo := mustPlan(t, journal, 1)
	journal.Commit(redo.To)

	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "coalesce_key") {
		t.Fatalf("a history move must strip segment metadata:\n%s", raw)
	}

	journal.Record("title → Ti", orgOnly("c"), orgOnly("d"), "edit-session", false)
	step := mustPlan(t, Open(dir, org), -1)
	if !step.Target.Equal(orgOnly("c")) {
		t.Fatalf("coalescing resumed across a history boundary: %#v", step.Target)
	}
}

// -- concurrency --------------------------------------------------------------

// The journal runs under the store's file lock in production, but its readers
// do not: a plan and a load may run while another goroutine records. Nothing
// here may race, and a reader must never observe a half-written index — the
// atomic replace is what guarantees that.
func TestConcurrentReadersAndWritersNeverRaceOrSeeAPartialIndex(t *testing.T) {
	dir, org := seed(t)
	writable(t, dir, org, 50).Record("baseline", orgOnly("a"), orgOnly("b"), "", false)

	var waiting sync.WaitGroup
	var lock sync.Mutex
	previous := orgOnly("b")
	for writer := 0; writer < 4; writer++ {
		waiting.Add(1)
		go func(writer int) {
			defer waiting.Done()
			for step := 0; step < 25; step++ {
				next := orgOnly(fmt.Sprintf("writer %d step %d", writer, step))
				lock.Lock()
				current := previous
				previous = next
				lock.Unlock()
				writable(t, dir, org, 50).Record("edit", current, next, "", false)
			}
		}(writer)
	}
	for reader := 0; reader < 4; reader++ {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			for step := 0; step < 50; step++ {
				history := Open(dir, org)
				index := history.Load()
				if index.Cursor < 0 || index.Cursor > len(index.States) {
					t.Errorf("observed an index with cursor %d over %d states", index.Cursor, len(index.States))
					return
				}
				history.Plan(-1)
			}
		}()
	}
	waiting.Wait()

	// Whatever interleaving happened, the index on disk is still readable and
	// self-consistent: an unreadable one would degrade to no history at all.
	final := Open(dir, org).Load()
	if len(final.States) == 0 {
		t.Fatal("the surviving index has no states")
	}
	if final.Cursor < 0 || final.Cursor > len(final.States)-1 {
		t.Fatalf("cursor %d is out of range over %d states", final.Cursor, len(final.States))
	}
}

func currentOrgBlob(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Cursor int `json:"cursor"`
		States []struct {
			OrgSHA *string `json:"org_sha"`
		} `json:"states"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	sha := document.States[document.Cursor].OrgSHA
	if sha == nil {
		t.Fatal("the current state has no org blob")
	}
	return filepath.Join(dir, "blobs", *sha)
}
