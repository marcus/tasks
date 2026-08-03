// Package atomic is durable, all-or-nothing file replacement — the Go
// counterpart of lib/tasks/atomic.rb, and the only writer the store uses.
//
// A plain write can be seen half-written by a concurrent reader (the live TUI,
// another CLI) and leaves a truncated tasks.jsonl behind if the process dies
// mid-write. Instead the full contents go to a sibling temp file, are flushed
// to disk, and the temp is renamed over the target: rename is atomic on a POSIX
// filesystem, so a reader — or a crash — only ever sees the whole old file or
// the whole new one, never a torn mix.
//
// Because rename installs a new inode, three things keep the swap transparent,
// and every one of them is a behavior a port gets wrong by omission:
//
//   - the target's symlink is followed, so a rename does not replace the link
//     itself and orphan a Dropbox/dotfiles setup;
//   - its permission bits are carried onto the replacement, because a fresh
//     temp is born at the umask and a chmod-600 tasks.jsonl would otherwise
//     silently widen to 644;
//   - the parent directory is fsynced after the rename, so the swap is durable
//     across a crash rather than merely atomic.
//
// Hardlinks are not preserved: atomic-rename replacement is fundamentally
// incompatible with keeping a second hardlink name in sync.
package atomic

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Write replaces path's contents with content, atomically and durably.
//
// The temp name carries the process id and a goroutine-unique counter for the
// same reason Ruby's carries pid and thread object id: concurrent writers to
// DIFFERENT files must not collide on the temp name, while writers to the same
// file are already serialized by the store's lock.
func Write(path, content string) error {
	target := Resolve(path)
	dir := filepath.Dir(target)
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(target), os.Getpid(), nextTempToken()))

	if err := writeTemp(tmp, content); err != nil {
		remove(tmp)
		return err
	}
	copyMode(target, tmp)
	if err := os.Rename(tmp, target); err != nil {
		remove(tmp)
		return err
	}
	fsyncDir(dir)
	return nil
}

func writeTemp(tmp, content string) error {
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		return err
	}
	// Flush the bytes before the rename so the replacement is durable, not
	// merely atomic. Ruby does the same, in the same order.
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// Resolve is the concrete file the write should land on. A symlink is followed
// to the real file so we replace IT, not the link — even a dangling link (a
// target on a briefly-unmounted volume) resolves to its intended path rather
// than being overwritten into a plain file. A path with no link and no file yet
// (archive.jsonl before the first sweep) is used as given.
func Resolve(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return path
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		link, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if filepath.IsAbs(link) {
			return filepath.Clean(link)
		}
		return filepath.Join(filepath.Dir(path), link)
	}
	// A real file still resolves, which picks up symlinked PARENT directories.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// copyMode carries the existing file's permission bits onto the replacement.
//
// Best-effort, exactly like fsyncDir: a filesystem that rejects chmod (some
// CIFS/exFAT/FUSE mounts), or a target deleted out-of-band mid-write, must not
// turn a working write into a hard failure — the write still lands, just
// without carrying perms the filesystem was not honoring anyway.
//
// The raw st_mode is masked to the twelve permission bits rather than to
// os.FileMode's nine, so setuid, setgid and the sticky bit survive a
// replacement the same way Ruby's File.chmod(File.stat(target).mode) keeps
// them.
func copyMode(target, tmp string) {
	var stat syscall.Stat_t
	if err := syscall.Stat(target, &stat); err != nil {
		return
	}
	_ = syscall.Chmod(tmp, uint32(stat.Mode)&0o7777)
}

// fsyncDir flushes the directory entry the rename created so the replacement
// survives a crash. Best-effort: some platforms and filesystems refuse an fsync
// on a directory, and the rename's atomicity holds regardless.
func fsyncDir(dir string) {
	file, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = file.Sync()
	_ = file.Close()
}

func remove(path string) {
	if _, err := os.Lstat(path); err == nil {
		_ = os.Remove(path)
	}
}
