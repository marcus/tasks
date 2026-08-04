package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/atomic"
	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/journal"
)

// The shared Ruby fixture, byte for byte: `test/test_helper.rb`'s FIXTURE. The
// undo tests below are the Go half of `test/test_journal.rb`, which drives its
// assertions off exactly these records — "Book flight in Concur" for an
// ordinary edit, "Old finished thing" for the archive sweep an undo has to
// replay in the right order.
const journalFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"random thought about the garden"}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Book flight in Concur","tags":["@computer","important","urgent"],"deadline":"2026-07-02"}
{"type":"task","id":"aaaa0005","parent":"aaaa0003","state":"NEXT","priority":"B","title":"Review PR backlog","tags":["@computer","important"]}
{"type":"task","id":"aaaa0006","parent":"aaaa0003","state":"TODO","priority":"A","title":"Midyear self-eval","tags":["@computer","important"],"scheduled":"2026-07-03"}
{"type":"task","id":"aaaa0007","parent":"aaaa0003","state":"WAITING","title":"Travel desk reply","tags":["@email","urgent"],"body":"Some note line."}
{"type":"task","id":"aaaa0008","parent":"aaaa0003","state":"DONE","priority":"C","title":"Old finished thing","tags":["@computer"],"closed":"2026-06-20"}
{"type":"section","id":"aaaa0009","title":"Home"}
{"type":"task","id":"aaaa000a","parent":"aaaa0009","state":"NEXT","title":"Water the plants","tags":["@home"]}
`

const flightID = "aaaa0004"

// -- helpers ------------------------------------------------------------------

func setPriority(t *testing.T, store *Store, id, value string) {
	t.Helper()
	expected, _ := store.ExpectedFor(id, FieldPriority)
	result := store.PatchTask(id, FieldPriority, value, expected, "", "2026-03-14")
	if result.Status != MutationOK {
		t.Fatalf("set priority %q = %q, errors %v", value, result.Status, result.Errors)
	}
}

func journalCursor(t *testing.T, root string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatalf("reading journal index: %v", err)
	}
	var document struct {
		Cursor int `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parsing journal index: %v", err)
	}
	return document.Cursor
}

func readFileOrAbsent(t *testing.T, path string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// denyAtomicWrite is `with_atomic_write_denied`: every write to one path fails
// with EACCES, and every other write passes through untouched.
func denyAtomicWrite(t *testing.T, path string) {
	t.Helper()
	restore := atomic.SetWriteHook(func(candidate, content string) error {
		if candidate == path {
			return os.ErrPermission
		}
		return atomic.WriteDirect(candidate, content)
	})
	t.Cleanup(restore)
}

// writerAt builds a second writing store over an existing sandbox — a separate
// process, in effect, sharing one journal.
func writerAt(t *testing.T, root string) *Store {
	t.Helper()
	return NewWriter(filepath.Join(root, "tasks.jsonl"), filepath.Join(root, "archive.jsonl"), Options{
		JournalDir:    filepath.Join(root, "journal"),
		Now:           func() time.Time { return pinnedNow },
		Device:        "fixture",
		CoalesceScope: "pinned-scope",
		MaxDepth:      4,
	})
}

// setRemoveHook denies the delete half of a restore, and returns the undo.
func setRemoveHook(hook func(string) error) func() {
	previous := removeFile
	removeFile = hook
	return func() { removeFile = previous }
}

// -- persistence --------------------------------------------------------------

// A brand-new Store — a fresh process, in effect — undoes an edit the previous
// one made. That is the whole point of an on-disk journal.
func TestUndoSurvivesANewStoreInstance(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	before := readStore(t, store)
	setPriority(t, store, flightID, "C")
	if readStore(t, store) == before {
		t.Fatal("the edit did not change the file")
	}

	second := writerAt(t, root)
	outcome, label := second.HistoryStep(-1)
	if outcome != HistoryOK {
		t.Fatalf("undo = %q, want ok", outcome)
	}
	if label == "" || !containsText(label, "Book flight") {
		t.Errorf("label = %q, want it to name the task", label)
	}
	if got := readStore(t, store); got != before {
		t.Errorf("undo did not restore the bytes\n got %q\nwant %q", got, before)
	}
}

func TestRedoSurvivesANewStoreInstance(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	after := readStore(t, store)
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Fatalf("undo = %q", outcome)
	}

	second := writerAt(t, root)
	if outcome, _ := second.HistoryStep(1); outcome != HistoryOK {
		t.Fatalf("redo = %q, want ok", outcome)
	}
	if got := readStore(t, store); got != after {
		t.Errorf("redo did not reapply the edit\n got %q\nwant %q", got, after)
	}
}

