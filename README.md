<p align="center">
  <img src="docs/assets/logo.png" alt="tasks logo" width="220">
</p>

<h1 align="center">tasks</h1>

<p align="center">
  <strong>A plain-text GTD task system built for human and AI co-working.</strong>
</p>

<p align="center">
  <a href="#three-flexible-interfaces">CLI</a> •
  <a href="#three-flexible-interfaces">TUI</a> •
  <a href="#three-flexible-interfaces">HTTP API</a> •
  <a href="#feature-comparison">Feature Matrix</a> •
  <a href="docs/technical-architecture.md">Technical Architecture</a>
</p>

---

![tasks TUI Dashboard](docs/assets/screenshot-tui.png)

```sh
tasks agenda            # (a) available dated work, soonest first
tasks next              # (n) next actions grouped by context (@computer, @email, …)
tasks quadrants         # (q) Covey Important/Urgent 2x2
tasks inbox             # (i) unprocessed captures
tasks list              # (l) all tasks grouped by state, with filters
tasks capture "..."     # (c) append a new item to the Inbox
tasks propose "..." --note "why" # inert suggestion pending your approval
tasks list --proposed   # review pending agent proposals
tasks approve "<ref>"   # accept a proposal into Inbox
tasks reject "<ref>"    # decline a proposal into Cancelled
tasks done "..."        # (d) mark a matching open item DONE
tasks links             # links in task notes, by system (slack, jira, …)
tasks open "..."        # (o) open a task's link in the browser
tasks undo              # revert the last mutation (redo mirrors it)
tasks archive           # (x) sweep DONE/CANCELLED items into archive.jsonl
tasks -p "..."          # hand a request to an LLM agent — it acts and reports back
```

Every command has a single-letter alias (`tasks n`, `tasks x`, …). `tasks` itself can be aliased to `bin/tasks` in your shell configuration.

---

## Hero Showcase: CLI & Agent Workflow

![tasks CLI Showcase](docs/assets/screenshot-cli.png)

---

## Three Flexible Interfaces

`tasks` provides three decoupled interface tiers designed to fit any workflow:

### 1. The CLI (`bin/tasks`)
Built for terminal speed and scripting. Features single-letter aliases (`n`, `q`, `i`, `c`, `d`, `o`, `x`, `p`), fuzzy date parsing (`aug 1`, `next friday`, `in 2 weeks`), JSON output mode (`--json`), and direct LLM agent invocation via `tasks -p`.

### 2. The Interactive TUI (`bin/tasks-tui`)
A full-screen, keyboard-driven dashboard built on `TermForm`. Features live file watching (instantly updates when you, the CLI, or an LLM agent modifies tasks), live undo/redo, side-by-side detail panels, mouse navigation, theme presets (`dracula`, `nord`, `catppuccin-mocha`, `tokyonight-night`), and zero web dependencies.

### 3. The Local REST API (`bin/tasks-api`)
A loopback HTTP server exposing full OpenAPI 3.1 endpoints ([`docs/api/openapi.yaml`](docs/api/openapi.yaml)) under `/api/v1`. Enables custom scripts, browser extensions, and external integrations with strict loopback isolation, header safety, and optimistic `If-Match` ETag concurrency control.

---

## Feature Comparison

Comparing `tasks` against traditional task managers across both classic GTD capabilities and modern agentic features:

### Traditional Task Management Features

| Feature / Capability | **tasks** | Org-mode | OmniFocus | Things 3 | Taskwarrior |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **Natural Language Date Input** | ✅ | ⚠️ (Emacs) | ✅ | ✅ | ⚠️ |
| **Subtasks & Project Hierarchy** | ✅ | ✅ | ✅ | ✅ | ⚠️ |
| **Contexts & Tagging (`@context`, `+tag`)** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Interactive Detail / Inspector View** | ✅ | ⚠️ | ✅ | ✅ | ❌ |
| **Offline-First & Fast Boot** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Deep Link & URL Integration** | ✅ | ✅ | ✅ | ✅ | ⚠️ |
| **Advanced Recurring Task Rules** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Multi-Device Synchronization** | ✅ (Git driver) | ⚠️ (Manual Git) | ✅ (OmniSync) | ✅ (Things Cloud) | ⚠️ (Taskserver) |

### Agentic & Modern Developer Features

| Feature / Capability | **tasks** | Org-mode | OmniFocus | Things 3 | Taskwarrior |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **Plain-Text JSONL Storage (1-Line Git Diffs)** | ✅ | ❌ (Org format) | ❌ | ❌ | ❌ |
| **Native LLM Agent Protocol & CLI (`tasks -p`)** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Atomic Agent Task Delegation & Claiming** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Inert Agent Proposals (`tasks propose`)** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Durable Agent Memory (`agent-memory.md`)** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Keyboard-Driven Terminal Dashboard (TUI)** | ✅ | ⚠️ (Emacs) | ❌ | ❌ | ⚠️ (Third-party) |
| **Single-Letter Command Aliases** | ✅ | ❌ | ❌ | ❌ | ⚠️ |
| **Local REST API + OpenAPI 3.1 Contract** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Content-Addressed Undo/Redo Journal** | ✅ | ⚠️ | ❌ | ❌ | ⚠️ |
| **Field-Aware Git 3-Way Merge Driver** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Zero Web Dependencies (CLI & TUI)** | ✅ | ✅ | ❌ | ❌ | ✅ |

---

## Core Architecture & Design Philosophy

For complete details on storage semantics, atomic swaps, and domain layer separation, see [`docs/technical-architecture.md`](docs/technical-architecture.md).

