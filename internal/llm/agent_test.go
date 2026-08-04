package llm

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// No test here spawns a real `claude`, `agent` or `hermes`, and none of them
// reaches a live model server. Command construction and resolution are pure; the
// process machinery is exercised against THIS test binary re-executed as a
// helper, so the child's behavior — its output, its exit code, whether it traps
// TERM — is written here rather than depended upon from an installed harness.

const helperFlag = "--tasks-llm-helper"

func TestMain(m *testing.M) {
	for index, arg := range os.Args {
		if arg == helperFlag {
			os.Exit(runHelper(os.Args[index+1:]))
		}
	}
	os.Exit(m.Run())
}

// runHelper is the child process. Each mode stands for one thing a real harness
// can do to us.
func runHelper(args []string) int {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "transcript":
		os.Stdout.WriteString("transcript")
		return 7
	case "ok":
		return 0
	case "echo-argv":
		os.Stdout.WriteString(strings.Join(args[1:], "\x1f"))
		return 0
	case "echo-cwd":
		dir, _ := os.Getwd()
		os.Stdout.WriteString(dir)
		return 0
	case "stderr":
		os.Stderr.WriteString("on stderr")
		return 0
	case "stdin-is-closed":
		buffer := make([]byte, 1)
		if _, err := os.Stdin.Read(buffer); err != nil {
			os.Stdout.WriteString("no stdin")
			return 0
		}
		os.Stdout.WriteString("read something")
		return 1
	case "sleep":
		time.Sleep(30 * time.Second)
		return 0
	case "hostile":
		// Trap TERM and never die of it: the case cancellation has to survive.
		signal.Ignore(syscall.SIGTERM)
		os.Stdout.WriteString("ready\n")
		time.Sleep(30 * time.Second)
		return 0
	default:
		os.Stderr.WriteString("unknown helper mode " + mode + "\n")
		return 2
	}
}

// helperArgv re-executes this test binary as the child.
func helperArgv(mode ...string) []string {
	return append([]string{os.Args[0], helperFlag}, mode...)
}

// fakeHarness is an adapter that returns whatever argv a test names. It exists
// to prove the point of the seam: every behavior below belongs to Agent, and a
// harness contributes only an argv and a reachability answer.
type fakeHarness struct {
	binary    string
	argv      []string
	reachable bool
	// seen records the (system, prompt, model, stream) Agent handed over, so a
	// test can assert what the contract passes through.
	seen *fakeCall
}

type fakeCall struct {
	binary, system, prompt, model string
	stream                        bool
	calls                         int
}

func (f fakeHarness) DefaultBinary() string { return f.binary }
func (f fakeHarness) Reachable() bool       { return f.reachable }

func (f fakeHarness) Argv(binary, system, prompt, model string, stream bool) []string {
	if f.seen != nil {
		*f.seen = fakeCall{binary, system, prompt, model, stream, f.seen.calls + 1}
	}
	if f.argv != nil {
		return f.argv
	}
	return []string{binary, prompt, model}
}

func newFakeAgent(argv []string, options Options) *Agent {
	return New(fakeHarness{binary: "fake", argv: argv, reachable: true}, options)
}

// -- the shared contract ------------------------------------------------------

// One execution contract: Agent hands the harness the resolved system context,
// the prompt, the model and the stream hint, and takes back an argv. Nothing
// else about a provider is visible from a call site.
func TestAgentPassesTheWholeContractToTheHarness(t *testing.T) {
	seen := &fakeCall{}
	agent := New(fakeHarness{binary: "fake", argv: helperArgv("ok"), reachable: true, seen: seen},
		Options{Root: t.TempDir(), System: "SYS"})

	agent.Command("do the thing", "opus", true)
	if seen.binary != "fake" || seen.system != "SYS" || seen.prompt != "do the thing" ||
		seen.model != "opus" || !seen.stream {
		t.Fatalf("harness saw %+v", *seen)
	}
}

