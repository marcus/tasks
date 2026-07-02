# Feature ideas

A backlog of features that would make the task system more useful, roughly in
priority order. These are captured for future work — nothing here is committed.
When you pick one up: spec it in `docs/cli-spec.md` first, then follow the
`tasks-cli-dev` skill (model layer → CLI dispatch → docs → tests).

## 1. Weekly-review helper — `tasks review`

`docs/conventions.md` calls the weekly review the habit that keeps the system
trustworthy, but nothing tools it. A `review` command (read-only, or with an
interactive `--fix`) would surface:

- **Inbox not empty** — count of unprocessed `INBOX` items.
- **Stalled projects** — any project heading whose children include no `NEXT`
  action (the GTD "every active project has a next action" rule).
- **Stale `WAITING`** — delegated items older than N days (see idea #4).
- **Undated commitments** — `NEXT [#A]` items with no `SCHEDULED`/`DEADLINE`.

This is the single most on-philosophy addition. Highest value.

## 2. `undo` and `delete` (both already 🚧 in the spec)

- **`undo`** — revert the last CLI mutation. Needs a file-backed journal
  (snapshot per mutation), which could also replace the TUI's in-memory
  history so both share one undo stack.
- **`delete <ref> --force`** — hard-remove a block (no archive). Refuses
  without `--force`; suggests `cancel` instead.

## 3. Recurring tasks

Org-mode's native repeaters (`+1w`, `.+1m`, `++1d`). On `done`, instead of
closing, roll the `SCHEDULED`/`DEADLINE` forward by the interval and keep the
task open. High value for a personal GTD system (bills, reviews, standups).
Requires: parse a repeater cookie on the timestamp, and special-case `done`.

## 4. `WAITING` aging

Surface delegated items by how long they've been waiting — e.g. flag anything
in `WAITING` whose capture/last-touch date is older than 7 days
("you've been waiting on X for 12 days"). Pairs naturally with the review
helper (#1). Needs a reliable "since when" signal — either the `Captured
[date]` note or a new `SINCE:`/last-touched stamp.

## 5. `--json` on read commands ✅ (done)

Shipped. Left here as a marker; see `docs/cli-spec.md`.

## 6. Full-text search including bodies/notes

Today `/text` (in `list`) and every `<ref>` match on the **title only**
(`resolve_ref`, `cmd_list`). Extending the match to body/note lines would make
search far more useful — e.g. finding a task by a name mentioned in its notes.
Consider a `--body`/`--all-text` flag so ref resolution stays predictable.

## 7. `stats` command

A quick dashboard: counts by state, overdue count, inbox size, and throughput
derived from `CLOSED:` stamps in `archive.org` (e.g. "12 done this week").
Cheap to build on the existing parser; nice for motivation and review.

## 8. Smaller polish

- **Agenda overdue summary** — a one-line header ("3 overdue, 2 due today").
- **`next` / `list` scheduled dates** ✅ — now shown as `~M/D`.
- **Project view** — list every project heading and whether it has a `NEXT`
  (a lighter-weight slice of the review helper).
