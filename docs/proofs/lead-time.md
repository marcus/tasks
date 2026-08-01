# Recurring lead time proof

Proof for `td-526a45` (epic `td-f18c31`), captured on 2026-08-01 against the
contract in
[`docs/plans/implemented/recurring-lead-time.md`](../plans/implemented/recurring-lead-time.md),
after an independent adversarial review and its fix cycle (findings below).
Every command ran in a temporary task-store sandbox under `/tmp`; nothing in
this proof wrote to the user's task files.

`today` is pinned per command through the same subprocess clock the CLI boundary
tests use (`test/support/sequenced_today.rb`), so the window boundaries below are
asserted against a fixed calendar rather than the day the proof was recorded.

## Transcript

Replay with `bash docs/proofs/lead-time-transcript.sh`.

```sh
#!/usr/bin/env bash
# Sandbox transcript for the recurring lead-time proof (td-526a45). Writes only
# to a temporary store; `today` is pinned per command through the same
# subprocess clock the CLI boundary tests use.
set -e
BIN=/Users/marcus/code/tasks/bin/tasks
SUPPORT=/Users/marcus/code/tasks/test/support/sequenced_today.rb
ROOT=/tmp/tasks-lead-time-proof-run
rm -rf "$ROOT"; mkdir -p "$ROOT/state"
export TASKS_FILE=$ROOT/tasks.jsonl TASKS_ARCHIVE=$ROOT/archive.jsonl XDG_STATE_HOME=$ROOT/state
export TZ=America/Denver RUBYOPT=-r$SUPPORT

on() { TASKS_TEST_TODAY_SEQUENCE="$1" "$BIN" "${@:2}"; }

echo "== motivating case 1: water succulents — the contract's capture, verbatim"
on 2026-06-01 capture "water succulents" --recur 2w:sun --lead 1d
on 2026-06-01 lead "water succulents"
echo "-- the schedule's first occurrence is the anchor, not today:"
on 2026-06-01 lead "water succulents" --json
echo "-- 2026-06-05, two days out: hidden"
on 2026-06-05 list
echo "-- 2026-06-06, the Saturday before: visible"
on 2026-06-06 list
echo "-- do it on the Sunday; the anchor rolls two weeks"
on 2026-06-07 done "water succulents"
echo "-- 2026-06-08, the rest of the cycle: hidden again, window re-armed"
on 2026-06-08 list
on 2026-06-08 list --unavailable
echo "-- 2026-06-20, the Saturday before the next occurrence: visible"
on 2026-06-20 list

echo
echo "== motivating case 2: clean gutters — the contract's capture, verbatim"
on 2026-01-05 capture "clean gutters" --recur y:06-01 --lead 17d
on 2026-01-05 lead "clean gutters"
echo "-- 2026-05-14: hidden"
on 2026-05-14 list
echo "-- 2026-05-15, seventeen days before June 1: visible"
on 2026-05-15 list
echo "-- do it; the anchor rolls a year and the window follows"
on 2026-06-01 done "clean gutters"
on 2026-06-02 list --unavailable

echo
echo "== activate releases exactly one occurrence"
on 2026-06-08 activate "water succulents"
on 2026-06-08 list
on 2026-06-08 lead "water succulents" --json
echo "-- the next roll retires the release"
on 2026-06-21 done "water succulents"
on 2026-06-22 list
on 2026-06-22 list --unavailable

echo
echo "== refusals (rules 1-5)"
on 2026-06-22 defer "water succulents" 2026-07-01 || true
on 2026-06-22 lead "water succulents" soonish || true
on 2026-06-22 capture "no date at all" --lead 3w || true
echo "-- rule 3 the other way: a scheduled-anchored lead task MAY move its own anchor"
on 2026-06-22 schedule "clean gutters" 2027-06-15
echo "-- rule 5: clearing the last date clears the lead with it"
on 2026-06-22 undate "clean gutters"
on 2026-06-22 lead "clean gutters"

echo
echo "== a proposal takes a lead on the same terms it takes a deadline"
on 2026-06-22 propose "maybe reseal the deck" --due 2026-09-01 --lead 2w
on 2026-06-22 lead "reseal the deck"

echo
echo "== activate on a task with NO lead keeps its long-standing meaning"
on 2026-06-22 capture "weekly report" --recur +1w --scheduled 2026-07-15 --state NEXT
on 2026-06-22 activate "weekly report"
on 2026-06-22 show "weekly report"

echo
echo "== clock lead: 5h before an all-day date is 19:00 the evening before"
on 2026-05-01 capture "board the flight" --due 2026-06-01 --lead 5h --state NEXT
on 2026-05-01 lead "board the flight"
echo "-- and rule 3 on a DEADLINE-anchored lead task refuses a second gate:"
on 2026-05-01 schedule "board the flight" 2026-05-20 || true

echo
echo "== check"
"$BIN" check
```

