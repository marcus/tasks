package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	lockBackoffMin = 5 * time.Millisecond
	lockBackoffMax = 50 * time.Millisecond
)

// lockWaitTimeout is how long a waiter sits on the sidecar flock. Reads take
// the same exclusive lock as writes, so the wait has to cover a short queue
// of healthy operations.
var lockWaitTimeout = 5 * time.Second

// SetLockWaitTimeoutForTest changes the flock wait for a test in another
// package. The caller must restore the previous value.
func SetLockWaitTimeoutForTest(timeout time.Duration) (restore func()) {
	previous := lockWaitTimeout
	lockWaitTimeout = timeout
	return func() { lockWaitTimeout = previous }
}

// LockTimeoutError is returned when the advisory sidecar lock cannot be
// acquired before lockWaitTimeout. The sidecar file is not itself a lock: a
// leftover file after a crash is inert because the OS drops flock.
type LockTimeoutError struct {
	Timeout time.Duration
	PID     int
	Since   time.Time
	Raw     string
}

func (e *LockTimeoutError) Error() string {
	if e == nil {
		return "lock timeout"
	}
	if e.PID > 0 && !e.Since.IsZero() {
		return fmt.Sprintf("lock timeout after %s: held by pid %d since %s",
			e.Timeout, e.PID, e.Since.UTC().Format(time.RFC3339))
	}
	if e.PID > 0 {
		return fmt.Sprintf("lock timeout after %s: held by pid %d", e.Timeout, e.PID)
	}
	return fmt.Sprintf("lock timeout after %s: holder unknown", e.Timeout)
}

// UnavailableMessage is the sentence a surface prints when a store operation
// cannot proceed. A lock timeout keeps its holder diagnostics; every other
// failure stays the historical "task store unavailable" wording so adapters
// do not start interpolating absolute paths.
func UnavailableMessage(err error) string {
	var timeout *LockTimeoutError
	if errors.As(err, &timeout) {
		return timeout.Error()
	}
	return "task store unavailable"
}

// IsLockTimeoutMessage reports whether a stored refusal sentence is a lock
// timeout. Surfaces use it to promote the diagnostic without changing every
// other unavailable path.
func IsLockTimeoutMessage(message string) bool {
	return strings.HasPrefix(message, "lock timeout after ")
}

func mutationUnavailable(err error) MutationResult {
	return MutationResult{Status: MutationUnavailable, Errors: []string{UnavailableMessage(err)}}
}

// withLock serializes mutations across processes on an exclusive sidecar flock.
// Acquire times out so a stuck holder cannot stall every other command;
// leftover sidecar bytes are diagnostics, not the lock.
func (s *Store) withLock(body func() error) error {
	return s.withFlock(syscall.LOCK_EX, body)
}

// withSharedLock is the snapshot/read counterpart: many readers may hold it at
// once, and a writer waits. Atomic replace already keeps a reader from seeing
// a torn file; the shared lock only keeps a write from starting while a
// snapshot is mid-read.
func (s *Store) withSharedLock(body func() error) error {
	return s.withFlock(syscall.LOCK_SH, body)
}

func (s *Store) withFlock(how int, body func() error) error {
	file, err := os.OpenFile(s.LockPath(), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		s.recordLockError(err)
		return err
	}
	defer file.Close()
	if err := acquireLock(file, how); err != nil {
		s.recordLockError(err)
		return err
	}
	s.clearLockError()
	defer releaseLock(file)
	writeLockHolder(file)
	return body()
}

// LastLockError is the sentence from the most recent flock failure on this
// Store, or "" when the last acquire succeeded. Boolean store APIs have no
// other place to put a timeout.
func (s *Store) LastLockError() string {
	s.rollbackMu.Lock()
	defer s.rollbackMu.Unlock()
	return s.lastLockError
}

func (s *Store) recordLockError(err error) {
	s.rollbackMu.Lock()
	defer s.rollbackMu.Unlock()
	s.lastLockError = UnavailableMessage(err)
}

func (s *Store) clearLockError() {
	s.rollbackMu.Lock()
	defer s.rollbackMu.Unlock()
	s.lastLockError = ""
}

func acquireLock(file *os.File, how int) error {
	deadline := time.Now().Add(lockWaitTimeout)
	backoff := lockBackoffMin
	fd := int(file.Fd())
	for {
		err := tryLock(fd, how)
		if err == nil {
			return nil
		}
		if !isLockBusy(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return readLockTimeout(file)
		}
		if backoff > remaining {
			backoff = remaining
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > lockBackoffMax {
			backoff = lockBackoffMax
		}
	}
}

func tryLock(fd int, how int) error {
	for {
		err := syscall.Flock(fd, how|syscall.LOCK_NB)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}

func releaseLock(file *os.File) {
	clearLockHolder(file)
	fd := int(file.Fd())
	for {
		err := syscall.Flock(fd, syscall.LOCK_UN)
		if err == syscall.EINTR {
			continue
		}
		return
	}
}

func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func writeLockHolder(file *os.File) {
	if err := file.Truncate(0); err != nil {
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		return
	}
	_, _ = fmt.Fprintf(file, "pid:%d\ntime:%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	_ = file.Sync()
}

func clearLockHolder(file *os.File) {
	_ = file.Truncate(0)
}

func readLockTimeout(file *os.File) error {
	timeout := &LockTimeoutError{Timeout: lockWaitTimeout}
	if _, err := file.Seek(0, 0); err != nil {
		return timeout
	}
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		return timeout
	}
	timeout.Raw = string(data)
	timeout.PID, timeout.Since = parseLockHolder(timeout.Raw)
	return timeout
}

func parseLockHolder(text string) (int, time.Time) {
	var pid int
	var since time.Time
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			parsed, err := strconv.Atoi(value)
			if err == nil && parsed > 0 {
				pid = parsed
			}
		case "time":
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				since = parsed
			}
		}
	}
	return pid, since
}
