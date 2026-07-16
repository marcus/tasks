# Timed task values proof

This proof exercises floating and fixed civil times through the real CLI and
TUI in a temporary task store. It never writes to the user's task files.

The setup script copies the checked-in example store, then creates all three
proof tasks through `bin/tasks`:

- a floating deadline at 11:00 tomorrow;
- a fixed `Europe/London` deadline at 17:00 tomorrow;
- a fixed `America/New_York` available-from time and separate deadline two
  days from now.

The TUI evaluates the store in `America/Los_Angeles` with 24-hour output. The
London 17:00 value therefore appears as 09:00 in Agenda, while the floating
11:00 value remains 11:00. Reveal mode exposes the New York task's exact
available-from badge without changing the task.

[`timed-task-values.keys`](./timed-task-values.keys) is the reproducible Betamax
script; [`timed-task-values-tui.sh`](./timed-task-values-tui.sh) creates and
cleans up the isolated store.

```sh
betamax --validate-only \
  "bash docs/proofs/timed-task-values-tui.sh" \
  -f docs/proofs/timed-task-values.keys

betamax \
  "bash docs/proofs/timed-task-values-tui.sh" \
  -f docs/proofs/timed-task-values.keys
```

![Agenda rendering floating, fixed, and unavailable timed tasks](./timed-task-values.png)

The same script then opens the London task's details, enters the real task
editor, and opens the structured temporal control. The date, time, Fixed mode,
and original IANA zone remain independently visible rather than being replaced
with the projected Agenda clock. The same control exposes searchable IANA-zone
selection and shows the earlier/later fold row only for ambiguous civil times.

![Task editor preserving the London fixed civil value](./timed-task-values-editor.png)

Artifact verification:

```text
timed-task-values.png:        PNG, 3832 x 2094
timed-task-values-editor.png: PNG, 3832 x 2094
```

## Semantic transcript

[`timed-task-values-transcript.sh`](./timed-task-values-transcript.sh) is the
deterministic sandbox proof for the eight semantic scenarios in the plan. It
runs only temporary fixtures and names the contract under test in each test
method. The coverage is:

1. parser-preserved all-day values;
2. floating instants under two evaluation zones;
3. fixed instants and projected local display;
4. an exact availability release with byte-identical storage and no journal;
5. an exact timed-overdue boundary;
6. fixed wall-time recurrence skipping the spring DST gap;
7. CLI-to-API and API-to-CLI temporal visibility with fresh-process undo; and
8. v1-to-v2 live/archive migration with every non-meta record unchanged.

Run it from any directory:

```sh
bash docs/proofs/timed-task-values-transcript.sh
```

The checked run completed four focused suites with zero failures or errors.
The cross-surface case boots the real `bin/tasks-api` Puma entrypoint and fresh
`bin/tasks` processes rather than calling adapters in-process.

## Migration recovery

Old binaries intentionally refuse schema v2 before writing. Before any v2 task
changes, recovery means stopping every tasks process, restoring the live and
archive `.v1.bak` files together, and checking them with the old binary. Every
existing source gets a backup; an originally empty archive produces a zero-byte
backup that must be restored exactly. Never restore only one side. Once v2
changes exist, restoring those backups would discard work; export or reconcile
the newer records first.

The end-to-end automated gates and independent review are recorded in the
implementation commits for `docs/plans/timed-task-values-and-timezones.md`.
