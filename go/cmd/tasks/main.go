// Command tasks is the Go port of bin/tasks.
//
// It is a REAL dispatcher, not a probe: the same command names, the same
// aliases, the same exit codes, and byte-identical output for every surface it
// implements — including, now, the write path. `capture`, `done`, `priority`,
// `delegate` and `claim` replace the store atomically, validate what they
// wrote, roll back if it does not hold, and append one journal step.
//
// What a command still cannot do it REFUSES, with a stated reason and a nonzero
// status, rather than approximating. A partial or subtly-wrong mutation is the
// one failure mode a task store cannot recover from on its own, so an unported
// flag is an error and never a silent default.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/journal"
	"tasks-go/internal/jsonout"
	"tasks-go/internal/store"
	"tasks-go/internal/updatestamp"
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
	"capture": "capture", "c": "capture",
	"propose":  "propose",
	"priority": "priority",
	"delegate": "delegate",
	"claim":    "claim",
}

// writeCommands are the mutations Ruby implements that this build does NOT.
// They are named here so an invocation gets an explicit refusal rather than
// "unknown command", which would misreport a supported command as a typo.
var writeCommands = map[string]bool{
	"approve": true, "reject": true,
	"undelegate": true, "workref": true, "release": true,
	"due": true, "schedule": true, "undate": true, "state": true, "cancel": true,
	"drop": true, "retitle": true, "tag": true, "note": true,
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
	case "capture":
		return surface.capture(rest, false)
	case "propose":
		return surface.capture(rest, true)
	case "priority":
		return surface.priority(rest)
	case "delegate":
		return surface.delegate(rest)
	case "claim":
		return surface.claim(rest)
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

// writeStore is the store a mutation runs through. It is built here, at the
// adapter boundary, because this is the one layer allowed to read the
// environment: the store itself stays env-free and takes injected values, which
// is what keeps a harness pin from becoming a second configuration system.
func (s *surfaceContext) writeStore() *store.Store {
	options := store.Options{
		JournalDir:    journal.DirFor(s.paths.Org, env),
		Device:        updatestamp.Device(env),
		CoalesceScope: coalesceScope(),
		MaxDepth:      s.paths.MaxDepth,
	}
	if clock := determinism.Clock(env); clock != nil {
		options.Now = clock
	}
	if sequence, err := determinism.IDSource(env); err == nil && sequence != nil {
		options.IDSource = sequence.Call
	}
	return store.NewWriter(s.paths.Org, s.paths.Archive, options)
}

// idSequence and delegationSequence are process-wide because ONE invocation can
// perform several mutations and they must draw from one sequence rather than
// each restarting it — the same reason Determinism memoizes them in Ruby.
var delegationSequence *determinism.IDSequence
var delegationSequenceReady bool

// mintDelegationKey draws sixteen hex characters from the pinned mint when
// there is one. The key is persisted into journal index.json, so it is
// observable in the bytes a conformance run digests: two runs of the same
// command agree only because this draw is reproducible.
func (s *surfaceContext) mintDelegationKey() string {
	if !delegationSequenceReady {
		delegationSequenceReady = true
		if sequence, err := determinism.DelegationKeySource(env); err == nil {
			delegationSequence = sequence
		}
	}
	if delegationSequence != nil {
		return delegationSequence.Call()
	}
	return randomHex(8)
}

// coalesceScope is the journal's per-process coalescing scope: a random token
// unless the harness pinned one. It is persisted on a keyed tip, which is what
// stops an unrelated process from extending this one's coalesced step.
func coalesceScope() string {
	if pinned, ok := determinism.CoalesceScope(env); ok {
		return pinned
	}
	if processScope == "" {
		processScope = randomHex(16)
	}
	return processScope
}

var processScope string

func jsonWriter() *jsonout.Writer { return jsonout.New() }

func jsonRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
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

// randomHex is SecureRandom.hex(n): the unpinned default for both the
// coalescing scope and a delegation key.
func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		// These tokens gate journal coalescing across processes. A predictable
		// one would let an unrelated process extend this one's history step.
		fmt.Fprintln(os.Stderr, "tasks: secure random unavailable")
		os.Exit(1)
	}
	return hex.EncodeToString(buffer)
}
