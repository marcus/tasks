package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tasks-go/internal/atomic"
)

// Rollback is the Go counterpart of the `rollback:` lambda `Journal#plan`
// returns in Ruby. It exists for exactly one situation — a commit that reported
// failure — and its two behaviors are both load-bearing: it must NOT rewrite an
// index that never moved, and it MUST restore one that did.

func rollbackFixture(t *testing.T) (*Journal, string, string) {
	t.Helper()
	root := t.TempDir()
	org := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(org, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "journal")
	history := Open(dir, org).Writable(Limit, "scope")

	first, second := "first\n", "second\n"
	if !history.Record("one", Snapshot{Org: &first}, Snapshot{Org: &second}, "", false) {
		t.Fatal("recording failed")
	}
	return history, dir, root
}

func cursorOf(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Cursor int `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document.Cursor
}

// An atomic replace that fails ordinarily leaves the old index untouched. There
// is nothing to restore, and rewriting anyway would burn a write that would fail
// for the same reason the commit just did.
func TestRollbackIsANoOpWhenTheIndexNeverMoved(t *testing.T) {
	history, dir, _ := rollbackFixture(t)
	step, ok := history.Plan(-1)
	if !ok {
		t.Fatal("no step to plan")
	}
	before, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}

	writes := 0
	restore := atomic.SetWriteHook(func(path, content string) error {
		writes++
		return atomic.WriteDirect(path, content)
	})
	ok = history.Rollback(step)
	restore()

	if !ok {
		t.Error("rollback of an unmoved index must report success")
	}
	if writes != 0 {
		t.Errorf("wrote %d times, want none — nothing had moved", writes)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if string(after) != string(before) {
		t.Error("the index bytes must be untouched")
	}
}

// A wrapper or filesystem that installs the new index and THEN reports failure
// is the case the rollback exists for.
func TestRollbackRestoresTheCursorWhenTheCommitDidLand(t *testing.T) {
	history, dir, _ := rollbackFixture(t)
	step, ok := history.Plan(-1)
	if !ok {
		t.Fatal("no step to plan")
	}
	original := cursorOf(t, dir)
	if original != 1 {
		t.Fatalf("cursor = %d, want 1", original)
	}

	if !history.Commit(step.To) {
		t.Fatal("commit failed")
	}
	if got := cursorOf(t, dir); got != 0 {
		t.Fatalf("cursor after commit = %d, want 0", got)
	}

	if !history.Rollback(step) {
		t.Fatal("rollback reported failure")
	}
	if got := cursorOf(t, dir); got != original {
		t.Errorf("cursor = %d, want the captured %d", got, original)
	}
	// The restored index still plans the same step.
	if again, ok := history.Plan(-1); !ok || again.Label != step.Label {
		t.Error("the restored history must still offer the step")
	}
}

// A rollback whose own write fails reports failure rather than claiming the
// cursor is back. The store retries on that answer.
func TestRollbackReportsFailureWhenItCannotWrite(t *testing.T) {
	history, dir, _ := rollbackFixture(t)
	step, _ := history.Plan(-1)
	if !history.Commit(step.To) {
		t.Fatal("commit failed")
	}

	restore := atomic.SetWriteHook(func(path, content string) error { return os.ErrPermission })
	ok := history.Rollback(step)
	restore()

	if ok {
		t.Error("a rollback that could not write must not report success")
	}
	if got := cursorOf(t, dir); got != 0 {
		t.Errorf("cursor = %d — the failed rollback left the committed value", got)
	}
	// And the retry succeeds once writes work again, which is what the store's
	// two attempts rely on.
	if !history.Rollback(step) {
		t.Fatal("the retry should succeed")
	}
	if got := cursorOf(t, dir); got != 1 {
		t.Errorf("cursor = %d, want 1", got)
	}
}

// Undo and redo are explicit history boundaries: Commit strips all segment
// metadata so even a redo back to the exact former tip cannot resume coalescing
// with the editor session that preceded the move.
func TestCommitStripsCoalescingMetadata(t *testing.T) {
	history, dir, root := rollbackFixture(t)
	second, third := "second\n", "third\n"
	if !history.Record("two", Snapshot{Org: &second}, Snapshot{Org: &third}, "session", false) {
		t.Fatal("recording failed")
	}
	if err := os.WriteFile(filepath.Join(root, "tasks.jsonl"), []byte(third), 0o644); err != nil {
		t.Fatal(err)
	}
	step, ok := history.Plan(-1)
	if !ok {
		t.Fatal("no step")
	}
	if !history.Commit(step.To) {
		t.Fatal("commit failed")
	}

	raw, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if containsBytes(raw, "coalesce_key") || containsBytes(raw, "coalesce_scope") {
		t.Errorf("the committed index still carries segment metadata:\n%s", raw)
	}
}

func containsBytes(haystack []byte, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for index := 0; index+len(needle) <= len(haystack); index++ {
				if string(haystack[index:index+len(needle)]) == needle {
					return true
				}
			}
			return false
		}()
}
