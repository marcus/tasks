# Recurring schedules

Status: implemented (2026-07-28). Supersedes `recurring-tasks.md`, which
described the interval-only system this grew out of.

A task *recurs* when it carries a `recur` schedule alongside a `scheduled` or
`deadline` date. Recurrence adds no state, no record type, and no second record:
the stamp on the task **is** its next occurrence, and the schedule only says how
that stamp advances when the task is completed. Nothing is materialized ahead of
time.

The feature began as org-mode repeater cookies (interval-only) while the project
used Org files; the July 2026 JSONL migration moved the cookie into its own
record field, and this change added calendar schedules beside it in the same
field. Every cookie ever written remains valid — no schema bump, no migration.

## Record shape

One scalar string field, `recur`, holds both stored shapes:

```json
{"type":"task","id":"e5f6a7b8","parent":"a1b2c3d4","state":"NEXT","title":"Water the plants","tags":["@home"],"scheduled":"2026-07-08","recur":".+1w","body":"- Did [2026-07-01]."}
{"type":"task","id":"b8c9d0e1","parent":"a1b2c3d4","state":"TODO","title":"Team sync","scheduled":"2026-07-29","recur":"w:mon,wed"}
```

Keeping it one scalar is deliberate: scalar last-writer-wins merge semantics
(multi-device) keep working for free, and the field round-trips through every
existing reader.

### Interval cookies

`<prefix><count><unit>` — advance the stamp by a fixed span.

| Cookie | Meaning | Next date |
|---|---|---|
| `+1w` | Fixed, one hop | Stored date + one interval; may remain overdue |
| `++1w` | Catch-up | Stored date + intervals until the result is not behind today |
| `.+1w` | From completion | Completion date + one interval |

Count is a positive integer (zero is rejected, not clamped — `++0d` would loop
forever). Unit is `d`/`w`/`m`/`y`; months and years step by calendar arithmetic
(`Date#>>`), which clamps overflow, so a monthly task on January 31 lands on
February 28 in a common year.

### Calendar schedules

Advance the stamp to the next matching calendar date.

```
calendar   := prefix? body
prefix     := "+"                      # one-hop; absent (default) = catch-up
body       := weekly | monthly | yearly
weekly     := [N] "w:" day ("," day)*  # N ≥ 2 = every Nth week, parity anchored
monthly    := [N] "m:" mspec ("," mspec)*
mspec      := 1..31 | "last" | ordday
ordday     := ("1".."5" | "last") day  # 2tue, lastfri
yearly     := [N] "y:" MM "-" DD | [N] "y:" MM ":" ordday
```

Examples: `w:mon,wed,fri`, `2w:mon`, `m:15`, `m:1,15`, `m:last`, `m:2tue`,
`m:lastfri`, `y:07-04`, `y:11:3thu`, `+w:mon`.

Only two prefixes exist, because "from completion" and "catch-up" coincide when
the occurrences are calendar-fixed — the next Monday after completion *is* the
next future Monday:

| Value | On completion, the stamp becomes |
|---|---|
| `w:mon` (bare, catch-up — the default) | the next matching date strictly after today |
| `+w:mon` (one hop) | the next matching date strictly after the stored date — may stay in the past, for cadences where every missed occurrence must be processed (invoicing, log reviews) |

`.+` and `++` on a calendar schedule are rejected with a message explaining that
the default already means "after today", and pointing at `+` for one-hop.

