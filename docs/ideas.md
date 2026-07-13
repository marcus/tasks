# Feature ideas

A backlog of features that would make the task system more useful, roughly in
priority order. Entries marked shipped are retained as implementation records;
the remaining entries are uncommitted future work. When you pick one up, spec
it in `docs/cli-spec.md` first, then follow the `tasks-cli-dev` skill (model
layer → CLI dispatch → docs → tests).

## Shipped: editable task panel

The TUI now has a read-by-default task panel with an explicit editable view,
responsive panel widths, and an embedded task-agnostic reactive form component.
The implementation record and remaining independent-review gate live in
[`docs/plans/editable-task-panel.md`](plans/editable-task-panel.md).

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

## 2. Hard delete

`undo` and `redo` are implemented through the file-backed journal shared by the
CLI and TUI. The remaining idea is `delete <ref> --force`: hard-remove a subtree
without archiving it. It should refuse without `--force` and suggest `cancel`
for the normal case.

## 3. Recurring tasks ✅ (done)

The `recur` field uses Org-style cookies (`+1w`, `.+1m`, `++1d`). On `done`,
advance `scheduled` or `deadline` by the interval and keep the task open. This
covers bills, reviews, standups, and other repeating work.
Shipped across CLI (`recur`, `capture --recur`, `list --recurring`, `done`
rolls forward) and TUI (`r` popup, `↻` badge) — see `docs/plans/recurring-tasks.md`.
Follow-on parked here: a full per-occurrence completions log (the current
`- Did [date]` line is a lightweight stand-in).

## 4. `WAITING` aging

Surface delegated items by how long they've been waiting — e.g. flag anything
in `WAITING` whose capture/last-touch date is older than 7 days
("you've been waiting on X for 12 days"). Pairs naturally with the review
helper (#1). Needs a reliable "since when" signal — either the `Captured
[date]` note or a new `SINCE:`/last-touched stamp.

## 5. `--json` on read commands ✅ (done)

Shipped. Left here as a marker; see `docs/cli-spec.md`.

## 6. Full-text search including bodies/notes ✅ (done)

Shipped as `list --body/-b`, backed by the structural index (`Tasks::Tree`,
`Store#body`). Ref resolution stays title-only, as planned. The same layer
carries `tasks links` (link extraction + per-system classification via
`Tasks::Links` — slack/jira/github/…, unknown hosts fall back to the host) and
`show`'s `project:`. This is the substrate for the review helper (#1) and the
project view (#8). The link feature shipped on top of it: `link.<name>`
shorthands (`jira:OPS-1234`) + `system.<name>` custom hosts in config, and
`tasks open <ref>` / the TUI's `o`. Remaining follow-on: capture-with-link
sugar (`capture --link URL`, auto-moving URLs out of capture titles).

## 7. `stats` command

A quick dashboard: counts by state, overdue count, inbox size, and throughput
derived from `closed` dates in `archive.jsonl` (e.g. "12 done this week"). Cheap
to build on the existing parser; useful for motivation and review.

## 8. Smaller polish

- **Agenda overdue summary** — a one-line header ("3 overdue, 2 due today").
- **`next` / `list` scheduled dates** ✅ — now shown as `~M/D`.
- **Project view** — list every project heading and whether it has a `NEXT`
  (a lighter-weight slice of the review helper).
