# malformed/wrong-types

Twenty-one records, each carrying exactly one type or value violation, so a
single run enumerates most of the per-field validation surface.

## What it exercises

One record each for: an integer `id`; an id that is not 8 hex characters; a
record with no id; an unknown `state`; a `priority` outside A/B/C; a non-string
`title`; a whitespace-only `title`; `tags` as a string; `tags` containing a
number; an impossible date (2026-02-30); a date in the wrong shape
(`14/06/2026`); a `closed` date on an open task; an unparseable `recur` cookie;
a `lead` with no anchor date; a `lead` beside both anchors; `scheduled_time`
with no `scheduled`; a malformed `scheduled_time.local`; an `updated` stamp
missing its device slug; a `delegation` on a `PROPOSED` task (which also trips
the delegation shape rule); a section carrying task-only fields; and an unknown
record `type`.

It also exercises two behaviors of the checker itself:

- **It never raises.** The integer id and the integer title would crash a naive
  regex or `strip` call; the guard order (type first, then value) is load-bearing
  because `check` runs *after* a write in the mutation path, where a raise would
  bypass the rollback.
- **Errors accumulate.** All 24 are reported in one pass, sorted by line, with
  multiple errors permitted on one line.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 3: malformed id 12345678 (expected 8 hex chars)
error  line 4: invalid state "STARTED" (expected PROPOSED/INBOX/TODO/NEXT/WAITING/DONE/CANCELLED)
error  line 5: invalid priority "Z" (expected A, B, or C)
error  line 6: title must be a string
error  line 7: tags must be an array
error  line 8: tags must all be strings
error  line 9: scheduled 2026-02-30 is not a real date
error  line 10: deadline "14/06/2026" is not a YYYY-MM-DD date
error  line 11: closed date on an open task (TODO)
error  line 12: invalid recur cookie "every week" (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)
error  line 13: lead "3w" with no scheduled date or deadline to hide before
error  line 14: lead "2d" beside both a scheduled date and a deadline (the two dates already express that window)
error  line 15: scheduled_time requires scheduled
error  line 16: scheduled_time.local must be HH:MM
error  line 17: updated "2026-06-01T10:00:00Z" is not an RFC3339 UTC timestamp with device slug
error  line 18: delegation on a proposed task (PROPOSED)
error  line 18: delegation.mode nil must be one of refine/research/implement
error  line 19: section must not carry "state"
error  line 19: section must not carry "deadline"
error  line 19: section must not carry "tags"
error  line 20: malformed id "short" (expected 8 hex chars)
error  line 21: record missing id
error  line 22: task has no title
error  line 23: unknown record type "widget"
24 error(s), 0 warning(s)
exit 1
```

Note the ordering within line 19: the section-field errors follow
`SECTION_FORBIDDEN`'s own order (`state`, `deadline`, `tags`), not the order the
keys appear in the record. And note what is **absent**: an unknown `type` short
-circuits the record, so line 23 produces one error rather than also complaining
about its missing state.
