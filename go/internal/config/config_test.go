package config

import (
	"os"
	"path/filepath"
	"testing"

	"tasks-go/internal/determinism"
)

// The path-resolution invariants from lib/tasks/config.rb and test/test_config.rb.
// The precedence ladder is the part a mis-port would break silently: a wrong
// answer here does not fail loudly, it reads and writes the wrong store.
//
// Salvaged in intent from port/config-resolution, whose own tests targeted an
// older Resolve(Options) signature this package no longer has. The remaining
// test_config.rb surface — urgent days, max depth, timezone, date order, theme,
// host contexts — belongs to the Wave 1 config packet.

// testEnv is a fully-pinned environment: HOME and XDG_CONFIG_HOME point into
// the sandbox so no test can read the developer's real config file.
func testEnv(t *testing.T, overrides map[string]string) (determinism.Env, string) {
	t.Helper()
	home := t.TempDir()
	env := determinism.Env{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}
	for key, value := range overrides {
		env[key] = value
	}
	return env, home
}

func writeConfig(t *testing.T, env determinism.Env, body string) {
	t.Helper()
	path := ConfigFile(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestDefaultsToDefaultDir(t *testing.T) {
	env, _ := testEnv(t, nil)
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if want := "/srv/tasks/tasks.jsonl"; paths.Org != want {
		t.Fatalf("org = %q, want %q", paths.Org, want)
	}
	if want := "/srv/tasks/archive.jsonl"; paths.Archive != want {
		t.Fatalf("archive = %q, want %q", paths.Archive, want)
	}
	if paths.Sources["org"] != "default" {
		t.Fatalf("org source = %q, want %q", paths.Sources["org"], "default")
	}
}

func TestTasksDirEnvPointsBothFiles(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TASKS_DIR": "/data/gtd"})
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/data/gtd/tasks.jsonl" || paths.Archive != "/data/gtd/archive.jsonl" {
		t.Fatalf("org/archive = %q/%q, want the TASKS_DIR pair", paths.Org, paths.Archive)
	}
	if paths.Sources["org"] != "TASKS_DIR env" {
		t.Fatalf("org source = %q", paths.Sources["org"])
	}
}

func TestPerFileEnvBeatsTasksDir(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TASKS_DIR": "/data/gtd", "TASKS_FILE": "/elsewhere/mine.jsonl"})
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/elsewhere/mine.jsonl" {
		t.Fatalf("org = %q, want the TASKS_FILE override", paths.Org)
	}
	// The archive keeps following the dir: the overrides are independent.
	if paths.Archive != "/data/gtd/archive.jsonl" {
		t.Fatalf("archive = %q, want the TASKS_DIR value", paths.Archive)
	}
	if paths.Sources["org"] != "TASKS_FILE env" || paths.Sources["archive"] != "TASKS_DIR env" {
		t.Fatalf("sources = %#v, want independent attribution", paths.Sources)
	}
}

func TestConfigFileDirKeyAndPerFileKeys(t *testing.T) {
	env, _ := testEnv(t, nil)
	writeConfig(t, env, "dir = /conf/dir\n")
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/conf/dir/tasks.jsonl" || paths.Sources["org"] != "config file" {
		t.Fatalf("org = %q (source %q), want the config dir key", paths.Org, paths.Sources["org"])
	}

	env2, _ := testEnv(t, nil)
	writeConfig(t, env2, "dir = /conf/dir\nfile = /conf/explicit.jsonl\n")
	paths2 := Resolve("/srv/tasks", env2, func() string { return "host" })
	if paths2.Org != "/conf/explicit.jsonl" {
		t.Fatalf("org = %q, want the per-file config key to beat the dir key", paths2.Org)
	}
}

func TestEnvBeatsConfigFile(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TASKS_FILE": "/env/wins.jsonl"})
	writeConfig(t, env, "file = /conf/loses.jsonl\n")
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/env/wins.jsonl" {
		t.Fatalf("org = %q, want the environment to win", paths.Org)
	}
}

func TestEmptyEnvValuesAreIgnored(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TASKS_DIR": "", "TASKS_FILE": "", "TASKS_MEMORY": ""})
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/srv/tasks/tasks.jsonl" {
		t.Fatalf("org = %q, want an empty override to be absent, not to blank the path", paths.Org)
	}
	if paths.Sources["org"] != "default" {
		t.Fatalf("org source = %q, want %q", paths.Sources["org"], "default")
	}
}

