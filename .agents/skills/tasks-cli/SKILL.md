---
name: tasks-cli
description: How to read and modify the user's GTD task list (tasks.jsonl) safely. Use whenever asked to view, add, complete, reschedule, prioritize, tag, or otherwise manage tasks in this repo. Always use the CLI — never hand-edit tasks.jsonl.
---

# Working with the task list

The task list lives in `tasks.jsonl` — a JSONL store (one JSON record per line)
that diffs one task per line. **Use `tasks` for every read and write.** The
CLI is the only writer: it keeps the record format correct, enforces the
conventions, validates the file after each write, and rolls back bad writes.
Never hand-edit `tasks.jsonl` — per-record ids, strict DFS ordering, fixed key
order, and the `meta` line 1 make a hand-edit error-prone (`docs/cli-spec.md`
marks each command ✅ implemented / 🚧 planned).

## Read first

```sh
tasks list -a          # everything incl. archive; filters: @ctx +tag /text -A
tasks list --proposed  # inert suggestions pending approval, ranked priority then due
tasks list --rejected  # proposals declined in the last 30 days, newest first
tasks list --delegated # handed to a person/agents (--all incl. closed)
tasks list --agent-ready --json # the claimable queue for heartbeat pickup
tasks list --unavailable # timed, inherited, and indefinite unavailability
tasks list --someday   # tasks with their own indefinite On Hold marker
tasks agenda           # dated items, soonest first
tasks next             # NEXT actions grouped by context
tasks quadrants        # Covey 2×2 (see note below); --json adds "quadrant"
tasks inbox            # unprocessed captures
tasks show "<ref>"     # one task in full (fields + notes); --json
tasks check            # is the file structurally sound? (exit 1 = no)
tasks config           # where tasks.jsonl/archive.jsonl resolve + urgent_days; --json
```

Quadrants are computed, not stored: **important** = priority `A`/`B` or the
`important` tag; **urgent** = a `deadline` within `urgent_days` (default 3, overdue
counts) or the `urgent` tag. To push a task toward Q1, set its priority and a near
deadline (`priority`/`due`) — you don't need to add tags.

The task files may live outside this repo (env vars or `~/.config/tasks/config`
can relocate them). If you need the file's path — e.g. before a direct edit —
get it from `tasks config`, don't assume the repo root.

All read commands accept `--json` (flat array, same sort as the text view) —
prefer it when you need to reason over tasks rather than display them.

**Every command answers in JSON** except `-p` (its result is an LLM transcript)
and the internal `merge-driver`. `tasks help --json` prints that table
itself — every command, its aliases, whether it takes `--json`, and the stated
reason when it does not — so you never have to guess whether a command is
scriptable.

Refusals are a different story: **branch on the exit code, not on stdout.**
A nonzero exit means the command refused, and stdout is often empty. `claim`,
`release`, `delegate`, `archive`, `undo`, `redo`, and `open` additionally print
an error object (`{"error", "action", "message"}`) you can branch on; most other
refusals print prose to stderr only. Never read an empty stdout as success.
A `lock timeout after … held by pid …` refusal means another `tasks` process
holds the store; retry. The leftover `.tasks.jsonl.lock` sidecar is not a lock.

## Mutate

