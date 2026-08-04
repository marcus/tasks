package agentdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A git-backed sandbox data dir with the task files and the memory sidecar
// committed, so a later edit shows up in `git diff` the way `tasks -p` presents
// it after a run. Nothing here reaches a real task store: every path is under a
// per-test temp dir.

const fixtureOrg = `{"meta":{"schema":2}}
{"id":"aaaa1111","title":"Water the garden","state":"TODO","tags":["@home"]}
`

type repo struct {
	dir     string
	org     string
	archive string
	memory  string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	dir := t.TempDir()
	r := &repo{
		dir:     dir,
		org:     filepath.Join(dir, "tasks.jsonl"),
		archive: filepath.Join(dir, "archive.jsonl"),
		memory:  filepath.Join(dir, "agent-memory.md"),
	}
	// archive.jsonl is deliberately absent: an empty 0-byte archive has no meta
	// line, and the real capture path rejects it as invalid before writing.
	write(t, r.org, fixtureOrg)
	write(t, r.memory, "## Defaults\n\n- Garden tasks: add @home.\n")

	r.git(t, "init", "-q")
	r.git(t, "config", "user.email", "t@example.com")
	r.git(t, "config", "user.name", "Test")
	r.git(t, "add", "-A")
	r.git(t, "commit", "-qm", "seed")
	return r
}

