# AGENTS.md — tasks application (coding)

This checkout is the **tasks CLI/TUI application** (Ruby). Instructions here are
for agents developing this repo — implementing features, fixing bugs, running
tests, updating docs.

Task *data* (`tasks.jsonl`, `archive.jsonl`, `agent-memory.md`) usually lives in
a separate directory or private repo, resolved via `~/.config/tasks/config` /
`TASKS_DIR`. Do not put an `AGENTS.md` in the data directory; list-agent
instructions are not loaded from cwd.

## Two contracts — do not mix them

| File | Audience | Who loads it |
| --- | --- | --- |
| [`AGENTS.md`](AGENTS.md) (this file) | Coding agents in this repo | Cursor / Claude Code workspace rules |
| [`TASK_AGENT.md`](TASK_AGENT.md) | List agents managing GTD data | `tasks -p` and the TUI queue via `Tasks::AgentContext` |

When the user asks you to change the list-agent prompt, edit `TASK_AGENT.md`
(and keep skills/docs in sync). When they ask you to implement application code,
edit source/tests here — do **not** capture a todo instead of doing the work.

## Developing this application

- Spec and architecture: [`docs/cli-spec.md`](docs/cli-spec.md),
  [`docs/conventions.md`](docs/conventions.md), [`README.md`](README.md).
- How to add or change CLI commands: skill `tasks-cli-dev`
  (`.claude/skills/tasks-cli-dev/` or `.agents/skills/tasks-cli-dev/`).
- How the list agent uses the CLI (reference only while coding): skill
  `tasks-cli`, and the full contract in `TASK_AGENT.md`.
- Tests: `ruby test/all.rb` (or a focused `ruby test/test_*.rb`). Prefer the
  project’s existing patterns; never test against the user’s real task files.
- The CLI is Ruby 3.4 and uses endless methods (`def foo(x) = bar(x)`) — valid
  syntax, not a bug.

## Task data while coding

**Never hand-edit `tasks.jsonl` or `archive.jsonl`.** If development work also
needs to change task data, use `bin/tasks` (or the absolute path from
`bin/tasks config`). The JSONL store depends on stable ids, DFS pre-order, fixed
key order, and a `meta` header — a hand-edit corrupts it. `bin/tasks check`
audits structural breakage if something was edited out-of-band.
