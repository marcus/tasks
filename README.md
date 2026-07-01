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
bin/tasks agenda      # what's due / scheduled, soonest first
bin/tasks next        # next actions grouped by context (@computer, @email, …)
bin/tasks quadrants   # Covey Important/Urgent 2x2
bin/tasks inbox       # unprocessed captures
```

## Working with Claude

Claude can read and edit `gtd.org` directly — add captures to the Inbox, process
items into lists, suggest next actions, and surface what matters. The plain-text
format means every change is a reviewable git diff.

## Roadmap / ideas

- `bin/tasks capture "..."` to append to Inbox from the shell.
- `bin/tasks done <line>` to mark complete + move to an archive.
- Weekly-review helper.
- Optional gem-based version if we outgrow stdlib parsing.