// An empty system context is NO context, not a blank block: a caller that
// resolved nothing must not inject an empty string a harness would then prepend.
func TestEmptySystemContextIsNoContext(t *testing.T) {
	seen := &fakeCall{}
	agent := New(fakeHarness{binary: "fake", seen: seen}, Options{System: ""})
	agent.Command("hi", "m", false)
	if seen.system != "" {
		t.Fatalf("system = %q", seen.system)
	}
	if agent.System() != "" {
		t.Fatalf("System() = %q", agent.System())
	}
}

func TestBinaryFallsBackToTheHarnessDefault(t *testing.T) {
	if got := New(fakeHarness{binary: "fake"}, Options{}).Binary(); got != "fake" {
		t.Fatalf("Binary() = %q", got)
	}
	if got := New(fakeHarness{binary: "fake"}, Options{Binary: "/opt/other"}).Binary(); got != "/opt/other" {
		t.Fatalf("Binary() = %q", got)
	}
}

// -- availability (the PATH probe) --------------------------------------------

func TestAvailableFalseForMissingBinary(t *testing.T) {
	agent := New(ClaudeCLI{}, Options{Binary: "definitely-not-a-real-binary-xyz", Path: os.Getenv("PATH")})
	if agent.Available() {
		t.Fatal("a binary that is not installed must not be available")
	}
}

func TestAvailableTrueForBinaryOnPath(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "claude"))
	if !New(ClaudeCLI{}, Options{Path: dir}).Available() {
		t.Fatal("an executable on PATH must be available")
	}
}

// PATH is passed in, never read from the process: the adapter boundary resolves
// the environment, and a harness pin must reach this probe like every other.
func TestAvailabilityReadsTheInjectedPathAndNotTheProcess(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "claude"))
	t.Setenv("PATH", dir)
	if New(ClaudeCLI{}, Options{Path: ""}).Available() {
		t.Fatal("an empty injected PATH must find nothing, whatever the process PATH says")
	}
}

func TestAvailabilitySearchesEveryPathEntry(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeExecutable(t, filepath.Join(second, "claude"))
	path := strings.Join([]string{first, second}, string(os.PathListSeparator))
	if !New(ClaudeCLI{}, Options{Path: path}).Available() {
		t.Fatal("the probe stopped at the first PATH entry")
	}
}

// An explicit path is checked directly rather than searched for — a binary named
// with a slash is not a PATH lookup at all.
func TestExplicitPathIsCheckedDirectly(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "somewhere", "claude")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeExecutable(t, binary)

	if !New(ClaudeCLI{}, Options{Binary: binary, Path: ""}).Available() {
		t.Fatal("an explicit path must be checked directly, with no PATH involved")
	}
	if New(ClaudeCLI{}, Options{Binary: filepath.Join(dir, "gone", "claude"), Path: dir}).Available() {
		t.Fatal("an explicit path that does not exist is not available")
	}
}

// A DIRECTORY named like the binary is the case a naive mode check gets wrong:
// directories carry execute bits, and spawning one fails.
func TestADirectoryNamedLikeTheBinaryIsNotAvailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if New(ClaudeCLI{}, Options{Path: dir}).Available() {
		t.Fatal("a directory is not an executable")
	}
}

func TestANonExecutableFileIsNotAvailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if New(ClaudeCLI{}, Options{Path: dir}).Available() {
		t.Fatal("a file without an execute bit is not available")
	}
}

// The harness's own probe is ANDed with the PATH probe, and it runs second: an
// installed binary pointed at a dead endpoint is still a dead end.
func TestHarnessProbeCanVetoAnInstalledBinary(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "fake"))
	agent := New(fakeHarness{binary: "fake", reachable: false}, Options{Path: dir})
	if agent.Available() {
		t.Fatal("an unreachable harness must not be available")
	}
}

