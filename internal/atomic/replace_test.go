package atomic

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// The cases the durability tests do not reach: the first write to a path that
// does not exist yet, byte fidelity, the special mode bits, and a symlinked
// parent directory. Every one of them is a behavior a port gets wrong by
// omission rather than by getting it backwards.

// archive.jsonl does not exist until the first sweep, so "no file yet" is the
// ordinary case, not an error.
func TestFirstWriteCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	if err := Write(path, "first\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "first\n" {
		t.Fatalf("read = %q/%v", raw, err)
	}
	// Resolve leaves an absent path alone rather than inventing one.
	absent := filepath.Join(t.TempDir(), "nothing.jsonl")
	if got := Resolve(absent); got != absent {
		t.Fatalf("Resolve = %q, want %q", got, absent)
	}
}

// The store's canonical bytes are the store's bytes: nothing is appended,
// re-encoded, or normalized on the way through.
func TestContentIsWrittenVerbatim(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"empty":            "",
		"no trailing line": "{\"type\":\"meta\",\"version\":2}",
		"non ascii":        "Café — résumé naïve\n",
		"embedded nul":     "a\x00b\n",
		"crlf":             "a\r\nb\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "verbatim.jsonl")
			if err := Write(path, content); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != content {
				t.Fatalf("read %q, wrote %q", raw, content)
			}
		})
	}
}

// A replacement is a new inode, so the mode has to be carried across — all
// twelve bits, not the nine os.FileMode exposes, or a setgid or sticky store
// would quietly lose it.
func TestSpecialModeBitsSurviveTheReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.jsonl")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const wanted = 0o2600 // setgid + rw-------
	if err := syscall.Chmod(path, wanted); err != nil {
		t.Skipf("this filesystem refuses setgid on a regular file: %v", err)
	}
	var before syscall.Stat_t
	if err := syscall.Stat(path, &before); err != nil {
		t.Fatal(err)
	}
	if before.Mode&0o7777 != wanted {
		t.Skipf("this filesystem dropped the setgid bit before the write: %o", before.Mode&0o7777)
	}

	if err := Write(path, "second\n"); err != nil {
		t.Fatal(err)
	}
	var after syscall.Stat_t
	if err := syscall.Stat(path, &after); err != nil {
		t.Fatal(err)
	}
	if after.Mode&0o7777 != wanted {
		t.Fatalf("mode = %o, want %o — the special bits were dropped by the replacement", after.Mode&0o7777, wanted)
	}
}

// A missing target is not a failure: copyMode is best-effort, because a
// filesystem that refuses chmod must not turn a working write into an error.
func TestModeCarryIsBestEffort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	if err := Write(path, "first\n"); err != nil {
		t.Fatalf("Write with no existing target to copy from: %v", err)
	}
}

// A symlinked PARENT directory resolves too, so two paths naming one file do
// not race each other into two different inodes.
func TestSymlinkedParentDirectoryResolves(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "tasks.jsonl")
	if err := os.WriteFile(target, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	through := filepath.Join(link, "tasks.jsonl")
	// The comparison resolves the temp root too: on macOS /var is itself a link
	// to /private/var, which is exactly the kind of parent this rule exists for.
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := Resolve(through); got != want {
		t.Fatalf("Resolve = %q, want the real path %q", got, want)
	}
	if err := Write(through, "second\n"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "second\n" {
		t.Fatalf("target = %q/%v", raw, err)
	}
	// The directory symlink itself survives the write.
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the directory link became %v (%v)", info.Mode(), err)
	}
}

// Serialized writers to ONE file — which is what the store's lock produces —
// each land whole, and the last one wins. No temp file survives.
func TestSerializedWritersToOneFileEachLandWhole(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks.jsonl")
	var lock sync.Mutex
	var wait sync.WaitGroup
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			lock.Lock()
			defer lock.Unlock()
			if err := Write(path, strings.Repeat("x", index+1)+"\n"); err != nil {
				t.Errorf("writer %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSuffix(string(raw), "\n")
	if strings.Trim(text, "x") != "" || len(text) < 1 || len(text) > 16 {
		t.Fatalf("file = %q, want one writer's complete content", raw)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the store", len(entries))
	}
}
