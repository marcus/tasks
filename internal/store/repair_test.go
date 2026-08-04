package store

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// The convergence fixture from test/test_repair.rb: n>=2 of BOTH known defects,
// interleaved. Three records with no id, two temporal objects carrying an
// unknown key. Neither defect is the file's last remaining error at any point,
// which is exactly the state no other command can leave.
//
// The two temporal records are RAW JSON rather than canonical output, because
// the canonical writer DROPS exactly the keys they exist to carry. That the
// schema writer cannot express this damage is the whole point: only a foreign
// writer or a hand edit produces it.
const repairBothDefects = `{"type":"meta","version":2}
{"type":"section","id":"1e000001","title":"Next Actions"}
{"type":"task","parent":"1e000001","state":"NEXT","title":"No id one"}
{"type":"task","parent":"1e000001","state":"TODO","title":"No id two"}
{"type":"task","id":"a1000001","parent":"1e000001","state":"TODO","title":"Start time carrying an unknown nested key","scheduled":"2026-06-16","scheduled_time":{"local":"08:30","timezone":"Europe/London","precision":"minute"},"updated":"2026-01-02T03:04:05Z#fixture"}
{"type":"task","parent":"1e000001","state":"TODO","title":"No id three"}
{"type":"task","id":"a1000002","parent":"1e000001","state":"TODO","title":"Due time carrying an unknown nested key","deadline":"2026-06-18","deadline_time":{"local":"17:00","calendar_uid":"abc"},"updated":"2026-01-03T03:04:05Z#fixture"}
`

// Both known defects plus one this command does not know how to fix. The
// unknown one must veto the whole pass.
const repairPlusUnknown = `{"type":"meta","version":2}
{"type":"section","id":"1e000001","title":"Next Actions"}
{"type":"task","parent":"1e000001","state":"NEXT","title":"No id one"}
{"type":"task","parent":"1e000001","state":"TODO","title":"No id two"}
{"type":"task","id":"a1000001","parent":"1e000001","state":"BOGUS","title":"A state no build knows"}
`

var hexID = regexp.MustCompile(`\A[0-9a-f]{8}\z`)

func TestRepairConvergesAStoreCarryingManyInstancesOfBothDefects(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	before := check.Check(store.org)
	if before.OK() || len(before.Errors) != 5 {
		t.Fatalf("seed has %d errors, want 5 (3 id-less records + 2 unknown temporal keys)",
			len(before.Errors))
	}

	result := store.Repair(false)
	if result.Status != RepairOK || !result.Written {
		t.Fatalf("repair = %q written=%v blockers=%v", result.Status, result.Written, result.Blockers)
	}
	if len(result.Fixes) != 5 {
		t.Errorf("fixes = %d, want 5", len(result.Fixes))
	}
	if !check.Check(store.org).OK() {
		t.Error("the store must validate after ONE pass")
	}
}

func TestRepairMintsADistinctIDForEveryIDLessRecord(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	if result := store.Repair(false); !result.OK() {
		t.Fatalf("repair = %q", result.Status)
	}
	raw, err := os.ReadFile(store.org)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, parsed := range record.Parse(raw).Records[1:] {
		id := parsed.String("id")
		if !hexID.MatchString(id) {
			t.Errorf("id = %q, want eight hex characters", id)
		}
		if seen[id] {
			t.Errorf("minted ids must not collide: %q", id)
		}
		seen[id] = true
	}
}

func TestRepairDropsOnlyTheUnknownKeysInsideATemporalObject(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	if result := store.Repair(false); !result.OK() {
		t.Fatalf("repair = %q", result.Status)
	}
	scheduled, _ := recordFor(t, store.org, "Start time carrying an unknown nested key")
	if got, _ := scheduled.Get("scheduled_time"); string(got) != `{"local":"08:30","timezone":"Europe/London"}` {
		t.Errorf("scheduled_time = %s", got)
	}
	deadline, _ := recordFor(t, store.org, "Due time carrying an unknown nested key")
	if got, _ := deadline.Get("deadline_time"); string(got) != `{"local":"17:00"}` {
		t.Errorf("deadline_time = %s", got)
	}
}

func TestRepairIsIdempotentAndACleanStoreIsANoOp(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	if result := store.Repair(false); !result.OK() {
		t.Fatalf("repair = %q", result.Status)
	}
	afterFirst := readStore(t, store)

	second := store.Repair(false)
	if !second.OK() || len(second.Fixes) != 0 || second.Written {
		t.Errorf("second pass = %q, %d fixes, written=%v", second.Status,
			len(second.Fixes), second.Written)
	}
	if got := readStore(t, store); got != afterFirst {
		t.Error("a clean store is left byte-identical")
	}
}

// A repair asserts nothing about a task's CONTENT: it converges bytes the store
// refuses to write over. Stamping would falsify "when this task last changed"
// and, in the last-write-wins merge, hand the repairing device a win it did not
// earn — for a dropped temporal key, that means beating the newer binary that
// understood the field with the copy that just discarded it.
func TestRepairLeavesTheUpdatedStampExactlyAsItFoundIt(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	if result := store.Repair(false); !result.OK() {
		t.Fatalf("repair = %q", result.Status)
	}
	scheduled, _ := recordFor(t, store.org, "Start time carrying an unknown nested key")
	if got := scheduled.String("updated"); got != "2026-01-02T03:04:05Z#fixture" {
		t.Errorf("updated = %q, want it untouched", got)
	}
	deadline, _ := recordFor(t, store.org, "Due time carrying an unknown nested key")
	if got := deadline.String("updated"); got != "2026-01-03T03:04:05Z#fixture" {
		t.Errorf("updated = %q, want it untouched", got)
	}
	minted, _ := recordFor(t, store.org, "No id one")
	if minted.Has("updated") {
		t.Error("a record that never carried a stamp does not gain one from a repair")
	}
}

