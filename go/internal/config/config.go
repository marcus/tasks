// Package config resolves the task-store paths shared by every Go adapter.
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Paths is the resolution result for the task files and their provenance.
// Sources use the same user-visible labels as the Ruby oracle.
type Paths struct {
	Org        string
	Archive    string
	Memory     string
	Sources    map[string]string
	ConfigFile string
}

// Options makes resolution deterministic for callers and tests. Empty HomeDir
// uses the process home directory; nil Env uses the process environment.
type Options struct {
	DefaultDir string
	Env        map[string]string
	HomeDir    string
}

// Resolve applies the Ruby resolver's store-path precedence: per-file
// environment variables, TASKS_DIR, config-file values, then DefaultDir.
func Resolve(options Options) (Paths, error) {
	home, err := options.homeDir()
	if err != nil {
		return Paths{}, err
	}
	configFile, err := configFile(options, home)
	if err != nil {
		return Paths{}, err
	}
	values, err := parseFile(configFile, home)
	if err != nil {
		return Paths{}, err
	}

	dir, dirSource := options.DefaultDir, "default"
	if value := options.value("TASKS_DIR"); value != "" {
		dir, dirSource = value, "TASKS_DIR env"
	} else if value := values["dir"]; value != "" {
		dir, dirSource = value, "config file"
	}

	org, orgSource, err := pick("tasks.jsonl", "TASKS_FILE", dir, dirSource, values["file"], options)
	if err != nil {
		return Paths{}, err
	}
	archive, archiveSource, err := pick("archive.jsonl", "TASKS_ARCHIVE", dir, dirSource, values["archive"], options)
	if err != nil {
		return Paths{}, err
	}
	memory, memorySource, err := pickMemory(org, values["memory"], options)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Org: org, Archive: archive, Memory: memory, ConfigFile: configFile,
		Sources: map[string]string{"org": orgSource, "archive": archiveSource, "memory": memorySource}}, nil
}

// ForDir pins all store paths to dir and intentionally ignores config and env.
func ForDir(dir string) (Paths, error) {
	base, err := absolute(dir)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Org: filepath.Join(base, "tasks.jsonl"), Archive: filepath.Join(base, "archive.jsonl"),
		Memory: filepath.Join(base, "agent-memory.md"), Sources: map[string]string{"org": "pinned", "archive": "pinned", "memory": "pinned"}}, nil
}

func pick(basename, envKey, dir, dirSource, configured string, options Options) (string, string, error) {
	if value := options.value(envKey); value != "" {
		path, err := absolute(value)
		return path, envKey + " env", err
	}
	if configured != "" {
		return configured, "config file", nil
	}
	path, err := absolute(filepath.Join(dir, basename))
	return path, dirSource, err
}

func pickMemory(org, configured string, options Options) (string, string, error) {
	if value := options.value("TASKS_MEMORY"); value != "" {
		path, err := absolute(value)
		return path, "TASKS_MEMORY env", err
	}
	if configured != "" {
		return configured, "config file", nil
	}
	path, err := absolute(filepath.Join(filepath.Dir(org), "agent-memory.md"))
	return path, "beside tasks.jsonl", err
}

func configFile(options Options, home string) (string, error) {
	base := options.value("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	base, err := absolute(base)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tasks", "config"), nil
}

func parseFile(path, home string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if value == "" || (key != "dir" && key != "file" && key != "archive" && key != "memory") {
			continue
		}
		path, err := expandPath(value, home)
		if err != nil {
			return nil, err
		}
		values[key] = path
	}
	return values, nil
}

func (options Options) value(key string) string {
	if options.Env != nil {
		return options.Env[key]
	}
	return os.Getenv(key)
}

func (options Options) homeDir() (string, error) {
	if options.HomeDir != "" {
		return absolute(options.HomeDir)
	}
	return os.UserHomeDir()
}

func expandPath(path, home string) (string, error) {
	if path == "~" {
		path = home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	}
	return absolute(path)
}

func absolute(path string) (string, error) { return filepath.Abs(path) }