```sh
tasks capture "text"             # new INBOX item (see flags below)
tasks capture "text" --link <url> --label "thread" # link filed in the same write
tasks propose "text" --note "why" --link <url> # inert PROPOSED item + its context
tasks approve "<ref>"             # accept PROPOSED → INBOX
tasks approve "<ref>" --done      # accept AND complete it (one undo step)
tasks reject "<ref>" [--note "why"] # decline PROPOSED → CANCELLED (+ rationale)
tasks unreject "<ref>"            # restore a declined proposal → PROPOSED, same id
tasks delegate "<ref>" --to pat@example.com # hand to a person (→ WAITING)
# the address must be real: local@domain.tld — "@work" is refused
tasks delegate "<ref>" research   # offer to agents: refine|research|implement
tasks undelegate "<ref>"         # clear the marker; revokes any live claim
tasks workref "<ref>" <url|off>  # where the work happened; off/none clears
tasks claim "<ref>" --worker <id> --json # atomic single-winner pickup
tasks release "<ref>" --worker <id> --note "blocked: why" # hand a claim back
tasks done "<ref>"               # mark DONE + closed date (cascades to open subtasks)
tasks cancel "<ref>" [--note "why"] # mark CANCELLED + closed date (+ rationale)
tasks due "<ref>" fri            # set/replace deadline (INBOX → TODO)
tasks due "<ref>" "tomorrow 5pm" --timezone Europe/London
tasks defer "<ref>" "two weeks from now" # also: in two weeks, 2w
tasks due "<ref>" "in two minutes" # timed; second/minute/hour phrases use now
tasks schedule "<ref>" +3        # set/replace available-from/start date
tasks undate "<ref>"             # remove dates; --kind deadline|scheduled
tasks state "<ref>" WAITING      # any state; DONE/CANCELLED manage closed
tasks priority "<ref>" A         # A|B|C|none
tasks retitle "<ref>" "new"      # replace the title; tags/state untouched
tasks tag "<ref>" +foo -bar @ctx # add/remove tags & contexts (-@ctx removes)
tasks note "<ref>" "text"        # append a body line under the task
tasks link add "<ref>" <url> [--label "description"] # link an existing task
tasks link rm "<ref>" <n-or-url>  # remove from formal links only
tasks link set "<ref>" <n> --label "description" # relabel a formal link only
tasks capture "sub" --under "<ref>" # nest a new task below an existing one
tasks move "<ref>" "Section"     # relocate the block under a top-level heading
tasks move "<ref>" --under "<ref>"  # nest the subtree below another task
tasks move "<ref>" --top         # unnest the subtree back to the section level
tasks move "<ref>" --before "<ref>" # reorder before a sibling (infers its parent)
tasks move "<ref>" --under "<parent>" --before "<sibling>" # reparent at an exact slot
tasks lead "<ref>" 3w             # hide until 3w before its date; "off" clears
tasks recur "<ref>" weekly       # repeat on done: weekly/2w/.+1m; "off" clears
tasks defer "<ref>" +4           # hide until available four days from today
tasks someday "<ref>"            # hold indefinitely (someday/maybe/on hold)
tasks activate "<ref>"           # make available now (undefer/resume)
tasks archive                    # sweep DONE/CANCELLED to archive.jsonl; --json → {roots, records, moved_ids}
tasks undo                       # revert the last mutation; --json → {action, label}
tasks redo                       # replay the last undone mutation; --json likewise
tasks delete "<ref>"             # hard-delete a task (--cascade for subtasks); undoable
```

`PROPOSED` is separate from accepted open work. It stays out of the default
list, agenda, next, quadrants, inbox, and project rollups; review it with
`list --proposed` or the TUI Approvals tab — both ranked in triage order
(priority A>B>C>none, then soonest due, undated last in a band), so setting
`--priority` and `--due` on a proposal changes where the owner sees it. A
proposal cannot recur or be
completed. Before the owner decides it, `priority`, `retitle`, `tag`, and
`note` can correct its presentation without changing its PROPOSED state.
Approval and rejection are undoable lifecycle decisions. A reject stamps
`rejected` with the day it was declined, which is what separates a declined
proposal from an ordinary cancellation: `list --rejected` lists declines from the
last 30 days (live plus recently archived rows), newest first, and
`unreject "<ref>"` puts one back to PROPOSED in place — same id, title, notes and
links, undoable like any other write. It refuses anything that is not a live
rejected proposal, and never creates a second task for work that already has an
id. Declines stay hidden everywhere else; the TUI Inbox reveals the same window
with `R` and restores the selected one with `a`.

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

`delete` hard-removes a task's subtree from the live file (it never touches the
archive and is not the same as `cancel`). A task with subtasks needs `--cascade`.
Prefer `cancel`/`archive` for normal "done with it" cases; `delete` is for a
true mistake, and `tasks undo` reverses it.

`scheduled` is the task's available-from/start/defer-until value; `deadline` is
its separate due value. A future available-from value hides the task from active
views until its exact boundary. A date without time is all-day. `tomorrow 9am`
and relative seconds/minutes/hours are timed; sub-minute results round up to
stored minute precision. Natural calendar phrases accept plain, `in ...`, and
`... from now` forms; `2d`/`2w`/`2m`/`2y` mean days/weeks/months/years. A timed
value is floating in the configured zone; `--timezone Europe/London` makes it
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

**Lead time** hides a dated task until a set span **before its own date**, and
keeps that window as a recurrence rolls:

```sh
tasks lead "<ref>" 3w         # hide until 3 weeks before its date
tasks lead "<ref>" "a week"   # phrases work: 3 weeks / a week / 10 days
tasks lead "<ref>" off        # clear the window
tasks lead "<ref>"            # read-only: the span + the date it opens
tasks capture "Renew passport" --due 2026-11-01 --lead 3w
```

The **anchor** is the task's `deadline` if it has one, else its available-from
date. The window opens at `anchor - lead` and the task is timed-unavailable
until then — it shows up in `list --unavailable` and the TUI reveal toggle like
any deferred task, and an ancestor's lead gates its whole subtree.

