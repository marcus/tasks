package cli

import (
	"bytes"
	"path/filepath"
	"testing"

	"tasks-go/internal/config"
)

func TestWriteConfigJSONUsesResolverPathsAndSources(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	err := WriteConfigJSON(&out, config.Options{
		DefaultDir: dir,
		HomeDir:    dir,
		Env: map[string]string{
			"TASKS_DIR":    "",
			"TASKS_FILE":   filepath.Join(dir, "chosen.jsonl"),
			"TASKS_MEMORY": "",
		},
	})
	if err != nil {
		t.Fatalf("WriteConfigJSON() error = %v", err)
	}

	want := "{\"org\":\"" + filepath.Join(dir, "chosen.jsonl") +
		"\",\"archive\":\"" + filepath.Join(dir, "archive.jsonl") +
		"\",\"memory\":\"" + filepath.Join(dir, "agent-memory.md") +
		"\",\"sources\":{\"archive\":\"default\",\"memory\":\"beside tasks.jsonl\",\"org\":\"TASKS_FILE env\"},\"memory_exists\":false,\"config_file\":\"" + filepath.Join(dir, ".config", "tasks", "config") + "\",\"config_file_exists\":false}\n"
	if got := out.String(); got != want {
		t.Errorf("WriteConfigJSON() = %q, want %q", got, want)
	}
}
