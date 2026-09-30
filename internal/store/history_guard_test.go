package store

import (
	"os"
	"path/filepath"
	"testing"
)

// The revision a guarded step and a change poll compare must be the SAME token
// the checked read publishes, or a client holding /meta's revision would be
// refused as stale against a store that never moved.
func TestStoreRevisionMatchesTheCheckedRead(t *testing.T) {
	for _, archive := range []string{"absent", "empty", "present"} {
		t.Run(archive, func(t *testing.T) {
			store, root := writerFixture(t, journalFixture)
			path := filepath.Join(root, "archive.jsonl")
			switch archive {
			case "empty":
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			case "present":
				if err := os.WriteFile(path, []byte(`{"type":"meta","version":2}`+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// An empty archive is store_invalid (no meta line), and the
			// revision is still the digest of its bytes — which is the case
			// that separates an empty file from an absent one.
			checked, err := store.CheckedReadSnapshot()
			if err != nil || checked.StoreRevision == "" {
				t.Fatalf("checked read: %v %v", err, checked.Status)
			}
			revision, err := store.StoreRevision()
			if err != nil {
				t.Fatal(err)
			}
			if revision != checked.StoreRevision {
				t.Fatalf("StoreRevision = %q, checked read = %q", revision, checked.StoreRevision)
			}
			if peek := store.PeekHistory(); peek.StoreRevision != checked.StoreRevision {
				t.Fatalf("peek revision = %q, checked read = %q", peek.StoreRevision, checked.StoreRevision)
			}
			if preview := store.ArchivePreviewFor("2026-03-14"); preview.StoreRevision != checked.StoreRevision {
				t.Fatalf("preview revision = %q, checked read = %q", preview.StoreRevision, checked.StoreRevision)
			}
		})
	}
}

func TestPeekHistoryNamesBothDirectionsWithoutMoving(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	if peek := store.PeekHistory(); peek.Undo != nil || peek.Redo != nil {
		t.Fatalf("fresh store peeks %v / %v", peek.Undo, peek.Redo)
	}
	setPriority(t, store, flightID, "B")
	peek := store.PeekHistory()
	if peek.Undo == nil || *peek.Undo == "" || peek.Redo != nil {
		t.Fatalf("after one write: undo %v redo %v", peek.Undo, peek.Redo)
	}
	cursor := journalCursor(t, root)
	before := readStore(t, store)
	store.PeekHistory()
	if journalCursor(t, root) != cursor || readStore(t, store) != before {
		t.Fatal("a peek moved the journal or the files")
	}

	label := *peek.Undo
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryOK {
		t.Fatalf("undo = %q", outcome)
	}
	after := store.PeekHistory()
	if after.Undo != nil || after.Redo == nil || *after.Redo != label {
		t.Fatalf("after undo: undo %v redo %v, want redo %q", after.Undo, after.Redo, label)
	}
}

func TestGuardedHistoryStepRefusesAStaleRevisionAndWritesNothing(t *testing.T) {
	store, root := writerFixture(t, journalFixture)
	setPriority(t, store, flightID, "B")
	seen := store.PeekHistory().StoreRevision
	setPriority(t, store, flightID, "C")

	cursor := journalCursor(t, root)
	before := readStore(t, store)
	result := store.GuardedHistoryStep(-1, seen)
	if result.Outcome != HistoryStale {
		t.Fatalf("outcome = %q, want stale", result.Outcome)
	}
	current, _ := store.StoreRevision()
	if result.StoreRevision != current {
		t.Fatalf("stale refusal carries %q, current is %q", result.StoreRevision, current)
	}
	if journalCursor(t, root) != cursor || readStore(t, store) != before {
		t.Fatal("a stale refusal moved the journal or the files")
	}

	applied := store.GuardedHistoryStep(-1, current)
	if applied.Outcome != HistoryOK {
		t.Fatalf("guarded undo at the current revision = %q", applied.Outcome)
	}
	after, _ := store.StoreRevision()
	if applied.StoreRevision != after || after == current {
		t.Fatalf("applied revision = %q, store now %q (was %q)", applied.StoreRevision, after, current)
	}
}

// The stale check comes before the plan: a caller holding a stale revision is
// told it is stale even when the journal is empty, because "nothing to undo"
// is an answer about a store it has not seen.
func TestGuardedHistoryStepChecksStalenessBeforeEmptiness(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	if result := store.GuardedHistoryStep(-1, "s1.not-this-one"); result.Outcome != HistoryStale {
		t.Fatalf("outcome = %q, want stale", result.Outcome)
	}
	current, _ := store.StoreRevision()
	if result := store.GuardedHistoryStep(-1, current); result.Outcome != HistoryEmpty {
		t.Fatalf("outcome = %q, want empty", result.Outcome)
	}
}

func TestArchiveSweepReportsThePostWriteRevision(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	preview := store.ArchivePreviewFor("2026-03-14")
	result := store.ArchiveSweep("2026-03-14", &preview)
	if !result.OK() || result.Roots != 1 {
		t.Fatalf("sweep = %+v", result)
	}
	current, _ := store.StoreRevision()
	if result.StoreRevision != current || current == preview.StoreRevision {
		t.Fatalf("sweep revision %q, store %q, preview %q", result.StoreRevision, current, preview.StoreRevision)
	}
	empty := store.ArchiveSweep("2026-03-14", nil)
	if !empty.OK() || empty.Roots != 0 || empty.StoreRevision != current {
		t.Fatalf("empty sweep = %+v, want revision %q", empty, current)
	}
}
