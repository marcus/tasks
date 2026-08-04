package main

import "testing"

func TestVersionDoesNotRequireTaskConfiguration(t *testing.T) {
	if status := run([]string{"--version"}); status != 0 {
		t.Fatalf("--version status = %d", status)
	}
}

func TestUnconfiguredServerRefusesBeforeListening(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root+"/config")
	for _, key := range []string{"TASKS_DIR", "TASKS_FILE", "TASKS_ARCHIVE", "TASKS_MEMORY"} {
		t.Setenv(key, "")
	}
	if status := run(nil); status != 1 {
		t.Fatalf("unconfigured status = %d", status)
	}
}
