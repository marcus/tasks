# valid/full-field-matrix

One record per interesting field combination the schema allows. This is the
reference store for "does the port understand every field".

## What it exercises

- **States** — all seven, including `PROPOSED` and both closed states with
  `closed` dates.
- **Priorities** — A, B, C, and absent.
- **Dates** — `scheduled` alone, `deadline` alone, both together, a leap day
  (2028-02-29).
- **Time metadata** — a floating local time (no `timezone`), a fixed-zone time
  (`Europe/London`), and `fold: 1` selecting the second instant of the
  2026-11-01 DST overlap in `America/Los_Angeles`.
- **Recurrence** — one task per grammar branch: `.+1w`, `++1m`, `+2d`,
  `w:mon,wed`, `m:15`, `y:07-04`.
- **Lead time** — a calendar span (`3w`) against a deadline, the clock unit
  (`5h`) against a timed start, and a `lead_skip` stamp on a recurring task
  whose current occurrence was already released.
- **Delegation** — human `delegated`, agent `ready`, and agent `claimed` with a
  `work_ref`.
- **Text** — multi-line bodies, a URL in a body, non-ASCII titles (accents, CJK,
  emoji), an ideographic space, and embedded quotes and backslashes.
- **Shape** — a task parenting two child tasks.

## What a correct implementation must do

Round-trip every record byte-identically (canonical key order, nested key order
inside `scheduled_time` / `deadline_time` / `delegation`, omission of empty
values), and interpret each field the way `docs/conventions.md` describes —
especially `fold`, which is the one field whose meaning cannot be recovered from
the date and local time alone.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 32 tasks parsed, no structural errors
exit 0
```
