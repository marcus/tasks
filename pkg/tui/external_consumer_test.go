package tui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOutOfModuleConsumer(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	data := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(root, "testdata", "fixtures", "valid", "small-gtd", "store", "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "tasks.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(data, "config", "tasks")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(data, "fake-provider")
	script := "#!/bin/sh\nprintf '%s' $$ > \"$FAKE_PID_FILE\"\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(provider, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configText := "llm_provider = claude-cli\nclaude-cli_command = " + provider + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(data, "provider.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = filepath.Join(root, "testdata", "external-tui-consumer")
	cmd.Env = append(os.Environ(),
		"GOWORK=off", "TASKS_DIR="+data, "XDG_STATE_HOME="+filepath.Join(data, "state"),
		"XDG_CONFIG_HOME="+filepath.Join(data, "config"), "FAKE_PID_FILE="+pidFile,
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("external consumer leaked or timed out: %v", ctx.Err())
	}
	if err != nil || !strings.Contains(string(output), "constructed, drove keys, rendered, saved, closed") {
		t.Fatalf("external consumer: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(data, "state", "tasks", "hosts", "external-proof", "tui.json")); err != nil {
		t.Fatalf("external session was not saved: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(data, "tasks.jsonl"))
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatalf("presentation filters changed task records: %v", err)
	}
}
