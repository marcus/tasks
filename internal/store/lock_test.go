package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	envHoldLock  = "TASKS_TEST_HOLD_LOCK"
	envLockPath  = "TASKS_TEST_LOCK_PATH"
	envLockReady = "TASKS_TEST_LOCK_READY"
)

func TestLockTimeoutReportsHolder(t *testing.T) {
	if holdLockIfRequested() {
		return
	}
	store := writeStore(t, metaLine+"\n", nil)
	previous := lockWaitTimeout
	lockWaitTimeout = 40 * time.Millisecond
	t.Cleanup(func() { lockWaitTimeout = previous })

	holder := startLockHolder(t, store.LockPath())
	_, err := store.ReadSnapshot(false)
	var timeout *LockTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("ReadSnapshot error = %v, want LockTimeoutError", err)
	}
	if timeout.PID != holder.Process.Pid {
		t.Fatalf("holder pid = %d, want %d", timeout.PID, holder.Process.Pid)
	}
	if timeout.Since.IsZero() {
		t.Fatal("timeout must name when the holder acquired the lock")
	}
	if !strings.Contains(err.Error(), "held by pid") {
		t.Fatalf("error = %q, want holder diagnostics", err)
	}

	result := store.CreateTask(CreateCommand{Title: "must not wait forever"}, "2026-03-14")
	if result.Status != MutationUnavailable {
		t.Fatalf("status = %q, want unavailable", result.Status)
	}
	if !IsLockTimeoutMessage(result.FirstError()) {
		t.Fatalf("mutation error = %q, want lock timeout", result.FirstError())
	}
	if !strings.Contains(result.FirstError(), "pid") {
		t.Fatalf("mutation error = %q, want pid", result.FirstError())
	}

	checked, err := store.CheckedReadSnapshot()
	if err != nil {
		t.Fatalf("checked read error = %v", err)
	}
	if checked.Status != StatusUnavailable {
		t.Fatalf("checked status = %q, want unavailable", checked.Status)
	}
	if len(checked.Errors) == 0 || !IsLockTimeoutMessage(checked.Errors[0].Message) {
		t.Fatalf("checked errors = %+v, want lock timeout", checked.Errors)
	}
}

func TestStaleLockSidecarIsNotALiveLock(t *testing.T) {
	store := writeStore(t, metaLine+"\n", nil)
	if err := os.WriteFile(store.LockPath(), []byte("pid:1\ntime:2020-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.ReadSnapshot(false)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stale sidecar blocked the read: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("stale sidecar was treated as a live lock")
	}
}

func TestLockIsReleasedWhenTheHolderExits(t *testing.T) {
	if holdLockIfRequested() {
		return
	}
	store := writeStore(t, metaLine+"\n", nil)
	holder := startLockHolder(t, store.LockPath())
	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	if _, err := holder.Process.Wait(); err != nil && !isWaitedExit(err) {
		t.Fatalf("wait holder: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.ReadSnapshot(false)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read after holder exit: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("lock survived the holder process")
	}
}

func TestSuccessfulLockAcquireClearsHolderOnRelease(t *testing.T) {
	store := writeStore(t, metaLine+"\n", nil)
	if _, err := store.ReadSnapshot(false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.LockPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("released sidecar still names a holder: %q", data)
	}
}

func TestArchiveLockTimeoutIsUnavailableNotEmpty(t *testing.T) {
	if holdLockIfRequested() {
		return
	}
	store := writeStore(t, metaLine+"\n"+
		`{"type":"task","id":"aaaa0001","state":"DONE","title":"closed","closed":"2026-03-01"}`+"\n", nil)
	previous := lockWaitTimeout
	lockWaitTimeout = 40 * time.Millisecond
	t.Cleanup(func() { lockWaitTimeout = previous })
	startLockHolder(t, store.LockPath())

	preview := store.ArchivePreviewFor("2026-03-14")
	if !IsLockTimeoutMessage(preview.Unavailable) {
		t.Fatalf("preview.Unavailable = %q", preview.Unavailable)
	}
	if preview.Roots != 0 {
		t.Fatalf("timed-out preview reported %d roots", preview.Roots)
	}

	result := store.ArchiveSweep("2026-03-14", nil)
	if result.Refusal != ArchiveUnavailable {
		t.Fatalf("sweep refusal = %q, want unavailable", result.Refusal)
	}
	if result.Failed {
		t.Fatal("a lock timeout is not a write that rolled back")
	}
	if len(result.Details) == 0 || !IsLockTimeoutMessage(result.Details[0]) {
		t.Fatalf("sweep details = %v", result.Details)
	}
}

func TestProjectRenameLockTimeoutIsNotNotFound(t *testing.T) {
	if holdLockIfRequested() {
		return
	}
	store := writeStore(t, metaLine+"\n"+
		`{"type":"section","id":"cccc0001","title":"Work"}`+"\n", nil)
	previous := lockWaitTimeout
	lockWaitTimeout = 40 * time.Millisecond
	t.Cleanup(func() { lockWaitTimeout = previous })
	startLockHolder(t, store.LockPath())

	_, found := store.RenameSection("cccc0001", "Renamed")
	if found {
		t.Fatal("rename succeeded while the lock was held")
	}
	if !IsLockTimeoutMessage(store.LastLockError()) {
		t.Fatalf("LastLockError = %q", store.LastLockError())
	}
}

func TestUnavailableMessageKeepsTimeoutDiagnostics(t *testing.T) {
	timeout := &LockTimeoutError{Timeout: 500 * time.Millisecond, PID: 99,
		Since: time.Date(2026, 8, 13, 9, 5, 54, 0, time.UTC)}
	got := UnavailableMessage(timeout)
	if got != "lock timeout after 500ms: held by pid 99 since 2026-08-13T09:05:54Z" {
		t.Fatalf("message = %q", got)
	}
	if UnavailableMessage(errors.New("open failed")) != "task store unavailable" {
		t.Fatal("non-timeout errors must stay generic")
	}
	if !IsLockTimeoutMessage(got) || IsLockTimeoutMessage("task store unavailable") {
		t.Fatal("IsLockTimeoutMessage mismatch")
	}
}

func holdLockIfRequested() bool {
	if os.Getenv(envHoldLock) != "1" {
		return false
	}
	path := os.Getenv(envLockPath)
	ready := os.Getenv(envLockReady)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		os.Exit(2)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		os.Exit(3)
	}
	if _, err := file.Seek(0, 0); err != nil {
		os.Exit(4)
	}
	if err := file.Truncate(0); err != nil {
		os.Exit(4)
	}
	if _, err := fmt.Fprintf(file, "pid:%d\ntime:%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		os.Exit(4)
	}
	if err := file.Sync(); err != nil {
		os.Exit(4)
	}
	if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
		os.Exit(5)
	}
	// The *os.File finalizer closes the fd, which drops flock. Touch the
	// descriptor so the holder stays live until the parent kills us.
	for {
		time.Sleep(time.Hour)
		_ = file.Fd()
	}
}

func startLockHolder(t *testing.T, lockPath string) *exec.Cmd {
	t.Helper()
	ready := lockPath + ".ready"
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(),
		envHoldLock+"=1",
		envLockPath+"="+lockPath,
		envLockReady+"="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(ready)
		if err == nil && string(data) == "ready" {
			return cmd
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("lock holder did not become ready")
	return cmd
}

func isWaitedExit(err error) bool {
	_, ok := err.(*exec.ExitError)
	return ok
}