Output:

```text
== motivating case 1: water succulents — the contract's capture, verbatim
TODO water succulents :@home:
TODO water succulents :@home:
  ⏳ 1 day before (1d)
  opens 2026-06-06 (Sat) — 1 day before 2026-06-07
-- the schedule's first occurrence is the anchor, not today:
{"id":"9bd1563b","line":3,"title":"water succulents","lead":"1d","lead_human":"1 day","anchor":"2026-06-07","opens":"2026-06-06","opens_at":"2026-06-06T06:00:00Z","lead_skip":null}
-- 2026-06-05, two days out: hidden
No matching tasks.
-- 2026-06-06, the Saturday before: visible
TODO
  water succulents  @home  ~6/7  ↻ every 2 weeks on Sunday

-- do it on the Sunday; the anchor rolls two weeks
↻ water succulents → next 2026-06-21 (Sun)
TODO water succulents :@home:
-- 2026-06-08, the rest of the cycle: hidden again, window re-armed
No matching tasks.
TODO
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday  (unavailable until 2026-06-20 · 1d before 6/21)

-- 2026-06-20, the Saturday before the next occurrence: visible
TODO
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday


== motivating case 2: clean gutters — the contract's capture, verbatim
TODO clean gutters :@home:
TODO clean gutters :@home:
  ⏳ 17 days before (17d)
  opens 2026-05-15 (Fri) — 17 days before 2026-06-01
-- 2026-05-14: hidden
No matching tasks.
-- 2026-05-15, seventeen days before June 1: visible
TODO
  clean gutters  @home  ~6/1  ↻ yearly on June 1

-- do it; the anchor rolls a year and the window follows
↻ clean gutters → next 2027-06-01 (Tue)
TODO clean gutters :@home:
TODO
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday  (unavailable until 2026-06-20 · 1d before 6/21)
  clean gutters  @home  ~6/1  ↻ yearly on June 1  (unavailable until 2027-05-15 · 17d before 6/1)


== activate releases exactly one occurrence
activate "water succulents" — available now
TODO
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday

{"id":"9bd1563b","line":3,"title":"water succulents","lead":"1d","lead_human":"1 day","anchor":"2026-06-21","opens":"2026-06-20","opens_at":"2026-06-20T06:00:00Z","lead_skip":"2026-06-21"}
-- the next roll retires the release
↻ water succulents → next 2026-07-05 (Sun)
TODO water succulents :@home:
No matching tasks.
TODO
  water succulents  @home  ~7/5  ↻ every 2 weeks on Sunday  (unavailable until 2026-07-04 · 1d before 7/5)
  clean gutters  @home  ~6/1  ↻ yearly on June 1  (unavailable until 2027-05-15 · 17d before 6/1)


== refusals (rules 1-5)
“water succulents” already hides until 1 day before its available-from date — change the window with `tasks lead`, or clear it with `tasks lead <ref> off` first
unrecognized lead time: "soonish"
try: 3w · 2d · 1m · "3 weeks" · "a week" · "10 days" · off
a lead time needs a date to hide before — add --due or --scheduled
-- rule 3 the other way: a scheduled-anchored lead task MAY move its own anchor
TODO clean gutters :@home:
-- rule 5: clearing the last date clears the lead with it
TODO clean gutters :@home:
TODO clean gutters :@home:
  no lead time — set one with `tasks lead "clean gutters" 3w`

== a proposal takes a lead on the same terms it takes a deadline
proposed: maybe reseal the deck [41e06345]
PROPOSED maybe reseal the deck :@home:
  ⏳ 2 weeks before (2w)
  opens 2026-08-18 (Tue) — 2 weeks before 2026-09-01

== activate on a task with NO lead keeps its long-standing meaning
NEXT weekly report :@home:
activate "weekly report" — available now
NEXT weekly report :@home:
  id:        61962816
  project:   Inbox
  availability: available now
  recur:     +1w (every week from the scheduled date)
  Captured [2026-06-22].

== clock lead: 5h before an all-day date is 19:00 the evening before
NEXT board the flight :@home:
NEXT board the flight :@home:
  ⏳ 5 hours before (5h)
  opens 2026-05-31 19:00 America/Denver — 5 hours before 2026-06-01
-- and rule 3 on a DEADLINE-anchored lead task refuses a second gate:
a lead time of 5 hours hides this task before its date — carrying a deadline AND an available-from date beside it would leave a second, ignored gate. Clear one of them (`tasks undate <ref> --kind scheduled`, or `tasks lead <ref> off`).

== check
ok — 5 tasks parsed, no structural errors
```

