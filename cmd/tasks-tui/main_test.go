package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/marcus/tasks/internal/agentcontext"
	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/llm"
	"github.com/marcus/tasks/internal/promptfacts"
	"github.com/marcus/tasks/internal/tui"
	"github.com/marcus/tasks/internal/tui/term/agent"
)

type errorRunner struct{ err error }

func (r errorRunner) Run() (tea.Model, error) { return nil, r.err }

type cleanupAdapter struct{ cancelled bool }

func TestVersionDoesNotRequireATerminalOrTaskConfiguration(t *testing.T) {
	if status := run([]string{"--version"}); status != 0 {
		t.Fatalf("--version status = %d", status)
	}
}

func (*cleanupAdapter) Available() bool                    { return true }
func (*cleanupAdapter) Start(string, string) error         { return nil }
func (*cleanupAdapter) Pump() (bool, error)                { return false, nil }
func (a *cleanupAdapter) Cancel() error                    { a.cancelled = true; return nil }
func (*cleanupAdapter) Output() string                     { return "partial" }
func (*cleanupAdapter) Success() bool                      { return false }
func (*cleanupAdapter) ExitStatus() (int, bool)            { return 0, false }
func (*cleanupAdapter) ProcessStatus() agent.ProcessStatus { return agent.ProcessStatus{} }

