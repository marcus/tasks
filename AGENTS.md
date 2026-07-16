# AGENTS.md — tasks application (coding)

This checkout is the **tasks CLI/TUI/API application** (Ruby). Instructions here are
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
  [`docs/api/openapi.yaml`](docs/api/openapi.yaml),
  [`docs/conventions.md`](docs/conventions.md), [`README.md`](README.md).
- How to add or change CLI commands: skill `tasks-cli-dev`
  (`.claude/skills/tasks-cli-dev/` or `.agents/skills/tasks-cli-dev/`).
- How the list agent uses the CLI (reference only while coding): skill
  `tasks-cli`, and the full contract in `TASK_AGENT.md`.
- Tests: `ruby test/all.rb` (or a focused `ruby test/test_*.rb`). Prefer the
  project’s existing patterns; never test against the user’s real task files.
- The CLI supports Ruby 3.4 and Ruby 4.x and uses endless methods (`def foo(x) = bar(x)`) — valid
  syntax, not a bug.

## CLI/API parity is the default

The CLI and loopback HTTP API are thin adapters over the same
`Tasks::Application` commands and checked query views. When adding or changing
user-visible task behavior, keep the CLI and API semantically equivalent by
default:

- Put shared reads, validation, mutations, locking, undo, revisions, and task
  semantics in `lib/tasks/`, behind `Tasks::Application`; do not reimplement
  domain behavior independently in `bin/tasks` or `lib/tasks/api/`.
- Update both [`docs/cli-spec.md`](docs/cli-spec.md) and
  [`docs/api/openapi.yaml`](docs/api/openapi.yaml) when a capability is exposed
  by both adapters, and add parity tests at the application/adapter boundaries.
- Surface-specific mechanics may differ: CLI fuzzy refs, friendly input and
  terminal output; HTTP stable ids, JSON representations, status codes,
  Host/Origin policy, and ETag preconditions. Preserve those adapter contracts
  without changing the shared outcome.
- An intentional CLI-only or API-only capability needs a specifically discussed
  product or security reason. Record that decision in the relevant spec and,
  when architectural, an ADR or plan; do not let parity drift silently.
- Keep Rack/Puma/OpenAPI dependencies isolated to `bin/tasks-api`,
  `lib/tasks/api/`, and `test/api/`. `bin/tasks`, `bin/tasks-tui`, and
  `ruby test/all.rb` must remain free of web dependencies.

For changes that affect the HTTP surface, run `bundle exec ruby test/api/all.rb`
in addition to the core test suite.

## Task data while coding

**Never hand-edit `tasks.jsonl` or `archive.jsonl`.** If development work also
needs to change task data, use `bin/tasks` (or the absolute path from
`bin/tasks config`). The JSONL store depends on stable ids, DFS pre-order, fixed
key order, and a `meta` header — a hand-edit corrupts it. `bin/tasks check`
audits structural breakage if something was edited out-of-band.
