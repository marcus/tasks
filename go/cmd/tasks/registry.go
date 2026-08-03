package main

import "fmt"

// The command registry. A command file owns its own dispatch: it calls
// register from an init, naming its canonical spelling, its aliases, and its
// handler. Nothing in main.go changes when a command lands.
//
// That is the point. `main.go` is the one file every implementer would
// otherwise have to edit, which makes it the file every parallel packet
// collides in. With the registry, adding `due` means adding `due.go` — and
// because canonicalCommands below is the FULL Ruby command set, registering a
// command also removes it from the refusal list automatically. The unported
// refusal and the working command can never disagree about what this binary
// supports, because they read the same map.

// commandFunc is a command's whole contract: the resolved surface, the
// arguments after the command name, and the process exit status.
type commandFunc func(*surfaceContext, []string) int

var (
	// handlers maps a canonical command name to its implementation.
	handlers = map[string]commandFunc{}
	// spellings maps every accepted spelling — canonical and alias alike — to
	// its canonical name.
	spellings = map[string]string{}
)

// register wires one command into the dispatcher. It panics on a duplicate
// name or an unknown canonical spelling: both are programmer errors that would
// otherwise surface as a command silently shadowing another, and a package
// init is the right place to fail loudly.
func register(canonical string, aliases []string, handler commandFunc) {
	if !canonicalCommands[canonical] {
		panic(fmt.Sprintf("register: %q is not a canonical tasks command", canonical))
	}
	if _, exists := handlers[canonical]; exists {
		panic(fmt.Sprintf("register: %q registered twice", canonical))
	}
	handlers[canonical] = handler
	for _, spelling := range append([]string{canonical}, aliases...) {
		if owner, taken := spellings[spelling]; taken {
			panic(fmt.Sprintf("register: %q already dispatches to %q", spelling, owner))
		}
		spellings[spelling] = canonical
	}
}

// canonicalCommands is every command name Ruby's Tasks::CliCommands accepts,
// whether or not this build implements it. A name absent here is a typo; a
// name present but unregistered is an honest "not ported yet".
var canonicalCommands = map[string]bool{
	"config": true, "check": true, "list": true, "agenda": true,
	"undo": true, "done": true, "capture": true, "propose": true,
	"priority": true, "delegate": true, "claim": true,
	"approve": true, "reject": true,
	"undelegate": true, "workref": true, "release": true,
	"due": true, "schedule": true, "undate": true, "state": true, "cancel": true,
	"drop": true, "retitle": true, "tag": true, "note": true,
	"move": true, "delete": true, "recur": true, "lead": true, "defer": true,
	"snooze": true, "someday": true, "activate": true, "archive": true, "x": true,
	"redo": true, "repair": true, "fix": true, "id": true, "project": true,
}

// dispatch resolves a spelling to its handler. The second result distinguishes
// a command this build has not ported from one that does not exist, which are
// different answers to the user: one is "not yet", the other is "typo".
func dispatch(name string) (commandFunc, bool) {
	canonical, known := spellings[name]
	if !known {
		return nil, canonicalCommands[name]
	}
	return handlers[canonical], true
}