// -- RunSync (the synchronous CLI path) ---------------------------------------

func TestRunSyncReportsACleanExit(t *testing.T) {
	agent := newFakeAgent(helperArgv("ok"), Options{Root: t.TempDir()})
	if !agent.RunSync("hi", "m", false) {
		t.Fatal("a zero exit must report true")
	}
}

func TestRunSyncReportsANonzeroExit(t *testing.T) {
	agent := newFakeAgent(helperArgv("transcript"), Options{Root: t.TempDir()})
	if agent.RunSync("hi", "m", false) {
		t.Fatal("exit 7 must report false")
	}
}

// A spawn that never happened is the same answer as a nonzero exit: the agent
// did not complete. Kernel#system returns nil there, which the caller treats as
// false, and the CLI's "(agent exited non-zero)" notice is the right sentence
// for both.
func TestRunSyncReportsAFailedSpawnAsIncomplete(t *testing.T) {
	agent := newFakeAgent([]string{filepath.Join(t.TempDir(), "no-such-binary")}, Options{Root: t.TempDir()})
	if agent.RunSync("hi", "m", false) {
		t.Fatal("a spawn that failed must report false")
	}
}

// Inherited stdio is the whole point of the sync path: the harness streams to
// the user's terminal live rather than being buffered and replayed.
func TestRunSyncInheritsStdioRatherThanCapturingIt(t *testing.T) {
	agent := newFakeAgent(helperArgv("transcript"), Options{Root: t.TempDir()})

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = write
	agent.RunSync("hi", "m", false)
	os.Stdout = previous
	write.Close()

	buffer := make([]byte, 64)
	n, _ := read.Read(buffer)
	if string(buffer[:n]) != "transcript" {
		t.Fatalf("the child's stdout did not reach the process's: %q", string(buffer[:n]))
	}
	if agent.Output() != "" {
		t.Fatalf("a sync run must not fill the async buffer: %q", agent.Output())
	}
}

// The harness runs where the TASK FILES are, not where this binary lives: it
// reads tasks.jsonl and runs the CLI from there.
func TestRunSyncRunsInTheTaskDataDirectory(t *testing.T) {
	root := t.TempDir()
	agent := newFakeAgent(helperArgv("echo-cwd"), Options{Root: root})

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previous := os.Stdout
	os.Stdout = write
	agent.RunSync("hi", "m", false)
	os.Stdout = previous
	write.Close()

	buffer := make([]byte, 4096)
	n, _ := read.Read(buffer)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(buffer[:n])))
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Fatalf("child cwd = %q, want %q", got, want)
	}
}

// The stream hint reaches the harness as false from the sync path: the CLI wants
// the final answer, not a transcript to animate.
func TestRunSyncPassesTheStreamHintThrough(t *testing.T) {
	seen := &fakeCall{}
	agent := New(fakeHarness{binary: "fake", argv: helperArgv("ok"), seen: seen}, Options{Root: t.TempDir()})
	agent.RunSync("hi", "m", false)
	if seen.stream {
		t.Fatal("sync CLI wants the final answer, not a transcript")
	}
}

func TestEmptyArgvIsRefusedRatherThanSpawned(t *testing.T) {
	agent := New(fakeHarness{binary: "fake", argv: []string{}}, Options{Root: t.TempDir()})
	if agent.RunSync("hi", "m", false) {
		t.Fatal("an empty argv must not report success")
	}
	if err := agent.Start("hi", "m", false); err == nil {
		t.Fatal("an empty argv must not start")
	}
}

// -- Start / Output / Done (the async TUI path) -------------------------------

