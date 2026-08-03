package merge

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The driver is where a merge bug becomes lost data: Git copies whatever the
// driver leaves in %A back over the working file whatever the exit status. So
// the two binaries are compared end to end — exit status, stdout, stderr, the
// bytes left in %A, and the audit log — and not only the in-memory merge.

var (
	buildOnce   sync.Once
	builtBinary string
	buildErr    error
)

// goBinary builds cmd/tasks once for the whole package. Comparing the real
// executable is the point: a difference in dispatch, environment reading or
// exit status is exactly the kind of thing an in-process test cannot see.
func goBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRoot(t)
		output := filepath.Join(t.TempDir(), "tasks-go-binary")
		command := exec.Command("go", "build", "-o", output, "./cmd/tasks")
		command.Dir = filepath.Join(root, "go")
		if combined, err := command.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build failed: %s", combined)
			return
		}
		builtBinary = output
	})
	if buildErr != nil {
		t.Fatalf("could not build the Go binary: %v", buildErr)
	}
	// t.TempDir is removed when the test that created it finishes, so a later
	// test in the same package must rebuild rather than run a deleted file.
	if _, err := os.Stat(builtBinary); err != nil {
		buildOnce = sync.Once{}
		return goBinary(t)
	}
	return builtBinary
}

type driverRun struct {
	status   int
	stdout   string
	stderr   string
	ours     string
	log      string
	logFound bool
}

// runDriver lays out one merge stage in a fresh directory and invokes one
// implementation over it, then reads back everything a caller could observe.
func runDriver(t *testing.T, command []string, base, ours, theirs string, extra []string, verbose bool) driverRun {
	t.Helper()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.jsonl")
	oursPath := filepath.Join(dir, "ours.jsonl")
	theirsPath := filepath.Join(dir, "theirs.jsonl")
	pathname := filepath.Join(dir, "tasks.jsonl")
	for _, side := range []struct{ path, text string }{
		{basePath, base}, {oursPath, ours}, {theirsPath, theirs},
	} {
		if err := os.WriteFile(side.path, []byte(side.text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	args := append(append([]string{}, command[1:]...), "merge-driver", basePath, oursPath, theirsPath, pathname)
	args = append(args, extra...)
	process := exec.Command(command[0], args...)
	home := t.TempDir()
	process.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_STATE_HOME=" + filepath.Join(home, "state")}
	if verbose {
		process.Env = append(process.Env, "TASKS_MERGE_VERBOSE=1")
	}
	var stdout, stderr strings.Builder
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	status := 0
	if exitErr, isExit := err.(*exec.ExitError); isExit {
		status = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", command, err)
	}

	result := driverRun{status: status, stdout: stdout.String(), stderr: stderr.String()}
	oursBytes, err := os.ReadFile(oursPath)
	if err != nil {
		t.Fatal(err)
	}
	result.ours = string(oursBytes)
	if logBytes, err := os.ReadFile(filepath.Join(dir, ".tasks-merge.log")); err == nil {
		result.log = strings.ReplaceAll(string(logBytes), dir, "<dir>")
		result.logFound = true
	}
	// The pathname is a temp directory, so it differs between the two runs by
	// construction; normalize it rather than exempting the whole message.
	result.stdout = strings.ReplaceAll(result.stdout, dir, "<dir>")
	result.stderr = strings.ReplaceAll(result.stderr, dir, "<dir>")
	return result
}

func rubyDriverCommand(t *testing.T) []string {
	t.Helper()
	return []string{requireRuby(t), filepath.Join(repoRoot(t), "bin", "tasks")}
}

type driverCase struct {
	name       string
	base       doc
	ours       doc
	theirs     doc
	theirsText string
	extra      []string
	verbose    bool
}

func driverCases(t *testing.T) []driverCase {
	t.Helper()
	base := baseRecords()
	v1 := baseRecords()
	v1[0]["version"] = 1
	moved := base.change("10000002", map[string]any{"parent": "10000003", "updated": workStamp})

	return []driverCase{
		{name: "a clean merge writes the merged file", base: base,
			ours:   base.change("10000002", map[string]any{"tags": []any{"@computer", "travel"}, "updated": homeStamp}),
			theirs: base.change("10000003", map[string]any{"title": "Call PSE billing", "updated": workStamp})},
		{name: "a clean merge is quiet unless asked", base: base, ours: base, theirs: base, verbose: true},
		{name: "an unparseable side is fenced", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2}\n{not json at all}\n"},
		{name: "a v1 side is fenced", base: base,
			ours: base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}), theirs: v1},
		{name: "a cyclic merge is fenced after the records are built", base: base,
			ours:   base.change("10000003", map[string]any{"parent": "10000002", "updated": homeStamp}),
			theirs: doc{moved[0], moved[1], moved[3], moved[2], moved[4]}},
		{name: "git's marker size and labels are honored", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2}\n<<<<<<< not json at all\n",
			extra:      []string{"12", "HEAD", "sync-branch"}},
		{name: "a nonsense marker size falls back to git's default", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "nope\n", extra: []string{"not-a-number", "  ", "\t"}},
		{name: "a narrower marker size than git's minimum is widened", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "nope\n", extra: []string{"3", "HEAD", "other"}},
		{name: "a side with no trailing newline stays recoverable", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2}\n{broken"},
		{name: "a side that is not valid UTF-8 is carried across raw", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2,\"x\":\"\xff\xfe\"}\n"},
	}
}

func (c driverCase) sides(t *testing.T) (string, string, string) {
	t.Helper()
	theirs := c.theirsText
	if c.theirs != nil {
		theirs = c.theirs.text(t)
	}
	return c.base.text(t), c.ours.text(t), theirs
}

func TestTheTwoDriversLeaveIdenticalBytes(t *testing.T) {
	ruby := rubyDriverCommand(t)
	binary := []string{goBinary(t)}

	for _, testCase := range driverCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			base, ours, theirs := testCase.sides(t)
			wanted := runDriver(t, ruby, base, ours, theirs, testCase.extra, testCase.verbose)
			got := runDriver(t, binary, base, ours, theirs, testCase.extra, testCase.verbose)

			if got.status != wanted.status {
				t.Errorf("exit status = %d, want %d\ngo stderr: %s", got.status, wanted.status, got.stderr)
			}
			if got.ours != wanted.ours {
				t.Errorf("the bytes Git copies back differ.\n--- ruby ---\n%q\n--- go ---\n%q", wanted.ours, got.ours)
			}
			if got.stdout != wanted.stdout {
				t.Errorf("stdout = %q, want %q", got.stdout, wanted.stdout)
			}
			if got.stderr != wanted.stderr {
				t.Errorf("stderr = %q, want %q", got.stderr, wanted.stderr)
			}
			if got.logFound != wanted.logFound || got.log != wanted.log {
				t.Errorf("audit log differs.\n--- ruby ---\n%s\n--- go ---\n%s", wanted.log, got.log)
			}
		})
	}
}

