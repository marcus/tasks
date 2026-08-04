// Package llm is the agent adapter layer: one execution contract, a
// config-assembled registry, and the built-in harnesses. It is the Go
// counterpart of lib/llm/.
//
// An "agent" is an autonomous harness: we hand it a prompt, a system-context
// string, and a working directory, and it acts on its own — reads tasks.jsonl,
// runs `bin/tasks`, edits files. Our code never parses its output for meaning;
// it streams a transcript to the user and reloads the store when the file
// changes on disk. There is deliberately no separate "completion" protocol — we
// reach local models by putting a harness in front of them, never by calling a
// bare model and coercing its text ourselves.
//
// Backends vary along exactly two axes, and Agent hides both:
//
//   - transport: how the harness is driven. Every adapter here spawns a CLI and
//     reuses this machinery; an SDK-transport adapter would supply a different
//     Harness and keep the same Agent surface.
//   - model: which model the harness runs, passed through to Command.
//
// Two entry points share one Command, so there is one source of truth for how a
// backend is invoked:
//
//   - sync (CLI): RunSync spawns with inherited stdio so output streams straight
//     to the terminal, and reports whether it exited cleanly.
//   - async (TUI): Start, then read Output as it fills and wait on Done; Cancel
//     stops the whole process group.
package llm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Cancellation grace windows. Some harnesses (or the tool subprocesses they
// spawn) trap TERM, so cancellation must not block a raw-terminal UI
// indefinitely: the group gets a short graceful window, then KILL, then the
// child is detached rather than held onto.
const (
	CancelTermGrace = 150 * time.Millisecond
	CancelKillGrace = 150 * time.Millisecond
)

// Harness is the per-backend half of the contract, and the only thing an
// adapter implements. Everything else — spawning, availability, cancellation,
// output capture — is Agent's and is identical for every provider, which is what
// keeps provider-specific branching out of every call site.
type Harness interface {
	// DefaultBinary is the executable this adapter spawns unless config points
	// at another install.
	DefaultBinary() string
	// Argv builds the command line for one run. `system` is the fully resolved
	// system context, or "" when there is none; adapters inject it however their
	// CLI allows. `stream` true means "emit a live transcript including tool
	// activity" (the TUI); false means "final answer only" (the sync CLI).
	Argv(binary, system, prompt, model string, stream bool) []string
	// Reachable is any probe BEYOND "the binary is on PATH" — Hermes pings its
	// model endpoint. An adapter with nothing further to check returns true.
	// Never returns an error: a dead backend is unavailable, not a crash.
	Reachable() bool
}

// Options are what constructing an adapter needs. The model is not here: it
// rides along at call time, because one constructed agent can serve several
// models.
type Options struct {
	// Root is the working directory the harness runs in — where tasks.jsonl
	// lives, not where this binary lives.
	Root string
	// System is the resolved system-context string (TASK_AGENT.md + the file
	// locations + task-set memory), or "" for none.
	System string
	// Binary overrides the harness's default executable.
	Binary string
	// Path is the PATH the availability probe searches. It is passed in rather
	// than read from the process, because this layer must honour whatever
	// environment the adapter boundary resolved. Empty means "nothing is on
	// PATH", which is Ruby's ENV.fetch("PATH", "") exactly.
	Path string

	// OllamaURL and InferenceProvider are Hermes' settings; other adapters
	// ignore them. InferenceProvider is a pointer so that "set to empty" (drop
	// the --provider flag) is expressible and distinct from "unset" (use the
	// conventional local-Ollama provider name).
	OllamaURL         string
	InferenceProvider *string
}

// Agent is one constructed backend, ready to RunSync or Start.
type Agent struct {
	harness Harness
	root    string
	system  string
	binary  string
	path    string

	// mu guards everything below it. Output is read by a UI while the reader
	// goroutine appends to it, so the buffer is not merely convention-protected.
	mu        sync.Mutex
	output    []byte
	cmd       *exec.Cmd
	running   bool
	cancelled bool
	state     *os.ProcessState
	done      chan struct{}
}

// New constructs an agent over a harness. An empty Binary takes the harness's
// default, and an empty System means no system context at all — the same
// `system.to_s.empty? ? nil : system` normalisation Ruby applies, so a caller
// cannot accidentally inject a blank block.
func New(harness Harness, options Options) *Agent {
	binary := options.Binary
	if binary == "" {
		binary = harness.DefaultBinary()
	}
	closed := make(chan struct{})
	close(closed)
	return &Agent{
		harness: harness,
		root:    options.Root,
		system:  options.System,
		binary:  binary,
		path:    options.Path,
		done:    closed,
	}
}

// Binary is the executable this agent spawns.
func (a *Agent) Binary() string { return a.binary }

// System is the resolved system context, or "".
func (a *Agent) System() string { return a.system }

// Command is the argv for a run. Both entry points funnel through it.
func (a *Agent) Command(prompt, model string, stream bool) []string {
	return a.harness.Argv(a.binary, a.system, prompt, model, stream)
}

// Available answers whether the backend is usable right now: the binary is
// reachable, and the harness's own probe passes. It never returns an error — a
// dead backend is a false, so a UI can flash rather than crash.
func (a *Agent) Available() bool {
	return a.binaryOnPath() && a.harness.Reachable()
}

// binaryOnPath is Agent#command_on_path?: an explicit path is checked directly,
// and a bare name is searched along PATH without spawning a shell.
func (a *Agent) binaryOnPath() bool {
	if strings.Contains(a.binary, "/") {
		return executable(a.binary)
	}
	for _, dir := range strings.Split(a.path, string(os.PathListSeparator)) {
		if executable(filepath.Join(dir, a.binary)) {
			return true
		}
	}
	return false
}

// executable is File.executable?: a regular file (or a symlink to one) carrying
// an execute bit. A directory named like the binary is not executable, which is
// the one case a naive mode check gets wrong.
func executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// RunSync is the blocking run for the CLI: inherit stdio so the harness streams
// to the terminal live, and report whether it exited cleanly. It defaults to the
// non-streaming (final-answer) shape at the call site, since there is no UI to
// animate.
//
// The boolean is Kernel#system's: true on a clean exit, false on any nonzero
// status AND on a spawn that never happened. Both mean "the agent did not
// complete", which is the only distinction the caller acts on.
func (a *Agent) RunSync(prompt, model string, stream bool) bool {
	argv := a.Command(prompt, model, stream)
	if len(argv) == 0 {
		return false
	}
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = a.root
	command.Env = childEnviron()
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run() == nil
}

// childEnviron is the environment a harness inherits: this process's, unchanged.
//
// It is set EXPLICITLY rather than left nil, and that is the whole point. With a
// nil Env and a non-empty Dir, os/exec appends `PWD=<Dir>` — a variable Ruby's
// `system(*argv, chdir:)` never sets. A differential run caught it: the same
// recorder script printed `/var/folders/…` under Go and `/private/var/folders/…`
// under Ruby, because sh's `pwd` prefers an exported PWD over the physical path.
// The directory was the same one either way, but the harness's environment was
// not, and a harness is an external contract we do not get to quietly amend.
func childEnviron() []string { return os.Environ() }

// Start spawns the harness asynchronously, capturing its combined output. The
// child is put in its own process group so Cancel can signal the whole tree:
// these harnesses spawn tool subprocesses that would otherwise be orphaned when
// only the leader is killed.
//
// This is the async surface Ruby's Agent#start/#pump/#cancel provides, in the
// shape Go gives it: a goroutine drains the pipe into Output and closes Done,
// rather than a caller multiplexing with IO.select. The observable contract —
// output captured, exit status recorded, cancellation distinct from a nonzero
// exit, a fresh run resetting the metadata — is the same.
func (a *Agent) Start(prompt, model string, stream bool) error {
	argv := a.Command(prompt, model, stream)
	if len(argv) == 0 {
		return errors.New("llm: adapter produced an empty command")
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = a.root
	command.Env = childEnviron()
	command.Stdin = nil // File::NULL: a headless harness must never block on input.
	command.Stdout = writer
	command.Stderr = writer
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := command.Start(); err != nil {
		reader.Close()
		writer.Close()
		return err
	}
	// Our copy of the write end must go once the child holds its own, or the
	// reader never sees EOF and Done never closes.
	writer.Close()

	a.output = nil
	a.state = nil
	a.cancelled = false
	a.running = true
	a.cmd = command
	done := make(chan struct{})
	a.done = done

	go a.drain(command, reader, done)
	return nil
}

// drain copies the child's output until EOF, reaps it, and closes done.
func (a *Agent) drain(command *exec.Cmd, reader *os.File, done chan struct{}) {
	buffer := make([]byte, 65536)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			a.mu.Lock()
			a.output = append(a.output, buffer[:n]...)
			a.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	reader.Close()
	waitErr := command.Wait()

	a.mu.Lock()
	if command.ProcessState != nil {
		a.state = command.ProcessState
	}
	a.running = false
	a.cmd = nil
	a.mu.Unlock()
	_ = waitErr
	close(done)
}

// Done closes when the run has exited and its output has been drained. A fresh
// Agent's channel is already closed, so a caller that selects on it before any
// run does not block forever.
func (a *Agent) Done() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.done
}

// Output is the transcript captured so far. It is a snapshot: safe to call while
// the run is still producing.
func (a *Agent) Output() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.output)
}

