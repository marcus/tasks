package merge

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
		output := filepath.Join(t.TempDir(), "tasks-binary")
		command := exec.Command("go", "build", "-o", output, "./cmd/tasks")
		command.Dir = root
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

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

type driverCase struct {
	name       string
	base       doc
	ours       doc
	theirs     doc
	theirsText string
	extra      []string
	verbose    bool
	refuse     bool
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
			theirsText: "{\"type\":\"meta\",\"version\":2}\n{not json at all}\n", refuse: true},
		{name: "a v1 side is fenced", base: base,
			ours: base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}), theirs: v1, refuse: true},
		{name: "a cyclic merge is fenced after the records are built", base: base,
			ours:   base.change("10000003", map[string]any{"parent": "10000002", "updated": homeStamp}),
			theirs: doc{moved[0], moved[1], moved[3], moved[2], moved[4]}, refuse: true},
		{name: "git's marker size and labels are honored", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2}\n<<<<<<< not json at all\n",
			extra:      []string{"12", "HEAD", "sync-branch"}, refuse: true},
		{name: "a nonsense marker size falls back to git's default", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "nope\n", extra: []string{"not-a-number", "  ", "\t"}, refuse: true},
		{name: "a narrower marker size than git's minimum is widened", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "nope\n", extra: []string{"3", "HEAD", "other"}, refuse: true},
		{name: "a side with no trailing newline stays recoverable", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2}\n{broken", refuse: true},
		{name: "a side that is not valid UTF-8 is carried across raw", base: base,
			ours:       base.change("10000002", map[string]any{"title": "Ours", "updated": homeStamp}),
			theirsText: "{\"type\":\"meta\",\"version\":2,\"x\":\"\xff\xfe\"}\n", refuse: true},
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

func TestDriverHandlesEveryRecordedCase(t *testing.T) {
	binary := []string{goBinary(t)}

	for _, testCase := range driverCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			base, ours, theirs := testCase.sides(t)
			got := runDriver(t, binary, base, ours, theirs, testCase.extra, testCase.verbose)
			if !testCase.refuse && got.status != 0 {
				t.Fatalf("valid merge failed: %s", got.stderr)
			}
			if testCase.refuse && got.status == 0 {
				t.Fatal("invalid merge unexpectedly succeeded")
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
// the archived migration decision for bounded merge conflict markers.
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
// stderr of a plumbing command Git is parsing. tasks dispatches it before
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

func TestMergedStoreIsValidAndDeterministic(t *testing.T) {
	binary := []string{goBinary(t)}
	base := baseRecords()
	ours := base.change("10000002", map[string]any{
		"tags": []any{"@computer", "travel"}, "delegation": ready(nil), "state": "NEXT", "updated": homeStamp})
	theirs := base.change("10000003", map[string]any{
		"title": "Call PSE billing", "scheduled": "2026-07-19", "updated": workStamp})

	merged := runDriver(t, binary, base.text(t), ours.text(t), theirs.text(t), nil, false)
	if merged.status != 0 {
		t.Fatalf("merge failed: %s", merged.stderr)
	}
	if !checkOK(merged.ours) {
		t.Fatal("the structural check rejects the merged store")
	}
	again := Merge(base.text(t), merged.ours, theirs.text(t))
	if !again.OK() {
		t.Fatalf("re-merge failed: %s", again.Error)
	}
	if again.Text != merged.ours {
		t.Fatalf("re-merge changed bytes:\nfirst: %q\nagain: %q", merged.ours, again.Text)
	}
}
