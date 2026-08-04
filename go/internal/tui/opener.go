package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"tasks-go/internal/determinism"
)

// SystemOpener launches a URL in the user's browser — the Go port of
// Tasks::Opener's behavior. In Ruby that module is shared by `tasks open` and
// the TUI's `o`; on this side it is TUI-OWNED, because the Go `open` command
// resolves its launcher itself. What is shared is the contract, not the code.
//
// TASKS_OPENER overrides the platform launcher and is SHELL-SPLIT, so
// `TASKS_OPENER="open -a Safari"` works. That override is also how a test
// observes an open without a browser appearing, which is why it is honoured
// before the platform default rather than after it.
type SystemOpener struct {
	// Env is the environment the override is read from. A nil Env reads the
	// process environment.
	Env determinism.Env
}

// Command is the launcher argv without the URL, and whether there is one to
// run at all.
//
// A RAW non-empty override is AUTHORITATIVE: if the user set TASKS_OPENER, that
// is the launcher, and a malformed or empty-splitting value means there is no
// launcher — never a quiet fall back to the platform default. Falling back
// would silently open a browser the user had explicitly configured away from,
// which is the one outcome an override exists to prevent.
//
// This is exactly what Ruby does, by a different route: `Shellwords.split`
// raises on an unmatched quote and `Opener.open_url` rescues ArgumentError into
// false; a whitespace-only or quoted-empty override splits to no usable
// launcher and the spawn fails. Both arrive at "no open, and say so".
func (o SystemOpener) Command() ([]string, bool) {
	raw := o.lookup("TASKS_OPENER")
	if raw != "" {
		fields, ok := shellSplit(raw)
		if !ok || len(fields) == 0 {
			return nil, false
		}
		return fields, true
	}
	if runtime.GOOS == "darwin" {
		return []string{"open"}, true
	}
	return []string{"xdg-open"}, true
}

// Open launches the URL detached and reports whether the launcher could be
// spawned at all.
//
// Output is DISCARDED: a TUI must not have a browser's stderr scribbled over
// it. And any spawn failure — a missing launcher, a non-executable
// TASKS_OPENER — returns false rather than propagating, because a bad opener
// must not take the interface down on a keystroke.
func (o SystemOpener) Open(url string) bool {
	launcher, ok := o.Command()
	if !ok {
		return false
	}
	argv := append(launcher, url)
	command := exec.Command(argv[0], argv[1:]...)
	command.Stdout = nil
	command.Stderr = nil
	command.Stdin = nil
	if err := command.Start(); err != nil {
		return false
	}
	// Detach: reap the child in the background so a long-lived browser process
	// does not become a zombie for the life of the TUI.
	go func() { _ = command.Wait() }()
	return true
}

func (o SystemOpener) lookup(name string) string {
	if o.Env != nil {
		return o.Env.Get(name)
	}
	return os.Getenv(name)
}

// shellSplit is Shellwords.split for the shapes an opener override takes: words
// separated by whitespace, with single or double quotes grouping and a
// backslash escaping the next character. The second return is false for input
// Ruby REFUSES, which is an unmatched quote and nothing else.
//
// A dangling trailing backslash is deliberately NOT an error: Ruby keeps it as
// a literal backslash (`Shellwords.split("open \\")` is `["open", "\\"]`),
// and matching that matters because the outcomes differ — a refusal opens
// nothing, while a literal backslash produces a launcher name that will simply
// fail to spawn.
//
// It is spelled here rather than borrowed because the ONLY input is a launcher
// command a user wrote in their own config, and a full shell grammar — globs,
// expansions, pipelines — would be a promise this cannot keep.
func shellSplit(value string) ([]string, bool) {
	fields := []string{}
	var current strings.Builder
	quote := rune(0)
	escaped := false
	started := false
	for _, char := range value {
		switch {
		case escaped:
			current.WriteRune(char)
			escaped = false
		case char == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
		case char == '\'' || char == '"':
			quote = char
			started = true
		case char == ' ' || char == '\t' || char == '\n':
			if started || current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(char)
		}
	}
	if escaped {
		// Ruby keeps a dangling escape as a literal backslash rather than
		// refusing.
		current.WriteByte('\\')
		started = true
	}
	if quote != 0 {
		// An unmatched quote is the one thing Ruby refuses outright.
		return nil, false
	}
	if started || current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields, true
}

var _ Opener = SystemOpener{}