func TestMemoryFollowsFinalTasksFileNotTheBaseDir(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TASKS_DIR": "/data/gtd", "TASKS_FILE": "/elsewhere/mine.jsonl"})
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if want := "/elsewhere/agent-memory.md"; paths.Memory != want {
		t.Fatalf("memory = %q, want %q — memory is the sibling of the FINAL org path", paths.Memory, want)
	}
	if paths.Sources["memory"] != "beside tasks.jsonl" {
		t.Fatalf("memory source = %q", paths.Sources["memory"])
	}
}

func TestMemoryOverridePrecedence(t *testing.T) {
	// The config key beats the sibling default.
	env, _ := testEnv(t, nil)
	writeConfig(t, env, "memory = /conf/memory.md\n")
	if paths := Resolve("/srv/tasks", env, func() string { return "host" }); paths.Memory != "/conf/memory.md" {
		t.Fatalf("memory = %q, want the config key", paths.Memory)
	}
	// The environment beats the config key.
	env2, _ := testEnv(t, map[string]string{"TASKS_MEMORY": "/env/memory.md"})
	writeConfig(t, env2, "memory = /conf/memory.md\n")
	if paths := Resolve("/srv/tasks", env2, func() string { return "host" }); paths.Memory != "/env/memory.md" {
		t.Fatalf("memory = %q, want the environment", paths.Memory)
	}
}

func TestConfigFileIgnoresCommentsBlanksAndUnknownKeys(t *testing.T) {
	env, _ := testEnv(t, nil)
	writeConfig(t, env, "# a comment\n\n  \nnot_a_key = whatever\ndir = /conf/dir\n")
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/conf/dir/tasks.jsonl" {
		t.Fatalf("org = %q — noise around the one real key changed the answer", paths.Org)
	}
}

func TestMissingConfigFileIsFine(t *testing.T) {
	env, _ := testEnv(t, nil)
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.Org != "/srv/tasks/tasks.jsonl" {
		t.Fatalf("org = %q, want the default with no config file present", paths.Org)
	}
	if paths.ConfigFile == "" {
		t.Fatal("ConfigFile should still report where it looked")
	}
}

func TestConfigFileAndPathsExpandTilde(t *testing.T) {
	env, home := testEnv(t, nil)
	writeConfig(t, env, "file = ~/notes/tasks.jsonl\n")
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if want := filepath.Join(home, "notes", "tasks.jsonl"); paths.Org != want {
		t.Fatalf("org = %q, want %q", paths.Org, want)
	}
}

func TestExpandPathIsPurelyLexical(t *testing.T) {
	env, home := testEnv(t, nil)
	// A path that does not exist expands exactly like one that does.
	if got, want := ExpandPath("~/nope/../yes", env), filepath.Join(home, "yes"); got != want {
		t.Fatalf("ExpandPath = %q, want %q", got, want)
	}
	if got := ExpandPath("~", env); got != home {
		t.Fatalf("ExpandPath(~) = %q, want %q", got, home)
	}
}

func TestXDGBaseFallsBackUnderHome(t *testing.T) {
	env := determinism.Env{"HOME": "/home/marcus"}
	if got, want := XDGBase(env, "XDG_CONFIG_HOME", ".config"), "/home/marcus/.config"; got != want {
		t.Fatalf("XDGBase = %q, want %q", got, want)
	}
	env["XDG_CONFIG_HOME"] = "/xdg"
	if got := XDGBase(env, "XDG_CONFIG_HOME", ".config"); got != "/xdg" {
		t.Fatalf("XDGBase = %q, want the explicit value", got)
	}
}

func TestDefaultScalarsWhenNothingConfigured(t *testing.T) {
	env, _ := testEnv(t, nil)
	paths := Resolve("/srv/tasks", env, func() string { return "host" })
	if paths.UrgentDays != DefaultUrgentDays {
		t.Fatalf("urgent days = %d, want %d", paths.UrgentDays, DefaultUrgentDays)
	}
	if paths.MaxDepth != DefaultMaxDepth {
		t.Fatalf("max depth = %d, want %d", paths.MaxDepth, DefaultMaxDepth)
	}
	if paths.DateOrder != DefaultDateOrder {
		t.Fatalf("date order = %q, want %q", paths.DateOrder, DefaultDateOrder)
	}
	if paths.Theme != DefaultTheme {
		t.Fatalf("theme = %q, want %q", paths.Theme, DefaultTheme)
	}
	if paths.TimeFormat != DefaultTimeFormat {
		t.Fatalf("time format = %d, want %d", paths.TimeFormat, DefaultTimeFormat)
	}
}