// The driver's own contract, asserted against the Go binary rather than
// inferred from agreement with Ruby: a refusal must leave a CONFLICTED file
// from which BOTH sides are recoverable verbatim, and which `tasks check`
// rejects. Leaving %A alone would leave ours' full content there, clean and
// markerless, and the reflex `git add` would then discard all of theirs.
func TestARefusalLeavesBothSidesRecoverableAndCheckFailing(t *testing.T) {
	binary := []string{goBinary(t)}
	base := baseRecords()
	ours := base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp})
	theirsText := "{\"type\":\"meta\",\"version\":2}\n{not json at all}\n"

	run := runDriver(t, binary, base.text(t), ours.text(t), theirsText, nil, false)

	if run.status == 0 {
		t.Fatal("a refused merge must exit nonzero")
	}
	assertConflictShape(t, run.ours, ours.text(t), theirsText, 7)
	if result := checkTextOf(run.ours); result {
		t.Fatal("a refused merge must not leave a file `tasks check` accepts")
	}
	if !strings.Contains(run.stderr, "merge failed") {
		t.Fatalf("stderr = %q", run.stderr)
	}
	if !strings.Contains(run.log, "failed") {
		t.Fatalf("audit log = %q", run.log)
	}
}

// assertConflictShape splits on the fences and compares, rather than matching a
// rendered string: that is exactly how a human or a script recovers a side, so
// asserting it this way asserts the recovery works.
func assertConflictShape(t *testing.T, conflicted, oursBefore, theirsBefore string, size int) {
	t.Helper()
	opening := regexp.MustCompile(`(?m)^` + strings.Repeat("<", size) + `[^\n]*\n`)
	head := opening.Split(conflicted, 2)
	if len(head) != 2 || head[0] != "" {
		t.Fatalf("conflict markers must open the file, with nothing before them: %q", conflicted)
	}
	middle := strings.SplitN(head[1], "\n"+strings.Repeat("=", size)+"\n", 2)
	if len(middle) != 2 {
		t.Fatalf("no %s fence: %q", strings.Repeat("=", size), conflicted)
	}
	oursSide := middle[0] + "\n"
	closing := regexp.MustCompile(`(?m)^` + strings.Repeat(">", size) + `[^\n]*\n`)
	tail := closing.Split(middle[1], 2)
	if len(tail) != 2 {
		t.Fatalf("no closing fence: %q", conflicted)
	}
	if oursSide != oursBefore {
		t.Fatalf("ours is not recoverable verbatim: %q vs %q", oursSide, oursBefore)
	}
	wantedTheirs := theirsBefore
	if !strings.HasSuffix(wantedTheirs, "\n") {
		wantedTheirs += "\n"
	}
	if tail[0] != wantedTheirs {
		t.Fatalf("theirs is not recoverable verbatim: %q vs %q", tail[0], wantedTheirs)
	}
	if tail[1] != "" {
		t.Fatalf("nothing may follow the closing marker: %q", tail[1])
	}
}

func checkTextOf(text string) bool {
	return checkOK(text)
}

