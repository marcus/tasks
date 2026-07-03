# AGENTS.md — tasks repo

You are acting on a personal GTD task list via natural-language prompts
passed to `tasks -p`. Today's date is available from the system.

## Files
- `gtd.org` — the live list. Org-mode headlines:
  `** STATE [#A] Title :tag:@context:` where STATE ∈
  INBOX|TODO|NEXT|WAITING|DONE|CANCELLED, priority `[#A|B|C]` optional.
  Metadata lines below a headline: `DEADLINE: <YYYY-MM-DD>`,
  `SCHEDULED: <YYYY-MM-DD>`, `CLOSED: [YYYY-MM-DD]`.
- `archive.org` — completed/cancelled history. Don't edit by hand.
- The files may live outside the CLI's repo. Absolute paths for this run
  (the CLI and both files) are appended below this prompt under
  "File locations for this run" — use those for direct reads/edits, and the
  absolute CLI path if `bin/tasks` isn't in your working directory.

## How to act
- Your job is to change task **data**, never the tool. Do not read, "fix", or
  edit the CLI's own source (`bin/tasks`, anything under `lib/`) or any project
  code — just run `bin/tasks` and, when needed, edit `gtd.org` task lines.
- The tasks CLI is known-good (Ruby 3.4). It uses Ruby endless methods like
  `def foo(x) = bar(x)` — valid syntax, NOT a bug. Always invoke it by the
  absolute path given below. If a command seems to error, re-run it with that
  absolute path; never conclude the CLI is broken or hand-edit files as a
  workaround.
- **Always use the CLI to set dates, priority, state, and tags** — it writes the
  exact org format (e.g. `DEADLINE: <YYYY-MM-DD>` with the required angle
  brackets). Do NOT hand-write `DEADLINE:`/`SCHEDULED:` lines; a plain
  `DEADLINE: 2026-07-05` without `< >` is silently ignored. `due`/`schedule`
  accept relative dates (`+3`, `tomorrow`, `fri`) so you never format a date by hand.
- Read state first with `bin/tasks list -a` (or targeted filters).
- Prefer the CLI for operations it supports; it keeps formatting correct:
  - complete a task:  `bin/tasks done "<fuzzy title>"`
  - add a task:       `bin/tasks capture "<text>"` (flags: --due/--scheduled/
                      --priority/--tag/--context/--state/--project)
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
  - inspect a task:   `bin/tasks show "<ref>" [--json]`
  - archive done:     `bin/tasks archive`
  - validate file:    `bin/tasks check` (exit 1 = structural errors)
  (full command set + roadmap: `docs/cli-spec.md`)
- Edit `gtd.org` directly only for what the CLI still lacks (e.g. hard-delete,
  rewriting an existing body line, restructuring section headings).
- After ANY direct edit to `gtd.org`, run `bin/tasks check` and fix
  whatever it reports before finishing.
- Match tasks by fuzzy title. If a prompt is ambiguous (multiple matches),
  don't guess — say which ones matched and stop.
- When you give an `INBOX` item a `SCHEDULED`/`DEADLINE` date, also change
  its state to `TODO` (dated = processed; the TUI enforces the same rule).
- Resolve relative dates ("next Friday", "tomorrow") to `<YYYY-MM-DD>`.
- Quadrants (`bin/tasks quadrants`) are computed, not stored: **important** =
  priority `A`/`B` or the `:important:` tag; **urgent** = a `DEADLINE` within a few
  days or the `:urgent:` tag. To make something "urgent"/"important", prefer setting
  its deadline/priority over adding tags.

## Report
End with ONE line listing every change made — including any external
action (Slack, email) — so the caller has a full audit trail.
