package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const metaLine = `{"type":"meta","version":2}`

func writeStore(t *testing.T, live string, archive *string) *Store {
	t.Helper()
	dir := t.TempDir()
	org := filepath.Join(dir, "tasks.jsonl")
	if err := os.WriteFile(org, []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(dir, "archive.jsonl")
	if archive != nil {
		if err := os.WriteFile(archivePath, []byte(*archive), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New(org, archivePath)
}

func checked(t *testing.T, store *Store) CheckedRead {
	t.Helper()
	result, err := store.CheckedReadSnapshot()
	if err != nil {
		t.Fatalf("checked read: %v", err)
	}
	return result
}

func TestStoreRevisionDistinguishesAbsentFromEmpty(t *testing.T) {
	absent := StoreRevisionForContents([]byte("x"), nil)
	empty := StoreRevisionForContents([]byte("x"), []byte{})
	if absent == empty {
		t.Fatal("an absent archive must not digest the same as an empty one")
	}
	// The length prefix is what makes the pair unambiguous: no re-split of the
	// concatenation can produce another pair with the same digest.
	if StoreRevisionForContents([]byte("ab"), []byte("c")) == StoreRevisionForContents([]byte("a"), []byte("bc")) {
		t.Fatal("contents must be length-prefixed")
	}
	if got := StoreRevisionForContents(nil, nil); len(got) != len("s1.")+64 {
		t.Fatalf("unexpected token shape %q", got)
	}
}

func TestCheckedReadStatuses(t *testing.T) {
	tests := []struct {
		name   string
		live   string
		status Status
		// A store revision is produced even for invalid bytes: the token is a
		// digest of bytes, not an assertion that they parse.
		revisioned bool
	}{
		{"valid", metaLine + "\n" + `{"type":"task","id":"aaaa0001","state":"TODO","title":"t"}` + "\n", StatusOK, true},
		{"empty store", metaLine + "\n", StatusOK, true},
		{"unparseable line", metaLine + "\n{\n", StatusStoreInvalid, true},
		{"invalid utf8", metaLine + "\n\xff\xfe\n", StatusStoreInvalid, true},
		{"future schema", `{"type":"meta","version":3}` + "\n", StatusUnsupportedSchema, true},
		{"null version", `{"type":"meta","version":null}` + "\n", StatusStoreInvalid, true},
		{"duplicate ids", metaLine + "\n" +
			`{"type":"task","id":"aaaa0001","state":"TODO","title":"a"}` + "\n" +
			`{"type":"task","id":"aaaa0001","state":"TODO","title":"b"}` + "\n", StatusStoreInvalid, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := checked(t, writeStore(t, test.live, nil))
			if result.Status != test.status {
				t.Fatalf("status = %q, want %q (errors %v)", result.Status, test.status, result.Errors)
			}
			if test.revisioned && result.StoreRevision == "" {
				t.Fatal("want a store revision even for bytes that do not parse")
			}
			if result.Status == StatusOK && result.Snapshot == nil {
				t.Fatal("an ok read must carry a snapshot")
			}
			if result.Status != StatusOK && result.Snapshot != nil {
				t.Fatal("only an ok read may carry a snapshot")
			}
		})
	}
}

func TestCheckedReadMissingLiveFileIsInvalidNotUnavailable(t *testing.T) {
	dir := t.TempDir()
	result := checked(t, New(filepath.Join(dir, "tasks.jsonl"), filepath.Join(dir, "archive.jsonl")))
	if result.Status != StatusStoreInvalid {
		t.Fatalf("status = %q, want store_invalid", result.Status)
	}
}

func TestCheckedReadUnavailableWhenTheStoreCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	org := filepath.Join(dir, "tasks.jsonl")
	if err := os.Mkdir(org, 0o755); err != nil {
		t.Fatal(err)
	}
	result := checked(t, New(org, filepath.Join(dir, "archive.jsonl")))
	if result.Status != StatusUnavailable {
		t.Fatalf("status = %q, want unavailable", result.Status)
	}
	if result.StoreRevision != "" {
		t.Fatal("an unavailable store has no content to digest")
	}
}

// A read takes the store lock, so the sidecar is a real observable effect of
// reading. The conformance corpus compares its presence and its mode, so this
// is behaviour, not an implementation detail.
func TestReadingCreatesTheLockSidecar(t *testing.T) {
	store := writeStore(t, metaLine+"\n", nil)
	sidecar := store.LockPath()
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("the sidecar must not exist before the read")
	}
	checked(t, store)
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatalf("the read must create %s: %v", sidecar, err)
	}
	if filepath.Base(sidecar) != ".tasks.jsonl.lock" {
		t.Fatalf("sidecar named %q", filepath.Base(sidecar))
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("sidecar mode = %04o, want 0644 under a 0022 umask", got)
	}
}