func TestAnOrdinaryMutationSucceedsAfterRepairAndStampsNormally(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	if result := store.PatchTask("a1000001", FieldPriority, "A", "", "", sweepDay); result.Status != MutationStoreInvalid {
		t.Fatalf("pre-repair mutation = %q, want the store to be unwritable", result.Status)
	}
	if result := store.Repair(false); !result.OK() {
		t.Fatalf("repair = %q", result.Status)
	}

	expected, _ := store.ExpectedFor("a1000002", FieldPriority)
	if result := store.PatchTask("a1000002", FieldPriority, "B", expected, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("post-repair mutation = %q %v", result.Status, result.Errors)
	}
	edited, _ := recordFor(t, store.org, "Due time carrying an unknown nested key")
	if got := edited.String("updated"); got == "2026-01-03T03:04:05Z#fixture" {
		t.Error("a real edit does bump the stamp")
	}
	if !check.Check(store.org).OK() {
		t.Error("the store still validates")
	}
}

func TestDryRunReportsEveryFixAndWritesNothing(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects)
	before := readStore(t, store)

	result := store.Repair(true)
	if !result.OK() || !result.DryRun || result.Written {
		t.Fatalf("dry run = %q dry=%v written=%v", result.Status, result.DryRun, result.Written)
	}
	if len(result.Fixes) != 5 {
		t.Errorf("fixes = %d, want 5", len(result.Fixes))
	}
	if got := readStore(t, store); got != before {
		t.Error("byte-identical after a dry run")
	}
	kinds := map[RepairKind]int{}
	for _, fix := range result.Fixes {
		kinds[fix.Kind]++
	}
	if kinds[RepairMintedID] != 3 || kinds[RepairDroppedTemporalKeys] != 2 {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestRepairRefusesADefectItDoesNotKnowAndWritesNothing(t *testing.T) {
	store, _ := writerFixture(t, repairPlusUnknown)
	before := readStore(t, store)

	result := store.Repair(false)
	if result.Status != RepairUnrepairable || result.Written {
		t.Fatalf("repair = %q written=%v", result.Status, result.Written)
	}
	if len(result.Fixes) != 2 {
		t.Errorf("fixes = %d, want the two it COULD fix", len(result.Fixes))
	}
	if len(result.Blockers) != 1 || !containsText(result.Blockers[0].Message, "invalid state") {
		t.Errorf("blockers = %+v", result.Blockers)
	}
	if got := readStore(t, store); got != before {
		t.Error("a refused pass must never leave a partially repaired file")
	}
}

// A line this binary cannot parse is a line it must not rewrite the file
// without: writing here would silently DELETE it.
func TestRepairRefusesAFileWithAnUnparseableLine(t *testing.T) {
	store, _ := writerFixture(t, repairBothDefects+"not json at all\n")
	before := readStore(t, store)

	result := store.Repair(false)
	if result.Status != RepairUnrepairable || result.Written {
		t.Fatalf("repair = %q written=%v", result.Status, result.Written)
	}
	if got := readStore(t, store); got != before {
		t.Error("the unreadable line survives")
	}
}

func TestRepairRefusesAStoreWhoseSchemaVersionThisBuildCannotRead(t *testing.T) {
	future := `{"type":"meta","version":99}
{"type":"task","parent":"1e000001","state":"TODO","title":"No id"}
`
	store, _ := writerFixture(t, future)
	before := readStore(t, store)

	result := store.Repair(false)
	if result.Status != RepairUnsupportedSchema {
		t.Fatalf("repair = %q, want unsupported_schema", result.Status)
	}
	if len(result.Blockers) != 1 || !containsText(result.Blockers[0].Message, "unsupported meta version") {
		t.Errorf("blockers = %+v", result.Blockers)
	}
	if got := readStore(t, store); got != before {
		t.Error("nothing was written")
	}
}

// post_write_failure validates BOTH files, so an id-less record in the archive
// wedges every mutation exactly as one in the live file does. A live-only repair
// would be the same dead end one file over.
func TestRepairConvergesTheArchiveTooAndMintsFromOnePool(t *testing.T) {
	store, root := writerFixture(t, repairBothDefects)
	archive := `{"type":"meta","version":2}
{"type":"task","state":"DONE","title":"swept one","closed":"2026-01-01","archived":"2026-01-02"}
{"type":"task","state":"DONE","title":"swept two","closed":"2026-01-01","archived":"2026-01-02"}
`
	if err := os.WriteFile(filepath.Join(root, "archive.jsonl"), []byte(archive), 0o644); err != nil {
		t.Fatal(err)
	}

	result := store.Repair(false)
	if !result.OK() || !result.Written {
		t.Fatalf("repair = %q %+v", result.Status, result.Blockers)
	}
	if !check.Check(store.org).OK() || !check.Check(store.archive).OK() {
		t.Fatal("both files must validate")
	}
	sawArchive := false
	for _, fix := range result.Fixes {
		if fix.File == "archive.jsonl" {
			sawArchive = true
		}
	}
	if !sawArchive {
		t.Error("the report names the archive it repaired")
	}

	live := map[string]bool{}
	liveRaw, _ := os.ReadFile(store.org)
	for _, parsed := range record.Parse(liveRaw).Records {
		if id := parsed.String("id"); id != "" {
			live[id] = true
		}
	}
	archiveRaw, _ := os.ReadFile(store.archive)
	for _, parsed := range record.Parse(archiveRaw).Records {
		if id := parsed.String("id"); id != "" && live[id] {
			t.Errorf("id %q collides across the two files — ids mint from ONE pool", id)
		}
	}
}