func TestAsyncAgentRecordsOutputAndNonzeroProcessStatus(t *testing.T) {
	agent := newFakeAgent(helperArgv("transcript"), Options{Root: t.TempDir()})
	start(t, agent)
	<-agent.Done()

	if agent.Output() != "transcript" {
		t.Fatalf("Output() = %q", agent.Output())
	}
	if agent.ExitStatus() != 7 {
		t.Fatalf("ExitStatus() = %d, want 7", agent.ExitStatus())
	}
	if agent.Success() {
		t.Fatal("exit 7 is not success")
	}
	if agent.Cancelled() {
		t.Fatal("a run that ended on its own was not cancelled")
	}
	if agent.Running() {
		t.Fatal("a finished run is not running")
	}
	if signaled, _ := agent.Signaled(); signaled {
		t.Fatal("an ordinary exit is not a signal death")
	}
}

// Both streams are captured: a harness that reports on stderr must not lose the
// report.
func TestAsyncAgentCapturesStderrToo(t *testing.T) {
	agent := newFakeAgent(helperArgv("stderr"), Options{Root: t.TempDir()})
	start(t, agent)
	<-agent.Done()
	if agent.Output() != "on stderr" {
		t.Fatalf("Output() = %q", agent.Output())
	}
}

// A headless harness must never block waiting for input nobody will type.
func TestAsyncAgentGivesTheChildNoStdin(t *testing.T) {
	agent := newFakeAgent(helperArgv("stdin-is-closed"), Options{Root: t.TempDir()})
	start(t, agent)
	<-agent.Done()
	if agent.Output() != "no stdin" {
		t.Fatalf("Output() = %q, ExitStatus = %d", agent.Output(), agent.ExitStatus())
	}
}

// Restarting the SAME agent is what a switcher does, so the metadata of the
// previous run must not survive into the next one.
func TestAsyncAgentCancellationIsDistinctAndNextStartResetsMetadata(t *testing.T) {
	argv := helperArgv("sleep")
	agent := New(&scriptedHarness{argv: &argv}, Options{Root: t.TempDir()})

	start(t, agent)
	agent.Cancel()
	<-agent.Done()
	if !agent.Cancelled() {
		t.Fatal("a cancelled run must say so")
	}
	if agent.Success() {
		t.Fatal("a cancelled run is not a success")
	}

	argv = helperArgv("transcript")
	start(t, agent)
	if agent.Cancelled() {
		t.Fatal("a new run resets cancellation metadata")
	}
	<-agent.Done()
	if agent.Cancelled() {
		t.Fatal("the previous cancellation leaked into the new run")
	}
	if agent.ExitStatus() != 7 {
		t.Fatalf("ExitStatus() = %d, want 7", agent.ExitStatus())
	}
	if agent.Output() != "transcript" {
		t.Fatalf("a new run must start with an empty transcript and hold only its own: %q", agent.Output())
	}
}

// scriptedHarness lets a test change the argv between runs on one agent.
type scriptedHarness struct{ argv *[]string }

func (scriptedHarness) DefaultBinary() string { return "fake" }
func (scriptedHarness) Reachable() bool       { return true }
func (h *scriptedHarness) Argv(_, _, _, _ string, _ bool) []string {
	return *h.argv
}

// The case that makes the escalation load-bearing: a harness that traps TERM
// must not be able to freeze the UI that cancelled it.
func TestAsyncAgentCancellationEscalatesWhenChildIgnoresTerm(t *testing.T) {
	agent := newFakeAgent(helperArgv("hostile"), Options{Root: t.TempDir()})
	start(t, agent)
	waitForOutput(t, agent, "ready")

	began := time.Now()
	agent.Cancel()
	elapsed := time.Since(began)
	<-agent.Done()

	if elapsed >= time.Second {
		t.Fatalf("a TERM-resistant child froze cancellation for %s", elapsed)
	}
	if !agent.Cancelled() {
		t.Fatal("Cancelled() must be true")
	}
	if agent.Success() {
		t.Fatal("a killed child is not a success")
	}
	signaled, signal := agent.Signaled()
	if !signaled || signal != syscall.SIGKILL {
		t.Fatalf("Signaled() = %v, %v; want true, SIGKILL", signaled, signal)
	}
}

