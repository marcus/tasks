package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tasks-go/internal/agentcontext"
	"tasks-go/internal/agentdiff"
	"tasks-go/internal/llm"
	"tasks-go/internal/promptfacts"
)

// `tasks -p` hands a natural-language request to an LLM agent with TASK_AGENT.md
// as context. It runs the configured harness headless, auto-applies, and then
// shows the file diff. `--provider`/`--model` (leading flags only) override the
// config default for one run.
//
// It is the one command with no structured result: the answer is a harness's
// free-form transcript, not a value this CLI computes.

const promptUsage = `usage: tasks -p [--provider NAME] [--model NAME] "do something with my tasks"`

// rejectJSONMessage says so instead of folding `--json` into the prompt text and
// exiting 0 — a caller that scripted it would get an LLM transcript where it
// expected a document. Only the LEADING position is rejected: "explain --json to
// me" is a legitimate prompt.
const rejectJSONMessage = "-p has no --json: its result is the agent's transcript, not a value this CLI computes. " +
	"Read the mutations back with a command that does emit JSON (see `tasks help --json`)."

func (s *surfaceContext) prompt(args []string) int {
	// The schema gate first, in the position bin/tasks:3582 applies it — at
	// dispatch, BEFORE the handler runs, so a store this build cannot read is
	// refused ahead of the --json refusal and ahead of the usage line.
	//
	// nil args, not `args`: the registry entry for `-p` promises no JSON result,
	// so `enforce_schema_gate!` passes `json: false` however the caller spelled
	// the invocation. A command that never emits a document must not start
	// emitting error objects on this one path.
	if status := s.refuseUnsupportedSchema(nil, "-p"); status != 0 {
		return status
	}

	// Kept ahead of everything cmd_prompt does so the refusal costs nothing: it
	// answers before the provider registry and the agent context it would
	// otherwise never use are built.
	if _, _, words := extractLLMFlags(args); len(words) > 0 && words[0] == "--json" {
		return abort(rejectJSONMessage)
	}

	provider, model, words := extractLLMFlags(args)
	request := strings.Join(words, " ")
	if rubyStrip(request) == "" {
		return abort(promptUsage)
	}

	dataDir := filepath.Dir(s.paths.Org)

	// Build the system context (TASK_AGENT.md + file locations + task-set
	// memory) BEFORE spawning: an oversize or unreadable agent-memory.md must
	// abort with the path here, not run the agent without the user's saved
	// defaults.
	system, err := agentcontext.Build(s.paths, repoRoot(), promptfacts.Sources{})
	if err != nil {
		return abort(err.Error())
	}

	conf := llm.LoadConfig(env, "")
	entry, err := llm.DefaultEntry(provider, model, conf)
	if err != nil {
		return abort(err.Error())
	}
	agent, err := llm.Build(entry, llm.BuildOptions{
		Root:   dataDir,
		System: system,
		Path:   llm.PathFrom(env),
	}, conf)
	if err != nil {
		return abort(err.Error())
	}

	if !agent.Available() {
		return abort(fmt.Sprintf("agent '%s' not available — "+
			"check the CLI is installed and any local model server is running", entry.Provider))
	}

	if !agent.RunSync(rubyStrip(request), entry.Model, false) {
		fmt.Fprintln(os.Stderr, "\n(agent exited non-zero)")
	}
	s.showAgentDiff(dataDir)

	// Zero even when the agent exited non-zero. `tasks -p` reports whether IT
	// could run the harness; what the harness concluded is the transcript's to
	// say, and the notice above already said it out loud.
	return 0
}

// extractLLMFlags peels leading `--provider NAME` / `--model NAME` flags off the
// -p arguments so the rest is the prompt VERBATIM. It only strips while they
// lead, so a prompt that happens to mention "--model" mid-sentence is left
// untouched — and a trailing `--provider` with no value consumes the flag and
// leaves the name unset, exactly as Array#shift on an empty array does.
func extractLLMFlags(args []string) (provider, model string, words []string) {
	rest := args
	for len(rest) > 0 {
		switch rest[0] {
		case "--provider":
			rest = rest[1:]
			if len(rest) > 0 {
				provider, rest = rest[0], rest[1:]
			}
		case "--model":
			rest = rest[1:]
			if len(rest) > 0 {
				model, rest = rest[0], rest[1:]
			}
		default:
			return provider, model, rest
		}
	}
	return provider, model, rest
}

// showAgentDiff shows what the agent changed — the task files plus the memory
// sidecar when it lives in the same repo. Only possible when the task files sit
// in a git repo; a memory sidecar relocated outside that repo cannot be diffed
// here, so it gets a one-line notice instead.
//
// The decision lives in internal/agentdiff so it can be tested against a real
// sandbox repo; this stays thin human formatting.
func (s *surfaceContext) showAgentDiff(dataDir string) {
	result, ok := agentdiff.Compute(agentdiff.Request{
		DataDir: dataDir,
		Org:     s.paths.Org,
		Archive: s.paths.Archive,
		Memory:  s.paths.Memory,
		Color:   stdoutIsTTY,
		Stderr:  os.Stderr,
	})
	if !ok {
		return
	}

	if rubyStrip(result.Diff) != "" {
		out("\n" + bold("Changes to task files:"))
		fmt.Print(result.Diff)
		if !strings.HasSuffix(result.Diff, "\n") {
			fmt.Println()
		}
	}
	if result.Notice != "" {
		out(dim("Note: task-set memory at " + result.Notice + " is outside the task-data " +
			"repo — review its changes there separately."))
	}
}

// rubyStrip is String#strip: ASCII whitespace and NUL, and nothing else.
// strings.TrimSpace would also take U+00A0 and the rest of the Unicode spaces,
// so a prompt made only of them would be a usage error here and a real request
// to the oracle.
func rubyStrip(text string) string {
	return strings.Trim(text, " \t\n\v\f\r\x00")
}

func init() {
	register("-p", (*surfaceContext).prompt)
}
