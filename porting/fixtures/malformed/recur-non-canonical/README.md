# malformed/recur-non-canonical

Twenty-seven tasks, one rejected `recur` value each. The reject side of the
grammar `valid/recur-calendar-grammar` accepts.

## Why `malformed/`

`Check` reports every one of these as an **error**, so the file exits 1. That is
the definition of this class: a broken store paired with the diagnostic Ruby
actually produces.

`malformed/wrong-types` already holds one bad cookie (`"every week"`), but it
sits among 23 unrelated violations and it is the *easy* rejection — a string that
looks nothing like a schedule. Every record here is the hard kind: something a
lenient parser would accept.

## What it exercises

Three distinct rejection mechanisms, which a port is likely to collapse into one:

**1. Grammar violations** — the value cannot be parsed at all.

| Line | `recur` | Why |
|---|---|---|
| 6 | `0w:mon` | interval must be at least 1 |
| 10 | `w:` | empty weekly body |
| 11 | `w:mon,` | trailing empty day |
| 12 | `w:xyz` | unknown day of week |
| 13 | `m:0` | day of month below range |
| 14 | `m:32` | day of month above range |
| 17 | `m:6tue` | ordinal weekday above 5 |
| 19 | `m:` | empty monthly body |
| 20 | `y:13-01` | month above range |
| 21 | `y:02-30` | February has no 30th |
| 24 | `y:11:6thu` | yearly ordinal weekday above 5 |
| 27 | `+0d` | zero interval — `COOKIE` excludes it deliberately |
| 28 | `+1W` | uppercase unit; the stored grammar is lowercase |

**2. Parseable but not canonical** — `Recur` understands the value, and
`Recur.parse` would happily normalize it, but `schedule()` requires
`canonical == input` so a stored value in any other spelling is refused. This is
the branch a port most easily gets wrong, because "it parses" feels like enough.

| Line | `recur` | Canonical spelling |
|---|---|---|
| 3 | `.+w:mon` | — (`.+` is an interval prefix; a calendar schedule already advances) |
| 4 | `++m:15` | — (same refusal, other prefix) |
| 5 | `1w:mon` | `w:mon` (interval 1 is implicit) |
| 7 | `w:monday` | `w:mon` |
| 8 | `w:wed,mon` | `w:mon,wed` (days sort by `DAY_INDEX`) |
| 9 | `w:mon,mon` | `w:mon` (days are uniq'd) |
| 15 | `m:01` | `m:1` (no zero padding on day-of-month) |
| 16 | `m:15,1` | `m:1,15` (rules sort by `spec_key`) |
| 18 | `m:2tues` | `m:2tue` (`tues` is an input alias only) |
| 22 | `y:7-04` | `y:07-04` (yearly month IS zero-padded) |
| 23 | `y:07-4` | `y:07-04` (and so is the day) |

Lines 15 and 22 together are the trap: day-of-month is *not* zero-padded, the
yearly month *is*. One canonicalizer, two opposite rules.

**3. Padding and type, rejected by `Check` rather than by `Recur`.**

| Line | `recur` | Note |
|---|---|---|
| 25 | `" w:mon"` | `Recur.cookie?` returns **true** — it strips. `Check` requires `rc == rc.strip`. |
| 26 | `"w:mon "` | same, trailing. |
| 29 | `7` (integer) | the type guard: `Check` must report, never raise, on a non-String. |

Lines 25 and 26 are the only rows where `Recur.cookie?` and `Check` disagree. A
port that validates by calling its recurrence parser and nothing else will accept
both and pass every other row in this file.

## What a correct implementation must do

Reject all twenty-seven, exit 1, and report them on the lines given.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 3: invalid recur cookie ".+w:mon" (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)
… one such error per record, lines 3–29 …
error  line 29: invalid recur cookie 7 (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)
27 error(s), 0 warning(s)
exit 1
```

## Finding

**`Check` discards every reason `Recur` produced.** `Recur.parse_result` returns
richly specific rejections — `"day of month must be 1–31: \"32\""`,
`"\".+\" is an interval prefix; a calendar schedule already advances…"`,
`"unknown day of week: \"xyz\""`. `check_task` calls the boolean `Recur.cookie?`
instead, so all twenty-seven rows above collapse into one message that varies
only by the inspected value.

Two consequences for the port:

1. On the `check` surface, a port owes **`cookie?` fidelity only** — accept/reject
   agreement and the one fixed message. It does not owe Ruby's per-reason
   wording here, and reproducing it would be a divergence, not an improvement.
2. Those per-reason messages *are* user-visible on the input surface
   (`tasks recur`, `Recur.explain`), which is a different slice. The same grammar
   is validated twice with two different levels of diagnosis, and a port that
   unifies them will change one of the two surfaces' output.
