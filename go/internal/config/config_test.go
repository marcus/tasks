package config

import (
	"encoding/json"
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

func TestResolvePathPrecedenceTable(t *testing.T) {
	temp := t.TempDir()
	home := filepath.Join(temp, "home")
	xdg := filepath.Join(temp, "xdg")
	configPath := filepath.Join(xdg, "tasks", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("dir = ~/from-config\nfile = ~/configured/tasks.jsonl\narchive = ~/configured/archive.jsonl\nmemory = ~/configured/agent-memory.md\nignored = value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	defaultDir := filepath.Join(temp, "default")
	path := func(parts ...string) string { return filepath.Join(append([]string{temp}, parts...)...) }
	cases := []struct {
		name                 string
		env                  map[string]string
		org, archive, memory string
		orgSource            string
		archiveSource        string
		memorySource         string
	}{
		{
			name: "config file supplies each path",
			env:  map[string]string{"XDG_CONFIG_HOME": xdg},
			org:  path("home", "configured", "tasks.jsonl"), archive: path("home", "configured", "archive.jsonl"),
			memory:    path("home", "configured", "agent-memory.md"),
			orgSource: "config file", archiveSource: "config file", memorySource: "config file",
		},
		{
			name: "config file paths beat tasks dir",
			env:  map[string]string{"XDG_CONFIG_HOME": xdg, "TASKS_DIR": path("from-env")},
			org:  path("home", "configured", "tasks.jsonl"), archive: path("home", "configured", "archive.jsonl"),
			memory:    path("home", "configured", "agent-memory.md"),
			orgSource: "config file", archiveSource: "config file", memorySource: "config file",
		},
		{
			name: "per file environment beats every lower source",
			env:  map[string]string{"XDG_CONFIG_HOME": xdg, "TASKS_DIR": path("from-env"), "TASKS_FILE": path("override", "tasks.jsonl"), "TASKS_ARCHIVE": path("override", "archive.jsonl"), "TASKS_MEMORY": path("override", "memory.md")},
			org:  path("override", "tasks.jsonl"), archive: path("override", "archive.jsonl"), memory: path("override", "memory.md"),
			orgSource: "TASKS_FILE env", archiveSource: "TASKS_ARCHIVE env", memorySource: "TASKS_MEMORY env",
		},
		{
			name: "memory follows final file override",
			env:  map[string]string{"XDG_CONFIG_HOME": filepath.Join(temp, "empty-xdg"), "TASKS_FILE": path("override", "tasks.jsonl")},
			org:  path("override", "tasks.jsonl"), archive: filepath.Join(defaultDir, "archive.jsonl"),
			memory:    path("override", "agent-memory.md"),
			orgSource: "TASKS_FILE env", archiveSource: "default", memorySource: "beside tasks.jsonl",
		},
		{
			name: "empty environment values are ignored",
			env:  map[string]string{"XDG_CONFIG_HOME": xdg, "TASKS_DIR": "", "TASKS_FILE": "", "TASKS_ARCHIVE": "", "TASKS_MEMORY": ""},
			org:  path("home", "configured", "tasks.jsonl"), archive: path("home", "configured", "archive.jsonl"),
			memory:    path("home", "configured", "agent-memory.md"),
			orgSource: "config file", archiveSource: "config file", memorySource: "config file",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, err := Resolve(Options{DefaultDir: defaultDir, HomeDir: home, Env: tc.env})
			if err != nil {
				t.Fatal(err)
			}
			if paths.Org != tc.org || paths.Archive != tc.archive || paths.Memory != tc.memory {
				t.Fatalf("Paths = %#v, want org=%q archive=%q memory=%q", paths, tc.org, tc.archive, tc.memory)
			}
			if paths.Sources["org"] != tc.orgSource || paths.Sources["archive"] != tc.archiveSource || paths.Sources["memory"] != tc.memorySource {
				t.Fatalf("Sources = %#v, want org=%q archive=%q memory=%q", paths.Sources, tc.orgSource, tc.archiveSource, tc.memorySource)
			}
		})
	}
}

func TestResolvePathSourcesAreIndependentAcrossAllOverrides(t *testing.T) {
	temp := t.TempDir()
	home := filepath.Join(temp, "home")
	xdg := filepath.Join(temp, "xdg")
	configPath := filepath.Join(xdg, "tasks", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("dir = ~/configured-dir\nfile = ~/configured/tasks.jsonl\narchive = ~/configured/archive.jsonl\nmemory = ~/configured/agent-memory.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	defaultDir := filepath.Join(temp, "default")
	configured := filepath.Join(home, "configured")
	override := filepath.Join(temp, "override")
	for mask := 0; mask < 8; mask++ {
		env := map[string]string{"XDG_CONFIG_HOME": xdg}
		if mask&1 != 0 {
			env["TASKS_FILE"] = filepath.Join(override, "tasks.jsonl")
		}
		if mask&2 != 0 {
			env["TASKS_ARCHIVE"] = filepath.Join(override, "archive.jsonl")
		}
		if mask&4 != 0 {
			env["TASKS_MEMORY"] = filepath.Join(override, "agent-memory.md")
		}

		paths, err := Resolve(Options{DefaultDir: defaultDir, HomeDir: home, Env: env})
		if err != nil {
			t.Fatalf("mask %03b: %v", mask, err)
		}
		wantOrg, wantArchive, wantMemory := filepath.Join(configured, "tasks.jsonl"), filepath.Join(configured, "archive.jsonl"), filepath.Join(configured, "agent-memory.md")
		wantOrgSource, wantArchiveSource, wantMemorySource := "config file", "config file", "config file"
		if mask&1 != 0 {
			wantOrg, wantOrgSource = filepath.Join(override, "tasks.jsonl"), "TASKS_FILE env"
		}
		if mask&2 != 0 {
			wantArchive, wantArchiveSource = filepath.Join(override, "archive.jsonl"), "TASKS_ARCHIVE env"
		}
		if mask&4 != 0 {
			wantMemory, wantMemorySource = filepath.Join(override, "agent-memory.md"), "TASKS_MEMORY env"
		}
		if paths.Org != wantOrg || paths.Archive != wantArchive || paths.Memory != wantMemory {
			t.Fatalf("mask %03b paths = %#v, want org=%q archive=%q memory=%q", mask, paths, wantOrg, wantArchive, wantMemory)
		}
		if paths.Sources["org"] != wantOrgSource || paths.Sources["archive"] != wantArchiveSource || paths.Sources["memory"] != wantMemorySource {
			t.Fatalf("mask %03b sources = %#v", mask, paths.Sources)
		}
	}
}

func TestConfigReportProjectsResolvedPathsAndExistence(t *testing.T) {
	temp := t.TempDir()
	memory := filepath.Join(temp, "agent-memory.md")
	configFile := filepath.Join(temp, "config")
	if err := os.WriteFile(memory, []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := Paths{
		Org:        filepath.Join(temp, "tasks.jsonl"),
		Archive:    filepath.Join(temp, "archive.jsonl"),
		Memory:     memory,
		ConfigFile: configFile,
		Sources:    map[string]string{"org": "default", "archive": "default", "memory": "beside tasks.jsonl"},
	}

	report := ConfigReport(paths)
	if !report.MemoryExists || report.ConfigFileExists {
		t.Fatalf("existence = memory:%t config:%t, want memory:true config:false", report.MemoryExists, report.ConfigFileExists)
	}
	if report.Org != paths.Org || report.Archive != paths.Archive || report.Memory != paths.Memory || report.ConfigFile != paths.ConfigFile {
		t.Fatalf("report paths = %#v, want projection of %#v", report, paths)
	}
	if report.Sources["memory"] != "beside tasks.jsonl" {
		t.Fatalf("sources = %#v", report.Sources)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"org", "archive", "memory", "sources", "memory_exists", "config_file", "config_file_exists"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("JSON report omitted %q: %s", key, encoded)
		}
	}
}
