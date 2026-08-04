package llm

// CursorCLI is Cursor's local `agent` CLI, run headlessly in print mode. The CLI
// has no system-prompt flag, so the shared TASK_AGENT.md context is prepended to
// the user's request, as it is for Hermes.
//
// Text output contains the final assistant message rather than structured tool
// progress, so both the TUI and the synchronous CLI use the same invocation.
//
// Verified against Cursor Agent 2026.07.09-a3815c0. This is an external CLI
// contract, not a stable API — re-check `agent --help` when upgrading.
type CursorCLI struct{}

func (CursorCLI) DefaultBinary() string { return "agent" }

func (CursorCLI) Reachable() bool { return true }

func (CursorCLI) Argv(binary, system, prompt, model string, _ bool) []string {
	argv := []string{binary, "-p", "--force", "--output-format", "text"}
	if model != "" {
		argv = append(argv, "--model", model)
	}
	return append(argv, prependSystem(system, prompt))
}

// prependSystem is the injection every harness without a system-prompt flag
// uses: the context, a blank line, then the user's request.
func prependSystem(system, prompt string) string {
	if system == "" {
		return prompt
	}
	return system + "\n\n" + prompt
}
