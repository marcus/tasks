# Recurring tasks

Status: implemented (2026-07-04), updated for the JSONL store (2026-07-10)

Recurring tasks use the existing task record plus a `recur` field. Recurrence
does not add a state or record type. A task still has a normal `scheduled` or
`deadline` date; `recur` controls how that date advances when the task is done.

The feature was first designed while the project used Org files. The July 8
JSONL migration kept the useful Org-style cookie grammar but moved the cookie
into its own record field. Git history preserves the original implementation
plan; this document describes the current system.

## Record shape

A recurring task has a date and a `recur` cookie:

```json
{"type":"task","id":"e5f6a7b8","parent":"a1b2c3d4","state":"NEXT","title":"Water the plants","tags":["@home"],"scheduled":"2026-07-08","recur":".+1w","body":"- Did [2026-07-01]."}
```

The cookie grammar is `<prefix><count><unit>`:

- Prefix: `+`, `++`, or `.+`
- Count: one or more digits
- Unit: `d`, `w`, `m`, or `y`

| Cookie | Meaning | Next date |
|---|---|---|
| `+1w` | Fixed cadence | Stored date plus one interval; it may remain overdue |
| `++1w` | Catch-up cadence | Stored date plus intervals until the result is in the future |
| `.+1w` | From completion | Completion date plus one interval |

Friendly inputs such as `daily`, `weekly`, `2w`, and `every 3 months` normalize
to cookies. Bare intervals default to `.+`, which avoids leaving a completed
task overdue.

Month and year intervals use calendar arithmetic. A monthly task on January 31
advances to February 28 in a non-leap year.

## Completion behavior

Completing a recurring task:

- Advances the date carrying the recurrence.
- Leaves the task in its current open state.
- Does not set `closed`.
- Appends `- Did [YYYY-MM-DD].` to the body.
- Creates one undoable journal entry.

Cancelling the task closes it and stops recurrence. Completing a non-recurring
parent closes its open descendants; a recurring descendant closes outright in
that cascade instead of advancing. Completing a recurring parent advances only
the parent and does not cascade.

Dating commands preserve `recur` while changing a date. Removing the task's
last date also removes recurrence because an undated task has nothing to
advance.

## CLI

Set or replace recurrence:

```sh
tasks recur "Water the plants" weekly
tasks recur "Pay rent" +1m
tasks recur "Quarterly review" "every 3 months" --from schedule
```

Clear recurrence:

```sh
tasks recur "Water the plants" off
```

Seed a date while adding recurrence:

```sh
tasks recur "Water the plants" weekly --on today
tasks capture "Water the plants" --scheduled today --recur weekly
```

Review recurring tasks with `tasks list --recurring`. `show --json` and the
list-family JSON commands expose the normalized cookie as `recur`.

## TUI

- `r` opens the recurrence editor for the selected task.
- `c` completes the selected task and advances it when recurring.
- `↻` marks recurring tasks in list views.
- `u` restores the previous date through the shared undo journal.

## Implementation map

- `lib/tasks/recur.rb` parses intervals and computes the next date.
- `Tasks::Store` sets recurrence and applies completion behavior.
- `bin/tasks` exposes `recur`, `capture --recur`, and `list --recurring`.
- The TUI uses the same Store mutation path as the CLI.

Coverage lives in `test/test_recur.rb` and the Store, CLI mutation, TUI app,
view, and shortcut tests. All mutating paths use the normal file lock, journal,
atomic write, and post-write structural check described in
`docs/cli-spec.md`.
