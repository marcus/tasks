package config

import "os"

// Report is the path-resolution portion of `tasks config --json`. The broader
// settings report belongs to the slices that own those settings; keeping this
// shape here gives a future CLI adapter one shared, deterministic source for
// the paths this package resolves.
type Report struct {
	Org              string            `json:"org"`
	Archive          string            `json:"archive"`
	Memory           string            `json:"memory"`
	Sources          map[string]string `json:"sources"`
	MemoryExists     bool              `json:"memory_exists"`
	ConfigFile       string            `json:"config_file"`
	ConfigFileExists bool              `json:"config_file_exists"`
}

// ConfigReport presents resolved paths in the JSON names used by the Ruby
// config command. It is deliberately a projection: resolution itself remains
// in Resolve, so CLI and probe adapters cannot reimplement precedence.
func ConfigReport(paths Paths) Report {
	return Report{
		Org:              paths.Org,
		Archive:          paths.Archive,
		Memory:           paths.Memory,
		Sources:          paths.Sources,
		MemoryExists:     regularFile(paths.Memory),
		ConfigFile:       paths.ConfigFile,
		ConfigFileExists: regularFile(paths.ConfigFile),
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
