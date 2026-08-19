// Command tasks is the scriptable CLI over the Tasks JSONL store.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/journal"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/updatestamp"
)

// env is the process environment, read once. Every seam that honours a harness
// pin reads it from here rather than from os.Getenv, so a single map is the
// whole determinism surface.
var env = determinism.OSEnv()

// unavailableExit is deliberately not 2: exit 2 means "the ref did not
// resolve, refine it", and a caller that retried a different ref against an
// unavailable command would loop forever.
const unavailableExit = 1

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tasks <command> [args]")
		return 1
	}
	name, rest := argv[0], argv[1:]

	handler, isCommand := dispatch(name)

	// The early commands run BEFORE any store is resolved, which is what
	// tasks does at its top: "Dispatch before resolving a configured task
	// store so the driver depends only on the three explicit files Git
	// supplied." Git invokes merge-driver with temporary stage paths and no
	// task store in sight, so resolving one first would make an unrelated
	// misconfiguration — an invalid TASKS_TIMEZONE, say — add a stderr note to
	// a plumbing command Git is parsing. They get no surfaceContext for the
	// same reason: there is nothing resolved yet to put in one.
	if handler != nil && earlyCommands[aliasTokens[name]] {
		return handler(nil, rest)
	}
	if !isCommand {
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", name)
		os.Stderr.WriteString(helpText(helpModes()))
		return 1
	}
	if handler == nil {
		fmt.Fprintf(os.Stderr, "%s: unavailable in this build\n", name)
		return unavailableExit
	}

	paths := config.Resolve("", env, nil)
	if !paths.Configured && aliasTokens[name] != "config" {
		fmt.Fprintln(os.Stderr, config.ConfigurationRequiredMessage(paths))
		return 1
	}
	// Resolution notes go to stderr before the command runs, which is where
	// Ruby's Config.resolve writes them. config returns them rather than
	// printing so the TUI and API can place them differently; the CLI's choice
	// is to keep Ruby's placement exactly.
	for _, warning := range paths.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}
	surface := &surfaceContext{paths: paths, store: store.NewReader(paths.Org, paths.Archive, paths.Modes())}

	return handler(surface, rest)
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
		Modes:         s.paths.Modes(),
	}
	if clock := determinism.Clock(env); clock != nil {
		options.Now = clock
	}
	// SharedIDSource, not IDSource: every writeStore in one invocation must draw
	// from ONE sequence. A fresh mint per store restarts the pinned sequence at
	// its first token, so a second store would re-mint an id the first already
	// used and the collision loop would silently renumber it.
	if sequence, err := determinism.SharedIDSource(env); err == nil && sequence != nil {
		options.IDSource = sequence.Call
	}
	return store.NewWriter(s.paths.Org, s.paths.Archive, options)
}

// mintDelegationKey draws sixteen hex characters from the pinned mint when
// there is one. The key is persisted into journal index.json, so it is
// observable in the bytes a conformance run digests: two runs of the same
// command agree only because this draw is reproducible.
//
// The memoization this adapter used to keep in its own package-level pair now
// lives in determinism, where Ruby keeps it and where the id mint needs it too.
func (s *surfaceContext) mintDelegationKey() string {
	if sequence, err := determinism.SharedDelegationKeySource(env); err == nil && sequence != nil {
		return sequence.Call()
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