// A journal keyed to one org path must not replay onto a different file.
func TestJournalPersistsOnlyBetweenMatchingOrg(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")

	other := t.TempDir()
	elsewhere := filepath.Join(other, "tasks.jsonl")
	if err := os.WriteFile(elsewhere, []byte(journalFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := NewWriter(elsewhere, filepath.Join(other, "archive.jsonl"), Options{
		JournalDir: filepath.Join(root, "journal"),
		Now:        store.options.Now, Device: "fixture",
	})
	if outcome, _ := foreign.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo against a foreign org = %q, want empty", outcome)
	}
}

// Capping keeps exactly UNDO_LIMIT steps, across instances.
func TestCappingKeepsOnlyRecentHistoryAcrossInstances(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	priorities := []string{"A", "B", "C"}
	for index := 0; index < 55; index++ {
		setPriority(t, writerAt(t, root), flightID, priorities[(index+1)%3])
	}
	undone := 0
	for {
		outcome, _ := store.HistoryStep(-1)
		if outcome != HistoryOK {
			break
		}
		undone++
	}
	if undone != store.options.UndoLimit {
		t.Errorf("undid %d steps, want the %d-step limit", undone, store.options.UndoLimit)
	}
}

// A no-op mutation records nothing: it must not burn an undo slot with a label
// that reverts nothing.
func TestNoopMutationRecordsNoHistory(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")

	// Re-setting the same priority writes identical content.
	fresh := writerAt(t, root)
	expected, _ := fresh.ExpectedFor(flightID, FieldPriority)
	if result := fresh.PatchTask(flightID, FieldPriority, "C", expected, "", "2026-03-14"); result.Status != MutationNoChange {
		t.Fatalf("idempotent patch = %q, want no_change", result.Status)
	}

	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Fatal("the priority change should be undoable")
	}
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("the no-op recorded a history step")
	}
}

// -- the conflict gate ---------------------------------------------------------

func TestUndoRefusesAfterAnOutOfBandEdit(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	edited := readStore(t, store) +
		`{"type":"task","id":"aaaa00ff","parent":"aaaa0003","state":"TODO","title":"added out of band"}` + "\n"
	if err := os.WriteFile(store.org, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, label := store.HistoryStep(-1)
	if outcome != HistoryConflict {
		t.Fatalf("undo = %q, want conflict", outcome)
	}
	if label == "" {
		t.Error("a conflict must name the label it declined to revert")
	}
	if got := readStore(t, store); got != edited {
		t.Error("a refused undo must not clobber the file")
	}
}

// -- resilience ----------------------------------------------------------------

func TestCorruptCursorDegradesToEmptyNotCrash(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")

	index := filepath.Join(root, "journal", "index.json")
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["cursor"] = 999
	patched, _ := json.Marshal(document)
	if err := os.WriteFile(index, patched, 0o644); err != nil {
		t.Fatal(err)
	}

	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo over a corrupt cursor = %q, want empty", outcome)
	}
	// ...and a fresh mutation must still succeed rather than crash in record().
	setPriority(t, writerAt(t, root), flightID, "B")
}

func TestCorruptTopLevelIndexShapeDegradesToEmpty(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	if err := os.WriteFile(filepath.Join(root, "journal", "index.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo = %q, want empty", outcome)
	}
}

func TestMissingBlobDegradesToEmptyNotCrash(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	blobs := filepath.Join(root, "journal", "blobs")
	entries, err := os.ReadDir(blobs)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(blobs, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo with no blobs = %q, want empty", outcome)
	}
}

