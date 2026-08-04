package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"tasks-go/internal/determinism"
)

// SystemOpener launches a URL in the user's browser — the Go port of
// Tasks::Opener, which the CLI's `tasks open` and the TUI's `o` share.
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

// Command is the launcher argv, without the URL.
func (o SystemOpener) Command() []string {
	if override := strings.TrimSpace(o.lookup("TASKS_OPENER")); override != "" {
		if fields := shellSplit(override); len(fields) > 0 {
			return fields
		}
	}
	if runtime.GOOS == "darwin" {
		return []string{"open"}
	}
	return []string{"xdg-open"}
}

// Open launches the URL detached and reports whether the launcher could be
// spawned at all.
//
// Output is DISCARDED: a TUI must not have a browser's stderr scribbled over
// it. And any spawn failure — a missing launcher, a non-executable
// TASKS_OPENER — returns false rather than propagating, because a bad opener
// must not take the interface down on a keystroke.
func (o SystemOpener) Open(url string) bool {
	argv := append(o.Command(), url)
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
// backslash escaping the next character.
//
// It is spelled here rather than borrowed because the ONLY input is a launcher
// command a user wrote in their own config, and a full shell grammar — globs,
// expansions, pipelines — would be a promise this cannot keep.
func shellSplit(value string) []string {
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
	if started || current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}

var _ Opener = SystemOpener{}
