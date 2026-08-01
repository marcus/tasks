# Recurring lead time proof

Proof for `td-526a45` (epic `td-f18c31`), captured on 2026-08-01 against the
contract in
[`docs/plans/implemented/recurring-lead-time.md`](../plans/implemented/recurring-lead-time.md).
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

echo "== motivating case 1: water succulents — 2w:sun, 1d lead"
on 2026-06-01 capture "water succulents" --recur 2w:sun --lead 1d --state NEXT --scheduled 2026-06-07
on 2026-06-01 lead "water succulents"
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
echo "== motivating case 2: clean gutters — y:06-01, 17d lead"
on 2026-01-05 capture "clean gutters" --recur y:06-01 --lead 17d --state NEXT --due 2026-06-01
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
on 2026-06-22 schedule "clean gutters" 2026-05-01 || true
echo "-- rule 5: clearing the last date clears the lead with it"
on 2026-06-22 undate "clean gutters"
on 2026-06-22 lead "clean gutters"

echo
echo "== a proposal takes a lead on the same terms it takes a deadline"
on 2026-06-22 propose "maybe reseal the deck" --due 2026-09-01 --lead 2w
on 2026-06-22 lead "reseal the deck"

echo
echo "== clock lead: 5h before an all-day date is 19:00 the evening before"
on 2026-05-01 capture "board the flight" --due 2026-06-01 --lead 5h --state NEXT
on 2026-05-01 lead "board the flight"

echo
echo "== check"
"$BIN" check
```

Output:

```text
== motivating case 1: water succulents — 2w:sun, 1d lead
NEXT water succulents :@home:
NEXT water succulents :@home:
  ⏳ 1 day before (1d)
  opens 2026-06-06 (Sat) — 1 day before 2026-06-07
-- 2026-06-05, two days out: hidden
No matching tasks.
-- 2026-06-06, the Saturday before: visible
NEXT
  water succulents  @home  ~6/7  ↻ every 2 weeks on Sunday

-- do it on the Sunday; the anchor rolls two weeks
↻ water succulents → next 2026-06-21 (Sun)
NEXT water succulents :@home:
-- 2026-06-08, the rest of the cycle: hidden again, window re-armed
No matching tasks.
NEXT
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday  (unavailable until 2026-06-20 · 1d before 6/21)

-- 2026-06-20, the Saturday before the next occurrence: visible
NEXT
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday


== motivating case 2: clean gutters — y:06-01, 17d lead
NEXT clean gutters :@home:
NEXT clean gutters :@home:
  ⏳ 17 days before (17d)
  opens 2026-05-15 (Fri) — 17 days before 2026-06-01
-- 2026-05-14: hidden
No matching tasks.
-- 2026-05-15, seventeen days before June 1: visible
NEXT
  clean gutters  @home  6/1  ↻ yearly on June 1

-- do it; the anchor rolls a year and the window follows
↻ clean gutters → next 2027-06-01 (Tue)
NEXT clean gutters :@home:
NEXT
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday  (unavailable until 2026-06-20 · 1d before 6/21)
  clean gutters  @home  6/1  ↻ yearly on June 1  (unavailable until 2027-05-15 · 17d before 6/1)


== activate releases exactly one occurrence
activate "water succulents" — available now
NEXT
  water succulents  @home  ~6/21  ↻ every 2 weeks on Sunday

{"id":"d5f82bd5","line":3,"title":"water succulents","lead":"1d","lead_human":"1 day","anchor":"2026-06-21","opens":"2026-06-20","opens_at":"2026-06-20T06:00:00Z","lead_skip":"2026-06-21"}
-- the next roll retires the release
↻ water succulents → next 2026-07-05 (Sun)
NEXT water succulents :@home:
No matching tasks.
NEXT
  water succulents  @home  ~7/5  ↻ every 2 weeks on Sunday  (unavailable until 2026-07-04 · 1d before 7/5)
  clean gutters  @home  6/1  ↻ yearly on June 1  (unavailable until 2027-05-15 · 17d before 6/1)


