---
name: tasks-cli
description: How to read and modify the user's GTD task list (tasks.jsonl) safely. Use whenever asked to view, add, complete, reschedule, prioritize, tag, or otherwise manage tasks in this repo. Always use the CLI — never hand-edit tasks.jsonl.
---

# Working with the task list

The task list lives in `tasks.jsonl` — a JSONL store (one JSON record per line)
that diffs one task per line. **Use `bin/tasks` for every read and write.** The
CLI is the only writer: it keeps the record format correct, enforces the
conventions, validates the file after each write, and rolls back bad writes.
Never hand-edit `tasks.jsonl` — per-record ids, strict DFS ordering, fixed key
order, and the `meta` line 1 make a hand-edit error-prone (`docs/cli-spec.md`
marks each command ✅ implemented / 🚧 planned).

## Read first

```sh
bin/tasks list -a          # everything incl. archive; filters: @ctx +tag /text -A
bin/tasks list --unavailable # timed, inherited, and indefinite unavailability
bin/tasks list --someday   # tasks with their own indefinite On Hold marker
bin/tasks agenda           # dated items, soonest first
bin/tasks next             # NEXT actions grouped by context
bin/tasks quadrants        # Covey 2×2 (see note below); --json adds "quadrant"
bin/tasks inbox            # unprocessed captures
bin/tasks show "<ref>"     # one task in full (fields + notes); --json
bin/tasks check            # is the file structurally sound? (exit 1 = no)
bin/tasks config           # where tasks.jsonl/archive.jsonl resolve + urgent_days; --json
```

Quadrants are computed, not stored: **important** = priority `A`/`B` or the
`important` tag; **urgent** = a `deadline` within `urgent_days` (default 3, overdue
counts) or the `urgent` tag. To push a task toward Q1, set its priority and a near
deadline (`priority`/`due`) — you don't need to add tags.

The task files may live outside this repo (env vars or `~/.config/tasks/config`
can relocate them). If you need the file's path — e.g. before a direct edit —
get it from `bin/tasks config`, don't assume the repo root.

All read commands accept `--json` (flat array, same sort as the text view) —
prefer it when you need to reason over tasks rather than display them.

## Mutate

```sh
bin/tasks capture "text"             # new INBOX item (see flags below)
bin/tasks done "<ref>"               # mark DONE + closed date (cascades to open subtasks)
bin/tasks cancel "<ref>"             # mark CANCELLED + closed date
bin/tasks due "<ref>" fri            # set/replace deadline (INBOX → TODO)
bin/tasks schedule "<ref>" +3        # set/replace available-from/start date
bin/tasks undate "<ref>"             # remove dates; --kind deadline|scheduled
bin/tasks state "<ref>" WAITING      # any state; DONE/CANCELLED manage closed
bin/tasks priority "<ref>" A         # A|B|C|none
bin/tasks retitle "<ref>" "new"      # replace the title; tags/state untouched
bin/tasks tag "<ref>" +foo -bar @ctx # add/remove tags & contexts (-@ctx removes)
bin/tasks note "<ref>" "text"        # append a body line under the task
bin/tasks capture "sub" --under "<ref>" # nest a new task below an existing one
bin/tasks move "<ref>" "Section"     # relocate the block under a top-level heading
bin/tasks move "<ref>" --under "<ref>"  # nest the subtree below another task
bin/tasks move "<ref>" --top         # unnest the subtree back to the section level
bin/tasks recur "<ref>" weekly       # repeat on done: weekly/2w/.+1m; "off" clears
bin/tasks defer "<ref>" +4           # hide until available four days from today
bin/tasks someday "<ref>"            # hold indefinitely (someday/maybe/on hold)
bin/tasks activate "<ref>"           # make available now (undefer/resume)
bin/tasks archive                    # sweep DONE/CANCELLED to archive.jsonl
bin/tasks delete "<ref>"             # hard-delete a task (--cascade for subtasks); undoable
```

`delete` hard-removes a task's subtree from the live file (it never touches the
archive and is not the same as `cancel`). A task with subtasks needs `--cascade`.
Prefer `cancel`/`archive` for normal "done with it" cases; `delete` is for a
true mistake, and `bin/tasks undo` reverses it.

