package store

import "testing"

// TestSnapshotIsNotYetImmutable pins a KNOWN DEFECT, deliberately.
//
// `Snapshot` documents itself as a coherent view a caller can hold while
// rendering, but `Items`, `ArchiveItems`, `LiveRecords` and `ArchiveRecords` are
// public slices, and each Item's tag slices share their backing array with the
// snapshot's own copy. A caller that only meant to READ one can therefore
// corrupt it — and every reader of a snapshot is a caller.
//
// The fix is unexported fields with copying accessors, which is what
// `port/store-snapshot-items` proposed and what Wave 0 recorded. It is a
// mechanical change across 28 non-test call sites in five packages. It has been
// deferred twice, both times for a good reason and a different one:
//
//   - Wave 2 judged that mixing an API-shape change into the write wave would
//     make that wave's differential evidence harder to trust;
//   - this packet judged that `taskquery` — 11 of the 28 sites — is the package
//     two TUI worktrees are reading from right now, and that `api` and
//     `application` were reserved to it by name. An API-shape change across
//     three packages, in the same commit as placement and the project
//     lifecycle, would collide with live work by construction and would carry no
//     differential evidence of its own, because none of it is visible from the
//     CLI.
//
// So this test exists instead: it states the defect executably, so the next
// owner has a specification rather than a paragraph, and so the fix cannot land
// half-done without a failing test saying so. WHEN IT FAILS, THE DEFECT IS
// FIXED — delete it, do not repair it.
func TestSnapshotIsNotYetImmutable(t *testing.T) {
	target, _ := writerFixture(t, patchFixture)
	snapshot, err := target.ReadSnapshot(false)
	if err != nil {
		t.Fatal(err)
	}

	var tagged Item
	for _, item := range snapshot.Items {
		if len(item.AllTags) > 0 {
			tagged = item
			break
		}
	}
	if tagged.ID == "" {
		t.Fatal("the fixture has no tagged task")
	}

	// A caller holding an Item mutates the snapshot's copy through the shared
	// backing array. Nothing about the value it was handed says it may not.
	original := tagged.AllTags[0]
	tagged.AllTags[0] = "corrupted-by-a-reader"
	for _, item := range snapshot.Items {
		if item.ID != tagged.ID {
			continue
		}
		if item.AllTags[0] == original {
			t.Fatal("the snapshot is now immutable — delete this test, the defect is fixed")
		}
	}

	// And the slice itself is assignable, so a caller can empty a snapshot
	// another goroutine is rendering from.
	snapshot.Items = nil
	if snapshot.Items != nil {
		t.Fatal("Items is no longer assignable — delete this test, the defect is fixed")
	}
}
