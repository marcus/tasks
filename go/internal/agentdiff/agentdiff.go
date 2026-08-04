// Package agentdiff builds the post-run "what did the agent change?" git diff
// for `tasks -p`. It is the Go counterpart of lib/tasks/agent_diff.rb, and it is
// separate from the CLI for the same reason: the decision — which files to diff,
// and how to handle a relocated memory sidecar — can then be exercised against a
// real sandbox repo without driving an actual agent.
//
// The memory sidecar (agent-memory.md) normally sits beside tasks.jsonl, so it
// is diffed right alongside the task files. But TASKS_MEMORY or the config
// `memory` key can put it outside the task-data repo's work tree, where
// `git -C data_dir diff` cannot see it. In that case it is dropped from the diff
// and, when it exists (so the agent could have edited it), flagged with a
// one-line notice rather than silently omitted.
package agentdiff

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// Result is what the CLI renders. Diff is the captured `git diff` text for the
// in-repo targets and may be empty; Notice is the path of an out-of-repo memory
// sidecar to flag, or "".
type Result struct {
	Diff   string
	Notice string
}

// Request is one computation's inputs.
type Request struct {
	DataDir string
	Org     string
	Archive string
	Memory  string
	// Color forces ANSI colour in the captured diff. Callers pass "is stdout a
	// terminal", so an interactive run stays coloured and a piped one stays
	// plain — the diff is captured before it is printed, so git cannot decide
	// this for itself.
	Color bool
	// Stderr is where git's own diagnostics go. Ruby's backticks inherit stderr,
	// so a git that complains does so on the CLI's stderr; nil discards.
	Stderr io.Writer
}

// Compute answers what changed. The second result is false when DataDir is not a
// git work tree — the diff is only meaningful for a git-backed task set, and
// there is nothing honest to show for one that is not.
func Compute(request Request) (Result, bool) {
	if !gitWorkTree(request.DataDir) {
		return Result{}, false
	}

	targets := []string{request.Org, request.Archive}
	notice := ""
	if request.Memory != "" {
		switch {
		case inSameRepo(request.DataDir, request.Memory):
			targets = append(targets, request.Memory)
		case exists(request.Memory):
			notice = request.Memory
		}
	}

	return Result{Diff: captureDiff(request, targets), Notice: notice}, true
}

func gitWorkTree(dir string) bool {
	command := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	return command.Run() == nil
}

// inSameRepo is true when path resolves inside DataDir's git work tree — the
// same repo, so `git -C data_dir diff` can show it. A path in no repo, or in a
// different (nested or sibling) repo, is outside.
func inSameRepo(dataDir, path string) bool {
	dir := path
	if !isDir(path) {
		dir = parentDir(path)
	}
	if !isDir(dir) {
		return false
	}
	top := toplevel(dataDir)
	return top != "" && top == toplevel(dir)
}

func toplevel(dir string) string {
	command := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func captureDiff(request Request, targets []string) string {
	colorFlag := "--color=never"
	if request.Color {
		colorFlag = "--color=always"
	}

	args := append([]string{"-C", request.DataDir, "--no-pager", "diff", colorFlag, "--"}, targets...)
	diff := gitOutput(request.Stderr, args...)

	// Plain `git diff` only sees tracked files, but the first "remember …"
	// request CREATES agent-memory.md (and a fresh task set may have uncommitted
	// task files too). Surface those as new-file diffs so the create path is
	// never silently absent from the audit.
	for _, path := range untracked(request.DataDir, targets) {
		diff += gitOutput(request.Stderr, "-C", request.DataDir, "--no-pager", "diff",
			"--no-index", colorFlag, "--", os.DevNull, path)
	}
	return diff
}

// gitOutput is Ruby's backticks: stdout captured, stderr inherited, and the exit
// status ignored. `git diff` exits 1 when there IS a difference, so treating a
// nonzero status as failure would drop exactly the output this exists to show.
func gitOutput(stderr io.Writer, args ...string) string {
	command := exec.Command("git", args...)
	command.Stderr = stderr
	output, _ := command.Output()
	return string(output)
}

func untracked(dataDir string, targets []string) []string {
	var missing []string
	for _, path := range targets {
		if !isFile(path) {
			continue
		}
		command := exec.Command("git", "-C", dataDir, "ls-files", "--error-unmatch", path)
		if command.Run() != nil {
			missing = append(missing, path)
		}
	}
	return missing
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// parentDir is File.dirname: the directory part, with Ruby's answers for the
// degenerate spellings ("" → ".", "/x" → "/").
func parentDir(path string) string {
	index := strings.LastIndexByte(path, '/')
	switch {
	case index < 0:
		return "."
	case index == 0:
		return "/"
	default:
		return path[:index]
	}
}
