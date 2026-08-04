package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/buildinfo"
	"github.com/marcus/tasks/internal/determinism"
)

func TestVersionCommandsRunWithoutTaskConfiguration(t *testing.T) {
	previousEnv := env
	previousVersion, previousCommit := buildinfo.Version, buildinfo.Commit
	env = determinism.Env{"XDG_CONFIG_HOME": t.TempDir(), "TZ": "UTC"}
	buildinfo.Version, buildinfo.Commit = "v1.2.3", "abc123"
	defer func() {
		env = previousEnv
		buildinfo.Version, buildinfo.Commit = previousVersion, previousCommit
	}()

	for _, argv := range [][]string{{"--version"}, {"version"}} {
		stdout, stderr := captureOutput(t, func() int { return run(argv) })
		if stdout.status != 0 || stderr.text != "" || stdout.text != "tasks v1.2.3 (abc123)\n" {
			t.Fatalf("run(%v) = status %d, stdout %q, stderr %q", argv, stdout.status, stdout.text, stderr.text)
		}
	}

	stdout, stderr := captureOutput(t, func() int { return run([]string{"version", "--json"}) })
	if stdout.status != 0 || stderr.text != "" {
		t.Fatalf("version --json = status %d, stderr %q", stdout.status, stderr.text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.text), &payload); err != nil {
		t.Fatalf("version JSON: %v: %q", err, stdout.text)
	}
	if payload["name"] != "tasks" || payload["version"] != "v1.2.3" || payload["commit"] != "abc123" {
		t.Fatalf("version JSON = %#v", payload)
	}
}

func TestUnconfiguredStoreCommandRefusesWithoutCreatingData(t *testing.T) {
	root := t.TempDir()
	previousEnv := env
	env = determinism.Env{
		"HOME":            root,
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_STATE_HOME":  filepath.Join(root, "state"),
		"TZ":              "UTC",
	}
	defer func() { env = previousEnv }()

	stdout, stderr := captureOutput(t, func() int { return run([]string{"agenda"}) })
	if stdout.status != 1 || stdout.text != "" {
		t.Fatalf("agenda = status %d, stdout %q", stdout.status, stdout.text)
	}
	if !strings.Contains(stderr.text, "not configured; refusing to choose a task-data directory") {
		t.Fatalf("stderr = %q", stderr.text)
	}
	for _, name := range []string{"tasks.jsonl", "archive.jsonl"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unconfigured command created %s", name)
		}
	}

	stdout, _ = captureOutput(t, func() int { return run([]string{"config", "--json"}) })
	if stdout.status != 0 || !strings.Contains(stdout.text, `"configured":false`) {
		t.Fatalf("config --json = status %d, stdout %q", stdout.status, stdout.text)
	}
}

func TestPartialPerFileConfigurationStillRefuses(t *testing.T) {
	for _, partial := range []determinism.Env{
		{"TASKS_FILE": filepath.Join(t.TempDir(), "tasks.jsonl")},
		{"TASKS_ARCHIVE": filepath.Join(t.TempDir(), "archive.jsonl")},
	} {
		root := t.TempDir()
		partial["HOME"] = root
		partial["XDG_CONFIG_HOME"] = filepath.Join(root, "config")
		partial["TZ"] = "UTC"
		previousEnv := env
		env = partial
		stdout, stderr := captureOutput(t, func() int { return run([]string{"agenda"}) })
		env = previousEnv
		if stdout.status != 1 || !strings.Contains(stderr.text, "per-file configuration requires both") {
			t.Fatalf("partial config = status %d, stdout %q, stderr %q", stdout.status, stdout.text, stderr.text)
		}
	}
}