func TestRunProgramShutsDownQueueWhenBubbleTeaReturnsAnError(t *testing.T) {
	adapter := &cleanupAdapter{}
	queue, err := agent.NewQueue(agent.Options{
		Factory:      func(agent.Entry) (agent.Adapter, error) { return adapter, nil },
		Availability: func(agent.Entry) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := tui.AgentEntry{ProviderName: "fake", ModelName: "model", Label: "fake:model"}
	if submission := queue.Enqueue("work", entry); !submission.Accepted() {
		t.Fatal(submission.Error)
	}
	if event := queue.StartNext(); event.Type != agent.Started {
		t.Fatalf("start = %+v", event)
	}
	model := tui.New(tui.Options{Queue: queue})
	want := errors.New("terminal failed")
	if got := runProgram(model, errorRunner{err: want}); !errors.Is(got, want) {
		t.Fatalf("runProgram error = %v", got)
	}
	if !adapter.cancelled || queue.Work() {
		t.Fatalf("cleanup cancelled=%v work=%v", adapter.cancelled, queue.Work())
	}
}

// These test the SHIPPING constructor.
//
// They exist because a model built with fakes proved nothing about the real
// binary: the first version of this entry point never passed Entries, Queue or
// Opener, so every prompt key answered "no agent is configured" and every `o`
// answered "no browser launcher found" while the fake-injected model tests
// stayed green. A production wiring defect has to be caught HERE.
//
// Nothing below invokes a provider or opens anything: the queue's factory and
// availability probe are called directly with a PATH that contains no launcher,
// which exercises the wiring without running a process.

// sandbox is a temp task directory with a resolved config, and never the real
// task store.
func sandbox(t *testing.T) (config.Paths, determinism.Env) {
	t.Helper()
	dir := t.TempDir()
	org := filepath.Join(dir, "tasks.jsonl")
	if err := os.WriteFile(org, []byte(`{"type":"meta","version":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := determinism.Env{
		"TASKS_FILE":      org,
		"TASKS_ARCHIVE":   filepath.Join(dir, "archive.jsonl"),
		"XDG_STATE_HOME":  filepath.Join(dir, "state"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"HOME":            dir,
		// An empty PATH: every availability probe answers "not installed", so
		// no provider binary can be found and none can be run.
		"PATH": filepath.Join(dir, "no-such-bin"),
	}
	return config.Resolve(dir, env, func() string { return "fixture" }), env
}

func TestBuildModelWiresRealOrderedEntriesAndAQueue(t *testing.T) {
	paths, env := sandbox(t)
	model, err := buildModel(paths, env)
	if err != nil {
		t.Fatal(err)
	}
	if model.Queue() == nil {
		t.Fatal("the shipping model has no request queue; every prompt would refuse")
	}
	entry := model.CurrentEntry()
	if entry.UILabel() == "" || entry.Provider() == "" || entry.Model() == "" {
		t.Fatalf("the shipping model has no agent entry: %+v", entry)
	}
	// The list is ORDERED with the resolved default first, and cycling has
	// somewhere to go.
	before := model.CurrentEntry().UILabel()
	model.ToggleModel()
	if model.CurrentEntry().UILabel() == before {
		t.Errorf("model cycling has only one entry (%q); the registry order was lost", before)
	}
	if strings.Contains(model.FlashMessage(), "no agent is configured") {
		t.Errorf("cycling refused in the shipping build: %q", model.FlashMessage())
	}
}

func TestBuildModelWiresTheLinkOpener(t *testing.T) {
	paths, env := sandbox(t)
	model, err := buildModel(paths, env)
	if err != nil {
		t.Fatal(err)
	}
	if model.LinkOpener() == nil {
		t.Fatal("the shipping model has no opener; every valid link would refuse")
	}
}

// The availability probe is deliberately context-free: it must not read
// agent-memory.md, so a submit cannot fail on a memory error.
func TestAvailabilityProbeIgnoresABrokenMemorySidecar(t *testing.T) {
	paths, env := sandbox(t)
	writeBrokenMemory(t, paths.Memory)

	entries, queue, err := buildAgentQueue(paths, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || queue == nil {
		t.Fatal("the queue was not built")
	}
	// The probe runs and answers — false, because PATH holds no launcher — but
	// it must ANSWER rather than fail on the sidecar.
	submission := queue.Enqueue("hello", entries[0])
	if submission.Accepted() {
		t.Fatal("a provider with no binary on PATH was accepted")
	}
	if strings.Contains(strings.ToLower(submission.Error), "memory") {
		t.Errorf("the availability probe read the memory sidecar: %q", submission.Error)
	}
}

// The factory DOES read it, and at START time — that is what lets an external
// edit reach the next queued request, and what turns a broken sidecar into a
// failed request rather than a crashed event loop.
func TestFactoryReadsTheMemorySidecarAtStartAndFailsTheRequest(t *testing.T) {
	paths, env := sandbox(t)
	writeBrokenMemory(t, paths.Memory)

	entries, queue, err := buildAgentQueue(paths, env)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass the availability probe so the FACTORY is what is under test: an
	// always-available queue over the same factory the binary installs.
	conf := llm.LoadConfig(env, "")
	direct, err := agent.NewQueue(agent.Options{
		Factory: agentFactory(paths, env, conf,
			filepath.Dir(paths.Org), filepath.Join(t.TempDir(), "tasks")),
		Availability: func(agent.Entry) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	submission := direct.Enqueue("hello", entries[0])
	if !submission.Accepted() {
		t.Fatalf("the request was refused before the factory ran: %q", submission.Error)
	}
	event := direct.StartNext()
	if !event.Occurred() {
		t.Fatal("starting produced no event")
	}
	if event.Request.Status != agent.Failed {
		t.Fatalf("a broken memory sidecar produced %q, want a failed request",
			event.Request.Status)
	}
	if !strings.Contains(event.Request.Error, paths.Memory) {
		t.Errorf("the failure does not name the sidecar: %q", event.Request.Error)
	}
	_ = queue
}

// A HEALTHY sidecar reaches the context the factory builds — the property that
// makes an external edit visible to the next queued request.
func TestFactoryPicksUpAnEditedMemorySidecar(t *testing.T) {
	paths, env := sandbox(t)
	if err := os.WriteFile(paths.Memory, []byte("remember: prefer mornings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := buildAgentQueue(paths, env)
	if err != nil {
		t.Fatal(err)
	}
	cliPath := filepath.Join(t.TempDir(), "tasks")
	system, err := agentcontext.Build(paths, cliPath, promptfacts.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "prefer mornings") {
		t.Errorf("the built context does not carry the sidecar:\n%s", system)
	}

	if err := os.WriteFile(paths.Memory, []byte("remember: prefer evenings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := agentcontext.Build(paths, cliPath, promptfacts.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "prefer evenings") {
		t.Errorf("a later build did not see the edit:\n%s", again)
	}
}

func writeBrokenMemory(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Invalid UTF-8: MemorySection refuses it with the path, which is exactly
	// the error the factory has to turn into a failed request.
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 0xfd}, 0o644); err != nil {
		t.Fatal(err)
	}
}