func TestSnapshotItemsAndArchiveHalf(t *testing.T) {
	archive := metaLine + "\n" + `{"type":"task","id":"bbbb0001","state":"DONE","title":"old","closed":"2026-01-02"}` + "\n"
	store := writeStore(t, metaLine+"\n"+
		`{"type":"section","id":"5ec70001","title":"Work"}`+"\n"+
		`{"type":"task","id":"aaaa0001","parent":"5ec70001","state":"TODO","title":"live","tags":["@home","urgent","defer"],"scheduled":"2026-03-14"}`+"\n",
		&archive)
	result := checked(t, store)
	if result.Status != StatusOK {
		t.Fatalf("status = %q (%v)", result.Status, result.Errors)
	}
	snapshot := result.Snapshot
	items := snapshot.Items()
	archiveItems := snapshot.ArchiveItems()
	if len(items) != 1 || len(archiveItems) != 1 {
		t.Fatalf("items = %d live / %d archive", len(items), len(archiveItems))
	}
	item := items[0]
	if item.ID != "aaaa0001" || item.State != "TODO" || item.Title != "live" || item.Line != 3 {
		t.Fatalf("item = %+v", item)
	}
	if item.Scheduled != "2026-03-14" || item.Parent != "5ec70001" || item.Source != SourceLive {
		t.Fatalf("item = %+v", item)
	}
	if len(item.Contexts) != 1 || item.Contexts[0] != "@home" {
		t.Fatalf("contexts = %v", item.Contexts)
	}
	if len(snapshot.ChildrenOf("5ec70001")) != 1 || len(snapshot.Roots()) != 0 {
		t.Fatal("parent pointers must place the task under its section")
	}
	if archiveItems[0].Source != SourceArchive {
		t.Fatal("archive items carry the archive source")
	}

	resources := snapshot.Resources()
	if len(resources) != 2 {
		t.Fatalf("resources = %+v", resources)
	}
	// Sorted by (id, kind), and the archived task carries its own kind.
	if resources[0].ID != "aaaa0001" || resources[0].Kind != "task" {
		t.Fatalf("resources[0] = %+v", resources[0])
	}
	if resources[1].Kind != "archived_task" {
		t.Fatalf("resources[1] = %+v", resources[1])
	}
	for _, resource := range resources {
		if len(resource.Revision) != len("v1.")+3*64+2 {
			t.Fatalf("revision %q is not v1.<own>.<location>.<lifecycle>", resource.Revision)
		}
	}
}

// The three revision components are separate so a title-only edit can ignore a
// sibling-list change while a move or a cascade still invalidates. These
// assertions are what keep them separate.
func TestRevisionComponentsMoveIndependently(t *testing.T) {
	base := metaLine + "\n" +
		`{"type":"task","id":"aaaa0001","state":"TODO","title":"one"}` + "\n" +
		`{"type":"task","id":"aaaa0002","state":"TODO","title":"two"}` + "\n"
	revision := func(text, id string) string {
		result := checked(t, writeStore(t, text, nil))
		if result.Status != StatusOK {
			t.Fatalf("status = %q (%v)", result.Status, result.Errors)
		}
		for _, resource := range result.Snapshot.Resources() {
			if resource.ID == id {
				return resource.Revision
			}
		}
		t.Fatalf("no revision for %s", id)
		return ""
	}
	original := revision(base, "aaaa0001")
	if original != revision(base, "aaaa0001") {
		t.Fatal("a revision must be a pure function of the bytes")
	}
	retitled := revision(metaLine+"\n"+
		`{"type":"task","id":"aaaa0001","state":"TODO","title":"ONE"}`+"\n"+
		`{"type":"task","id":"aaaa0002","state":"TODO","title":"two"}`+"\n", "aaaa0001")
	if retitled == original {
		t.Fatal("a title edit must change the task's own component")
	}
	// A tag reordering is not a semantic change to the OTHER task, but adding a
	// sibling is a location change for both.
	withSibling := revision(base+`{"type":"task","id":"aaaa0003","state":"TODO","title":"three"}`+"\n", "aaaa0001")
	if withSibling == original {
		t.Fatal("a new sibling must change the location component")
	}
}

// Nested key order and the two halves of the token.
//
// The OWN component normalizes a nested object — `revision_value` sorts its
// keys — so a writer's member order cannot move it. The LIFECYCLE component
// does NOT: it digests the stored object as parsed, in source order.
//
// That asymmetry is the oracle's, reproduced deliberately rather than tidied.
// It is arguably a defect in the oracle (see this slice's report): two stores
// that mean the same thing get different lifecycle fingerprints, so a
// re-serializing writer invalidates every If-Match in the subtree. Ruby is the
// oracle, so Go matches it byte for byte and the question goes to a human.
func TestRevisionNormalizesOwnButNotLifecycleAcrossNestedKeyOrder(t *testing.T) {
	revision := func(text string) string {
		result := checked(t, writeStore(t, text, nil))
		if result.Status != StatusOK {
			t.Fatalf("status = %q (%v)", result.Status, result.Errors)
		}
		return result.Snapshot.Resources()[0].Revision
	}
	line := func(scheduledTime string) string {
		return metaLine + "\n" +
			`{"type":"task","id":"aaaa0001","state":"TODO","title":"t","scheduled":"2026-03-14","scheduled_time":` +
			scheduledTime + "}\n"
	}
	left := revision(line(`{"local":"09:00","timezone":"UTC"}`))
	right := revision(line(`{"timezone":"UTC","local":"09:00"}`))
	if left == right {
		t.Fatal("the oracle's lifecycle component moves with nested key order; it must here too")
	}
	component := func(revision string, index int) string {
		parts := strings.Split(revision, ".")
		if len(parts) != 4 {
			t.Fatalf("revision %q is not v1.<own>.<location>.<lifecycle>", revision)
		}
		return parts[index]
	}
	if component(left, 1) != component(right, 1) {
		t.Fatal("the own component must be key-order independent")
	}
	if component(left, 2) != component(right, 2) {
		t.Fatal("the location component must be key-order independent")
	}
	if component(left, 3) == component(right, 3) {
		t.Fatal("the lifecycle component is where the oracle's key-order sensitivity lives")
	}
}
