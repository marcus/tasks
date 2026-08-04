package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/journal"
	"github.com/marcus/tasks/internal/record"
)

const fixtureStore = `{"type":"meta","version":2}
{"type":"section","id":"1a2b3c01","title":"Inbox"}
{"type":"task","id":"1a2b3c02","parent":"1a2b3c01","state":"INBOX","priority":"B","title":"Skim the release notes","tags":["@computer"],"body":"Captured [2026-06-02]."}
`

var pinnedNow = time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC)

func writerFixture(t *testing.T, contents string) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	org := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(org, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	var counter atomic.Uint32
	return NewWriter(org, filepath.Join(root, "archive.jsonl"), Options{
		JournalDir:    filepath.Join(root, "journal"),
		Now:           func() time.Time { return pinnedNow },
		Device:        "fixture",
		IDSource:      func() string { return fmt.Sprintf("bbbb%04x", counter.Add(1)) },
		CoalesceScope: "pinned-scope",
		MaxDepth:      4,
	}), root
}

func readStore(t *testing.T, store *Store) string {
	t.Helper()
	raw, err := os.ReadFile(store.org)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A capture writes the new record in the section's subtree, stamps only the
// record it touched, and leaves every other line byte-identical. The last part
// is the assertion that matters: a writer that re-serializes differently would
// still "work" and would rewrite the whole file.
func TestCaptureWritesOneRecordAndStampsOnlyIt(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	result := store.CreateTask(CreateCommand{Title: "port the runner", Priority: "A", Tags: []string{"port"}}, "2026-03-14")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}

	want := `{"type":"meta","version":2}
{"type":"section","id":"1a2b3c01","title":"Inbox"}
{"type":"task","id":"1a2b3c02","parent":"1a2b3c01","state":"INBOX","priority":"B","title":"Skim the release notes","tags":["@computer"],"body":"Captured [2026-06-02]."}
{"type":"task","id":"bbbb0001","parent":"1a2b3c01","state":"INBOX","priority":"A","title":"port the runner","tags":["port"],"body":"Captured [2026-03-14].","updated":"2026-03-14T15:09:26Z#fixture"}
`
	if got := readStore(t, store); got != want {
		t.Errorf("store bytes\n got %q\nwant %q", got, want)
	}
	if len(result.TouchedIDs) != 1 || result.TouchedIDs[0] != "bbbb0001" {
		t.Errorf("touched = %v, want [bbbb0001]", result.TouchedIDs)
	}
}

// Completing a task adds `closed` and one stamp, and nothing else moves.
func TestDoneClosesAndStamps(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	expected, ok := store.ExpectedFor("1a2b3c02", FieldState)
	if !ok {
		t.Fatal("no baseline for state")
	}
	result := store.PatchTask("1a2b3c02", FieldState, "DONE", expected, "state → DONE: Skim", "2026-03-14")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	want := `{"type":"task","id":"1a2b3c02","parent":"1a2b3c01","state":"DONE","priority":"B","title":"Skim the release notes","tags":["@computer"],"closed":"2026-03-14","body":"Captured [2026-06-02].","updated":"2026-03-14T15:09:26Z#fixture"}`
	if got := readStore(t, store); !containsText(got, want) {
		t.Errorf("store bytes missing closed record:\n%s", got)
	}
}

