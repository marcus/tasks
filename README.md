# tasks

A plain-text, Claude co-managed task system. Org-mode-inspired format, GTD workflow,
Covey Important/Urgent matrix. Ruby tooling on top.

## Layout

```
gtd.org            The one file that matters — all tasks live here.
docs/conventions.md  The format + methodology spec (read this).
bin/tasks          Ruby CLI for querying gtd.org (stdlib only, no gems needed).
```

## Quick start

```sh
tasks agenda            # what's due / scheduled, soonest first
tasks next              # next actions grouped by context (@computer, @email, …)
tasks quadrants         # Covey Important/Urgent 2x2
tasks inbox             # unprocessed captures
tasks capture "..."     # append a new item to the Inbox
tasks done "..."        # mark a matching open item DONE
tasks archive           # sweep DONE/CANCELLED items into archive.org
```

`tasks` is aliased to `bin/tasks` in `~/.zshrc`.

## Working with Claude

Claude can read and edit `gtd.org` directly — add captures to the Inbox, process
items into lists, suggest next actions, and surface what matters. The plain-text
format means every change is a reviewable git diff.

## Auto-commit

A launchd agent (`com.marcus.tasks-autocommit`) runs `bin/autocommit` daily at
21:00, committing only if something changed. Missed runs (Mac asleep) fire on next
wake. Local repo only — no remote yet.

## Roadmap / ideas

- Weekly-review helper (empty inbox, flag projects with no NEXT).
- Add a remote for off-machine backup.
- Optional gem-based version if we outgrow stdlib parsing.
