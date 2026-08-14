package buildinfo

import (
	"fmt"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "unknown"
)

func init() {
	info, _ := debug.ReadBuildInfo()
	Version = resolvedVersion(Version, info)
}

func resolvedVersion(linked string, info *debug.BuildInfo) string {
	if linked != "dev" || info == nil {
		return linked
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs" {
			return linked
		}
	}
	if version := info.Main.Version; version != "" && version != "(devel)" {
		return version
	}
	return linked
}

func String(name string) string {
	if Commit == "" || Commit == "unknown" {
		return fmt.Sprintf("%s %s", name, Version)
	}
	return fmt.Sprintf("%s %s (%s)", name, Version, Commit)
}
