package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/store"
)

// A live holder must fail a CLI command in tens of milliseconds with the
// holder named on stderr, not sit forever with empty stdout — that is the
// hang issue 7 recorded on `tasks done` / `tasks note`.
func TestCLILockTimeoutNamesTheHolder(t *testing.T) {
	if holdCLILockIfRequested() {
		return
	}
	dir := seedStore(t, `{"type":"meta","version":2}`+"\n"+
		`{"type":"task","id":"aaaa0001","state":"TODO","title":"Readable"}`+"\n")
	defer store.SetLockWaitTimeoutForTest(40 * time.Millisecond)()
	lockPath := filepath.Join(dir, ".tasks.jsonl.lock")
	holder := startCLILockHolder(t, lockPath)

	result := runCLI(t, dir, "done", "Readable")
	if result.status != 1 {
		t.Fatalf("exit = %d, want 1; stdout %q stderr %q", result.status, result.stdout, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("stdout = %q, want empty on a lock timeout", result.stdout)
	}
	if !strings.Contains(result.stderr, "lock timeout after") {
		t.Fatalf("stderr = %q, want lock timeout", result.stderr)
	}
	if !strings.Contains(result.stderr, fmt.Sprintf("pid %d", holder.Process.Pid)) {
		t.Fatalf("stderr = %q, want pid %d", result.stderr, holder.Process.Pid)
	}

	listed := runCLI(t, dir, "list")
	if listed.status != 1 || !strings.Contains(listed.stderr, "lock timeout after") {
		t.Fatalf("list: exit %d stderr %q", listed.status, listed.stderr)
	}
}

func holdCLILockIfRequested() bool {
	if os.Getenv("TASKS_TEST_HOLD_LOCK") != "1" {
		return false
	}
	path := os.Getenv("TASKS_TEST_LOCK_PATH")
	ready := os.Getenv("TASKS_TEST_LOCK_READY")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		os.Exit(2)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		os.Exit(3)
	}
	_, _ = fmt.Fprintf(file, "pid:%d\ntime:%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	_ = file.Sync()
	if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
		os.Exit(4)
	}
	for {
		time.Sleep(time.Hour)
		_ = file.Fd()
	}
}

func startCLILockHolder(t *testing.T, lockPath string) *exec.Cmd {
	t.Helper()
	ready := lockPath + ".ready"
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(),
		"TASKS_TEST_HOLD_LOCK=1",
		"TASKS_TEST_LOCK_PATH="+lockPath,
		"TASKS_TEST_LOCK_READY="+ready,
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
