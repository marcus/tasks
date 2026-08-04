package llm

// ClaudeCLI is the local `claude` CLI, run headless with -p.
//
// `claude -p --output-format text` streams the assistant's text the same way
// whether or not a transcript is wanted, so the `stream` hint is a no-op here —
// it only matters for harnesses with distinct one-shot and transcript modes
// (see Hermes).
type ClaudeCLI struct{}

func (ClaudeCLI) DefaultBinary() string { return "claude" }

// Reachable: nothing beyond the PATH probe. An installed `claude` is a usable
// `claude`; it reaches its model over the network on its own.
func (ClaudeCLI) Reachable() bool { return true }

func (ClaudeCLI) Argv(binary, system, prompt, model string, _ bool) []string {
	// --dangerously-skip-permissions: a headless run cannot answer permission
	// prompts, and the whole point is letting the agent edit tasks.jsonl freely.
	argv := []string{binary, "-p", prompt, "--model", model,
		"--output-format", "text", "--dangerously-skip-permissions"}
	if system != "" {
		argv = append(argv, "--append-system-prompt", system)
	}
	return argv
}