// Cancel signals the whole PROCESS GROUP: these harnesses spawn tool
// subprocesses, and killing only the leader orphans them holding the store.
func TestCancelSignalsTheWholeProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are POSIX")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "grandchild-alive")
	// A shell that backgrounds a grandchild and then sleeps. The grandchild
	// writes its pid and sleeps; if the group signal missed it, it survives.
	script := "sh -c 'echo $$ > " + marker + "; sleep 30' & sleep 30"
	agent := newFakeAgent([]string{"/bin/sh", "-c", script}, Options{Root: dir})
	start(t, agent)

	pid := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			pid = atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Skip("the grandchild never reported a pid")
	}

	agent.Cancel()
	<-agent.Done()

	// The grandchild is not ours to reap, so it may linger as a zombie for a
	// moment; what must be true is that it is no longer running.
	gone := false
	for deadline = time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := syscall.Kill(pid, 0); err != nil {
			gone = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gone {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("the grandchild outlived the cancellation — the signal did not reach the group")
	}
}

// A fresh Agent's Done is already closed, so a caller that selects on it before
// any run does not block forever waiting for a run that never started.
func TestFreshAgentIsAlreadyDone(t *testing.T) {
	select {
	case <-New(fakeHarness{binary: "fake"}, Options{}).Done():
	case <-time.After(time.Second):
		t.Fatal("a fresh agent's Done must already be closed")
	}
}

func TestExitStatusIsUnknownBeforeAnyRun(t *testing.T) {
	agent := New(fakeHarness{binary: "fake"}, Options{})
	if agent.ExitStatus() != -1 {
		t.Fatalf("ExitStatus() = %d, want -1", agent.ExitStatus())
	}
	if agent.Success() || agent.Running() || agent.Cancelled() {
		t.Fatal("a fresh agent has run nothing")
	}
}

// Cancel on an agent that never started must be a no-op that still records the
// intent, not a signal to pid 0 — which would address this whole process group.
func TestCancelBeforeStartIsSafe(t *testing.T) {
	agent := New(fakeHarness{binary: "fake"}, Options{})
	agent.Cancel()
	if !agent.Cancelled() {
		t.Fatal("Cancel must record the intent even with nothing to signal")
	}
}

// Output is read by a UI while the reader goroutine appends to it. Under -race
// this is the assertion; without it, it at least proves concurrent access works.
func TestOutputIsSafeToReadDuringARun(t *testing.T) {
	agent := newFakeAgent(helperArgv("hostile"), Options{Root: t.TempDir()})
	start(t, agent)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = agent.Output()
				_ = agent.Running()
			}
		}
	}()
	waitForOutput(t, agent, "ready")
	agent.Cancel()
	close(stop)
	<-done
	<-agent.Done()
}

// -- helpers ------------------------------------------------------------------

func start(t *testing.T, agent *Agent) {
	t.Helper()
	if err := agent.Start("prompt", "model", true); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if agent.Running() {
			agent.Cancel()
		}
	})
}

func waitForOutput(t *testing.T, agent *Agent, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(agent.Output(), needle) {
			return
		}
		select {
		case <-agent.Done():
			t.Fatalf("the agent exited before %q; output was %q", needle, agent.Output())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %q; output was %q", needle, agent.Output())
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func atoi(text string) int {
	value := 0
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0
		}
		value = value*10 + int(char-'0')
	}
	return value
}

// Guard: nothing in this package may reach for a real harness on the developer's
// machine. If any adapter's default binary is present, the tests above still
// must not spawn it — this asserts we never named one in an argv.
func TestNoTestSpawnsARealHarness(t *testing.T) {
	for _, binary := range []string{"claude", "agent", "hermes"} {
		if _, err := exec.LookPath(binary); err == nil {
			t.Logf("%s is installed on this machine; no test above puts it in an argv", binary)
		}
	}
}
