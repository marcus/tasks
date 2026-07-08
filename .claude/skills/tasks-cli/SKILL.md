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
bin/tasks list --deferred  # only deferred (someday/maybe) tasks — a review list
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
bin/tasks done "<ref>"               # mark DONE + closed date
bin/tasks cancel "<ref>"             # mark CANCELLED + closed date
bin/tasks due "<ref>" fri            # set/replace deadline (INBOX → TODO)
bin/tasks schedule "<ref>" +3        # set/replace scheduled (INBOX → TODO)
bin/tasks undate "<ref>"             # remove dates; --kind deadline|scheduled
bin/tasks state "<ref>" WAITING      # any state; DONE/CANCELLED manage closed
bin/tasks priority "<ref>" A         # A|B|C|none
bin/tasks retitle "<ref>" "new"      # replace the title; tags/state untouched
bin/tasks tag "<ref>" +foo -bar @ctx # add/remove tags & contexts (-@ctx removes)
bin/tasks note "<ref>" "text"        # append a body line under the task
bin/tasks move "<ref>" "Section"     # relocate the block under a top-level heading
bin/tasks recur "<ref>" weekly       # repeat on done: weekly/2w/.+1m; "off" clears
bin/tasks defer "<ref>"              # hide as someday/maybe (adds defer tag)
bin/tasks activate "<ref>"           # bring a deferred task back (undefer/resume)
bin/tasks archive                    # sweep DONE/CANCELLED to archive.jsonl
```

Deferral is a semantic `defer` tag (like `important`/`urgent`): a deferred
task keeps its state but drops out of `agenda`/`next`/`quadrants`/`inbox` and the
default `list` until you `activate` it. Review the backlog with `list --deferred`.

Recurrence is a `recur` cookie alongside the task's date (`.+1w`, `++1m`, `+2d`).
`recur "<ref>" weekly` (or `2w`, `.+1m`, `every 3 days`) sets it; `off` clears it.
Completing a recurring task with `done` rolls its date forward and keeps it open
(no `closed`) — use `cancel` to actually stop it. `list --recurring` reviews them.

`capture` flags: `--due <date>`, `--scheduled <date>`, `--priority A|B|C`,
`--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`,
`--project "Heading"`. A date makes it land as TODO (override with `--state`);
`--project` files it under that section (default Inbox).

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

## Report

End with one line listing every change made (the CLI prints resulting
headlines — quote them). The caller uses this as the audit trail.
