// Command tasks-tui is the Go port of bin/tasks-tui: the interactive terminal
// surface over tasks.jsonl.
//
// The Ruby entry point refuses to start without a TTY on both ends, and so does
// this one — for the same reason, which is not politeness: the TUI takes over
// the alternate screen and reads raw keys, and doing that to a pipe produces
// escape bytes in someone's log file and a process that cannot be quit.
//
// Everything above the entry point lives in internal/tui. This file resolves
// configuration, builds the shared application facade, and hands off.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"tasks-go/internal/application"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/journal"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
	"tasks-go/internal/tui"
	"tasks-go/internal/updatestamp"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	flags := flag.NewFlagSet("tasks-tui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tasks-tui")
		flags.PrintDefaults()
	}
	if err := flags.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tasks-tui does not accept positional arguments")
		return 1
	}
	if !isatty.IsTerminal(os.Stdout.Fd()) || !isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(os.Stderr, "tasks-tui needs an interactive terminal")
		return 1
	}

	env := determinism.OSEnv()
	paths := config.Resolve(repoRoot(), env, nil)
	for _, warning := range paths.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}

	model, err := buildModel(paths, env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks-tui: "+err.Error())
		return 1
	}

	options := []tea.ProgramOption{tea.WithAltScreen()}
	if model.MouseEnabled() {
		// Only when the config asks for it. Enabling mouse reporting takes the
		// terminal's own text selection away from the user, and a user who
		// turned the mouse off did so to get it back.
		options = append(options, tea.WithMouseCellMotion())
	}
	program := tea.NewProgram(model, options...)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tasks-tui: "+err.Error())
		return 1
	}
	// Belt and braces: the quit key saves before returning, but an exit through
	// any other path must still leave the view where the user left it. Saving
	// twice costs one file write; not saving costs the session.
	model.Save()
	return 0
}

// buildModel wires the shared application facade onto the resolved store pair.
// The TUI is a CLIENT of the same facade as the CLI and the API — it has no
// privileged path into the store and holds no business logic of its own.
func buildModel(paths config.Paths, env determinism.Env) (*tui.Model, error) {
	writeOptions := store.Options{
		JournalDir: journal.DirFor(paths.Org, env),
		Device:     updatestamp.Device(env),
		MaxDepth:   paths.MaxDepth,
	}
	if clock := determinism.Clock(env); clock != nil {
		writeOptions.Now = clock
	}
	if sequence, err := determinism.SharedIDSource(env); err == nil && sequence != nil {
		writeOptions.IDSource = sequence.Call
	}

	temporalContext := func() temporal.Context {
		built, err := temporal.NewContext(time.Now().UTC(), paths.Timezone, paths.TimeFormat)
		if err != nil {
			return temporal.Context{Now: time.Now().UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
		}
		return built
	}

	app, err := application.New(application.Options{
		Factory: func() application.Store {
			options := writeOptions
			return store.NewWriter(paths.Org, paths.Archive, options)
		},
		TemporalContext: temporalContext,
		HostContext:     paths.HostContext,
		QueryOptions:    []taskquery.Option{taskquery.WithLinkConfig(paths.Links, paths.LinkSystems)},
	})
	if err != nil {
		return nil, err
	}

	return tui.New(tui.Options{
		App:     app,
		Paths:   paths,
		Env:     env,
		Session: tui.LoadSession(env),
	}), nil
}

// repoRoot is bin/tasks-tui's ROOT: the repository the binary was built into,
// used only as the last fallback for where the task files live.
func repoRoot() string {
	executable, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(executable)))
}
