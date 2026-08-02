# Oracle capture: `Store#to_date`'s accepted date grammar

Slice: store-snapshot-items · captured 2026-08-02 · session ses_bbeb66
Probe: `porting/runners/ruby/date-iso8601-probe`
Corpus: `porting/evidence/store-snapshot-items/ruby/date-iso8601-grammar.json`
(60 cases, Ruby 4.0.6, captured under `today = 2026-08-02`)

This capture closes the open question in td-eaf193's handoff — "Date.iso8601's
exact rejection set needs its own Ruby probe" — and is the input the Go repair
of `isoDate` (`go/internal/store/snapshot.go`) must satisfy. Nothing is
implemented here; this iteration only fixes the structured-id defect and
records the date oracle.

`lib/tasks/store.rb#to_date` is `Date.iso8601(str)` rescuing
`ArgumentError`/`Date::Error`, guarded only by `str.is_a?(String) && !str.empty?`.
So the accepted set is all of `Date.iso8601`, not `YYYY-MM-DD`.

## What is accepted

| Form | Example | Result |
|---|---|---|
| Extended calendar | `2026-08-02` | 2026-08-02 |
| Basic calendar | `20260802` | 2026-08-02 |
| Signed year | `-2026-08-02`, `+2026-08-02` | -2026-08-02, 2026-08-02 |
| Reduced to month | `2026-08` | 2026-08-01 (day defaults to 1) |
| Ordinal | `2026-215`, `2026215` | 2026-08-03 |
| Week date | `2026-W31-7`, `2026W317` | 2026-08-02 |
| Datetime prefix | `2026-08-02T10:11:12`, `...T10:11:12.5`, `...Z`, `...+05:00`, `20260802T101112Z`, `2026-215T10:11`, `2026-W31-7T10:11` | the date part; the time is parsed and discarded |
| Out-of-range time | `2026-08-02T25:00:00` | 2026-08-02 — the time is never validated |
| Surrounding whitespace | `" 2026-08-02"`, `"2026-08-02 "`, `"\t2026-08-02\n"` | 2026-08-02 |

## What is rejected (`to_date` returns nil)

- Unpadded components: `2026-8-2`, `2026-08-2`.
- Year alone: `2026` (parses as a *time* — `{hour: 20, min: 26}` — with no date).
- `202608` — basic parsing reads it as `{year: 2020, mon: 26, mday: 8}`, invalid.
- Reduced week: `2026-W31`, `-W31` (a week with no weekday).
- Out-of-range calendar values: `2026-13-01`, `2026-00-01`, `2026-02-30`,
  `2025-02-29`, `2026-000`, `2026-366`, `2026-W00-1`, `2026-W54-1`,
  `2026-W31-0`, `2026-W31-8`. Leap years pass: `2024-02-29`, `2024-366`.
- A bare hour: `2026-08-02T10`; a zone with no time: `2026-08-02Z`.
- A space instead of `T`: `2026-08-02 10:11:12`.
- Non-breaking space as trailing whitespace: `"2026-08-02 "`.
- Anything with leading or trailing junk: `2026-08-02x`, `x2026-08-02`,
  `02-08-2026`, `2026/08/02`, `x`, `""`, `" "`.

## The part that is not pure: truncated forms complete from today

`Date.iso8601` fills components the string omits from the **current date**:

| Input | Parts | Result under `today = 2026-08-02` |
|---|---|---|
| `--08-02` | `{mon: 8, mday: 2}` | 2026-08-02 (year from today) |
| `--0802` | `{mon: 8, mday: 2}` | 2026-08-02 |
| `---02` | `{mday: 2}` | 2026-08-02 (year and month from today) |
| `-215` | `{yday: 215}` | 2026-08-03 (year from today) |
| `-W31-7` | `{cweek: 31, cwday: 7}` | 2026-08-02 (cwyear from today) |

Two-digit years go through Ruby's sliding-window completion, which is also
today-relative and asymmetric between the two spellings:

| Input | Parts | Result |
|---|---|---|
| `08-02` | `{year: 2008, mon: 2}` | 2008-02-01 |
| `-08-02` | `{year: 1992, mon: 2}` | 1992-02-01 |

**Classification: nondeterminism to inject, not to normalize.** These are real
Ruby behaviors on user-visible input (a hand-written `scheduled: "---02"` in a
store parses today-relatively in production Ruby), so the port preserves them —
but `isoDate` cannot read the wall clock directly. The next tick must thread the
slice's existing "today" seam into date coercion so conformance is reproducible,
and the probe records the `today` it captured under for exactly that reason.

## Consequence for the Go port

`time.Parse("2006-01-02")` accepts only the first row of the accept table and
silently drops every other accepted form to `nil`. A store whose `scheduled` is
`20260802`, `2026-215`, or `2026-08-02T10:11:12` reads as unscheduled in Go and
scheduled in Ruby — a divergence on ordinary lenient reads, which is why the
prior review classified it as a Go defect rather than an intentional difference.

## Open question for the implementer

The truncated/two-digit-year rows need a decision on where "today" enters
`store.NewSnapshot`. If threading a clock through the read model turns out to
change this slice's public shape, that is a slicing question for the top tier,
not something the translator should improvise.
