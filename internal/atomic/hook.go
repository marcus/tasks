package atomic

import "sync"

// The fault-injection seam.
//
// lib/tasks/atomic.rb needs none: `test/test_journal.rb` stubs
// `Tasks::Atomic.write` wholesale and drives the store's rollback paths through
// a writer that fails for one path, or fails on the second call, or installs
// the file and THEN reports failure. Those paths — the undo that cannot commit
// its cursor, the rollback that cannot restore the old bytes, the retry — are
// the whole safety story of undo, and there is no other way to reach them: a
// real EACCES cannot be arranged for one path in a temp directory the test also
// has to write.
//
// Go has no method stubbing, so the seam is explicit. Write consults the hook;
// WriteDirect never does, so a hook can call through to the real writer the way
// Ruby's stub calls `original.call`. Nothing in the product ever sets it.

var (
	hookMu sync.RWMutex
	hook   func(path, content string) error
)

// SetWriteHook installs a replacement for Write and returns a function that
// removes it. It is a TEST SEAM: production code must never call it.
//
// The returned restore function is what a test defers; nesting is not
// supported, and a second SetWriteHook while one is installed replaces it
// rather than stacking, which keeps a leaked hook loud instead of subtle.
func SetWriteHook(replacement func(path, content string) error) (restore func()) {
	hookMu.Lock()
	hook = replacement
	hookMu.Unlock()
	return func() {
		hookMu.Lock()
		hook = nil
		hookMu.Unlock()
	}
}

func writeHook() func(path, content string) error {
	hookMu.RLock()
	defer hookMu.RUnlock()
	return hook
}
