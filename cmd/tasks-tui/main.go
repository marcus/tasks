// Command tasks-tui is the interactive terminal surface over tasks.jsonl.
//
// It refuses to start without a TTY on both ends because the TUI takes over
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

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-isatty"

	"github.com/marcus/tasks/internal/buildinfo"
	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/llm"
	"github.com/marcus/tasks/internal/tui"
	"github.com/marcus/tasks/internal/tui/term/agent"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	flags := flag.NewFlagSet("tasks-tui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	version := flags.Bool("version", false, "Print version and exit")
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
	if *version {
		fmt.Println(buildinfo.String("tasks-tui"))
		return 0
	}
	if !isatty.IsTerminal(os.Stdout.Fd()) || !isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(os.Stderr, "tasks-tui needs an interactive terminal")
		return 1
	}

	env := determinism.OSEnv()
	paths := config.Resolve("", env, nil)
	if !paths.Configured {
		fmt.Fprintln(os.Stderr, config.ConfigurationRequiredMessage(paths))
		return 1
	}
	for _, warning := range paths.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}

	model, err := buildModel(paths, env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks-tui: "+err.Error())
		return 1
	}

	// Alt screen and mouse mode are set on View each frame (Bubble Tea v2),
	// not as program options. MouseEnabled still gates View.MouseMode.
	program := tea.NewProgram(model)
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

// buildModel wires the shared application facade onto the resolved store pair.
// The TUI is a CLIENT of the same facade as the CLI and the API — it has no
// privileged path into the store and holds no business logic of its own.
func buildModel(paths config.Paths, env determinism.Env) (*tui.Model, error) {
	return tui.NewRuntime(tui.RuntimeOptions{
		Paths: paths, Env: env, Session: tui.LoadSession(env),
	})
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
	return tui.BuildAgentQueue(paths, env)
}

// agentFactory builds the adapter for one request, WITH the system context, at
// the moment the request starts.
func agentFactory(paths config.Paths, env determinism.Env, conf llm.Config,
	dataDir, cliPath string) func(agent.Entry) (agent.Adapter, error) {

	return tui.AgentFactory(paths, env, conf, dataDir, cliPath)
}

// agentAvailability is the submit-time probe. It builds the agent WITHOUT a
// system context on purpose — see buildAgentQueue.
func agentAvailability(env determinism.Env, conf llm.Config, dataDir string) func(agent.Entry) bool {
	return tui.AgentAvailability(env, conf, dataDir)
}
