package atomic

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The replacement is all-or-nothing: a reader either sees the whole old file or
// the whole new one. This drives a reader alongside a writer and asserts every
// observation is a COMPLETE version — a torn read would show a prefix of one.
func TestReadersNeverSeeATornFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.jsonl")
	oldText := strings.Repeat("a", 200000) + "\n"
	newText := strings.Repeat("b", 300000) + "\n"
	if err := os.WriteFile(path, []byte(oldText), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if text := string(raw); text != oldText && text != newText {
				t.Errorf("torn read: %d bytes, neither version", len(text))
				return
			}
		}
	}()

	for round := 0; round < 20; round++ {
		text := newText
		if round%2 == 0 {
			text = oldText
		}
		if err := Write(path, text); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wait.Wait()
}

// A fresh temp file is born at the umask, so a restricted store would silently
// widen without the mode carry.
func TestPermissionBitsSurviveTheReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, "second\n"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// A rename over a symlink would replace the LINK, orphaning a dotfiles or
// Dropbox setup. The write has to land on the target.
func TestSymlinkIsFollowedNotReplaced(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.jsonl")
	link := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(target, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, "second\n"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink became a regular file")
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "second\n" {
		t.Errorf("target = %q, want the new contents", raw)
	}
}

// A DANGLING link resolves to its intended path rather than being overwritten
// into a plain file — the target may be on a briefly-unmounted volume.
func TestDanglingSymlinkResolvesToItsIntendedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "absent.jsonl")
	link := filepath.Join(root, "tasks.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(link); got != target {
		t.Errorf("Resolve = %q, want %q", got, target)
	}
}

// A failed write leaves no temp file behind: a leftover
// .tasks.jsonl.<pid>.<n>.tmp is a real finding — a crashed write — and the
// conformance corpus compares the file set, so it must not be noise we create.
func TestFailedWriteLeavesNoTemp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if err := Write(path, "second\n"); err == nil {
		t.Skip("this filesystem permits writes into a 0555 directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("leftover temp file %q", entry.Name())
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "first\n" {
		t.Errorf("the failed write disturbed the file: %q", raw)
	}
}

// Concurrent writers to DIFFERENT files must not collide on a temp name, which
// is the whole reason the name carries a per-writer token.
func TestConcurrentWritersToDifferentFiles(t *testing.T) {
	root := t.TempDir()
	var wait sync.WaitGroup
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			path := filepath.Join(root, "file.jsonl")
			if index%2 == 0 {
				path = filepath.Join(root, "other.jsonl")
			}
			if err := Write(path, strings.Repeat("x", index+1)); err != nil {
				t.Errorf("writer %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("leftover temp file %q", entry.Name())
		}
	}
}
