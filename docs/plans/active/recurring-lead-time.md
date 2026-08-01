# Recurring lead time

Hide a dated task until a set span before its occurrence date, and keep that
window as the recurrence rolls.

Epic: td-f18c31. This document is the contract; the child tasks reference
sections here rather than duplicating them, and this file wins if they disagree.

## 1. Goal

Two motivating captures:

1. **Deadline-anchored.** "Renew the passport" is due 2026-11-01. It is real
   work, but seeing it in July is noise. Show it three weeks before it is due
   and not before:

   ```
   tasks capture "Renew the passport" --due 2026-11-01 --lead 3w
   ```

   Hidden until 2026-10-11; from that morning on it is an ordinary available
   task.

2. **Scheduled-anchored and recurring.** "File quarterly sales tax" recurs on
   the 20th of every third month and needs a week of runway:

   ```
   tasks capture "File quarterly sales tax" --scheduled 2026-04-20 \
     --recur "every 3 months on the 20th" --lead 1w
   ```

   Visible from the 13th through the 20th of each quarter, hidden the rest of
   the cycle, and the window re-arms itself every time `done` rolls the anchor.

Neither case is expressible today. A timed defer (`tasks defer <ref> <date>`)
is a one-off date that a recurrence roll does not maintain, and an indefinite
hold (`tasks someday`) never releases on its own.

## 2. Product decisions

- **Anchor.** The occurrence date a lead is measured back from is the
  **deadline if the task has one, otherwise the available-from date**. That is
  the same precedence recurrence completion uses when it rolls a stamp, so a
  task never has two different notions of "its date".
- **The lead owns the task's own timed gate.** With a lead set, the task's own
  release instant is `anchor − lead`, not its available-from date. There is
  exactly one own timed gate; rule 3 below refuses the shapes that would create
  a second one.
- **A lead can reveal earlier as well as hide.** On a deadline-anchored task
  (no available-from date) the task is available today and the lead hides it.
  On a scheduled-anchored task the available-from date already hides it and the
  lead releases it early. One rule — `anchor − lead` — produces both.
- **Ancestors participate identically.** A lead on a parent gates its whole
  subtree, exactly as an ancestor's available-from date does today, and the
  existing "furthest gate, nearest task wins ties" precedence is unchanged.
- **Per-occurrence early release.** `tasks activate` on a lead-gated task
  releases **this occurrence only**, by stamping `lead_skip` with the anchor
  date it released. The next roll (or any anchor edit) clears the stamp and the
  window comes back.
- **No new availability reason.** A lead-gated task reports
  `availability_reason: "scheduled"` (`"ancestor_scheduled"` via an ancestor)
  with `available_at` at the derived gate. Every existing filter, view, count,
  and rider therefore works unchanged.
