package runtimepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutablePreservesAnAbsoluteInvocationSymlink(t *testing.T) {
	previous := os.Args[0]
	invoked := filepath.Join(t.TempDir(), "stable", "tasks")
	os.Args[0] = invoked
	defer func() { os.Args[0] = previous }()

	if got := Executable(); got != invoked {
		t.Fatalf("Executable = %q, want stable invocation path %q", got, invoked)
	}
}

func TestTasksCLIIsTheTasksBinaryBesideThisExecutable(t *testing.T) {
	executable := Executable()
	want := filepath.Join(filepath.Dir(executable), "tasks")
	if filepath.Base(executable) == "tasks" {
		want = executable
	}
	if got := TasksCLI(); got != want {
		t.Fatalf("TasksCLI = %q, want %q", got, want)
	}
}
