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
bin/tasks list --proposed  # inert suggestions pending owner approval
bin/tasks list --delegated # handed to a person/agents (--all incl. closed)
bin/tasks list --agent-ready --json # the claimable queue for heartbeat pickup
bin/tasks list --unavailable # timed, inherited, and indefinite unavailability
bin/tasks list --someday   # tasks with their own indefinite On Hold marker
bin/tasks agenda           # dated items, soonest first
bin/tasks next             # NEXT actions grouped by context
bin/tasks quadrants        # Covey 2×2 (see note below); --json adds "quadrant"
bin/tasks inbox            # unprocessed captures
bin/tasks projects         # projects & areas rolled up over open tasks (pj); --json
bin/tasks project show "<ref>"  # one project/area in full (counts, date, body); --json
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
bin/tasks propose "text" --note "why" # inert PROPOSED item for owner review
bin/tasks approve "<ref>"             # accept PROPOSED → INBOX
bin/tasks reject "<ref>" [--note "why"] # decline PROPOSED → CANCELLED (+ rationale)
bin/tasks delegate "<ref>" --to pat@example.com # hand to a person (→ WAITING)
# the address must be real: local@domain.tld — "@work" is refused
bin/tasks delegate "<ref>" research   # offer to agents: refine|research|implement
bin/tasks undelegate "<ref>"         # clear the marker; revokes any live claim
bin/tasks workref "<ref>" <url|off>  # where the work happened; off/none clears
bin/tasks claim "<ref>" --worker <id> --json # atomic single-winner pickup
bin/tasks release "<ref>" --worker <id> --note "blocked: why" # hand a claim back
bin/tasks done "<ref>"               # mark DONE + closed date (cascades to open subtasks)
bin/tasks cancel "<ref>" [--note "why"] # mark CANCELLED + closed date (+ rationale)
bin/tasks due "<ref>" fri            # set/replace deadline (INBOX → TODO)
bin/tasks due "<ref>" "tomorrow 5pm" --timezone Europe/London
bin/tasks schedule "<ref>" +3        # set/replace available-from/start date
bin/tasks undate "<ref>"             # remove dates; --kind deadline|scheduled
bin/tasks state "<ref>" WAITING      # any state; DONE/CANCELLED manage closed
bin/tasks priority "<ref>" A         # A|B|C|none
bin/tasks retitle "<ref>" "new"      # replace the title; tags/state untouched
bin/tasks tag "<ref>" +foo -bar @ctx # add/remove tags & contexts (-@ctx removes)
bin/tasks note "<ref>" "text"        # append a body line under the task
bin/tasks capture "sub" --under "<ref>" # nest a new task below an existing one
bin/tasks move "<ref>" "Section"     # relocate the block under a heading (top-level OR nested project)
bin/tasks move "<ref>" --under "<ref>"  # nest the subtree below another task
bin/tasks move "<ref>" --top         # unnest the subtree back to the section level
bin/tasks move "<ref>" --before "<ref>" # reorder before a sibling (infers its parent)
bin/tasks move "<ref>" --under "<parent>" --before "<sibling>" # reparent at an exact slot
bin/tasks recur "<ref>" weekly       # repeat on done: intervals or calendar days; "off" clears
bin/tasks recur "<ref>" "every mon,wed"  # calendar schedule (see Recurrence below)
bin/tasks recur "<ref>"              # read-only: humanized schedule + next occurrences
bin/tasks recur --explain "<sched>"  # taskless parse/preview — no ref, no store access
bin/tasks defer "<ref>" +4           # hide until available four days from today
bin/tasks someday "<ref>"            # hold indefinitely (someday/maybe/on hold)
bin/tasks activate "<ref>"           # make available now (undefer/resume)
bin/tasks archive                    # sweep DONE/CANCELLED to archive.jsonl
bin/tasks delete "<ref>"             # hard-delete a task (--cascade for subtasks); undoable
bin/tasks project create "New project"  # new empty project under the "Projects" root
bin/tasks project complete "<ref>"   # close every open task in a project (aka done)
bin/tasks project rename "<ref>" "new"  # retitle a project/area section
bin/tasks project archive "<ref>"    # sweep a project's subtree to archive (--force past open tasks)
```

`PROPOSED` is separate from accepted open work. It stays out of the default
list, agenda, next, quadrants, inbox, and project rollups; review it with
`list --proposed` or the TUI Approvals tab. A proposal cannot recur or be
completed. Approval and rejection are undoable lifecycle decisions.

Use `capture` when the user explicitly asks to add, remember, or track a task.
You may use `propose` without asking when agent-initiated follow-up is plausibly
valuable but was not requested. Add concise rationale/evidence with repeatable
`--note`, do not perform the proposed work, and do not flood the queue. A
proposal is not permission to create accepted work, contact anyone, or change
external state. Never approve your own proposal unless the user explicitly
asks you to approve that specific proposal.

**Delegation** answers a different question: who holds the next action on work
the owner has already accepted. An accepted live task may carry one
`delegation` — a person (an email, which moves it to `WAITING`) or the agent
pool at an authority `mode`. `workref` records the one reference to where the
work happened and survives completion and archival.

Delegating is the owner's call: set or clear it when asked, never delegate a
task to yourself, and never widen a mode you were given. `refine` may improve
the task definition only. `research` adds read-only investigation and a durable
brief. `implement` adds changes within the task's named scope, and even then
the target repository's own instructions remain the only authority on commit,
push, and approval gates.

To pick up delegated work: read `list --agent-ready --json`, `claim` one task
(a compare-and-set — exactly one worker wins, and a lost race exits non-zero
naming the holder), take your authority from the task the claim returns, set a
`workref`, then complete it or `release` it with a blocker note. There are no
leases, so an abandoned claim stays claimed until the owner clears it.

Completing a delegated *recurring* task keeps the delegation standing: the next
occurrence carries the same mode or person, always unclaimed and without the
finished cycle's work reference.

**Make a project, then fill it.** To collect tasks into a brand-new project,
`project create "Name"` first (it creates the empty section, bootstrapping the
"Projects" root if needed), then `move "<task-ref>" "Name"` each task in — the
positional section name reaches a nested project, so no manual filing is needed:

```sh
bin/tasks project create "Mid-year Reviews"
bin/tasks move "prep slides" "Mid-year Reviews"   # lands under the new project
```

A **project** ref resolves against `projects`: an 8-hex section id, `L<line>`,
or a title substring across projects and areas (exit 2 on no-match/ambiguous).
`project create` rejects a blank or duplicate title (exit 1); `project complete`
closes the whole open subtree; `project archive` refuses while open tasks remain
unless `--force`. All `project` verbs take `--json`;
the three mutations take `--dry-run`.

`delete` hard-removes a task's subtree from the live file (it never touches the
archive and is not the same as `cancel`). A task with subtasks needs `--cascade`.
Prefer `cancel`/`archive` for normal "done with it" cases; `delete` is for a
true mistake, and `bin/tasks undo` reverses it.

`scheduled` is the task's available-from/start/defer-until value; `deadline` is
its separate due value. A future available-from value hides the task from active
views until its exact boundary. A date without time is all-day; `tomorrow 9am`
is floating in the configured zone; `--timezone Europe/London` makes a value
fixed. `--fold later` selects the later instant in a DST overlap. Times change
task semantics but do not create reminders. Translate "defer TASK 4 days" to `defer "TASK" +4` and
"defer TASK until Friday" to `defer "TASK" fri`: this atomically sets
`scheduled`, clears an own indefinite hold, and never moves `deadline`.

"Someday", "maybe", "on hold", and "indefinitely" mean `someday "TASK"`:
an indefinite On Hold marker with no release date. The backward-compatible
`defer "TASK"` spelling does the same thing, but prefer `someday` when that is
what the user means. `activate` removes the own hold and clears an own future
available-from date. An unavailable ancestor can still block the task. Review
all effective unavailability with `list --unavailable` (`--deferred` alias), or
only tasks carrying their own On Hold marker with `list --someday`.

Recurrence is a `recur` schedule alongside the task's date. The stamp **is** the
next occurrence; the schedule only says how it advances on `done`. Two shapes
share the field:

- **Interval** — `.+1w`, `++1m`, `+2d`: advance by a fixed span. `.+` from
  completion (today + interval), `+` one hop from the stored date (may stay
  overdue), `++` catch-up (repeat until future).
- **Calendar** — `w:mon,wed`, `2w:mon`, `m:15`, `m:last`, `m:2tue`, `m:lastfri`,
  `y:07-04`, `y:11:3thu`: advance to the next matching date. Only two prefixes,
  since "from completion" and "catch-up" coincide here: bare (default) = next
  match after **today**, `+` = next match after the **stored date** (one hop, for
  cadences where every missed occurrence matters). `.+`/`++` here are rejected.

Input is never required to be canonical: `recur`, `capture --recur`, and the API
also take friendly intervals (`weekly`, `2w`, `every 3 days`) and natural phrases
(`every monday`, `weekdays`, `the 15th`, `2nd tuesday`, `last day of the month`,
`every 2 weeks on monday`, `every july 4`), all normalized to one stored spelling.
`off`/`none`/`never` clear it. `--from schedule|completion` is interval-only; with
a calendar schedule it exits 1 pointing at the `+` prefix. Edge rules: `m:31`
clamps in short months, `m:5fri` skips months without one, `y:02-29` clamps to
Feb 28.

**Explain before you commit.** `recur "<ref>"` (no schedule) prints the humanized
schedule and the next `--count N` dates (default 5, max 50) starting from the
task's stamp; `recur --explain "<schedule>"` does it with no ref and no store
access. Both take `--json`. A schedule that parses but could never fire from the
task's stamp — or would roll past four-digit years — is refused at **write** time
with the engine's reason, so check the projection first. (Accepted gap: editing a
date later can strand a calendar schedule; `done` then refuses with a reason, and
`recur "<ref>" off` clears it.)

Completing a recurring task with `done` rolls its date forward and keeps it open
(no `closed`; `- Did [date].` appended) — use `cancel` to actually stop it.
`list --recurring` reviews them; rows show `↻ <humanized>` and JSON carries
`recur` (canonical) plus `recur_human`.

Completing a parent cascades: `done` (or `state … DONE`) closes every open
descendant too (recurring descendants close outright — their cookie is retired),
as one undo step. A recurring parent is the exception — it rolls forward and does
not cascade. `cancel` never cascades; reopening a parent does not reopen its
descendants.

`capture` flags: `--due <date/time>`, `--scheduled <date/time>`, per-field
`--due-timezone`/`--scheduled-timezone`, floating and fold flags, `--priority A|B|C`,
`--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`,
`--project "Heading"`, `--under <ref>`, and repeatable `--note`. A date makes
it land as TODO (override
with `--state`); `--project` files it under that section (default Inbox);
`--under <ref>` nests it below an existing task instead (mutually exclusive with
`--project`).

Nesting is capped at `max_depth` (default 4; `tasks config` shows it). `capture
--under` / `move --under` past the cap fail with a depth message (exit 1) and
write nothing; nesting a task under its own subtree is a cycle (exit 1). Moving
to a section or `move --top` (unnest) is never depth-checked, so it's the escape
hatch for a file already deeper than the cap. `move --top` on an already
top-level task is a harmless no-op.

Use `move "<ref>" --before "<sibling>"` for exact manual ordering; it infers the
sibling's current parent. Add `--under "<parent>"` or a positional section name
to reparent at the same time. The anchor must be a direct child of that explicit
destination. `--before` cannot be combined with `--top`.

Mutations accept `--dry-run` (print, don't write), `--json` (structured
result), and dates in any form: `fri`, `+3`, `07-15`, `2026-07-15`, `today`.

Ref rules: an exact 8-hex stable `id`, a case-insensitive substring of the
title, or `L<line>` for an exact headline line. Zero or multiple matches exit 2
and list candidates as `L<line>: <headline>` — retry with a longer substring or
the `L<line>` ref.
Don't guess between candidates; if the user's request is genuinely ambiguous,
stop and ask, listing the matches.

When the user's prompt includes an exact task `id`, treat it as context for an
existing task unless they explicitly ask to create a separate new task. Resolve
it with `bin/tasks show "<id>"` first, then apply requested changes to that
task; do not capture the prompt as a new task merely because it also contains
task text.

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
full policy is in `TASK_AGENT.md` (Task-set memory); report any change you make to
the file alongside your task changes.

## Report

End with one line listing every change made (the CLI prints resulting
headlines — quote them), including any `agent-memory.md` change. The caller uses
this as the audit trail.
