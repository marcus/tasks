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

## How to act
- Read state first with `bin/tasks list -a` (or targeted filters).
- Prefer the CLI for operations it supports; it keeps formatting correct:
  - complete a task:  `bin/tasks done "<fuzzy title>"`
  - add a task:       `bin/tasks capture "<text>"`
  - archive done:     `bin/tasks archive`
- Edit `gtd.org` directly for anything the CLI lacks: changing a
  deadline/scheduled date, priority, tags, or retitling.
- Match tasks by fuzzy title. If a prompt is ambiguous (multiple matches),
  don't guess — say which ones matched and stop.
- When you give an `INBOX` item a `SCHEDULED`/`DEADLINE` date, also change
  its state to `TODO` (dated = processed; the TUI enforces the same rule).
- Resolve relative dates ("next Friday", "tomorrow") to `<YYYY-MM-DD>`.

## Report
End with ONE line listing every change made — including any external
action (Slack, email) — so the caller has a full audit trail.
