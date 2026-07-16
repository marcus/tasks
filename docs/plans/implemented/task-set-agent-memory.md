# Plan: task-set agent memory

Status: implemented (2026-07-14)

## Goal

Let an agent retain explicit, user-approved defaults for one task set, so a
request such as “water the garden, and remember garden tasks use `@home`” both
captures the task and makes that default available to every later agent run.
The defaults must travel with the task set, be reviewable in Git, and work the
same way from `tasks -p` and the TUI.

This is durable preference memory, not a record of prior conversations and not
an autonomous learning system. An agent may add or change it only when the user
explicitly asks to remember, forget, or change a default.

## Recommended design

Store the memory in a human-authored Markdown sidecar named
`agent-memory.md`, next to the resolved `tasks.jsonl`:

```text
my-private-tasks/
├── tasks.jsonl
├── archive.jsonl
└── agent-memory.md
```

This makes the task-set directory, rather than this application checkout or a
machine-global configuration directory, the ownership boundary. A private task
repository can commit all three files together; cloning or switching task sets
brings the right defaults with it.

`agent-memory.md` is deliberately Markdown rather than a second structured
store. It is easy for a person to read and edit in Git, can explain exceptions,
and lets an agent preserve a useful reason alongside the rule. `tasks.jsonl`
and `archive.jsonl` remain CLI-only stores; this sidecar does not alter that
constraint.

### Initial document shape

The file is optional and absent files mean “no saved defaults.” On its first
explicit memory request, the agent creates this small template:

```markdown
# Task-set agent memory

User-approved, durable defaults for agents managing this task set.
Current request instructions override these defaults. Keep entries concise.

## Defaults

- Garden-related tasks: add the `@home` context.

## Notes and exceptions

<!-- Add narrow exceptions or rationale here when needed. -->
```

The agent should make a minimal edit in the appropriate section instead of
rewriting the file. Markdown headings are a convention for people and agents,
not an application parser or schema.

## Resolution and prompt contract

Add `memory` to `Tasks::Config::Paths` and resolve it independently of the
application source tree:

| Precedence | Location | Purpose |
| --- | --- | --- |
| 1 | `TASKS_MEMORY` | One-run or test override. |
| 2 | `memory = /path/to/agent-memory.md` in the tasks config | Intentional nonstandard location. |
| 3 | `agent-memory.md` beside the resolved `tasks.jsonl` | Normal per-task-set default. |

Deriving the default from the final `tasks.jsonl` path is important: a
`TASKS_FILE` override must select its sibling memory even if the archive or
base directory comes from somewhere else. Note this means the default cannot
reuse the existing `Config.pick` helper, which defaults from the resolved
*directory*; memory must derive from `File.dirname(org)` after `org` is fully
resolved. Mechanically: add `memory` to `PATH_KEYS` so the config value gets
`~`/relative expansion like `dir`/`file`/`archive`, add `memory` and its source
to the `Paths` struct and `sources` map, and have `Tasks::Config.for_dir` pin
`agent-memory.md` in the sandbox with source `"pinned"`. `tasks config` should
display the resolved memory path, its source, and whether the file exists; its
JSON output should expose the same data.

Extract the current duplicated system-prompt assembly from `bin/tasks`
(`cmd_prompt`) and `Tui::App.agent_system` into a small shared builder (for
example, `Tasks::AgentContext.build`). This extraction is also the natural
point to retire the deprecated `Paths#claude_context` alias, which was kept
for one release. The builder will assemble, in this order:

1. The repository `AGENTS.md` contract.
2. Absolute paths for the CLI, task files, archive, and memory sidecar.
3. A short policy for interpreting and changing task-set memory.
4. The current contents of `agent-memory.md`, clearly delimited as
   user-approved task-set defaults, when the file exists and is nonempty.

Recommendation for item 3: put the policy prose in a section of `AGENTS.md`
itself rather than a string literal in the builder. The policy is generic to
every task set, `AGENTS.md` is already the versioned, reviewable agent
contract, and a second prose source embedded in Ruby would drift. The builder
then only contributes paths and the memory file's contents.

The builder must read the memory immediately before each agent is started—not
once per TUI session. Today `Tui::App#initialize` builds `@sys_prompt` once and
every `build_agent` call reuses it; that memoization must go. This matters for
the queued TUI: a first request may save a default and a later queued request
should see it. It also means an external Git pull or a human edit is picked up
on the next request without restarting the TUI.

## Agent behavior

The injected policy should state the following rules plainly:

- Apply a saved default only when the new task clearly falls in its stated
  scope. For example, garden tasks receive `@home`; “call Garden State Bank”
  does not.
- The current user request wins over memory. A direct “do not add a context” or
  an explicit different context overrides a saved default for that request.
- More specific, non-conflicting saved rules refine general rules. Conflicting
  saved rules, or a request whose relevance is unclear, require clarification
  rather than a guessed durable change.
- Create, edit, or remove memory only after an explicit request such as
  “remember,” “always,” “by default,” “forget,” or “change that rule.” Do not
  infer a new preference from one or many task edits.
- Record stable task-management preferences only—contexts, tags, projects,
  filing rules, recurrence preferences, and narrowly stated task wording
  conventions. Do not store credentials, tokens, private facts unnecessary for
  task creation, transient deadlines, or a transcript of the interaction.
- Report any memory-file mutation alongside task-file mutations, including the
  exact rule added, changed, or removed.

The existing capture-by-default policy still governs what the agent does with
the task itself. Memory merely supplies defaults while performing that requested
task-list operation; it never authorizes underlying work.

## Changes by slice