func TestDirectoryAtCurrentBlobMakesUndoEmptyAndNextRecordRepairsHistory(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	afterFirst := readStore(t, store)

	blob := currentOrgBlob(t, root)
	if err := os.Remove(blob); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blob, 0o755); err != nil {
		t.Fatal(err)
	}

	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo = %q, want empty", outcome)
	}
	if got := readStore(t, store); got != afterFirst {
		t.Error("a failed inspection must not touch task bytes")
	}

	second := writerAt(t, root)
	setPriority(t, second, flightID, "B")
	if info, err := os.Lstat(blob); err != nil || !info.Mode().IsRegular() {
		t.Fatal("the next record must replace the empty directory with a blob")
	}
	if outcome, _ := second.HistoryStep(-1); outcome != HistoryOK {
		t.Fatalf("undo after repair = %q, want ok", outcome)
	}
	if got := readStore(t, store); got != afterFirst {
		t.Error("the repaired history restores the previous bytes")
	}
}

func TestSymlinkAtCurrentBlobIsNeverFollowedAndNextRecordRepairsHistory(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	afterFirst := readStore(t, store)

	blob := currentOrgBlob(t, root)
	sentinel := filepath.Join(root, "journal-symlink-target")
	if err := os.WriteFile(sentinel, []byte("do not read or replace me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blob); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, blob); err != nil {
		t.Fatal(err)
	}

	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo = %q, want empty", outcome)
	}
	if got := readStore(t, store); got != afterFirst {
		t.Error("a failed inspection must not touch task bytes")
	}

	setPriority(t, writerAt(t, root), flightID, "B")
	if info, err := os.Lstat(blob); err != nil || !info.Mode().IsRegular() {
		t.Fatal("the next record must replace the symlink with a real blob")
	}
	if raw, _ := os.ReadFile(sentinel); string(raw) != "do not read or replace me" {
		t.Error("the symlink target was written through")
	}
}

func TestDirectoryIndexDegradesToEmptyAndNextRecordRebuildsHistory(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	afterFirst := readStore(t, store)

	index := filepath.Join(root, "journal", "index.json")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o755); err != nil {
		t.Fatal(err)
	}

	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Errorf("undo = %q, want empty", outcome)
	}
	if got := readStore(t, store); got != afterFirst {
		t.Error("a failed inspection must not touch task bytes")
	}

	second := writerAt(t, root)
	setPriority(t, second, flightID, "B")
	if info, err := os.Lstat(index); err != nil || !info.Mode().IsRegular() {
		t.Fatal("the next record must replace the directory with an index file")
	}
	if outcome, _ := second.HistoryStep(-1); outcome != HistoryOK {
		t.Fatalf("undo after rebuild = %q, want ok", outcome)
	}
}

// -- rollback, under injected faults -------------------------------------------
//
// These are the tests the write half exists for. Every one of them denies a
// specific write and asserts the same two things: the task bytes are exactly
// what they were, and the journal cursor still points where it did. An undo
// that half-applied would pass neither.

func TestUndoCursorCommitFailureRestoresOrgArchiveAndOriginalCursor(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	if result := store.ArchiveSweep("2026-03-14", nil); result.Roots != 1 {
		t.Fatalf("sweep moved %d roots, want 1 (%v)", result.Roots, result.Refusal)
	}
	orgBefore := readStore(t, store)
	archiveBefore, present := readFileOrAbsent(t, store.archive)
	if !present {
		t.Fatal("the sweep should have written an archive")
	}
	cursor := journalCursor(t, root)

	denyAtomicWrite(t, filepath.Join(root, "journal", "index.json"))
	outcome, label := store.HistoryStep(-1)
	if outcome != HistoryConflict || label != "archive sweep" {
		t.Fatalf("undo = (%q, %q), want (conflict, archive sweep)", outcome, label)
	}
	if got := readStore(t, store); got != orgBefore {
		t.Error("the live file was not restored")
	}
	if got, present := readFileOrAbsent(t, store.archive); !present || got != archiveBefore {
		t.Error("the archive was not restored")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want the original %d", got, cursor)
	}

	// The retained cursor still points at the undoable sweep.
	atomic.SetWriteHook(nil)
	if outcome, label := store.HistoryStep(-1); outcome != HistoryOK || label != "archive sweep" {
		t.Fatalf("second undo = (%q, %q), want (ok, archive sweep)", outcome, label)
	}
	if _, present := readFileOrAbsent(t, store.archive); present {
		t.Error("undoing the sweep must remove the archive it created")
	}
}