func (r *repo) git(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func (r *repo) compute(t *testing.T, memory string) (Result, bool) {
	t.Helper()
	return Compute(Request{DataDir: r.dir, Org: r.org, Archive: r.archive, Memory: memory})
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDiffIncludesACommittedMemoryEditAlongsideTaskFiles(t *testing.T) {
	r := newRepo(t)
	// Simulate the agent both editing a task and saving a default.
	write(t, r.memory, "## Defaults\n\n- Garden tasks: add @home and @weekend.\n")
	write(t, r.org, strings.Replace(fixtureOrg, "Water the garden", "Water the garden well", 1))

	result, ok := r.compute(t, r.memory)
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if result.Notice != "" {
		t.Fatalf("an in-repo sidecar is diffed, not flagged: %q", result.Notice)
	}
	if !strings.Contains(result.Diff, "agent-memory.md") {
		t.Fatalf("diff:\n%s", result.Diff)
	}
	if !strings.Contains(result.Diff, "@weekend") {
		t.Fatalf("the memory edit is not in the diff body:\n%s", result.Diff)
	}
	if !strings.Contains(result.Diff, "tasks.jsonl") {
		t.Fatalf("the task files are not in the diff:\n%s", result.Diff)
	}
}

// The first "remember …" request CREATES the sidecar, so it is untracked and
// invisible to plain `git diff` — the create path must still show in the audit.
func TestDiffIncludesANewlyCreatedUntrackedMemoryFile(t *testing.T) {
	r := newRepo(t)
	if err := os.Remove(r.memory); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r.git(t, "add", "-A")
	r.git(t, "commit", "-qm", "drop sidecar")

	write(t, r.memory, "## Defaults\n\n- Garden tasks: add @home.\n")
	result, ok := r.compute(t, r.memory)
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if result.Notice != "" {
		t.Fatalf("an in-repo sidecar is diffed, not flagged: %q", result.Notice)
	}
	if !strings.Contains(result.Diff, "agent-memory.md") {
		t.Fatalf("diff:\n%s", result.Diff)
	}
	if !strings.Contains(result.Diff, "@home") {
		t.Fatalf("the new file's contents are not in the diff body:\n%s", result.Diff)
	}
}

// An untracked task file counts too: a fresh task set may never have been
// committed, and the audit must not go quiet for it.
func TestDiffIncludesAnUntrackedTaskFile(t *testing.T) {
	dir := t.TempDir()
	org := filepath.Join(dir, "tasks.jsonl")
	write(t, org, fixtureOrg)
	command := exec.Command("git", "-C", dir, "init", "-q")
	if err := command.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	result, ok := Compute(Request{DataDir: dir, Org: org,
		Archive: filepath.Join(dir, "archive.jsonl"), Memory: filepath.Join(dir, "agent-memory.md")})
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if !strings.Contains(result.Diff, "tasks.jsonl") || !strings.Contains(result.Diff, "Water the garden") {
		t.Fatalf("an untracked task file is missing from the audit:\n%s", result.Diff)
	}
}

func TestDiffIsAbsentOutsideAGitWorkTree(t *testing.T) {
	dir := t.TempDir()
	org := filepath.Join(dir, "tasks.jsonl")
	write(t, org, fixtureOrg)

	if _, ok := Compute(Request{DataDir: dir, Org: org,
		Archive: filepath.Join(dir, "archive.jsonl"),
		Memory:  filepath.Join(dir, "agent-memory.md")}); ok {
		t.Fatal("the diff is only meaningful for a git-backed task set")
	}
}

// A memory sidecar relocated outside the task-data repo cannot be diffed there;
// if it exists — the agent could have edited it — it is flagged, not dropped.
func TestOutOfRepoMemoryThatExistsIsFlaggedNotDiffed(t *testing.T) {
	r := newRepo(t)
	outside := filepath.Join(t.TempDir(), "agent-memory.md")
	write(t, outside, "## Defaults\n\n- Relocated defaults.\n")

	result, ok := r.compute(t, outside)
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if result.Notice != outside {
		t.Fatalf("Notice = %q, want %q", result.Notice, outside)
	}
	if strings.Contains(result.Diff, "agent-memory.md") {
		t.Fatalf("an out-of-repo sidecar must not be in the git diff:\n%s", result.Diff)
	}
}

// Out of repo AND absent: nothing to review, so no notice and no diff entry.
func TestOutOfRepoMemoryThatIsAbsentIsNeitherDiffedNorFlagged(t *testing.T) {
	r := newRepo(t)
	outside := filepath.Join(t.TempDir(), "no-such-tasks-memory.md")

	result, ok := r.compute(t, outside)
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if result.Notice != "" {
		t.Fatalf("Notice = %q, want empty", result.Notice)
	}
	if strings.Contains(result.Diff, "agent-memory.md") {
		t.Fatalf("diff:\n%s", result.Diff)
	}
}

// A sidecar in a DIFFERENT repo — nested inside the data dir — is outside this
// work tree as far as `git -C data_dir diff` is concerned, so it is flagged.
func TestMemoryInANestedRepoIsTreatedAsOutOfRepo(t *testing.T) {
	r := newRepo(t)
	nested := filepath.Join(r.dir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	command := exec.Command("git", "-C", nested, "init", "-q")
	if err := command.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	sidecar := filepath.Join(nested, "agent-memory.md")
	write(t, sidecar, "- nested defaults\n")

	result, _ := r.compute(t, sidecar)
	if result.Notice != sidecar {
		t.Fatalf("Notice = %q, want %q", result.Notice, sidecar)
	}
}

// A sidecar whose PARENT DIRECTORY does not exist cannot be in any repo, and
// does not exist either — no notice, no diff, and no git invocation that errors.
func TestMemoryUnderAMissingDirectoryIsNeitherDiffedNorFlagged(t *testing.T) {
	r := newRepo(t)
	result, _ := r.compute(t, filepath.Join(t.TempDir(), "gone", "agent-memory.md"))
	if result.Notice != "" {
		t.Fatalf("Notice = %q", result.Notice)
	}
}

// An unset memory path is simply not considered: `if memory` in Ruby.
func TestUnsetMemoryPathIsSkipped(t *testing.T) {
	r := newRepo(t)
	write(t, r.org, strings.Replace(fixtureOrg, "Water", "Soak", 1))
	result, ok := r.compute(t, "")
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if result.Notice != "" {
		t.Fatalf("Notice = %q", result.Notice)
	}
	if !strings.Contains(result.Diff, "tasks.jsonl") {
		t.Fatalf("the task files must still be diffed:\n%s", result.Diff)
	}
}

// An unchanged store produces an empty diff and no notice — the CLI prints
// nothing at all, rather than an empty "Changes to task files:" heading.
func TestNoChangesProduceAnEmptyDiff(t *testing.T) {
	r := newRepo(t)
	result, ok := r.compute(t, r.memory)
	if !ok {
		t.Fatal("a git work tree must produce a result")
	}
	if strings.TrimSpace(result.Diff) != "" {
		t.Fatalf("diff should be empty:\n%s", result.Diff)
	}
	if result.Notice != "" {
		t.Fatalf("Notice = %q", result.Notice)
	}
}

// Colour is the CALLER's decision, because the diff is captured before it is
// printed and git cannot see the caller's terminal from inside a pipe.
func TestColorIsForcedOnAndOffByTheCaller(t *testing.T) {
	r := newRepo(t)
	write(t, r.org, strings.Replace(fixtureOrg, "Water", "Soak", 1))

	plain, _ := Compute(Request{DataDir: r.dir, Org: r.org, Archive: r.archive, Memory: r.memory})
	if strings.Contains(plain.Diff, "\x1b[") {
		t.Fatalf("a piped run must stay plain:\n%q", plain.Diff)
	}

	colored, _ := Compute(Request{DataDir: r.dir, Org: r.org, Archive: r.archive,
		Memory: r.memory, Color: true})
	if !strings.Contains(colored.Diff, "\x1b[") {
		t.Fatalf("an interactive run must stay coloured:\n%q", colored.Diff)
	}
}

// The untracked pass uses --no-index, which exits 1 whenever there IS a
// difference. Treating that status as failure would drop exactly the output the
// audit exists to show.
func TestUntrackedDiffSurvivesGitsNonzeroStatus(t *testing.T) {
	r := newRepo(t)
	if err := os.Remove(r.memory); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r.git(t, "add", "-A")
	r.git(t, "commit", "-qm", "drop sidecar")
	write(t, r.memory, "- a brand new default\n")

	result, _ := r.compute(t, r.memory)
	if !strings.Contains(result.Diff, "a brand new default") {
		t.Fatalf("diff:\n%s", result.Diff)
	}
}

// Paths are handed to git as arguments, never through a shell, so a data
// directory or sidecar with a space or a quote in its name still diffs.
func TestPathsWithShellMetacharactersAreDiffed(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "task data; echo pwned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	org := filepath.Join(dir, "tasks.jsonl")
	write(t, org, fixtureOrg)
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "Test"},
		{"add", "-A"}, {"commit", "-qm", "seed"},
	} {
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	write(t, org, strings.Replace(fixtureOrg, "Water", "Soak", 1))

	result, ok := Compute(Request{DataDir: dir, Org: org,
		Archive: filepath.Join(dir, "archive.jsonl"), Memory: filepath.Join(dir, "agent-memory.md")})
	if !ok || !strings.Contains(result.Diff, "Soak") {
		t.Fatalf("ok = %v, diff:\n%s", ok, result.Diff)
	}
}

func TestParentDir(t *testing.T) {
	for path, want := range map[string]string{
		"/a/b/c": "/a/b",
		"/a":     "/",
		"a":      ".",
		"":       ".",
	} {
		if got := parentDir(path); got != want {
			t.Fatalf("parentDir(%q) = %q, want %q", path, got, want)
		}
	}
}
