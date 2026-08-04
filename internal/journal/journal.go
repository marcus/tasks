// Package journal carries the two path facts every surface needs about the
// undo journal: the canonical identity of a store file, and the directory a
// store's history lives in. It is the read-only half of lib/tasks/journal.rb —
// there is deliberately no write path here.
package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
)

// DirFor is the journal directory for an org file: XDG_STATE_HOME (the
// standard home for persist-but-non-precious state), namespaced by a digest of
// the org's CANONICAL path so distinct task files never share a history — and,
// just as importantly, so two spellings of the same file (a symlink, a
// relative path) resolve to one history rather than silently diverging.
func DirFor(org string, env determinism.Env) string {
	base := config.XDGBase(env, "XDG_STATE_HOME", ".local", "state")
	digest := sha256.Sum256([]byte(Canonical(org)))
	key := hex.EncodeToString(digest[:])[:16]
	return filepath.Join(base, "tasks", "journal", key)
}

// Canonical is the absolute, symlink-resolved path — the stable identity of a
// task file across the different ways callers spell it.
//
// When the file does not exist yet (first-run capture bootstraps it) the
// containing directory — which does exist — is resolved instead, so the
// identity stays stable once the file appears. Otherwise a /tmp → /private/tmp
// style symlink, unresolved while the file is missing and resolved afterward,
// would shift the journal key between a capture and its undo.
func Canonical(org string) string {
	if resolved, err := filepath.EvalSymlinks(org); err == nil {
		return absolute(resolved)
	}
	dir := filepath.Dir(org)
	if _, err := os.Stat(dir); err == nil {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
	}
	return filepath.Join(absolute(dir), filepath.Base(org))
}

func absolute(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}
