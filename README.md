# tasks

A plain-text, Claude co-managed task system. Org-mode-inspired format, GTD workflow,
Covey Important/Urgent matrix. Ruby tooling on top.

## Layout

```
gtd.org            The one file that matters — you provide this (see below).
examples/gtd.org   A tiny sample; copy it to get started.
docs/conventions.md  The format + methodology spec (read this).
bin/tasks          Ruby CLI for querying gtd.org (stdlib only, no gems needed).
bin/tasks-tui      Interactive TUI: live views, single-key actions, agent prompt.
lib/tui/           TUI modules (store, views, frame, app loop).
lib/llm/           LLM agent adapters (Claude CLI, Hermes) behind one protocol.
test/              Minitest suite — run with `ruby test/all.rb`.
```

## Quick start

```sh
tasks agenda            # (a) what's due / scheduled, soonest first
tasks next              # (n) next actions grouped by context (@computer, @email, …)
tasks quadrants         # (q) Covey Important/Urgent 2x2
tasks inbox             # (i) unprocessed captures
tasks list              # (l) all tasks grouped by state, with filters (see below)
tasks capture "..."     # (c) append a new item to the Inbox
tasks done "..."        # (d) mark a matching open item DONE
tasks links             # links in task notes, by system (slack, jira, …)
tasks open "..."        # (o) open a task's link in the browser
tasks undo              # revert the last mutation (redo mirrors it)
tasks archive           # (x) sweep DONE/CANCELLED items into archive.org
tasks -p "..."          # hand a request to an LLM agent — it acts and reports back
```

Every command has the single-letter alias shown in parentheses (`tasks n`, `tasks x`, …).
`tasks` itself is aliased to `bin/tasks` in `~/.zshrc`.

## Where your tasks live

Your tasks live in a `gtd.org` (plus `archive.org`) that you own — keep it
wherever you like; the data is fully separable from the code. Point the tooling
at it:

```sh
mkdir -p ~/tasks && cp examples/gtd.org ~/tasks/gtd.org   # seed from the sample
mkdir -p ~/.config/tasks
echo "dir = ~/tasks" > ~/.config/tasks/config
tasks config          # shows the resolved paths and where each came from
```

If you set nothing, the tooling falls back to the repo root.

Resolution order (both CLI and TUI): `TASKS_ORG`/`TASKS_ARCHIVE` env vars,
then `TASKS_DIR`, then the config file (`dir = …`, or per-file `org = …` /
`archive = …`), then the repo root. Env vars make one-off sandboxes easy:
`TASKS_ORG=/tmp/scratch.org tasks capture "test"`.

### Filtering with `list`

```sh
tasks list                     # open items only (default)
tasks list -d                  # done items still in gtd.org
tasks list -x                  # archived items
tasks list -a                  # everything, both files
tasks list @computer -A /denver  # compose: context, priority, text — all at once
```

Scope flags: `--open/-o` (default) `--done/-d` `--archived/-x` `--all/-a`.
Filter sigils: `@context`  `/text` (or a bare word)  `+tag`  `-A|-B|-C` (priority).

## Working with an agent

An LLM agent can read and edit `gtd.org` directly — add captures to the Inbox,
process items into lists, suggest next actions, and surface what matters. The
plain-text format means every change is a reviewable git diff.

**From the terminal**, `tasks -p "..."` hands a natural-language request to an
autonomous agent with `AGENTS.md` as context. It acts on your tasks right where
you're working, auto-applies changes, and prints a git diff of the task files
plus a one-line summary of what it did:

```sh
tasks -p "close the Drew review task and push the Denver flight deadline to next Friday"
tasks -p --provider hermes "capture: renew passport"   # a local Ollama-backed harness
```

The agent is a pluggable **harness**: by default the local `claude` CLI, but any
configured backend — e.g. the [Hermes agent](https://hermes-agent.nousresearch.com)
driving a local Ollama model — works the same way. Pick the default and add
models in `~/.config/tasks/config`; see `docs/cli-spec.md` (LLM agent settings).
`AGENTS.md` documents the org format and conventions so any agent stays consistent.

## TUI

`bin/tasks-tui` is a full-screen interactive view over the same file (stdlib only,
like everything else). The views update live when `gtd.org` changes — whether you,
Claude, or another process edited it.

```
1-4 / ←→   switch view: Agenda · Next · Quadrants · Inbox (arrows cycle)
↑↓ / jk    select a task (also flips tasks inside a detail modal)
return     task detail modal
?          keyboard shortcuts modal
c          mark selected task DONE
d          reschedule — accepts fri, +3, 07-15, 2026-07-15, today, tomorrow
r          recur — weekly · 2w · .+1m · off
z / Z      defer (someday/maybe) the selected task / show-hide deferred
J / K      lower / raise priority (A ↔ B ↔ C ↔ none)
/          filter tasks by text (live; enter keeps it, esc clears, / edits)
u / ctrl-r undo / redo (persistent journal, shared with the CLI's `tasks undo`)
o          open the selected task's link in the browser
y / Y      yank task ref / full task as markdown to the clipboard
p          paste a quoted task ref into the agent prompt
x          archive sweep (move DONE/CANCELLED to archive.org)
M          cycle the agent/model (provider:model shown in the header)
tab or :   focus the agent prompt — natural-language CRUD on your tasks
esc        dismiss the response / cancel a running request / close modal
pgup/pgdn  scroll a long response (footer grows, then collapses on esc)
q          quit
```

The agent runs asynchronously (the local `claude` CLI by default, same as
`tasks -p`, or any configured harness), so the UI stays responsive while it
works; its answer appears in an expanding footer pane and the views refresh with
whatever it changed. `M` switches backend/model between requests.

## Roadmap / ideas

Near-term:

- Weekly-review helper (empty inbox, flag projects with no NEXT).
- Optional gem-based version if we outgrow stdlib parsing.

The full backlog of feature ideas lives in [`docs/ideas.md`](docs/ideas.md).
