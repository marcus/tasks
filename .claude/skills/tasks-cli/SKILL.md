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
bin/tasks check            # is the file structurally sound? (exit 1 = no)
```

## Mutate

```sh
bin/tasks capture "text"   # new INBOX item
bin/tasks done "<ref>"     # fuzzy title substring; must match exactly one open task
bin/tasks archive          # sweep DONE/CANCELLED to archive.org
```

Ref rules: case-insensitive substring of the title. If it matches several
tasks the command lists the candidates and exits 2 — retry with a longer
substring. Don't guess between candidates; if the user's request is genuinely
ambiguous, stop and ask, listing the matches.

## Direct edits (for what the CLI lacks yet)

When you must edit `gtd.org` directly (reschedule, priority, tags, retitle,
move between sections — until their commands land):

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