func TestRedoCursorCommitFailureRestoresArchiveAbsenceAndOriginalCursor(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	if result := store.ArchiveSweep("2026-03-14", nil); result.Roots != 1 {
		t.Fatalf("sweep = %d roots", result.Roots)
	}
	sweptOrg := readStore(t, store)
	sweptArchive, _ := readFileOrAbsent(t, store.archive)
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Fatal("undo of the sweep should succeed")
	}
	before := readStore(t, store)
	if _, present := readFileOrAbsent(t, store.archive); present {
		t.Fatal("undo should have removed the archive")
	}
	cursor := journalCursor(t, root)

	denyAtomicWrite(t, filepath.Join(root, "journal", "index.json"))
	if outcome, label := store.HistoryStep(1); outcome != HistoryConflict || label != "archive sweep" {
		t.Fatalf("redo = (%q, %q), want (conflict, archive sweep)", outcome, label)
	}
	if got := readStore(t, store); got != before {
		t.Error("the live file was not restored")
	}
	if _, present := readFileOrAbsent(t, store.archive); present {
		t.Error("a failed redo must leave the archive absent")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want %d", got, cursor)
	}

	atomic.SetWriteHook(nil)
	if outcome, _ := store.HistoryStep(1); outcome != HistoryOK {
		t.Fatal("the retained cursor still points at the redoable sweep")
	}
	if got := readStore(t, store); got != sweptOrg {
		t.Error("redo did not reapply the sweep to the live file")
	}
	if got, present := readFileOrAbsent(t, store.archive); !present || got != sweptArchive {
		t.Error("redo did not reapply the sweep to the archive")
	}
}

func TestOrgRestoreFailureKeepsFilesAndCursorAtOriginalState(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	before := readStore(t, store)
	cursor := journalCursor(t, root)

	denyAtomicWrite(t, store.org)
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryConflict {
		t.Fatalf("undo = %q, want conflict", outcome)
	}
	if got := readStore(t, store); got != before {
		t.Error("a failed restore must leave the file alone")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want %d", got, cursor)
	}

	atomic.SetWriteHook(nil)
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Error("the undo is still available once writes work again")
	}
}

func TestArchiveWriteFailureDuringRedoKeepsArchiveAbsent(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	if result := store.ArchiveSweep("2026-03-14", nil); result.Roots != 1 {
		t.Fatalf("sweep = %d roots", result.Roots)
	}
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Fatal("undo of the sweep should succeed")
	}
	before := readStore(t, store)
	cursor := journalCursor(t, root)

	denyAtomicWrite(t, store.archive)
	if outcome, label := store.HistoryStep(1); outcome != HistoryConflict || label != "archive sweep" {
		t.Fatalf("redo = (%q, %q), want (conflict, archive sweep)", outcome, label)
	}
	if got := readStore(t, store); got != before {
		t.Error("the live file was not restored")
	}
	if _, present := readFileOrAbsent(t, store.archive); present {
		t.Error("the archive must stay absent when its write failed")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want %d", got, cursor)
	}
}

// The rollback retries once, and the retry is what keeps a transient failure
// from leaving the two files in different states.
func TestTransientRollbackWriteFailureRetriesWithoutSplitState(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	before := readStore(t, store)
	cursor := journalCursor(t, root)
	index := filepath.Join(root, "journal", "index.json")

	orgWrites := 0
	restore := atomic.SetWriteHook(func(candidate, content string) error {
		if candidate == store.org {
			orgWrites++
			if orgWrites == 2 {
				return os.ErrPermission
			}
		}
		if candidate == index {
			return os.ErrPermission
		}
		return atomic.WriteDirect(candidate, content)
	})
	outcome, _ := store.HistoryStep(-1)
	restore()

	if outcome != HistoryConflict {
		t.Fatalf("undo = %q, want conflict", outcome)
	}
	if orgWrites != 3 {
		t.Errorf("org writes = %d, want 3 (forward, failed rollback, retried rollback)", orgWrites)
	}
	if got := readStore(t, store); got != before {
		t.Error("the retry must restore the original bytes")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want %d", got, cursor)
	}
}