`Nw`/`Nm`/`Ny` parity anchors on the stamp at the moment the schedule is set
(`2w:mon` anchors on the stamp's ISO week) and stays anchored: rolls step in
whole blocks from the anchor, so "every other Monday" never drifts.

### Edge-date rules

- **Numeric day a month lacks** (`m:31` in April) — clamps to the month's last
  day, consistent with the `Date#>>` clamp intervals already use. `m:31` is
  therefore a synonym for `m:last` in short months, by design.
- **Ordinal weekday a month lacks** (`m:5fri`) — skips to the next month that
  has one. A 5th Friday genuinely doesn't exist, and clamping would silently
  mean "last Friday", which has its own spelling.
- **`y:02-29`** — clamps to Feb 28 in non-leap years (same clamp rule).
- **DST gaps** — a candidate whose local wall time doesn't exist skips to the
  next occurrence, keeping its clock time and fixed zone.
- **Both dates present** — the deadline carries the schedule; the scheduled date
  shifts by the same day offset, so lead time is preserved.

## Input: two syntaxes, one stored spelling

Nobody has to type the canonical form. `Recur.parse` / `Recur.parse_result` is
the single entry point every surface uses, and it accepts:

1. **Natural phrases** — `daily`, `weekly`, `2w`, `every 3 days`, `every
   monday`, `every mon wed fri`, `weekdays`, `weekends`, `the 15th`, `1st of the
   month`, `last day of the month`, `2nd tuesday`, `last friday of the month`,
   `every 2 weeks on monday`, `every july 4`. Case-insensitive; filler words
   (`on`, `the`, `of`, `each`, `every`) ignored.
2. **Canonical grammar** — passthrough, validated.
3. **Clearing words** — `off`, `none`, `never`, `clear`, `no`, `stop` → `:off`.

Output is the one canonical spelling, so two inputs meaning the same schedule
store identically (`every mon wed` and `w:wed,mon` both store `w:mon,wed`).

Bare **intervals** default to `.+` (from completion), which avoids leaving a
completed task overdue; bare **calendar** input defaults to the prefixless
catch-up form. Different defaults, each matching its shape's natural
expectation. A `default_prefix:` keyword picks the interval default (`recur
--from schedule` passes `+`); calendar schedules carry their own prefix and
ignore it.

Ambiguous input is refused rather than guessed: `2 tuesdays` could be a cadence
or an ordinal, so the parser names both spellings in its error.

Cron and RRULE input were considered and dropped (2026-07-28 review). Cron cannot
express the core cases (every 2 weeks, 2nd Tuesday, from-completion intervals)
and its dom+dow union rule is a footgun; RRULE is verbose for humans and only
earns its keep if calendar interop becomes a real requirement. The explain
surface makes the supported grammar cheap for an agent to discover instead.

## Explain: the discoverability contract

Every surface can answer "what does this schedule mean, and when does it fire
next" **without mutating anything**. One core function powers all of it:

```
Recur.explain(input, context:, count: 5, from: nil)
```

It returns exactly one of three structurally-distinguishable shapes:

| Shape | Keys | Meaning |
|---|---|---|
| Projected | `input, canonical, human, next` | Understood and projected |
| Never fires | `input, canonical, human, next: [], error` | Understood, but unreachable from this anchor |
| Unparsable | `input, error` | Not a schedule |

The middle shape matters: whether a schedule ever fires depends on the anchor,
not on the schedule alone (`2y:02:5fri` needs a leap February on the right
parity), so a projection failure must still report what was typed in canonical
form rather than masquerading as a parse error.

`human` is a one-line rendering — "every Mon, Wed, Fri", "monthly on the 2nd
Tuesday", "every 2 weeks from completion" — produced by one renderer shared by
`show`, `list`, the TUI, and the API.

## Completion behavior

Completing a recurring task (`done`, or `state … DONE`):

- Advances the date carrying the recurrence (deadline preferred; scheduled
  shifts by the same offset when both exist).
- Leaves the task in its current open state and does **not** set `closed`.
- Appends `- Did [YYYY-MM-DD].` to the body, since the task never closes.
- Drops the `defer` On Hold marker and rolls any delegation forward — same mode
  or person, always unclaimed, fresh timestamp, no `work_ref`.
- Creates one undoable journal entry.
- Does **not** cascade: a recurring parent is an occurrence, not the project.

Timed `++` catch-up compares the exact release/due boundary; `.+` uses the
completion date in the value's effective zone.

`cancel` closes the task and stops the recurrence. A recurring *descendant*
caught in a parent's completion cascade closes outright — its schedule is
retired, no roll-forward. Dating commands (`due`/`schedule`/`reschedule`)
preserve `recur`; removing the task's last date removes it, because an undated
task has nothing to advance. A `PROPOSED` task cannot carry recurrence.

## Write-time guards

A schedule can parse cleanly and still be unwritable in two ways, both of which
would leave a task nothing could ever complete. Both are checked at **write**
time by `Store#unreachable_recurrence`, which computes the one occurrence the
schedule would produce and refuses the write with the engine's own reason:

- **Unreachable** — `2y:02:5fri` anchored in an odd year needs a February with
  five Fridays, and odd years are never leap, so the roll has no target.
- **Unstorable** — `+9999y` (or `9999y:07-04`) rolls past the four-digit years a
  stored date is written with, so the roll would succeed and then fail the
  post-write check, rolling every completion back forever.

Because this is anchor-dependent, `recur --explain` can accept an input from
today's anchor that a specific task's stamp then refuses. That asymmetry is the
guard working, and the CLI/API errors say why.

`check` validates stored values against the full grammar and reports — never
repairs — anything unparsable. An unparsable stored value degrades to
non-recurring on read (so completion closes the task normally) and is echoed
verbatim by the humanizer rather than hidden.

## Surfaces

### CLI (`bin/tasks`)

- `recur <ref> <schedule>` — set/replace. Accepts every input form. `--from
  schedule|completion` applies to bare *intervals*; combined with a calendar
  schedule it exits 1 naming the prefix the input lacks. `--on <date>` seeds a
  deadline when the task has no date yet, and the seed plus the schedule land in
  **one checked transaction**. Success prints `↻ <humanized> (<canonical>) →
  next <date> (<Dow>)`.
- `recur <ref>` — read-only preview: humanized schedule plus `--count N`
  occurrences (default 5, max 50), the list starting at the task's stamp,
  because the stamp *is* the next occurrence. Rejects
  `--from`/`--on`/`--dry-run`.
- `recur --explain "<schedule>"` — taskless: no ref, no store access. Exit 0
  when projected, exit 1 for both failure shapes with the reason on stderr.
  `--json` emits the engine payload verbatim.
- `capture --recur` / `add --recur` — same parser; `off` is rejected (a new task
  has no schedule to clear), and a recurring capture with no date is scheduled
  today so it has something to repeat from.
- `list --recurring` — rows show `↻ <humanized>`; JSON carries `recur`
  (canonical) and `recur_human`.

### TUI (`lib/tui`)

- `r` opens the recurrence popup on the selected task, pre-filled with its
  current value and suffixed with `(now <humanized>)`. A live footer runs
  `Recur.explain` on whatever is typed — humanized text plus the next dates,
  fitted to the width — or shows the parse error.
- The detail panel shows a `repeats` row with the humanized schedule.
- The `↻` badge marks recurring tasks in list views; completing one rolls it
  forward and the selection follows it.
- Shortcut hint: `recur — weekly · every mon · m:15 · off`. No new keybindings.

### API (`lib/tasks/api`)

- `recurrence` on create/patch takes both input syntaxes through the shared
  parser and stores/echoes canonical. Rejections are `422` carrying the parser's
  reason.
- `recurrence_human` accompanies `recurrence` on single-task and collection
  payloads — a pure string render, no occurrence math in lists.
- `GET /api/v1/recurrence/explain?input=…&count=…` is the twin of `recur
  --explain`, returning the `RecurrenceExplanation` payload with no store
  access.
- The `recurring` list filter is unchanged.

**One deliberate CLI/API difference**: a `--count` above 50 is *rejected* by the
CLI (a typed flag with a typo should be told, not silently reinterpreted), while
the API *clamps* it to 50 (a client asking for "as many as you have" should get
the ceiling, not a 422). Non-integer input fails on both.

## Accepted gaps

- **Timed-stamp projection edge.** For a `++` interval on a *timed* stamp whose
  local time has already passed today, completion rejects today and rolls to the
  next occurrence, while the date-only projection still reports today. Catch-up
  deliberately stops *at* today rather than past it, so the stamp's end-of-day
  boundary decides — and a date-only projection has no time to compare with.
  Recovering the difference would mean projecting instants, not dates.
- **Date edits can strand a calendar schedule.** Editing a task's date after the
  schedule is set can produce a stamp/schedule pairing that never fires; the
  date commands do not re-run the satisfiability guard. `done` then refuses the
  roll with a clear reason, and the schedule is clearable with `recur <ref> off`
  or a new date. Deferred rather than fixed because the write-time guard already
  covers the common path.

## Implementation map

| Piece | Where |
|---|---|
| Grammar, parser, normalizer, humanizer, occurrence math, `explain` | `lib/tasks/recur.rb` |
| Roll-forward, write validation, satisfiability/storability guard | `lib/tasks/store.rb` (`advance_recurrence_records`, `patch_recurrence`, `normalize_create_recurrence`, `unreachable_recurrence`) |
| Stored-value validation | `lib/tasks/check.rb` |
| CLI command, preview, explain | `bin/tasks` (`cmd_recur`, `recur_preview`, `recur_explain`, `parse_recur_input`) |
| TUI popup, live preview, detail row | `lib/tui/app.rb` (`open_recur_popup`, `recur_preview`), `task_details.rb`, `shortcuts.rb`, `form.rb` |
| API normalizer, representation, explain endpoint | `lib/tasks/api/app.rb`, `api/representation.rb` |
| Contracts | `docs/cli-spec.md` (Recurrence), `docs/api/openapi.yaml` |

All writes go through `Tasks::Application` / `Tasks::Store`: lock, journal,
atomic write, post-write structural check, automatic rollback. No surface has a
private mutation path.

## Test coverage

- `test/test_recur.rb` — interval parsing, `next_date`, temporal/DST cases.
- `test/test_recur_calendar.rb` — every calendar grammar production, natural
  phrases, rejection messages, canonicalization, edge-date rules, `explain`.
- `test/test_store_recur_calendar.rb` — store-level completion: catch-up vs one
  hop, parity anchoring, both-dates offset, the unreachable/unstorable guards,
  cascade and retire rules.
- `test/test_store.rb`, `test/test_check.rb` — write validation and stored-value
  linting.
- `test/test_cli_mutations.rb` — `recur` set/preview/explain, exit codes, JSON.
- `test/test_app.rb`, `test/test_modals.rb`, `test/test_form.rb`,
  `test/test_shortcuts.rb`, `test/test_task_editor_session.rb` — TUI popup, live
  preview, detail rendering.
- `test/api/test_recurrence.rb` — create/patch normalization, representation,
  and the explain endpoint against the OpenAPI contract.
