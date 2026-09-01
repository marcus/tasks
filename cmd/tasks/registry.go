package main

import "fmt"

// The command registry. A command file owns its own dispatch: it calls
// register from an init, naming its canonical spelling and its handler.
// Nothing in main.go changes when a command lands.
//
// That is the point. `main.go` is the one file every implementer would
// otherwise have to edit, which makes it the file every parallel packet
// collides in. With the registry, adding `due` means adding `due.go`.
//
// The vocabulary below is the stable public command and alias contract. A
// command does not get to invent an alias; the registry and help surface read
// the same map and cannot drift apart.

// commandFunc is a command's whole contract: the resolved surface, the
// arguments after the command name, and the process exit status.
type commandFunc func(*surfaceContext, []string) int

// handlers maps a canonical command name to its implementation. A canonical
// name absent here is unavailable in this build, not unknown.
var handlers = map[string]commandFunc{}

// register wires one command into the dispatcher. It panics on a name the
// public registry does not have or a name already registered: both are programmer errors that
// would otherwise surface as a command silently shadowing another or as a
// spelling no alias reaches, and a package init is the right place to fail
// loudly.
func register(canonical string, handler commandFunc) {
	if !canonicalCommands[canonical] {
		panic(fmt.Sprintf("register: %q is not a canonical tasks command", canonical))
	}
	if _, exists := handlers[canonical]; exists {
		panic(fmt.Sprintf("register: %q registered twice", canonical))
	}
	handlers[canonical] = handler
}

// earlyCommands dispatch before configuration is resolved. Their handler is called with a nil
// surfaceContext, so a command only belongs here if it needs nothing resolved.
var earlyCommands = map[string]bool{
	"help":                 true,
	"install-merge-driver": true,
	"merge-driver":         true,
	"version":              true,
}

// dispatch resolves a spelling to its handler. The second result distinguishes
// an unavailable registered command from an unknown spelling.
func dispatch(name string) (commandFunc, bool) {
	canonical, known := aliasTokens[name]
	if !known {
		return nil, false
	}
	return handlers[canonical], true
}

// canonicalCommands is every public command. `project` is listed alongside its
// four subcommand spellings because the bare word is what dispatch resolves;
// the subcommand is the handler's own argument.
var canonicalCommands = map[string]bool{
	"-p": true, "activate": true, "agenda": true, "approve": true,
	"archive": true, "cancel": true, "capture": true, "check": true,
	"claim": true, "config": true, "defer": true, "delegate": true,
	"delete": true, "done": true, "due": true, "help": true,
	"id": true, "inbox": true, "install-merge-driver": true, "lead": true, "link": true, "links": true,
	"list": true, "merge-driver": true, "move": true, "next": true,
	"note": true, "open": true, "priority": true, "project": true,
	"project archive": true, "project complete": true, "project create": true,
	"project drop": true, "project rename": true, "project reopen": true,
	"project show": true,
	"projects":     true, "propose": true, "quadrants": true, "recur": true,
	"redo": true, "reject": true, "release": true, "repair": true,
	"retitle": true, "schedule": true, "show": true, "someday": true,
	"state": true, "tag": true, "undate": true, "undelegate": true,
	"undo": true, "unreject": true, "version": true, "workref": true,
}

// aliasTokens is Tasks::CliCommands::TOKENS: every accepted spelling mapped to
// its canonical command, identity entries included.
var aliasTokens = map[string]string{
	"--help": "help", "--prompt": "-p", "--version": "version", "-h": "help",
	"-p": "-p", "a": "agenda", "activate": "activate",
	"add": "capture", "agenda": "agenda", "approve": "approve",
	"archive": "archive", "c": "capture", "cancel": "cancel",
	"capture": "capture", "check": "check", "claim": "claim",
	"close": "done", "complete": "done", "config": "config",
	"d": "done", "deadline": "due", "defer": "defer",
	"delegate": "delegate", "delete": "delete", "done": "done",
	"drop": "cancel", "due": "due", "every": "recur",
	"fix": "repair", "help": "help", "i": "inbox",
	"id": "id", "inbox": "inbox", "install-merge-driver": "install-merge-driver", "k": "check",
	"l": "list", "lead": "lead", "lead-time": "lead",
	"leadtime": "lead", "link": "link", "links": "links", "list": "list",
	"merge-driver": "merge-driver", "move": "move", "mv": "state",
	"n": "next", "next": "next", "note": "note",
	"o": "open", "open": "open", "pj": "projects",
	"pri": "priority", "priority": "priority", "project": "project",
	"projects": "projects", "propose": "propose", "q": "quadrants",
	"quadrants": "quadrants", "recur": "recur", "redo": "redo",
	"reject": "reject", "release": "release", "rename": "retitle",
	"repair": "repair", "repeat": "recur", "reschedule": "due",
	"resume": "activate", "retitle": "retitle", "s": "show",
	"schedule": "schedule", "show": "show", "snooze": "defer",
	"someday": "someday", "state": "state", "tag": "tag",
	"undate": "undate", "undefer": "activate", "undelegate": "undelegate",
	"undo": "undo", "unreject": "unreject", "urls": "links", "version": "version", "work-ref": "workref",
	"workref": "workref", "x": "archive",
}
