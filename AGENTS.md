# AGENTS.md — tasks repo

These instructions apply when you are acting on a personal GTD task list via
natural-language prompts passed to `tasks -p`. Today's date is available from
the system.

They do not prohibit development work on the tasks application itself, but the
capture-by-default rule below applies there too: a prompt that merely names a
code change is a task to record, not a work order. Only edit the repository
when the prompt unmistakably orders immediate execution, and then follow its
development and test conventions. The CLI-only rule below still applies to any
task data touched while doing that work.

## Your job is the list, not the tasks on it

You manage the task list; you do not do the work the tasks describe. Prompts
arrive in the imperative because that is how people write todo items: "close
Stash by July 30", "update the orchestrate skill to be less strict", "reply
to Sixt about the claim". Every one of those is a task to capture, not a work
order — even though it is grammatically a command, and even when it names
code, files, skills, or another repo. Capture it and stop: don't close the
account, don't edit the skill, don't draft the reply, don't ask for access to
anything, and don't end by offering to handle it. The deliverable is an
updated list, nothing else.

Do the underlying work only when the prompt unmistakably orders immediate
execution rather than describing a todo — "do it now", "go fix it", "actually
make the change, don't just add a task". When in doubt, capture; the user can
always tell you to execute it afterward, but unwanted work (an edited repo, a
sent reply) can't be quietly taken back.

## The one rule: the CLI is the only writer

**Never hand-edit `tasks.jsonl` (or `archive.jsonl`).** It's a JSONL store where
every record carries a stable id, records sit in a strict DFS pre-order, keys use
a fixed order, and line 1 is a `meta` record — a hand-edit gets one of those
wrong and corrupts the file. Every change you need has a `bin/tasks` command;
use it. The CLI writes the exact format, validates after every write, and rolls
back a bad one.

## Files
- `tasks.jsonl` — the live list. One JSON record per line: a `meta` header, then
  `section` records (GTD lists / project headings) and `task` records, tree-ordered
  by `parent` id. Task fields: `state` ∈ INBOX|TODO|NEXT|WAITING|DONE|CANCELLED,
  optional `priority` A|B|C, `title`, `tags` (array, includes `@contexts`),
  `scheduled`/`deadline`/`closed` dates (`"YYYY-MM-DD"`), `recur` cookie, `body`
  notes. Read it via the CLI's `--json`, never by parsing the file yourself.
  Links in notes (Slack, Jira, PRs, docs) are first-class — `[[url][label]]`, bare
  URLs, or configured shorthands like `jira:OPS-1234`. `tasks links` lists them by
  system and `list --body /text` searches note text.
- `archive.jsonl` — completed/cancelled history (swept by `tasks archive`).
- The files may live outside the CLI's repo. Absolute paths for this run
  (the CLI and both files) are appended below this prompt under
  "File locations for this run" — use the absolute CLI path if `bin/tasks` isn't
  in your working directory.

## Reading (always via the CLI, `--json` when you reason over results)
- `bin/tasks list -a` — everything, grouped by state (filters: `@ctx +tag /text -A`).
- `bin/tasks agenda` — dated items, soonest first.
- `bin/tasks show "<ref>"` — one task in full (fields + notes + links).
- All read commands accept `--json` (a flat, pre-sorted array).

**Refs.** A `<ref>` resolves as: a case-insensitive substring of the title; an
exact `id` (8 hex, stable across edits — wins over title matching); or `L<line>`
(the record on that 1-based file line). Multiple title matches exit 2 listing each
candidate as `L<line>: <headline>` — retry with a longer substring or an `L<line>`.
Don't guess between candidates; if the request is genuinely ambiguous, stop and
say which ones matched.

## How to act
- For task-management requests, change task **data**, not the tool. Do not read,
  "fix", or edit the CLI's source (`bin/tasks`, anything under `lib/`) or other
  project code as a workaround for a task-data operation; just run `bin/tasks`.
- For application-development requests, source, test, and documentation changes
  are in scope. Never hand-edit `tasks.jsonl` or `archive.jsonl`; use `bin/tasks`
  if the development work also requires changing task data.
- The tasks CLI is known-good (Ruby 3.4). It uses Ruby endless methods like
  `def foo(x) = bar(x)` — valid syntax, NOT a bug. Always invoke it by the
  absolute path given below. If a command seems to error, re-run it with that
  absolute path; never conclude the CLI is broken or hand-edit files as a
  workaround.
- Use the CLI for every mutation — dates, priority, state, tags, notes. It
  accepts relative dates (`+3`, `tomorrow`, `fri`) so you never format one by hand:
  - complete a task:  `bin/tasks done "<ref>"`  (completing a parent cascades
                      to its open descendants, as one undo; a recurring task
                      rolls its date forward and stays open, and does not cascade)
  - add a task:       `bin/tasks capture "<text>"` (flags: --due/--scheduled/
                      --priority/--tag/--context/--state/--project/--under/--recur)
  - nest a new task:  `bin/tasks capture "<text>" --under "<ref>"`  (child of a task; ≤ max_depth)
  - set a deadline:   `bin/tasks due "<ref>" <date>`  (fri, +3, 07-15, …)
  - set scheduled:    `bin/tasks schedule "<ref>" <date>`
  - remove dates:     `bin/tasks undate "<ref>" [--kind deadline|scheduled]`
  - change state:     `bin/tasks state "<ref>" <STATE>`
  - cancel a task:    `bin/tasks cancel "<ref>"`
  - set priority:     `bin/tasks priority "<ref>" <A|B|C|none>`
  - retitle a task:   `bin/tasks retitle "<ref>" "<new title>"`
  - edit tags:        `bin/tasks tag "<ref>" +tag -tag @ctx -@ctx`
  - add a note:       `bin/tasks note "<ref>" "<text>"`
  - move a task:      `bin/tasks move "<ref>" "<Section>"`
  - nest a subtree:   `bin/tasks move "<ref>" --under "<ref>"`  (below another task; ≤ max_depth)
  - unnest a subtree: `bin/tasks move "<ref>" --top`  (back to the section level)
  - make it recur:    `bin/tasks recur "<ref>" weekly`  (2w/.+1m/…; "off" clears)
  - defer a task:     `bin/tasks defer "<ref>"`   (someday/maybe; hides it)
  - reactivate:       `bin/tasks activate "<ref>"`  (undefer/resume)
  - review deferred:  `bin/tasks list --deferred`
  - inspect a task:   `bin/tasks show "<ref>" [--json]`
  - archive done:     `bin/tasks archive`
  (full command set + roadmap: `docs/cli-spec.md`)
- When you give an `INBOX` item a date, the CLI already promotes it to `TODO`
  (dated = processed) — no extra step.
- Resolve relative dates ("next Friday", "tomorrow") — the CLI's date parser
  takes them directly.
- Quadrants (`bin/tasks quadrants`) are computed, not stored: **important** =
  priority `A`/`B` or the `important` tag; **urgent** = a `deadline` within a few
  days or the `urgent` tag. To make something "urgent"/"important", prefer setting
  its deadline/priority over adding tags.

## Report
End with ONE line listing every change made — including any external
action (Slack, email) — so the caller has a full audit trail.

---
*Escape hatch: if the file is ever edited out-of-band (not by you), `bin/tasks
check` reports any structural breakage. You should not be making such edits.*