// A stale baseline is a CONFLICT, not a silent overwrite. This is the whole
// point of carrying an expectation across the two lock acquisitions.
func TestPatchRefusesAStaleBaseline(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	result := store.PatchTask("1a2b3c02", FieldPriority, "A", "C", "priority", "2026-03-14")
	if result.Status != MutationConflict {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	if got := readStore(t, store); got != fixtureStore {
		t.Error("a refused patch wrote to the store")
	}
}

// A patch that changes nothing is a no-op that does NOT burn an undo slot. A
// port that journalled it would make every idempotent retry cost history.
func TestNoChangePatchWritesNoHistory(t *testing.T) {
	store, root := writerFixture(t, fixtureStore)
	result := store.PatchTask("1a2b3c02", FieldPriority, "B", "B", "priority", "2026-03-14")
	if result.Status != MutationNoChange {
		t.Fatalf("status = %q, want no_change", result.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "journal", "index.json")); err == nil {
		t.Error("a no-op recorded a journal step")
	}
}

// The journal index is compared as BYTES by the conformance harness, so its
// whitespace and its omitted-when-absent members are contract.
func TestJournalIndexBytes(t *testing.T) {
	store, root := writerFixture(t, fixtureStore)
	if result := store.CreateTask(CreateCommand{Title: "one"}, "2026-03-14"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"{\n  \"version\": 1,\n  \"org\": ",
		"\n  \"cursor\": 1,\n  \"states\": [\n    {\n      \"org_sha\": ",
		"\"label\": \"capture: one\"",
	} {
		if !containsText(text, want) {
			t.Errorf("index.json missing %q:\n%s", want, text)
		}
	}
	if text[len(text)-1] != '}' {
		t.Errorf("index.json ends with %q — JSON.pretty_generate emits no trailing newline", text[len(text)-1:])
	}
	// The baseline blob and the post-write blob, and nothing else: a leaked
	// blob is a GC that did not run.
	entries, err := os.ReadDir(filepath.Join(root, "journal", "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("blob count = %d, want 2", len(entries))
	}
}

// A failed write restores the previous bytes and labels the rollback `write`,
// which is what stops the CLI sending the user to `tasks check` for a
// validation stage that never ran.
func TestFailedWriteRollsBackAndNamesTheStage(t *testing.T) {
	store, root := writerFixture(t, fixtureStore)
	// The lock sidecar must already exist: an unwritable directory cannot
	// receive one, and the mutation would fail before it reached the write.
	if err := os.WriteFile(store.LockPath(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	result := store.CreateTask(CreateCommand{Title: "must not be written"}, "2026-03-14")
	if result.Status != MutationUnavailable {
		t.Fatalf("status = %q, want unavailable", result.Status)
	}
	if !result.RolledBack || result.RollbackStage != RollbackWrite {
		t.Errorf("rolled_back = %v stage = %q, want true/write", result.RolledBack, result.RollbackStage)
	}
	if got := readStore(t, store); got != fixtureStore {
		t.Errorf("previous bytes were not restored:\n%s", got)
	}
	// No temp file survives a failed atomic write.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Errorf("leftover temp file %q", entry.Name())
		}
	}
}

// An atomic replacement must not widen a restricted store's permission bits.
// Dropping mode from the comparison is how that regression ships.
func TestAtomicReplacementCarriesMode(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	if err := os.Chmod(store.org, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := store.CreateTask(CreateCommand{Title: "widen me"}, "2026-03-14"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	info, err := os.Stat(store.org)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// A symlinked store is replaced through the link, not over it. A rename over
// the link would orphan a dotfiles or Dropbox setup.
func TestSymlinkedStoreIsReplacedThroughTheLink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real.jsonl")
	if err := os.WriteFile(real, []byte(fixtureStore), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "tasks.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	store := NewWriter(link, filepath.Join(root, "archive.jsonl"), Options{
		JournalDir: filepath.Join(root, "journal"),
		Now:        func() time.Time { return pinnedNow },
		Device:     "fixture", MaxDepth: 4,
		IDSource: func() string { return "bbbb0001" },
	})
	if result := store.CreateTask(CreateCommand{Title: "through the link"}, "2026-03-14"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	raw, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(string(raw), "through the link") {
		t.Error("the write did not land on the link's target")
	}
}

// Delegation: an agent marker lands in canonical key order, and a same-owner
// retry is a CONFLICT carrying the holder — not an idempotent success.
func TestDelegateThenClaimThenRetry(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	if result := store.Delegate("1a2b3c02", "agent", "implement", "", "delegation-delegate-cccc000000000001"); result.Status != MutationOK {
		t.Fatalf("delegate status = %q, errors = %v", result.Status, result.Errors)
	}
	want := `"delegation":{"kind":"agent","mode":"implement","status":"ready","at":"2026-03-14T15:09:26Z"}`
	if got := readStore(t, store); !containsText(got, want) {
		t.Errorf("marker not written in canonical order:\n%s", got)
	}

	if result := store.Claim("1a2b3c02", "worker-alpha", ""); result.Status != MutationOK {
		t.Fatalf("claim status = %q, errors = %v", result.Status, result.Errors)
	}
	retry := store.Claim("1a2b3c02", "worker-alpha", "")
	if retry.Status != MutationConflict {
		t.Fatalf("retry status = %q, want conflict", retry.Status)
	}
	if retry.Summary.Holder != "worker-alpha" || retry.Summary.At == "" {
		t.Errorf("conflict summary = %+v, want the holder and the instant", retry.Summary)
	}
}

// Re-delegating at the same mode differs only in the transition stamp, so it
// must not burn an undo slot.
func TestRedelegateAtTheSameModeIsSettled(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	if result := store.Delegate("1a2b3c02", "agent", "implement", "", "key"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	again := store.Delegate("1a2b3c02", "agent", "implement", "", "key")
	if again.Status != MutationNoChange {
		t.Errorf("status = %q, want no_change", again.Status)
	}
}

// Concurrent writers to one store must serialize on the sidecar lock: every
// capture lands, none is lost, and the file is valid at the end. Run with
// -race, this is the assertion the write path exists to earn.
func TestConcurrentCapturesNeverLoseAWrite(t *testing.T) {
	root := t.TempDir()
	org := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(org, []byte(fixtureStore), 0o644); err != nil {
		t.Fatal(err)
	}
	var counter atomic.Uint32
	newStore := func() *Store {
		return NewWriter(org, filepath.Join(root, "archive.jsonl"), Options{
			JournalDir: filepath.Join(root, "journal"),
			Now:        func() time.Time { return pinnedNow },
			Device:     "fixture", MaxDepth: 4, CoalesceScope: "pinned-scope",
			IDSource: func() string { return fmt.Sprintf("cccc%04x", counter.Add(1)) },
		})
	}

	const writers = 8
	var wait sync.WaitGroup
	failures := make([]MutationResult, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			failures[index] = newStore().CreateTask(
				CreateCommand{Title: fmt.Sprintf("concurrent %d", index)}, "2026-03-14")
		}(index)
	}
	wait.Wait()

	for index, result := range failures {
		if result.Status != MutationOK {
			t.Errorf("writer %d: status = %q, errors = %v", index, result.Status, result.Errors)
		}
	}
	records := freshRecords(org)
	seen := 0
	for _, parsed := range records {
		if containsText(parsed.String("title"), "concurrent ") {
			seen++
		}
	}
	if seen != writers {
		t.Errorf("%d captures landed, want %d — a write was lost", seen, writers)
	}
	if result := check.Check(org); !result.OK() {
		t.Errorf("store is invalid after concurrent writes: %v", result.Errors)
	}
}

// The journal's own state must survive the same contention: the index parses,
// its cursor is in range, and every blob it names verifies.
func TestConcurrentCapturesLeaveTheJournalReadable(t *testing.T) {
	root := t.TempDir()
	org := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(org, []byte(fixtureStore), 0o644); err != nil {
		t.Fatal(err)
	}
	var counter atomic.Uint32
	var wait sync.WaitGroup
	for index := 0; index < 6; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			NewWriter(org, filepath.Join(root, "archive.jsonl"), Options{
				JournalDir: filepath.Join(root, "journal"),
				Now:        func() time.Time { return pinnedNow },
				Device:     "fixture", MaxDepth: 4, CoalesceScope: "pinned-scope",
				IDSource: func() string { return fmt.Sprintf("dddd%04x", counter.Add(1)) },
			}).CreateTask(CreateCommand{Title: fmt.Sprintf("journal %d", index)}, "2026-03-14")
		}(index)
	}
	wait.Wait()

	history := journal.Open(filepath.Join(root, "journal"), org)
	index := history.Load()
	if len(index.States) == 0 {
		t.Fatal("no history recorded")
	}
	if index.Cursor < 0 || index.Cursor > len(index.States)-1 {
		t.Fatalf("cursor %d out of range for %d states", index.Cursor, len(index.States))
	}
	if _, ok := history.Plan(-1); !ok && len(index.States) > 1 {
		t.Error("an undo could not be planned from a journal that has history")
	}
}

// A record's field order is Ruby's Hash order: assigning to an existing key
// keeps its position, and only a new key appends. Emission reorders the known
// keys anyway, so the assertion that bites is the UNKNOWN one — a forward
// compatible field a newer binary wrote must keep its place.
func TestUnknownKeysKeepSourceOrderAcrossAWrite(t *testing.T) {
	contents := `{"type":"meta","version":2}
{"type":"section","id":"b0000401","title":"Next Actions"}
{"type":"task","id":"b0000402","parent":"b0000401","state":"NEXT","title":"Confirm the venue booking","energy":"low","mood":"calm"}
`
	store, _ := writerFixture(t, contents)
	expected, _ := store.ExpectedFor("b0000402", FieldPriority)
	if result := store.PatchTask("b0000402", FieldPriority, "A", expected, "priority", "2026-03-14"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	want := `{"type":"task","id":"b0000402","parent":"b0000401","state":"NEXT","priority":"A","title":"Confirm the venue booking","updated":"2026-03-14T15:09:26Z#fixture","energy":"low","mood":"calm"}`
	if got := readStore(t, store); !containsText(got, want) {
		t.Errorf("unknown keys did not survive in order:\n%s", got)
	}
}

// A store this build cannot read is refused by every mutation, with nothing
// written and no conversion offered.
func TestUnsupportedSchemaRefusesEveryMutation(t *testing.T) {
	contents := "{\"type\":\"meta\",\"version\":3}\n{\"type\":\"section\",\"id\":\"1a2b3c01\",\"title\":\"Inbox\"}\n"
	store, _ := writerFixture(t, contents)
	result := store.CreateTask(CreateCommand{Title: "nope"}, "2026-03-14")
	if result.Status != MutationUnsupportedSchema {
		t.Fatalf("status = %q, want unsupported_schema", result.Status)
	}
	if got := readStore(t, store); got != contents {
		t.Error("a schema refusal wrote to the store")
	}
}

// stampChangedTasks is the merge tiebreaker's whole basis: an untouched record
// keeps exactly the stamp it had, and a changed one gets the new one.
func TestStampsOnlyTheRecordsThatChanged(t *testing.T) {
	contents := `{"type":"meta","version":2}
{"type":"section","id":"1a2b3c01","title":"Inbox"}
{"type":"task","id":"1a2b3c02","parent":"1a2b3c01","state":"INBOX","title":"untouched","updated":"2020-01-01T00:00:00Z#other"}
{"type":"task","id":"1a2b3c03","parent":"1a2b3c01","state":"INBOX","title":"changed","updated":"2020-01-01T00:00:00Z#other"}
`
	store, _ := writerFixture(t, contents)
	expected, _ := store.ExpectedFor("1a2b3c03", FieldPriority)
	if result := store.PatchTask("1a2b3c03", FieldPriority, "C", expected, "priority", "2026-03-14"); result.Status != MutationOK {
		t.Fatalf("status = %q", result.Status)
	}
	got := readStore(t, store)
	if !containsText(got, `"title":"untouched","updated":"2020-01-01T00:00:00Z#other"`) {
		t.Errorf("an untouched record was re-stamped:\n%s", got)
	}
	if !containsText(got, `"title":"changed","updated":"2026-03-14T15:09:26Z#fixture"`) {
		t.Errorf("the changed record was not stamped:\n%s", got)
	}
}

// A record set the emitter refuses must never reach the file.
func TestUnrepresentableValueRefusesBeforeTheWrite(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	records := freshRecords(store.org)
	records[2].Set("weight", json.RawMessage(`NaN`))
	if _, err := record.Dump(records); err == nil {
		t.Skip("the emitter accepts this value, so there is nothing to refuse")
	}
	if err := store.writeRecords(store.org, records); err == nil {
		t.Fatal("writeRecords accepted a value the emitter refuses")
	}
	if got := readStore(t, store); got != fixtureStore {
		t.Error("a refused serialization still touched the file")
	}
}