## What the transcript shows

- **Motivating case 1, the contract's capture verbatim** —
  `capture "water succulents" --recur 2w:sun --lead 1d`, no other flags. The
  schedule's first occurrence becomes the anchor (2026-06-07, not today), and the
  task is invisible until the Saturday before it: absent from `list` on
  2026-06-05, present on 2026-06-06, hidden again from 2026-06-08 after `done`
  rolls the anchor to 2026-06-21, and visible again on 2026-06-20.
- **Motivating case 2, likewise verbatim** —
  `capture "clean gutters" --recur y:06-01 --lead 17d` anchors on June 1 and is
  invisible until May 15, and after completion the window follows the anchor to
  2027-05-15.
- **Activation releases exactly one occurrence.** `activate` makes the current
  occurrence available while keeping the date it is measured from, and the next
  roll retires the stamp so the following occurrence is hidden again. A task with
  **no** lead — including a recurring one — keeps activation's long-standing
  meaning of clearing a future available-from date.
- **Rule 3 in both directions.** A timed defer is refused on any lead task, and a
  second gate is refused on a deadline-anchored one — while a scheduled-anchored
  lead task may still move its own anchor, which is an occurrence edit rather
  than a second gate.
- **Rule 5.** `undate` clears the lead with the last date, in one write.
- **A proposal carries a lead** on the same terms it carries a deadline, and
  `lead <ref>` resolves it.
- **Clock lead.** `5h` before an all-day 2026-06-01 opens at 19:00 on 2026-05-31
  local — the anchor's first instant minus five real hours.
- `tasks check` reports the sandbox store structurally clean at the end.

## TUI artifact

Replay with `betamax -f docs/proofs/lead-time.keys docs/proofs/lead-time-tui.sh`.

![Lead-gated rows in the Next view](lead-time-rows.png)

Reveal (`Z`) shows both lead-gated rows carrying the ordinary timed-unavailable
marker with the **derived** date — `5/15` for the deadline-anchored gutters task,
`6/6` for the recurring succulents one — neither of which is a date either record
stores.

![The task detail panel](lead-time-detail.png)

The detail panel reads the span and the date it opens beside the availability
line, so "hidden" and "due 2036-06-01" never contradict each other on screen.

![The Lead time field in the editor](lead-time-editor.png)

The editor's **Lead time** field sits beside Recurrence in the Timing group,
renders the committed span as prose with the date it opens, and owns `lead`
alone.