// A commit that installs the index and THEN reports failure is the only case
// where the cursor rollback has real work to do — and it retries too.
func TestCursorRollbackRetriesWhenCommitFailedAfterInstallingIndex(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "C")
	before := readStore(t, store)
	cursor := journalCursor(t, root)
	index := filepath.Join(root, "journal", "index.json")

	indexWrites := 0
	restore := atomic.SetWriteHook(func(candidate, content string) error {
		if candidate == index {
			indexWrites++
			if indexWrites == 1 {
				if err := atomic.WriteDirect(candidate, content); err != nil {
					return err
				}
				return os.ErrPermission
			}
			if indexWrites == 2 {
				return os.ErrPermission
			}
		}
		return atomic.WriteDirect(candidate, content)
	})
	outcome, _ := store.HistoryStep(-1)
	restore()

	if outcome != HistoryConflict {
		t.Fatalf("undo = %q, want conflict", outcome)
	}
	if indexWrites != 3 {
		t.Errorf("index writes = %d, want 3 (commit, failed rollback, retried rollback)", indexWrites)
	}
	if got := readStore(t, store); got != before {
		t.Error("the files must be back where they started")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want the original %d", got, cursor)
	}
}

// When the rollback cannot succeed at all, the sweep's forward install is what
// survives — a live copy AND an archive copy, both valid. Duplicated data is
// recoverable; a task that exists in neither file is not.
func TestPersistentRollbackFailureKeepsArchiveCopyAndOriginalCursor(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	if result := store.ArchiveSweep("2026-03-14", nil); result.Roots != 1 {
		t.Fatalf("sweep = %d roots", result.Roots)
	}
	cursor := journalCursor(t, root)

	orgWrites := 0
	restore := atomic.SetWriteHook(func(candidate, content string) error {
		if candidate == store.org {
			orgWrites++
			if orgWrites > 1 {
				return os.ErrPermission
			}
		}
		return atomic.WriteDirect(candidate, content)
	})
	denyRemove := setRemoveHook(func(path string) error {
		if path == store.archive {
			return os.ErrPermission
		}
		return os.Remove(path)
	})
	outcome, label := store.HistoryStep(-1)
	restore()
	denyRemove()

	if outcome != HistoryConflict || label != "archive sweep" {
		t.Fatalf("undo = (%q, %q), want (conflict, archive sweep)", outcome, label)
	}
	if orgWrites != 3 {
		t.Errorf("org writes = %d, want 3", orgWrites)
	}
	if !containsText(readStore(t, store), "Old finished thing") {
		t.Error("the forward install preserves a live copy when archive deletion fails")
	}
	archive, present := readFileOrAbsent(t, store.archive)
	if !present || !containsText(archive, "Old finished thing") {
		t.Error("the archive copy remains when rollback cannot restore the old live bytes")
	}
	if got := journalCursor(t, root); got != cursor {
		t.Errorf("cursor = %d, want %d", got, cursor)
	}
	if !check.Check(store.org).OK() {
		t.Error("the live file must stay valid")
	}
	if !check.Check(store.archive).OK() {
		t.Error("the archive must stay valid")
	}
}

// -- the repair exemption -------------------------------------------------------

