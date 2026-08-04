package runtimepath

import (
	"os"
	"os/exec"
	"path/filepath"
)

func Executable() string {
	invoked := os.Args[0]
	if filepath.IsAbs(invoked) {
		return filepath.Clean(invoked)
	}
	if path, err := exec.LookPath(invoked); err == nil {
		if absolute, absErr := filepath.Abs(path); absErr == nil {
			return filepath.Clean(absolute)
		}
	}
	if absolute, err := filepath.Abs(invoked); err == nil {
		return filepath.Clean(absolute)
	}
	if path, err := os.Executable(); err == nil {
		return filepath.Clean(path)
	}
	return invoked
}

// TasksCLI returns the tasks command installed beside the current executable.
// This matters for tasks-tui: agents must receive the scriptable CLI path, not
// the interactive binary that happened to assemble their prompt.
func TasksCLI() string {
	executable := Executable()
	if filepath.Base(executable) == "tasks" {
		return executable
	}
	return filepath.Join(filepath.Dir(executable), "tasks")
}
