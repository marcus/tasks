// Command tasks is the Go port of bin/tasks.
//
// It is a REAL dispatcher, not a probe: the same command names, the same
// aliases, the same exit codes, and byte-identical output for every surface it
// implements. What it deliberately does NOT implement yet is the write path —
// there is no capture, no state transition, no journal append. A command that
// would write refuses with a stated reason and a nonzero status rather than
// producing a half-write, because a partial mutation is the one failure mode a
// task store cannot recover from on its own.
//
// The commands that ARE implemented are the read-only surfaces and the
// refusals that precede a write: `config`, `check`, `list`, `agenda`, `undo`
// (which, with no applicable journal entry, only ever refuses), and `done`'s
// ref resolution, whose exit-2-on-no-match/ambiguous distinction exists so an
// agent can refine a ref instead of aborting.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/store"
)

// env is the process environment, read once. Every seam that honours a harness
// pin reads it from here rather than from os.Getenv, so a single map is the
// whole determinism surface.
var env = determinism.OSEnv()

// notImplementedExit is the status a refused-because-unported command exits
// with. It is deliberately NOT 2: exit 2 means "the ref did not resolve, refine
// it", and a caller that retried a different ref against a command this binary
// cannot perform would loop forever.
const notImplementedExit = 1

func main() {
	os.Exit(run(os.Args[1:]))
}

// aliases maps every accepted spelling to its canonical command name, matching
// Tasks::CliCommands. Only the commands this port implements appear here.
var aliases = map[string]string{
	"config": "config",
	"check":  "check", "k": "check",
	"list": "list", "l": "list",
	"agenda": "agenda", "a": "agenda",
	"undo": "undo",
	"done": "done", "d": "done",
}

// writeCommands are the names Ruby implements as mutations. They are named
// here so an invocation gets an explicit refusal rather than "unknown
// command", which would misreport a supported command as a typo.
var writeCommands = map[string]bool{
	"capture": true, "c": true, "propose": true, "approve": true, "reject": true,
	"delegate": true, "undelegate": true, "workref": true, "claim": true, "release": true,
	"due": true, "schedule": true, "undate": true, "state": true, "cancel": true,
	"drop": true, "priority": true, "retitle": true, "tag": true, "note": true,
	"move": true, "delete": true, "recur": true, "lead": true, "defer": true,
	"snooze": true, "someday": true, "activate": true, "archive": true, "x": true,
	"redo": true, "repair": true, "fix": true, "id": true, "project": true,
}

func run(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tasks <command> [args]")
		return 1
	}
	name, rest := argv[0], argv[1:]

	paths := config.Resolve(repoRoot(), env, nil)
	surface := &surfaceContext{paths: paths, store: store.New(paths.Org, paths.Archive)}

	switch aliases[name] {
	case "config":
		return surface.config(rest)
	case "check":
		return surface.check(rest)
	case "list":
		return surface.list(rest)
	case "agenda":
		return surface.agenda(rest)
	case "undo":
		return surface.undo(rest)
	case "done":
		return surface.done(rest)
	}

	if writeCommands[name] {
		fmt.Fprintf(os.Stderr, "%s: not implemented in the Go port — this build has no write path, "+
			"and refusing is the only answer that cannot leave a half-written store\n", name)
		return notImplementedExit
	}
	fmt.Fprintf(os.Stderr, "unknown command: %q\n", name)
	return 1
}

// surfaceContext is what every command needs and nothing more: the resolved
// configuration and the store it names.
type surfaceContext struct {
	paths config.Paths
	store *store.Store
}

// repoRoot is bin/tasks' ROOT — the repository the binary was built into. It is
// only ever the LAST fallback for where the task files live (TASKS_DIR, the
// per-file overrides and the config file all outrank it), so resolving it from
// the executable rather than the working directory keeps `tasks` answering the
// same way from every cwd.
func repoRoot() string {
	executable, err := os.Executable()
	if err != nil {
		return "."
	}
	// <root>/go/bin/tasks → <root>.
	return filepath.Dir(filepath.Dir(filepath.Dir(executable)))
}
