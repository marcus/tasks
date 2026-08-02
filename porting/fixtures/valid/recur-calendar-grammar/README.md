# valid/recur-calendar-grammar

Twenty tasks, one canonical `recur` value each, covering the parts of the
recurrence grammar `valid/full-field-matrix` does not reach.

`full-field-matrix` carries the six simplest forms — `.+1w`, `++1m`, `+2d`,
`w:mon,wed`, `m:15`, `y:07-04`. Everything `Recur` accepts *beyond* those six
lives here: intervals, the calendar one-hop prefix, ordinal weekdays, `last`,
multi-rule bodies, and multi-digit interval counts.

## Why `valid/`

Every record is a healthy task and the file lints clean. Nothing here is skewed,
broken, or adversarial — it is the accept side of one grammar, so it belongs
with the healthy stores. Its reject-side sibling is
`malformed/recur-non-canonical`.

## What it exercises

`Check#check_task` gates `recur` on `Recur.cookie?`, which is
`COOKIE || !schedule(s).nil?` — and `schedule` accepts a string only when
`canonical_calendar` reproduces it **exactly**. So the accept set is not
"parseable", it is "already in its single canonical spelling". Each record picks
one axis of that:

| Line | `recur` | Axis |
|---|---|---|
| 3 | `2w:mon` | weekly interval > 1 |
| 4 | `+w:mon` | the calendar one-hop prefix |
| 5 | `+3w:tue,thu` | prefix + interval + multi-day body together |
| 6 | `w:mon,tue,wed,thu,fri` | the weekday set, spelled out in `DAYS` order |
| 7 | `w:sat,sun` | the weekend set |
| 8 | `m:last` | the `:last` day-of-month symbol |
| 9 | `m:2tue` | an ordinal weekday |
| 10 | `m:1,15` | a multi-rule monthly body, in canonical (sorted) order |
| 11 | `m:lastfri` | `last` as an *ordinal*, not as a day |
| 12 | `2m:5fri` | monthly interval + the 5th-weekday rule that some months lack |
| 13 | `m:1,last` | numeric and symbolic rules mixed, in `spec_key` order |
| 14 | `m:31` | a day-of-month that only some months have |
| 15 | `y:11:3thu` | the yearly ordinal-weekday form |
| 16 | `2y:02:5fri` | yearly interval + an ordinal that needs a leap year |
| 17 | `+y:12-25` | prefix on a yearly fixed date |
| 18 | `y:02-29` | Feb 29 — valid because `Date.valid_date?` is asked about 2024 |
| 19 | `.+10d` | multi-digit interval count |
| 20 | `++12w` | multi-digit catch-up count |
| 21 | `+1y` | the `y` unit on an interval cookie |
| 22 | `.+3m` | the `m` unit on an interval cookie |

Lines 12, 14, 16 and 18 are deliberately schedules that do not fire in every
cycle. **That is not what this fixture tests.** Whether `2y:02:5fri` ever fires
from a given anchor is next-date math and belongs to campaign 5; validation must
accept it regardless, because acceptance is decided by spelling alone.

## What a correct implementation must do

Accept all twenty as written, and exit 0. A port that validates recurrence by
"can I parse it" rather than "is it already canonical" will pass this fixture and
fail its sibling — which is precisely why the two exist as a pair.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 20 tasks parsed, no structural errors
exit 0
```
