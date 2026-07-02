---
name: tasks-cli
description: How to read and modify the user's GTD task list (gtd.org) safely. Use whenever asked to view, add, complete, reschedule, prioritize, tag, or otherwise manage tasks in this repo. Prefer the CLI over editing gtd.org directly.
---

# Working with the task list

The task list lives in `gtd.org` (org-mode-ish plain text). **Use `bin/tasks`
for everything it supports** — it keeps formatting correct, enforces the
conventions, validates the file after each write, and rolls back bad writes.
Edit the file directly only for operations the CLI doesn't cover yet
(`docs/cli-spec.md` marks each command ✅ implemented / 🚧 planned).

## Read first

```sh
bin/tasks list -a          # everything incl. archive; filters: @ctx +tag /text -A
bin/tasks agenda           # dated items, soonest first
bin/tasks inbox            # unprocessed captures
bin/tasks show "<ref>"     # one task in full (fields + notes); --json
bin/tasks check            # is the file structurally sound? (exit 1 = no)
bin/tasks config           # where gtd.org/archive.org actually resolve; --json
```

The task files may live outside this repo (env vars or `~/.config/tasks/config`
can relocate them). If you need the file's path — e.g. before a direct edit —
get it from `bin/tasks config`, don't assume the repo root.

All read commands accept `--json` (flat array, same sort as the text view) —
prefer it when you need to reason over tasks rather than display them.

## Mutate

```sh
bin/tasks capture "text"             # new INBOX item (see flags below)
bin/tasks done "<ref>"               # mark DONE + CLOSED stamp
bin/tasks cancel "<ref>"             # mark CANCELLED + CLOSED stamp
bin/tasks due "<ref>" fri            # set/replace DEADLINE (INBOX → TODO)
bin/tasks schedule "<ref>" +3        # set/replace SCHEDULED (INBOX → TODO)
bin/tasks undate "<ref>"             # remove date stamps; --kind deadline|scheduled
bin/tasks state "<ref>" WAITING      # any state; DONE/CANCELLED manage CLOSED
bin/tasks priority "<ref>" A         # A|B|C|none
bin/tasks retitle "<ref>" "new"      # replace the title; tags/state untouched
bin/tasks tag "<ref>" +foo -bar @ctx # add/remove tags & contexts (-@ctx removes)
bin/tasks note "<ref>" "text"        # append a body line under the task
bin/tasks move "<ref>" "Section"     # relocate the block under a top-level heading
bin/tasks archive                    # sweep DONE/CANCELLED to archive.org
```

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

## Direct edits (rare — for what the CLI still lacks)

The CLI now covers capture, completion, cancel, state, dates, priority,
retitle, tags, notes, and moving between sections. Reach for a direct edit
only for something with no command yet (e.g. hard-deleting a block, editing an
existing body line, or restructuring section headings). When you must:

1. Read `docs/conventions.md` for the format. Key shapes:
   - `** STATE [#A] Title  :tag:@context:` — STATE ∈ INBOX/TODO/NEXT/WAITING/DONE/CANCELLED
   - `   DEADLINE: <YYYY-MM-DD>` / `   SCHEDULED: <YYYY-MM-DD>` / `   CLOSED: [YYYY-MM-DD]`
2. Apply the conventions the tooling enforces elsewhere:
   - dating an INBOX item ⇒ also change its state to TODO
   - marking DONE/CANCELLED ⇒ add `CLOSED: [today]`
   - resolve relative dates to absolute `<YYYY-MM-DD>`
3. **Always run `bin/tasks check` afterward.** If it reports errors, fix them
   before finishing — you may have mangled the file.
4. Never hand-edit `archive.org`.

## Report

End with one line listing every change made (the CLI prints resulting
headlines — quote them). The caller uses this as the audit trail.