- **One File, One Line Per Task**: Data lives in `tasks.jsonl`. Each record is a single JSON line with stable IDs and fixed key ordering. Tree structure uses explicit `parent` pointers instead of indentation, making outline-walking bugs structurally impossible.
- **Writes You Can Trust**: All writes use atomic swaps (temp file, `fsync`, atomic rename, parent dir `fsync`) following symlinks and preserving file permissions.
- **Journaled Undo/Redo**: Mutations persist to `$XDG_STATE_HOME/tasks/journal/`. The CLI and TUI share one unified history that survives shell restarts.
- **Layered Seam**: `Tasks::Store` owns file operations; `Tasks::Application` serves immutable read snapshots; CLI, TUI, and HTTP API sit cleanly on top as thin adapters.

---

## Quickstart & Task Location

Your tasks live in `tasks.jsonl` (and `archive.jsonl`). Point the tooling at your task directory:

```sh
mkdir -p ~/tasks && cp examples/tasks.jsonl ~/tasks/tasks.jsonl   # seed from sample
mkdir -p ~/.config/tasks
echo "dir = ~/tasks" > ~/.config/tasks/config
tasks config          # shows resolved paths and configuration sources
```

Resolution order: `TASKS_FILE`/`TASKS_ARCHIVE` env vars -> `TASKS_DIR` -> `~/.config/tasks/config` -> repo root.

### Configuration (`~/.config/tasks/config`)

```ini
timezone = America/Los_Angeles
time_format = 12
date_order = mdy

# Map hostnames to context filters
host_context.marcus-home.local = @home
host_context.work-mbp = @work
```

---

## Multi-Device Git Synchronization

Tasks carry an `updated` timestamp formatted as `2026-07-16T14:03:11Z#device`. Set up a private git repo and install the bundled 3-way merge driver:

```sh
printf 'tasks.jsonl merge=tasksjsonl\narchive.jsonl merge=tasksjsonl\n' >> ~/tasks/.gitattributes
bin/install-merge-driver ~/tasks
```

See [Set up multi-device Git sync](docs/multi-device-sync.md) for the full multi-device guide.

---

## Agentic Features & Working with LLMs

### Autonomous Task Execution (`tasks -p`)

`tasks -p "..."` passes natural language instructions to an AI agent with [`TASK_AGENT.md`](TASK_AGENT.md) context. The agent performs task operations and returns a summary alongside git diffs:

```sh
tasks -p "close the Drew review task and push the Denver flight deadline to next Friday"
tasks -p "defer the Fox task four days"
tasks -p --provider hermes "capture: renew passport"
```

### Inert Proposals (`tasks propose`)

Agents can suggest changes without committing them. `tasks propose` creates a `PROPOSED` task that remains hidden from active work views until you approve or reject it:

```sh
tasks list --proposed
tasks approve <ref>
tasks reject <ref>
```

### Delegation & Worker Claiming

Hand off tasks to human collaborators or agent workers:

```sh
tasks delegate 4f2a --to pat@example.com      # -> WAITING state
tasks delegate 4f2a research                  # make agent-ready at authority level
tasks list --agent-ready --json               # query claimable agent queue
tasks claim 4f2a --worker claude-code/313cf82e --json
tasks release 4f2a --worker ... --note "blocked: needs API access"
tasks undelegate 4f2a                         # revoke delegation
```

### Agent Memory (`agent-memory.md`)

Agents can store opt-in defaults in `agent-memory.md` alongside your tasks:

```sh
tasks -p "water the garden; remember garden tasks use @home"
# Agent captures the task AND saves the rule to agent-memory.md
```

---

## Interactive TUI Overview

Launch `bin/tasks-tui` for the full-screen terminal dashboard:

```
1-6 / ←→   switch view: Agenda · Next · Quadrants · Projects · Outline · Inbox
a / r      approve / reject selected proposal (Inbox approval section)
↑↓ / jk    select task; opens read-only detail panel
h / l      collapse / expand selected subtree
> / <      indent / outdent subtree (Outline view)
return     open task detail panel; e edits in place
c d r      complete · reschedule deadline · recur (weekly, 2w, every mon,wed, m:15, off)
z Z J K    defer (date/time/someday/now) · show unavailable · lower / raise priority
/          live text filter (enter commits, esc clears)
u ctrl-r   undo / redo via shared journal
o y p      open task link · yank stable id / markdown · paste id into agent prompt
x          archive sweep with preview counts
:          action palette
tab        focus agent prompt
```

The final Inbox tab is one intake workspace with an **Approvals** section first
and accepted **Inbox** captures second. Its tab label always reports the two
filtered counts in that order (`Inbox 4 | 2`, including zeroes).
Context filters and text search scope both sections; `Z` changes only Inbox
availability. Proposal rows keep their inert `PROPOSED` state and are the only
rows where `a`/`r` approve or reject.

---

## Local HTTP API

Boot the local REST API server:

```sh
bundle install
bin/tasks-api                 # http://127.0.0.1:4747
curl http://127.0.0.1:4747/healthz
curl http://127.0.0.1:4747/api/v1/tasks
```

Refer to [`docs/api/openapi.yaml`](docs/api/openapi.yaml) for full endpoint definitions.

---

## Development & Verification

```sh
ruby test/all.rb
bundle install
bundle check
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

- Core test suite (`ruby test/all.rb`) runs without web dependencies.
- HTTP API suite (`bundle exec ruby test/api/all.rb`) tests OpenAPI compliance and Puma integration.
- Architecture decisions are recorded in [`docs/adr/`](docs/adr), command specs in [`docs/cli-spec.md`](docs/cli-spec.md), and ideas in [`docs/ideas.md`](docs/ideas.md).

---

## License

[MIT](LICENSE).
