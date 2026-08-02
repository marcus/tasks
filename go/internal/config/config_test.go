package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedenceAndMemory(t *testing.T) {
	temp := t.TempDir()
	home := filepath.Join(temp, "home")
	xdg := filepath.Join(temp, "xdg")
	configPath := filepath.Join(xdg, "tasks", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("dir = ~/from-config\nfile = ~/special/tasks.jsonl\narchive = ~/special/archive.jsonl\nmemory = ~/notes/memory.md\nunknown = ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := Resolve(Options{DefaultDir: filepath.Join(temp, "default"), HomeDir: home, Env: map[string]string{
		"XDG_CONFIG_HOME": xdg, "TASKS_DIR": filepath.Join(temp, "from-env"),
		"TASKS_FILE": filepath.Join(temp, "override", "tasks.jsonl"), "TASKS_MEMORY": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(temp, "override", "tasks.jsonl"); paths.Org != want {
		t.Fatalf("Org = %q, want %q", paths.Org, want)
	}
	if want := filepath.Join(home, "special", "archive.jsonl"); paths.Archive != want {
		t.Fatalf("Archive = %q, want %q", paths.Archive, want)
	}
	if want := filepath.Join(home, "notes", "memory.md"); paths.Memory != want {
		t.Fatalf("Memory = %q, want %q", paths.Memory, want)
	}
	if paths.Sources["org"] != "TASKS_FILE env" || paths.Sources["archive"] != "config file" || paths.Sources["memory"] != "config file" {
		t.Fatalf("Sources = %#v", paths.Sources)
	}
}

func TestResolveDefaultsAndMemoryFollowsFinalFile(t *testing.T) {
	temp := t.TempDir()
	paths, err := Resolve(Options{DefaultDir: temp, HomeDir: filepath.Join(temp, "home"), Env: map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(temp, "xdg"), "TASKS_DIR": "", "TASKS_FILE": filepath.Join(temp, "outside", "tasks.jsonl"),
		"TASKS_ARCHIVE": "", "TASKS_MEMORY": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(temp, "outside", "agent-memory.md"); paths.Memory != want {
		t.Fatalf("Memory = %q, want %q", paths.Memory, want)
	}
	if paths.Sources["memory"] != "beside tasks.jsonl" {
		t.Fatalf("memory source = %q", paths.Sources["memory"])
	}
}

func TestForDirIgnoresEnvironment(t *testing.T) {
	paths, err := ForDir("/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Org != "/sandbox/tasks.jsonl" || paths.Archive != "/sandbox/archive.jsonl" || paths.Memory != "/sandbox/agent-memory.md" {
		t.Fatalf("Paths = %#v", paths)
	}
	if paths.Sources["org"] != "pinned" || paths.Sources["memory"] != "pinned" {
		t.Fatalf("Sources = %#v", paths.Sources)
	}
}
