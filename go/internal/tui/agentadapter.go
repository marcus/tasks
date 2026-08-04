package tui

import (
	"tasks-go/internal/llm"
	"tasks-go/internal/tui/term/agent"
)

// AgentAdapter bridges one *llm.Agent to the queue's Adapter interface.
//
// The two shapes differ in exactly one way that matters: llm.Agent is a
// long-running process with a Done channel, and the queue is a POLLED thing
// driven from the TUI's tick. Pump therefore must never block — a blocking read
// here would freeze the whole interface for as long as the model was thinking.
// So Pump asks the Done channel whether the process has finished and returns
// immediately either way; the transcript is read from the agent's own buffer,
// which its drain goroutine keeps filled.
//
// It lives in internal/tui rather than in cmd/tasks-tui so it can be tested
// without a terminal, and it is deliberately thin: every decision about the
// process — argv, streaming, cancellation grace, signal handling — stays in
// internal/llm, which the CLI shares.
type AgentAdapter struct{ agent *llm.Agent }

// NewAgentAdapter wraps a built agent.
func NewAgentAdapter(built *llm.Agent) *AgentAdapter { return &AgentAdapter{agent: built} }

// Available re-checks the provider at start time.
func (a *AgentAdapter) Available() bool { return a.agent.Available() }

// Start launches the process with streaming ON, so the activity modal and the
// footer can show the transcript as it arrives rather than only at the end.
func (a *AgentAdapter) Start(prompt, model string) error {
	return a.agent.Start(prompt, model, true)
}

// Pump reports whether the process has finished, without blocking.
func (a *AgentAdapter) Pump() (bool, error) {
	select {
	case <-a.agent.Done():
		return true, nil
	default:
		return false, nil
	}
}

// Cancel signals the process group and waits out the short grace period the
// llm layer owns.
func (a *AgentAdapter) Cancel() error {
	a.agent.Cancel()
	return nil
}

// Output is the transcript captured so far.
func (a *AgentAdapter) Output() string { return a.agent.Output() }

// Success reports a clean exit that was not cancelled.
func (a *AgentAdapter) Success() bool { return a.agent.Success() }

// ExitStatus is the process exit code once it is known.
//
// The second return is the whole contract: the queue turns `false` into an
// ABSENT exit status on the request snapshot, and llm.Agent reports -1 for both
// "has not exited" and "died on a signal". Reporting (-1, true) would put an
// exit code no process ever returns onto a cancelled request — which is exactly
// the path the TUI reaches every time a user presses escape on a running agent.
func (a *AgentAdapter) ExitStatus() (int, bool) {
	if a.agent.Running() {
		return 0, false
	}
	code := a.agent.ExitStatus()
	if code < 0 {
		return 0, false
	}
	return code, true
}

// ProcessStatus is how the process ended, including the signal a cancellation
// delivered — which is what tells "the user cancelled it" apart from "it
// crashed".
func (a *AgentAdapter) ProcessStatus() agent.ProcessStatus {
	if a.agent.Running() {
		return agent.ProcessStatus{}
	}
	signaled, signal := a.agent.Signaled()
	if signaled {
		return agent.ProcessStatus{Present: true, Signaled: true, Signal: int(signal)}
	}
	code := a.agent.ExitStatus()
	if code < 0 {
		// No wait status at all — the process never ran. Claiming Present here
		// would make the queue report "agent exited -1" for a request that
		// failed before it started.
		return agent.ProcessStatus{}
	}
	return agent.ProcessStatus{Present: true, Exited: true, ExitStatus: code}
}

var _ agent.Adapter = (*AgentAdapter)(nil)
