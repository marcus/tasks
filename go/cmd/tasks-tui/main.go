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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"tasks-go/internal/agentcontext"
	"tasks-go/internal/application"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/journal"
	"tasks-go/internal/llm"
	"tasks-go/internal/promptfacts"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
	"tasks-go/internal/tui"
	"tasks-go/internal/tui/term"
	"tasks-go/internal/tui/term/agent"
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
	if err := runProgram(model, program); err != nil {
		fmt.Fprintln(os.Stderr, "tasks-tui: "+err.Error())
		return 1
	}
	// Belt and braces: the quit key saves before returning, but an exit through
	// any other path must still leave the view where the user left it. Saving
	// twice costs one file write; not saving costs the session.
	model.Save()
	return 0
}

type programRunner interface {
	Run() (tea.Model, error)
}

// runProgram is the ensure-style runtime boundary. A terminal failure, signal,
// or non-key exit must not strand a provider process after the UI is gone.
func runProgram(model *tui.Model, program programRunner) error {
	if queue := model.Queue(); queue != nil {
		defer queue.Shutdown()
	}
	_, err := program.Run()
	return err
}

// coalesceScope is the journal's per-process coalescing scope: a random token
// unless the determinism harness pinned one. It is persisted on a keyed tip,
// which is what stops an unrelated process from extending this one's coalesced
// step — and what makes a burst of editor field saves cost exactly one undo.
func coalesceScope(env determinism.Env) string {
	if pinned, ok := determinism.CoalesceScope(env); ok {
		return pinned
	}
	if processScope == "" {
		buffer := make([]byte, 16)
		if _, err := rand.Read(buffer); err != nil {
			return "tasks-tui"
		}
		processScope = hex.EncodeToString(buffer)
	}
	return processScope
}

var processScope string

// buildModel wires the shared application facade onto the resolved store pair.
// The TUI is a CLIENT of the same facade as the CLI and the API — it has no
// privileged path into the store and holds no business logic of its own.
func buildModel(paths config.Paths, env determinism.Env) (*tui.Model, error) {
	writeOptions := store.Options{
		JournalDir: journal.DirFor(paths.Org, env),
		Device:     updatestamp.Device(env),
		MaxDepth:   paths.MaxDepth,
		// The journal coalescing scope. Without it the editor's "one editing
		// session is one undo step" contract silently does not hold: the
		// journal only extends a keyed tip when the SCOPE matches too, and an
		// empty scope is not persisted, so every field save opened its own undo
		// step. The CLI derives the same token the same way.
		CoalesceScope: coalesceScope(env),
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

	// The real styler, not the PlainStyler default. config.Resolve has already
	// applied the theme precedence — TASKS_THEME, then the config file, then
	// NO_COLOR selecting "mono" — so the name and the colour overrides arrive
	// resolved and this layer only has to honour them.
	//
	// This is also what makes the layout correct rather than merely colourful:
	// PlainStyler measures a rune as one cell, so a CJK title or an emoji would
	// misalign every column beside it. Every width decision in internal/tui
	// goes through Styler.Width.
	entries, queue, err := buildAgentQueue(paths, env)
	if err != nil {
		return nil, err
	}

	return tui.New(tui.Options{
		App:     app,
		Paths:   paths,
		Env:     env,
		Session: tui.LoadSession(env),
		Styler:  term.NewStyler(paths.Theme, paths.Colors),
		Entries: entries,
		Queue:   queue,
		// The production link launcher: TASKS_OPENER, then the platform
		// default. Injected rather than reached for, so a test can watch an
		// open happen without a browser appearing.
		Opener: tui.SystemOpener{Env: env},
	}), nil
}

// buildAgentQueue resolves the provider/model list and the request coordinator
// the prompt submits to — the Go shape of Tui::App's `build_agent` and
// `agent_available?`.
//
// The two seams differ in ONE important way, and it is the whole reason they
// are separate:
//
//   - availability is a LIGHTWEIGHT probe run at submit time, and it is
//     deliberately context-free. It never reads agent-memory.md, so a submit
//     cannot fail on a memory error;
//   - the factory builds the system context FRESH when the request actually
//     STARTS. That is what lets an external edit to agent-memory.md — or a
//     default the previous request saved — reach the next queued request
//     without restarting the TUI, and it is what turns an oversize or
//     unreadable sidecar into a failed request rather than a crashed loop.
func buildAgentQueue(paths config.Paths, env determinism.Env) ([]tui.AgentEntry, *agent.Queue, error) {
	conf := llm.LoadConfig(env, "")
	entries := []tui.AgentEntry{}
	for _, entry := range llm.Entries(conf) {
		entries = append(entries, tui.AgentEntry{
			ProviderName: entry.Provider, ModelName: entry.Model, Label: entry.UILabel(),
		})
	}

	// The harness runs in the TASK DATA directory, not in the checkout: that is
	// where the files it is being asked about live.
	dataDir := filepath.Dir(paths.Org)
	cliRoot := repoRoot()

	queue, err := agent.NewQueue(agent.Options{
		Factory:      agentFactory(paths, env, conf, dataDir, cliRoot),
		Availability: agentAvailability(env, conf, dataDir),
	})
	if err != nil {
		return nil, nil, err
	}
	return entries, queue, nil
}

// agentFactory builds the adapter for one request, WITH the system context, at
// the moment the request starts.
func agentFactory(paths config.Paths, env determinism.Env, conf llm.Config,
	dataDir, cliRoot string) func(agent.Entry) (agent.Adapter, error) {

	return func(entry agent.Entry) (agent.Adapter, error) {
		system, err := agentcontext.Build(paths, cliRoot, promptfacts.Sources{})
		if err != nil {
			return nil, err
		}
		built, err := llm.Build(
			llm.Entry{Provider: entry.Provider(), Model: entry.Model()},
			llm.BuildOptions{Root: dataDir, System: system, Path: llm.PathFrom(env)},
			conf)
		if err != nil {
			return nil, err
		}
		return tui.NewAgentAdapter(built), nil
	}
}

// agentAvailability is the submit-time probe. It builds the agent WITHOUT a
// system context on purpose — see buildAgentQueue.
func agentAvailability(env determinism.Env, conf llm.Config, dataDir string) func(agent.Entry) bool {
	return func(entry agent.Entry) bool {
		built, err := llm.Build(
			llm.Entry{Provider: entry.Provider(), Model: entry.Model()},
			llm.BuildOptions{Root: dataDir, Path: llm.PathFrom(env)},
			conf)
		return err == nil && built.Available()
	}
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