`scheduled` is the task's available-from/start/defer-until date; `deadline` is
its separate due date. A future available-from date hides the task from active
views until that day. Translate "defer TASK 4 days" to `defer "TASK" +4` and
"defer TASK until Friday" to `defer "TASK" fri`: this atomically sets
`scheduled`, clears an own indefinite hold, and never moves `deadline`.

"Someday", "maybe", "on hold", and "indefinitely" mean `someday "TASK"`:
an indefinite On Hold marker with no release date. The backward-compatible
`defer "TASK"` spelling does the same thing, but prefer `someday` when that is
what the user means. `activate` removes the own hold and clears an own future
available-from date. An unavailable ancestor can still block the task. Review
all effective unavailability with `list --unavailable` (`--deferred` alias), or
only tasks carrying their own On Hold marker with `list --someday`.

Recurrence is a `recur` cookie alongside the task's date (`.+1w`, `++1m`, `+2d`).
`recur "<ref>" weekly` (or `2w`, `.+1m`, `every 3 days`) sets it; `off` clears it.
Completing a recurring task with `done` rolls its date forward and keeps it open
(no `closed`) — use `cancel` to actually stop it. `list --recurring` reviews them.

Completing a parent cascades: `done` (or `state … DONE`) closes every open
descendant too (recurring descendants close outright — their cookie is retired),
as one undo step. A recurring parent is the exception — it rolls forward and does
not cascade. `cancel` never cascades; reopening a parent does not reopen its
descendants.

`capture` flags: `--due <date>`, `--scheduled <date>`, `--priority A|B|C`,
`--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`,
`--project "Heading"`, `--under <ref>`. A date makes it land as TODO (override
with `--state`); `--project` files it under that section (default Inbox);
`--under <ref>` nests it below an existing task instead (mutually exclusive with
`--project`).

Nesting is capped at `max_depth` (default 4; `tasks config` shows it). `capture
--under` / `move --under` past the cap fail with a depth message (exit 1) and
write nothing; nesting a task under its own subtree is a cycle (exit 1). Moving
to a section or `move --top` (unnest) is never depth-checked, so it's the escape
hatch for a file already deeper than the cap. `move --top` on an already
top-level task is a harmless no-op.

Mutations accept `--dry-run` (print, don't write), `--json` (structured
result), and dates in any form: `fri`, `+3`, `07-15`, `2026-07-15`, `today`.

Ref rules: case-insensitive substring of the title, or `L<line>` for an exact
headline line. Zero or multiple matches exit 2 and list candidates as
`L<line>: <headline>` — retry with a longer substring or the `L<line>` ref.
Don't guess between candidates; if the user's request is genuinely ambiguous,
stop and ask, listing the matches.

## Never hand-edit the file

`tasks.jsonl` (and `archive.jsonl`) are **CLI-only**. The CLI covers capture,
completion, cancel, state, dates, priority, retitle, tags, notes, moving between
sections, deferral, and recurrence — every mutation you need. Do not open the
JSONL and edit records by hand: each carries a stable id, records sit in a strict
DFS pre-order, keys use a fixed order, and line 1 is a `meta` record, so a manual
edit easily corrupts the store. The CLI writes the exact shape and validates
after every write; use it. Dating an INBOX item promotes it to TODO and marking
DONE/CANCELLED sets the `closed` date automatically — you don't manage those.

If the file was somehow edited out-of-band (not by you), run `bin/tasks check`
and fix whatever it reports before finishing.

## Remembered defaults (`agent-memory.md`)

A task set may carry `agent-memory.md` — a Markdown sidecar of durable,
user-approved defaults (e.g. "garden tasks use `@home`") beside `tasks.jsonl`.
When present, its contents are already injected into your system context; apply
those defaults only where a request clearly falls in scope, and always let the
current request override them. Find its resolved path with `bin/tasks config`
(it can be relocated by the `TASKS_MEMORY` env var or the config `memory` key).

Unlike `tasks.jsonl`, this sidecar is edited **directly** — it's plain Markdown,
not a CLI store. Create, change, or remove a rule only when the user explicitly
asks to remember / forget / change a default, editing minimally in the right
section (create the file from its template on the first such request). Never
infer a default from task edits, and never store secrets or transient facts. The
full policy is in `AGENTS.md` (Task-set memory); report any change you make to
the file alongside your task changes.

## Report

End with one line listing every change made (the CLI prints resulting
headlines — quote them), including any `agent-memory.md` change. The caller uses
this as the audit trail.
