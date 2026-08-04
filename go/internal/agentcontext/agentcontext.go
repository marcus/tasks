// Package agentcontext assembles the system-context string handed to any agent.
// It is the Go counterpart of lib/tasks/agent_context.rb, and it is the single
// assembler: the CLI `tasks -p` path and (later) the TUI queue both build
// through here, so they can never disagree about what an agent sees.
//
// In order:
//
//  1. the repository TASK_AGENT.md contract (list-agent instructions),
//  2. a short "Current environment" block (datetime, hostname, …) when enabled,
//  3. the absolute file locations for this run (CLI, task files, memory),
//  4. a short pointer to the task-set memory policy, and
//  5. the current contents of agent-memory.md, clearly delimited as
//     user-approved defaults, when that sidecar exists and is nonempty.
//
// Coding-agent instructions live in AGENTS.md and are intentionally not injected
// here — workspace tools load that file separately.
//
// The memory sidecar is read fresh on every call — never cached — so a default
// saved by one request is visible to the next, and an external edit or a git
// pull is picked up without restarting. An absent file simply omits the memory
// section; the builder never creates the file as a side effect.
package agentcontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"tasks-go/internal/config"
	"tasks-go/internal/promptfacts"
)

// Contract is the versioned list-agent contract, read from the APPLICATION
// checkout rather than from the task-data directory.
const Contract = "TASK_AGENT.md"

// MemoryMaxBytes is the prompt-injection budget for the memory sidecar. A larger
// file is a configuration mistake, so it fails loudly with the path instead of
// silently truncating a default the agent would then only half-apply.
const MemoryMaxBytes = 16 * 1024 // 16 KiB

const (
	MemoryHeader = "User-approved task-set defaults from agent-memory.md. These are " +
		"durable defaults for this task set; the current request still wins."
	MemoryBegin = "----- BEGIN AGENT MEMORY -----"
	MemoryEnd   = "----- END AGENT MEMORY -----"

	// MemoryPointer is a short pointer only. The full policy prose lives in
	// TASK_AGENT.md (item 1), the versioned list-agent contract, so it is never
	// duplicated as a string that would drift out of sync.
	MemoryPointer = "Task-set memory: apply the agent-memory.md defaults per the memory " +
		"policy in TASK_AGENT.md — add, change, or remove them only on an " +
		"explicit request, and report any change alongside task changes."
)

// Error is raised when the memory sidecar exists but cannot be safely injected
// (unreadable, invalid UTF-8, oversize, or carrying a reserved delimiter).
// Callers surface the message and abort the run rather than proceed without the
// user's defaults — a half-applied default is worse than a refused request.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func errorf(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// Build assembles the whole context. cliRoot is the application checkout, where
// bin/tasks and TASK_AGENT.md live — distinct from the task-data directory the
// harness runs in.
func Build(paths config.Paths, cliRoot string, sources promptfacts.Sources) (string, error) {
	base, err := contractText(cliRoot)
	if err != nil {
		return "", err
	}
	facts := promptfacts.Render(paths.PromptFacts, sources)
	memory, err := MemorySection(paths.Memory)
	if err != nil {
		return "", err
	}

	sections := []string{base, facts, FileLocations(paths, cliRoot), MemoryPointer, memory}
	kept := sections[:0]
	for _, section := range sections {
		// Blank sections drop out; the ones that stay keep their own trailing
		// newline, which is why the joined document has a three-newline gap after
		// the contract and after the file locations. That shape is Ruby's, and it
		// is what an agent's prompt cache is keyed on.
		if rubyStrip(section) == "" {
			continue
		}
		kept = append(kept, section)
	}
	return strings.Join(kept, "\n\n"), nil
}

// FileLocations is Config::Paths#agent_context: the block that lets a headless
// harness find the CLI and the task files even when they live outside the repo.
// Provider-agnostic — every backend uses it unchanged.
//
// The memory sidecar path is always listed, even when the file does not exist
// yet, so an agent can create or edit it without guessing.
//
// The CLI it names is `<cliRoot>/bin/tasks`, exactly as Ruby names it, and not
// this binary's own path. That is the installed entry point on both sides of
// the cutover — the plan switches the executable behind that path rather than
// moving it — so pointing anywhere else would hand the agent a path that stops
// resolving the moment Ruby is retired.
func FileLocations(paths config.Paths, cliRoot string) string {
	return fmt.Sprintf(
		"File locations for this run (absolute; use these, not relative paths):\n"+
			"- tasks CLI: %s\n"+
			"- tasks.jsonl: %s\n"+
			"- archive.jsonl: %s\n"+
			"- agent-memory.md: %s\n",
		filepath.Join(cliRoot, "bin", "tasks"), paths.Org, paths.Archive, paths.Memory)
}

// contractText reads TASK_AGENT.md from the application checkout. An absent file
// yields "", which drops the section: a checkout without the contract still
// gets the file locations and the memory policy pointer, which is strictly
// better than refusing to run at all.
func contractText(cliRoot string) (string, error) {
	path := filepath.Join(cliRoot, Contract)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Ruby lets Errno::EACCES escape here with a backtrace. Naming the file
		// is the same refusal with the machine-readable half removed.
		return "", errorf("cannot read the list-agent contract at %s: %s", path, err)
	}
	return string(data), nil
}

// MemorySection is the delimited memory block, or "" when the sidecar is absent
// or empty. An unreadable, invalid-UTF-8, oversize, or delimiter-carrying file
// is a hard error carrying the path — never a silent skip that would run the
// agent without the user's saved defaults.
func MemorySection(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		// File.exist? is false for anything that cannot be stat'd, including a
		// broken symlink and a path under an unsearchable directory.
		return "", nil
	}
	if !info.Mode().IsRegular() {
		return "", errorf("task-set memory at %s is not a regular file", path)
	}
	// Reject on size BEFORE slurping, so a pathologically large file cannot be
	// read wholesale just to be rejected.
	if size := info.Size(); size > MemoryMaxBytes {
		return "", errorf("task-set memory at %s is %d bytes, over the "+
			"%d-byte budget — trim agent-memory.md", path, size, MemoryMaxBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", errorf("cannot read task-set memory at %s: %s", path, err)
	}
	raw := string(data)

	if !utf8.ValidString(raw) {
		return "", errorf("task-set memory at %s is not valid UTF-8 — fix or remove agent-memory.md", path)
	}
	// The delimiters mark the block as data; a body containing one could escape
	// the fence and pose as trusted prompt text (e.g. from a pulled or cloned
	// sidecar). Reserved lines, same hard-error treatment.
	if strings.Contains(raw, MemoryBegin) || strings.Contains(raw, MemoryEnd) {
		return "", errorf("task-set memory at %s contains a reserved delimiter "+
			"line (%s) — remove it from agent-memory.md", path, MemoryEnd)
	}
	body := rubyStrip(raw)
	if body == "" {
		return "", nil
	}
	return MemoryHeader + "\n" + MemoryBegin + "\n" + body + "\n" + MemoryEnd, nil
}

// rubyStrip is String#strip: ASCII whitespace and NUL, and nothing else.
func rubyStrip(text string) string {
	return strings.Trim(text, " \t\n\v\f\r\x00")
}