== refusals (rules 1-5)
“water succulents” already hides until 1 day before its available-from date — change the window with `tasks lead`, or clear it with `tasks lead <ref> off` first
unrecognized lead time: "soonish"
try: 3w · 2d · 1m · "3 weeks" · "a week" · "10 days" · off
a lead time needs a date to hide before — add --due or --scheduled
a lead time hides this task until 17 days before its date — carrying a deadline AND an available-from date beside it would leave a second, ignored gate. Clear one of them (`tasks undate <ref> --kind scheduled`, or `tasks lead <ref> off`).
-- rule 5: clearing the last date clears the lead with it
NEXT clean gutters :@home:
NEXT clean gutters :@home:
  no lead time — set one with `tasks lead "clean gutters" 3w`

== a proposal takes a lead on the same terms it takes a deadline
proposed: maybe reseal the deck [07b3b4a8]
PROPOSED maybe reseal the deck :@home:
  ⏳ 2 weeks before (2w)
  opens 2026-08-18 (Tue) — 2 weeks before 2026-09-01

== clock lead: 5h before an all-day date is 19:00 the evening before
NEXT board the flight :@home:
NEXT board the flight :@home:
  ⏳ 5 hours before (5h)
  opens 2026-05-31 19:00 America/Denver — 5 hours before 2026-06-01

== check
ok — 4 tasks parsed, no structural errors
```

## What the transcript shows

- **Motivating case 1 — `--recur 2w:sun --lead 1d`.** "water succulents" is
  invisible until the Saturday before each occurrence Sunday: absent from `list`
  on 2026-06-05, present on 2026-06-06, hidden again from 2026-06-08 after
  `done` rolls the anchor to 2026-06-21, and visible again on 2026-06-20. No
  per-cycle maintenance, and no due-date commitment the chore did not have.
- **Motivating case 2 — `--recur y:06-01 --lead 17d`.** "clean gutters" is
  invisible until May 15 each year, and after completion the window follows the
  anchor to 2027-05-15.
- **Activation releases exactly one occurrence.** `activate` makes the current
  occurrence available while keeping the date it is measured from
  (`lead_skip: "2026-06-21"` in the JSON), and the next roll retires the stamp so
  the following occurrence is hidden again.
- **Every refusal names the fix.** A timed defer against a lead task, an
  unreadable span, a lead with no date to hide before, and `schedule` against a
  deadline-anchored lead task each exit non-zero with the rule's own sentence.
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
| `ruby test/all.rb` | 2044 runs, 29964 assertions, 0 failures, 0 errors |
| `bundle exec ruby test/api/all.rb` | 106 runs, 2586 assertions, 0 failures, 0 errors |
| `bin/tasks check` | ok — no structural errors |
| `git diff --check` | clean |

## Review findings

Findings from re-reading the shipped code against this contract, all fixed with
tests before this proof was recorded:

1. **Rule 3 was enforced from one direction only.** Adding an available-from
   date to a deadline-anchored lead task was refused, but adding a *deadline* to
   a scheduled-anchored one was not — which flips the anchor and strands the
   available-from date as the second gate rule 2 forbids.
2. **A dry-run inherited a release stamp the write would retire.** Previewing a
   new lead on a task whose current occurrence had been released by `activate`
   reported "available now", while the real write clears `lead_skip` and re-arms
   the window.
3. **Rule 5 was missing.** Clearing the last date left an orphan `lead` with
   nothing to measure from. `undate` and a date-clearing `due`/`schedule` now
   retire it in the same changeset, exactly as they already retire `recur`.
4. **A state rule the contract does not have.** Leads were refused on proposed
   and closed tasks, which contradicted "propose accepts it on the same terms it
   accepts other dated fields". Removed; `lead` now rides with the date fields.
5. **`Check` was silent on two shapes writes refuse.** A lead with no anchor,
   and a lead beside a two-date window, are reachable by a hand edit or a
   foreign writer and are now reported.
6. **`schedule` swallowed the store's reason.** A rule-3 refusal printed the
   generic "failed to set scheduled"; the store's own sentence now surfaces.