### 1. Path resolution and context builder

- Extend `lib/tasks/config.rb` with the resolved `memory` path and source.
- Add the shared context builder and make both `cmd_prompt` and `Tui::App` use
  it.
- Include the memory path in the existing absolute-path context so an agent can
  create or edit the right file without guessing its location.
- Build the context lazily for every request. In the TUI, make the agent factory
  construct context when the queue starts an item, not when the app boots or
  when an item is submitted. The current queue builds its adapter at submission
  time (`AgentQueue#enqueue` calls the factory and checks `available?`) and
  stores the live adapter on the queued `Item` for `start_next` to reuse;
  preserve the immediate-rejection UX with a lightweight availability probe at
  enqueue, then drop the adapter from `Item` (keep the frozen entry snapshot)
  and call the factory inside `start_next` so each run gets fresh context.
  `start_next`'s existing second availability check stays.

### 2. Auditing and documentation

- Extend `maybe_show_diff` to include the resolved memory file, so `tasks -p`
  presents both captured-task and remembered-default changes in its Git diff.
  Caveat: `maybe_show_diff` diffs inside the task-data directory's repo, so a
  memory file relocated by `TASKS_MEMORY` or the config `memory` key may sit
  outside that work tree. Include the memory path in the diff only when it
  lives under the same repo; otherwise print a one-line notice that the memory
  file at `<path>` changed, rather than silently omitting the mutation.
- Document the sidecar, precedence, creation behavior, and example in
  `README.md` and `docs/cli-spec.md`.
- Propagate to the agent-facing docs, per this repo's convention that a change
  isn't done until agents can find it: the memory policy section in
  `AGENTS.md` (see the builder recommendation above) and a short entry in
  `.claude/skills/tasks-cli/SKILL.md` teaching agents that the sidecar exists,
  where it resolves, and the edit rules.
- Add an example `examples/agent-memory.md`; do not add a real task-set memory
  file to this code repository because the root task data is intentionally
  ignored.

### 3. Guardrails and usability

- Start with direct Markdown edits by agents—no new command grammar is needed
  to prove the workflow. Keep a future `tasks memory show|init` command out of
  the MVP unless real usage shows direct editing is hard to discover.
- Add a modest, documented size budget (proposed: 16 KiB UTF-8) before prompt
  injection. If it is exceeded, fail the agent request with the path and a
  clear cleanup instruction rather than silently truncating a default.
- Treat unreadable or invalid-UTF-8 memory as a clear request error; never drop
  it silently and run without user defaults. In the CLI this is an abort with
  the path and reason; in the TUI it must surface as a failed queue event
  carrying the same message (the `start_next` failure path already exists),
  never a crash or a silent run.
- Do not automatically create a file just because an agent runs. Create it only
  while fulfilling an explicit request to remember a default.

## Test plan

- Config unit tests: default follows the final `tasks.jsonl`; config and
  `TASKS_MEMORY` overrides win in the stated order; `for_dir` stays hermetic;
  `tasks config --json` reports the result.
- Context-builder unit tests: no file omits the section; a valid file is
  delimited and included; malformed/unreadable/oversize files fail clearly;
  absolute paths are complete.
- CLI integration tests: the launched adapter receives the default, and the
  displayed Git diff includes an `agent-memory.md` edit.
- TUI/queue tests: two sequential queued requests construct separate real
  adapters at start time; a memory edit between them appears only in the second
  request’s system context, while immediate unavailable-provider rejection and
  provider/model snapshot behavior remain unchanged.
- Regression tests: ordinary `capture`, `list`, and non-agent TUI flows neither
  create nor depend on the memory file.
- Eval-harness hermeticity: `eval/llm/harness.rb` sandboxes runs via
  `TASKS_FILE`/`TASKS_ARCHIVE`, so sibling-derived memory already resolves
  inside the sandbox; confirm a memoryless sandbox omits the memory section
  and the harness needs no `TASKS_MEMORY` of its own — and never picks up the
  developer's real sidecar.

## Acceptance examples

1. With the example rule saved, `tasks -p "water the garden"` creates a garden
   task tagged `@home` without needing the context repeated.
2. `tasks -p "water the garden; remember garden tasks use @home"` creates the
   task, creates or minimally updates `agent-memory.md`, and prints a diff that
   includes both files.
3. `tasks -p "water the garden at the community plot; do not add a context"`
   obeys the current request and leaves the durable rule intact.
4. A separate task repository with its own `agent-memory.md` never receives the
   garden default from this one.
5. Opening the TUI once, saving a default in one request, then submitting a
   second garden request uses that new default without a restart.

## Rollout and decision checkpoints

Implement slice 1 behind the optional-file behavior, then slice 2 and its
tests. Try the acceptance examples against a disposable Git-backed task set
before updating a real personal task repository. After that trial, decide
whether the direct-edit UX is sufficient or warrants a small `tasks memory`
command family. Record the accepted storage and override decision in a proposed
ADR (next in sequence: 0009) before treating the Markdown filename and
precedence as a long-term public contract. The v1 HTTP API contract
(`docs/api/openapi.yaml`) does not expose config or memory; keep the sidecar
out of that surface until the ADR settles the contract.

## Open questions for the first trial

- Is `agent-memory.md` the preferred public filename, or would
  `task-defaults.md` better communicate that this is scoped preference memory?
- Should a user be able to disable injection explicitly (`memory = off`), or is
  pointing `TASKS_MEMORY` at an empty file enough for the first version?
- After real use, should memory rules gain optional machine-readable metadata
  (for example, a scope tag), or is disciplined Markdown materially clearer?