**Pick the right tool.** "Hide it until N before it's due" / "give me a week of
runway" is a **lead** — a standing window that survives every recurrence roll.
"Hide it until Friday" (this once) is a **timed defer** (`defer "<ref>" fri`).
"Someday / maybe / on hold" with no release date is `someday "<ref>"`.

Units are `d`/`w`/`m`/`y` plus `h` for hours; `m` always means **months**, never
minutes. A clock span (`5h`) measures a real duration back from the task's own
instant — an all-day date resolves to its first instant, so `5h` before June 1
opens at 19:00 on May 31 local.

Refused at write time, each naming the fix: a lead needs a date to hide before;
it may not sit beside a two-date window (a deadline AND an available-from date
already express that window), refused from either direction; `defer <date>` is
refused on a lead task and `schedule` on a deadline-anchored one, while moving a
scheduled-anchored task's own date stays a normal anchor edit; and clearing the
last date clears the lead with it. A lead rides with the dates, so a proposal
takes one on the same terms it takes `--due`.

`activate "<ref>"` on a lead task releases **this occurrence only** and keeps
every date; the next `done` roll re-arms the window. On a task with no lead —
including a recurring one — `activate` keeps its usual meaning of clearing a
future available-from date.

A recurring capture that also carries a lead seeds the schedule's **first
occurrence** rather than today, so `capture "clean gutters" --recur y:06-01
--lead 17d` needs no date flag: it anchors on June 1 and hides until May 15.
Without a lead, a dateless recurring capture still starts repeating today.

Recurrence is a `recur` cookie alongside the task's date (`.+1w`, `++1m`, `+2d`).
`recur "<ref>" weekly` (or `2w`, `.+1m`, `every 3 days`) sets it; `off` clears it.
Completing a recurring task with `done` rolls its date forward and keeps it open
(no `closed`) — use `cancel` to actually stop it. `list --recurring` reviews them.

Completing a parent cascades: `done` (or `state … DONE`) closes every open
descendant too (recurring descendants close outright — their cookie is retired),
as one undo step. A recurring parent is the exception — it rolls forward and does
not cascade. `cancel` never cascades; reopening a parent does not reopen its
descendants.

`capture` flags: `--due <date/time>`, `--scheduled <date/time>`, per-field
`--due-timezone`/`--scheduled-timezone`, floating and fold flags, `--priority A|B|C`,
`--tag t` (repeatable), `--context @x` (repeatable), `--state STATE`,
`--project "Heading"`, `--under <ref>`, `--no-host-context`, and repeatable
`--note` and `--link`. A date makes it land as TODO (override
with `--state`); `--project` files it under that section (default Inbox);
`--under <ref>` nests it below an existing task instead (mutually exclusive with
`--project`).

`--link <url>` is repeatable and may be followed immediately by `--label
"description"` to label that one link; links are stored in the order given.
The link is written in the SAME transaction as the task, so one `tasks undo`
removes both — file context URLs this way rather than following a capture with
`tasks link add`, which is a second write you can forget. `propose --link` works
identically and is what makes a proposal quick to approve. A URL is validated
exactly as `link add` validates it (http/https with a host, or a configured
shorthand, which expands and becomes the default label); a bad or duplicate URL
refuses the whole capture and writes nothing. If a title's last word is already
a bare URL, capture lifts it into a formal link and keeps the remaining words as
the title (trailing sentence punctuation stays on the title, not in the URL).

When `tasks config` reports a `host_context`, every capture adds it alongside
explicit contexts. Use `--no-host-context` only when the user explicitly wants
that one task to omit the current machine's context.

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
it with `tasks show "<id>"` first, then apply requested changes to that
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

If the file was somehow edited out-of-band (not by you), run `tasks check`
and fix whatever it reports before finishing.

## Remembered defaults (`agent-memory.md`)

A task set may carry `agent-memory.md` — a Markdown sidecar of durable,
user-approved defaults (e.g. "garden tasks use `@home`") beside `tasks.jsonl`.
When present, its contents are already injected into your system context; apply
those defaults only where a request clearly falls in scope, and always let the
current request override them. Find its resolved path with `tasks config`
(it can be relocated by the `TASKS_MEMORY` env var or the config `memory` key).

Unlike `tasks.jsonl`, this sidecar is edited **directly** — it's plain Markdown,
not a CLI store. Create, change, or remove a rule only when the user explicitly
asks to remember / forget / change a default, editing minimally in the right
section (create the file from its template on the first such request). Never
infer a default from task edits, and never store secrets or transient facts. The
full policy is in `internal/agentcontext/TASK_AGENT.md` (Task-set memory); report any change you make to
the file alongside your task changes.

## Report

End with one line listing every change made (the CLI prints resulting
headlines — quote them), including any `agent-memory.md` change. The caller uses
this as the audit trail.