func (a *Agent) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// Cancelled reports whether this run was stopped by Cancel. It is deliberately
// distinct from Success: a cancelled run and a run that failed on its own both
// end nonzero, and only the flag tells a UI which sentence to show.
func (a *Agent) Cancelled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

// ExitStatus is the child's exit code, or -1 when it has not exited or died on a
// signal.
func (a *Agent) ExitStatus() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == nil {
		return -1
	}
	return a.state.ExitCode()
}

func (a *Agent) Success() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state != nil && a.state.Success()
}

// Signaled reports whether the child died on a signal, and which one. This is
// how a caller distinguishes "the harness refused to stop and we killed it"
// from "the harness exited".
func (a *Agent) Signaled() (bool, syscall.Signal) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == nil {
		return false, 0
	}
	status, ok := a.state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false, 0
	}
	return true, status.Signal()
}

// Cancel stops a running agent. The signal goes to the whole process group
// started in Start — negative pid — so a harness's tool subprocesses go with it.
//
// TERM first, then KILL after a short window, and if even that is not reaped in
// time the child is left to the drain goroutine rather than blocking the caller.
// A UI must never be held hostage by a harness that traps signals.
func (a *Agent) Cancel() {
	a.mu.Lock()
	a.cancelled = true
	command, done := a.cmd, a.done
	a.mu.Unlock()

	if command == nil || command.Process == nil {
		return
	}
	signalGroup(command.Process.Pid, syscall.SIGTERM)
	if waitFor(done, CancelTermGrace) {
		return
	}
	signalGroup(command.Process.Pid, syscall.SIGKILL)
	waitFor(done, CancelKillGrace)
}

func signalGroup(pid int, signal syscall.Signal) {
	// A negative pid addresses the group. ESRCH means it is already gone, which
	// is the outcome we wanted.
	_ = syscall.Kill(-pid, signal)
}

func waitFor(done <-chan struct{}, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