## Gate commands

| command | result |
| --- | --- |
| `ruby test/all.rb` | 2049 runs, 29993 assertions, 0 failures, 0 errors |
| `bundle exec ruby test/api/all.rb` | 106 runs, 2586 assertions, 0 failures, 0 errors |
| `bin/tasks check` | ok — no structural errors |
| `git diff --check` | clean |

## Review findings

Six findings came from re-reading the shipped code against the contract, and six
more from an independent adversarial reviewer who had the contract and the code
but not the implementer's assumptions. All are fixed, each with a test.

**First pass (self-review):**

1. Rule 3 was enforced from one direction only — adding a *deadline* to a
   scheduled-anchored lead task stranded the available-from date.
2. A dry-run inherited a release stamp the write would retire, promising
   "available now" for a window about to re-arm.
3. Rule 5 was missing: clearing the last date left an orphan `lead`.
4. A state rule the contract does not have refused leads on proposed and closed
   tasks.
5. `Check` was silent on two shapes writes refuse (a lead with no anchor, a lead
   beside a two-date window).
6. `schedule` swallowed the store's rule-3 sentence behind a generic failure.

**Second pass (independent review):**

7. **P1** — `activate` had been changed for **non-lead recurring** tasks, which
   the contract explicitly says is unchanged. Reverted; the occurrence-release is
   now scoped to lead tasks, and a `lead_skip` with no `lead` can no longer erase
   an ordinary available-from gate (it is reported by `Check` instead).
8. **P1** — a single changeset that *moved* the anchor (clear one date, set the
   other) passed through a momentary dateless state, and rule 5's cleanup
   destroyed the lead with an HTTP 200 and no warning. The cleanup now runs once,
   after the whole changeset, and only for date-owning fields.
9. **P1** — `lead --dry-run` passed a bare `Date` where the write uses the full
   temporal value, so a timed anchor's preview was off by hours.
10. **P2** — the derived gate ignored the anchor's timezone, contradicting an
    explicit clause. It now opens at midnight in the anchor's own effective zone.
11. **P2** — the contract's verbatim captures did not work (a recurring capture
    with no date anchored on *today*, putting the window in the past), and the
    test that claimed to prove them quietly added three flags. A lead now seeds
    the schedule's first occurrence, and the test runs the captures as written.
12. **P2/P3** — OpenAPI documented a state rule the code does not enforce; the
    `lead` command printed a dangling em dash on a proposal; a matrix boundary
    assertion probed mid-day rather than the release instant; the TUI's
    date-grained fallback could not express a clock lead; error-message grammar
    ("a 3 weeks lead").

**Third pass (independent re-review of the fixes).** All six were confirmed
fixed, with the P1-2 fix specifically checked for having moved the bug rather
than removed it (it had not: `undate` still retires `recur`, `activate` still
preserves it). Five smaller findings came out of the re-read and are fixed here:

13. **P2** — both `tasks-cli` skill copies still told agents that activating a
    recurring task preserves its date, which is exactly the behavior finding 7
    reverted. Agent-facing guidance that is actively wrong is worse than absent.
14. **P3** — `Tui::Views#fallback_gate_date` had not received finding 7's
    ordering fix, and its clock-span fallback truncated instead of rounding away
    from the anchor, so the renderer's date-grained answer disagreed with the
    query in three cases. Both fixed, with a test that asserts the two agree.
15. **P3** — `capture` discarded the store's field refusal behind "no Inbox
    section found?". Inherited rather than introduced (recurrence's range guard
    was swallowed identically), and now fixed for both.
16. **P3** — the contract's delta list had gone stale: clock units and the
    first-occurrence seeding were documented elsewhere but missing from the list
    that claims to be complete.
17. **P3** — one grammar string in `bin/tasks` was missed by finding 12's pass.

One reported item was not a defect: the TUI-artifact prose says "due 2036-06-01"
because the proof's fixture deliberately anchors a decade out, so the windows
stay closed whenever the artifact is replayed.
