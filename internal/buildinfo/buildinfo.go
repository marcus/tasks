package buildinfo

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
)

func String(name string) string {
	if Commit == "" || Commit == "unknown" {
		return fmt.Sprintf("%s %s", name, Version)
	}
	return fmt.Sprintf("%s %s (%s)", name, Version, Commit)
}
