package tui

import (
	"testing"
	"time"

	"tasks-go/internal/llm"
)

// The adapter's job is to report the queue's vocabulary, not llm.Agent's.
//
// The two disagree in one place that matters: llm.Agent.ExitStatus() answers -1
// for BOTH "has not exited" and "died on a signal", while the queue expresses
// "no exit code" as a false second return, which it turns into an absent status
// on the request snapshot. Reporting (-1, true) would put an exit code no
// process ever returns onto a cancelled request — the path the TUI reaches
// every time a user presses escape on a running agent.
//
// No process is started here: an agent that was never started has exactly the
// state (no wait status) that a cancelled one has for this purpose.

func neverStarted(t *testing.T) *AgentAdapter {
	t.Helper()
	return NewAgentAdapter(llm.New(llm.ClaudeCLI{}, llm.Options{Root: t.TempDir()}))
}

func TestAdapterReportsNoExitCodeRatherThanMinusOne(t *testing.T) {
	adapter := neverStarted(t)
	code, ok := adapter.ExitStatus()
	if ok {
		t.Errorf("a process with no wait status reported exit code %d; the queue "+
			"would put that on the request snapshot", code)
	}
	if code != 0 {
		t.Errorf("the unknown code leaked as %d", code)
	}
}

func TestAdapterReportsNoProcessStatusWhenNoneExists(t *testing.T) {
	status := neverStarted(t).ProcessStatus()
	if status.Present {
		t.Errorf("a process that never ran reported %+v; the queue would render "+
			"that as \"agent exited %d\"", status, status.ExitStatus)
	}
}

// Pump must never block: it is called from the TUI's tick, and a blocking read
// would freeze the whole interface for as long as the model was thinking.
//
// A never-started agent's Done channel is nil, which is the WORST case for this
// property — a bare receive on a nil channel blocks forever. Pump selects with
// a default, so it returns immediately regardless.
func TestAdapterPumpNeverBlocks(t *testing.T) {
	adapter := neverStarted(t)
	returned := make(chan bool, 1)
	go func() {
		finished, err := adapter.Pump()
		if err != nil {
			t.Errorf("pump errored: %v", err)
		}
		returned <- finished
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Pump blocked; the interface would freeze on every tick")
	}
}