- **No `Format::VERSION` bump.** `lead` and `lead_skip` are additive keys. An
  older binary round-trips them untouched (Format's forward-compat rule) and
  simply does not apply the gate — a degradation that is visible, reversible,
  and documented, not data loss.
- **Not recurrence.** A lead never changes which dates a recurrence fires on.
  Recurrence advancement is untouched apart from clearing `lead_skip`.

## 3. Stored shape

Two new task-record keys, emitted after `recur` in `Format::KEY_ORDER`:

| key | shape | meaning |
| --- | --- | --- |
| `lead` | `"3w"` — `[1-9][0-9]*` + `d`/`w`/`m`/`y` | hide until this span before the anchor |
| `lead_skip` | `"2026-11-01"` | the anchor date whose occurrence `activate` already released |

`lead_skip` is internal bookkeeping: it never appears on the HTTP wire, and no
surface lets a user type it. It is written only by activation and deleted by
anchor edits, by recurrence advancement, and by any `lead` write. The
derivation also compares it against the *current* anchor, so a stale stamp left
by a foreign writer can never release a different occurrence.

The stamp is not lead-only. `tasks activate` on a **recurring** task uses it
too: that task's future available-from date is its next occurrence, not a
defer, so releasing it early must not delete the series' only anchor. This also
retires a long-standing defect — activation used to delete that date
permanently, leaving a repeater `done` could never roll.

`Check` validates the grammar of both, requires `lead_skip` to have an anchor
to name, and adds both to `SECTION_FORBIDDEN`.

## 4. Granularity and release instant

The derived gate is a **calendar date**, released at **local midnight of that
date in the reader's zone** — the same release rule an all-day available-from
date already has. Consequences, all deliberate:

- A lead holds its wall-clock date across a DST change between the gate and the
  anchor: `3w` before a 02:00-timed anchor still releases at 00:00 local.
- A timezone-carrying anchor still releases the gate at the *reader's* local
  midnight. The anchor's zone fixes when the task is *due*, not when the window
  opens.
- Month and year leads clamp exactly as `Date#>>` does (the same clamp
  recurrence intervals use): `1m` before March 31 is February 28 in a common
  year.

`TaskQueries#effective_gate` therefore returns an **instant**, not a date, so
that the follow-up clock-unit work (`5h` before an anchor — td-556c53) is
purely additive rather than a rework of this seam.

## 5. Validation rules

Every surface refuses the same five shapes with the same messages, and each
message names the fix.

1. **A lead needs an anchor.** No deadline and no available-from date → refuse:
   `"“<title>” has no date to hide before — add a deadline or an available-from date first"`.
2. **Accepted, open tasks only.** `PROPOSED` → `"can't set a lead time on a PROPOSED task"`;
   `DONE`/`CANCELLED` → `"can't set a lead time on a <STATE> task — reopen it first"`.
   (Clearing with `off` is always allowed.)
3. **One own timed gate.** A lead may not sit beside a *separate* available-from
   date: setting a lead on a task that carries **both** a deadline and an
   available-from date is refused, as is setting an available-from date on a
   deadline-anchored lead task. `tasks defer <ref> <date>` against any lead task
   is refused for the same reason — the lead already owns "hide until". The
   indefinite hold (`tasks someday`) is unaffected; an own or inherited hold
   still outranks any timed gate.
4. **Grammar.** A positive whole count and one of `d`/`w`/`m`/`y`. `h` is
   rejected with a message that names it as planned-but-absent, so the follow-up
   can land without changing what a valid lead means.
5. **Storable range.** `anchor − lead` must stay a real four-digit-year date,
   the same range guard recurrence has.

## 6. Read and JSON/API contract

- `TaskView` carries `lead` (canonical span) and renders `lead_human`.
- `available_at` / `availability_reason` / `availability_blocker_id` are the
  derived answer, unchanged in shape.
- The HTTP resource gains `lead` and `lead_human`; `POST /tasks` and
  `PATCH /tasks/{id}` accept `lead` (`null`/`"off"` clears). `lead_skip` stays
  off the wire.
- No new collection query parameter: `available=false` already selects
  lead-gated tasks.

## 7. CLI contract

```
tasks lead <ref> <span|off> [--dry-run] [--json]
tasks lead <ref>                      # read-only: span + derived date
tasks capture … --lead <span>
```

`lead` mirrors `recur` in shape: same ref resolution, same `--dry-run` and
`--json` conventions, same "state the mutation, then the resulting
availability" human output.

## 8. View and TUI contract

- The task editor gains a **Lead time** field beside Recurrence, with inline
  validation and the resolved date shown, obeying the save-on-blur
  field-ownership rule (it writes `lead` and nothing else).
- Task details and export render the span plus the derived date.
- No new list marker and no new picker mode: a lead-gated row is timed-
  unavailable like any other, and `z`'s `now` performs the `lead_skip`
  activation while its timed choices refuse per rule 3.

## 9. Slices

| # | slice | td |
| - | ----- | -- |
| 2 | canonical model, store rules, activation | td-190c8e |
| 3 | CLI surface | td-b93664 |
| 4 | HTTP API surface | td-b7b218 |
| 5 | TUI surface | td-cbca83 |
| 5b | clock units (`h`) — optional follow-up | td-556c53 |
| 6 | compatibility matrix | td-6d7763 |
| 7 | docs and agent prompts | td-6d7763 |
| 8 | adversarial review | td-526a45 |
| 9 | proof | td-526a45 |

## 10. Planned clock units

`h` is deliberately absent from slice 2 and reserved for td-556c53: accept `h`
in the grammar (never `m`, which means months), compute `anchor_instant −
duration`, and keep the result a **raw instant** — rebuilding a `TemporalValue`
from it would reintroduce DST fall-back ambiguity. All-day anchors accept clock
leads: `5h` before June 1 is 19:00 on May 31.