// A step marked `repair` restores bytes that FAIL today's invariants, on
// purpose: they are the malformed record the user asked to fix. That is the
// exemption, and `repair store` earns it the same way a targeted repair does.
func TestUndoOfARepairStepRestoresTheMalformedBytes(t *testing.T) {
	invalid := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"W"}
{"type":"task","parent":"aaaa0001","state":"TODO","title":"No id one"}
{"type":"task","parent":"aaaa0001","state":"TODO","title":"No id two"}
`
	store, _ := writerFixture(t, invalid)
	if check.Check(store.org).OK() {
		t.Fatal("the seed must be invalid")
	}
	before := readStore(t, store)

	result := store.Repair(false)
	if result.Status != RepairOK || !result.Written {
		t.Fatalf("repair = %q written=%v blockers=%v", result.Status, result.Written, result.Blockers)
	}
	if !check.Check(store.org).OK() {
		t.Fatal("the repair must leave the file valid")
	}

	outcome, label := store.HistoryStep(-1)
	if outcome != HistoryOK {
		t.Fatalf("undo of a repair = %q, want ok — the exemption is the point", outcome)
	}
	if label != "repair store" {
		t.Errorf("label = %q", label)
	}
	if got := readStore(t, store); got != before {
		t.Errorf("undo must restore the EXACT pre-repair bytes\n got %q\nwant %q", got, before)
	}
	if outcome, _ := store.HistoryStep(1); outcome != HistoryOK {
		t.Fatal("redo must re-apply the repair")
	}
	if !check.Check(store.org).OK() {
		t.Error("the redone repair leaves the store valid")
	}
}

// A targeted field patch that fixed its OWN invalid record earns the same
// exemption, and it survives being coalesced into by a follow-up edit — the
// coalesced step replaces the tip's content but keeps its before-state, so
// losing the flag there would make the undo refuse the bytes the user asked to
// restore.
func TestCoalescingOntoARepairStepKeepsTheUndoExemption(t *testing.T) {
	invalid := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"W"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"TODO","title":"Fix me","scheduled":"not-a-date"}
`
	store, _ := writerFixture(t, invalid)
	if check.Check(store.org).OK() {
		t.Fatal("the seed must be invalid")
	}
	invalidBytes := readStore(t, store)

	repaired := store.Patch(PatchRequest{
		ID: "aaaa0002", Field: FieldScheduled, Value: TextValue("2026-08-01"),
		Today: "2026-03-14", CoalesceKey: "edit-session",
	})
	if repaired.Status != MutationOK {
		t.Fatalf("targeted repair = %q %v", repaired.Status, repaired.Errors)
	}
	if !check.Check(store.org).OK() {
		t.Fatal("the repair leaves the file clean")
	}
	expected, _ := store.ExpectedFor("aaaa0002", FieldScheduled)
	followup := store.Patch(PatchRequest{
		ID: "aaaa0002", Field: FieldScheduled, Value: TextValue("2026-08-02"),
		Expected: expected, Today: "2026-03-14", CoalesceKey: "edit-session",
	})
	if followup.Status != MutationOK {
		t.Fatalf("follow-up = %q %v", followup.Status, followup.Errors)
	}

	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Fatal("undo of a coalesced repair step must not hit the validity gate")
	}
	if got := readStore(t, store); got != invalidBytes {
		t.Error("undo restores the exact pre-repair bytes")
	}
	if outcome, _ := store.HistoryStep(1); outcome != HistoryOK {
		t.Fatal("redo re-applies the coalesced repair")
	}
	if !check.Check(store.org).OK() {
		t.Error("the redone repair leaves the store valid")
	}
}

// The gate itself. An UNMARKED step whose restored state would fail today's
// invariants is refused, and the files are put back — undo is not a way to
// write bytes no command would accept.
func TestUndoRefusesToRestoreAStateThatFailsTodaysInvariants(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	valid := readStore(t, store)

	// A history whose before-state is invalid and is NOT marked as a repair.
	// Fabricated directly, because no ported command produces one — which is
	// exactly why the gate has to be asserted rather than assumed.
	broken := valid + "not a record at all\n"
	history := journal.Open(filepath.Join(root, "journal"), store.org).
		Writable(journal.Limit, "pinned-scope")
	if !history.Record("hand-edited", journal.Snapshot{Org: &broken}, store.FileSnapshot(), "", false) {
		t.Fatal("recording the fabricated history failed")
	}

	if outcome, label := store.HistoryStep(-1); outcome != HistoryConflict || label != "hand-edited" {
		t.Fatalf("undo = (%q, %q), want (conflict, hand-edited)", outcome, label)
	}
	if got := readStore(t, store); got != valid {
		t.Errorf("a refused undo must put the file back\n got %q\nwant %q", got, valid)
	}
}

func currentOrgBlob(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Cursor int `json:"cursor"`
		States []struct {
			OrgSHA string `json:"org_sha"`
		} `json:"states"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "journal", "blobs", document.States[document.Cursor].OrgSHA)
}
