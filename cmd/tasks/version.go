package main

import (
	"fmt"

	"github.com/marcus/tasks/internal/buildinfo"
)

func init() {
	register("version", versionCommand)
}

func versionCommand(_ *surfaceContext, args []string) int {
	if len(args) == 0 {
		fmt.Println(buildinfo.String("tasks"))
		return 0
	}
	if len(args) == 1 && args[0] == "--json" {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("name", "tasks")
		w.KeyStr("version", buildinfo.Version)
		w.KeyStr("commit", buildinfo.Commit)
		w.EndObject()
		out(w.String())
		return 0
	}
	return abort("usage: tasks version [--json]")
}