// Git's %L is a small integer and Git validates it, so the only way to ask for
// a marker wider than a megabyte is by hand. Ruby's Integer#to_i is unbounded
// and would try to build the line; Go clamps. See
// porting/intentional-differences.md, merge-conflict-marker-size-is-bounded.
func TestMarkerSizeIsClampedAtBothEnds(t *testing.T) {
	cases := map[string]int{
		"":               7,
		"   ":            7,
		"not-a-number":   7,
		"3":              7,
		"7":              7,
		"12":             12,
		"99999999999999": 1 << 20,
	}
	for supplied, wanted := range cases {
		if got := resolvedMarkerSize(supplied); got != wanted {
			t.Errorf("resolvedMarkerSize(%q) = %d, want %d", supplied, got, wanted)
		}
	}
}

func TestTheDriverRefusesTheWrongNumberOfArguments(t *testing.T) {
	binary := goBinary(t)
	for _, args := range [][]string{
		{"merge-driver"},
		{"merge-driver", "a", "b", "c"},
		{"merge-driver", "a", "b", "c", "d", "e", "f", "g", "h"},
	} {
		command := exec.Command(binary, args...)
		command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
		var stderr strings.Builder
		command.Stderr = &stderr
		err := command.Run()
		exitErr, isExit := err.(*exec.ExitError)
		if !isExit || exitErr.ExitCode() != 2 {
			t.Fatalf("args %v: err = %v, want exit 2", args, err)
		}
		if !strings.Contains(stderr.String(), "usage: tasks merge-driver") {
			t.Fatalf("args %v: stderr = %q", args, stderr.String())
		}
	}
}

// The driver must not resolve a task store at all. Git invokes it with four
// temporary stage paths and no store in sight, so an unrelated misconfiguration
// — an invalid TASKS_TIMEZONE here — must not add a resolution note to the
// stderr of a plumbing command Git is parsing. bin/tasks dispatches it before
// resolution for this reason, and cmd/tasks/main.go now does the same via
// earlyCommands.
//
// This replaces a recorded intentional difference that has been retired.
func TestTheDriverResolvesNoConfiguration(t *testing.T) {
	base := baseRecords().text(t)
	dir := t.TempDir()
	for _, name := range []string{"base", "ours", "theirs"} {
		if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(base), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(goBinary(t), "merge-driver",
		filepath.Join(dir, "base.jsonl"), filepath.Join(dir, "ours.jsonl"),
		filepath.Join(dir, "theirs.jsonl"), filepath.Join(dir, "tasks.jsonl"))
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"),
		"TASKS_TIMEZONE=Not/AZone"}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("the merge itself must still succeed: %v (%s)", err, stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("the driver resolved configuration: stderr = %q", stderr.String())
	}
}

// Cross-language interoperability is a cutover requirement: both binaries will
// be installed during the compatibility window, so each must accept what the
// other's merge wrote.
func TestEachImplementationAcceptsTheOthersMergedStore(t *testing.T) {
	ruby := rubyDriverCommand(t)
	binary := []string{goBinary(t)}
	base := baseRecords()
	ours := base.change("10000002", map[string]any{
		"tags": []any{"@computer", "travel"}, "delegation": ready(nil), "state": "NEXT", "updated": homeStamp})
	theirs := base.change("10000003", map[string]any{
		"title": "Call PSE billing", "scheduled": "2026-07-19", "updated": workStamp})

	rubyMerged := runDriver(t, ruby, base.text(t), ours.text(t), theirs.text(t), nil, false)
	goMerged := runDriver(t, binary, base.text(t), ours.text(t), theirs.text(t), nil, false)
	if rubyMerged.status != 0 || goMerged.status != 0 {
		t.Fatalf("both merges must succeed: ruby=%d go=%d", rubyMerged.status, goMerged.status)
	}
	if rubyMerged.ours != goMerged.ours {
		t.Fatalf("merged bytes differ:\n%q\n%q", rubyMerged.ours, goMerged.ours)
	}

	// Go reads what Ruby merged.
	if !checkOK(rubyMerged.ours) {
		t.Fatal("the Go check rejects Ruby's merged store")
	}
	// Ruby reads what Go merged.
	assertRubyCheckAccepts(t, goMerged.ours)
	// And each implementation can merge again on top of the other's output,
	// which is what a second sync does.
	again := Merge(base.text(t), rubyMerged.ours, theirs.text(t))
	if !again.OK() {
		t.Fatalf("Go cannot re-merge Ruby's output: %s", again.Error)
	}
	assertRubyCheckAccepts(t, again.Text)
}

func assertRubyCheckAccepts(t *testing.T, text string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(requireRuby(t), filepath.Join(repoRoot(t), "bin", "tasks"), "check")
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"),
		"TASKS_FILE=" + path, "TASKS_ARCHIVE=" + filepath.Join(dir, "archive.jsonl")}
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ruby check rejects Go-merged bytes: %v\n%s\n%s", err, combined, text)
	}
}
