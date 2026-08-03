# `recur-interval-cookies` Ruby oracle capture

This directory records the Ruby oracle for manifest slice
`recur-interval-cookies`, characterized against source revision
`28424b77950f59f2d78bf089be316c38d9f54aec`. The source closure has no drift
from that revision on this branch.

The focused interval-only oracle passes with **20 runs, 66 assertions, 0
failures, 0 errors, and 0 skips**:

```sh
ruby test/test_recur.rb
```

Representative direct observations confirm the boundaries the Go translation
must preserve: `weekly` normalizes to `.+1w`; a bare `2w` obeys the supplied
default prefix; `++0d` is rejected; month arithmetic clamps 2026-01-31 +1m to
2026-02-28; and catch-up may land on today.

No Go code or Go output was used to produce this capture. The next step is a
**mid-tier translation** in a different context: implement only the interval
cookie parser/projection in `internal/recur`, add the medium-tier property
coverage, then compile and run differential conformance without changing this
Ruby baseline.

## Translation handoff (2026-08-02)

The interval-only translation now lives in `go/internal/recur`. It implements
the positive-count cookie grammar, friendly interval words and bare intervals,
off words, canonical rendering, fixed/from-completion/catch-up projection, and
the Ruby `Date#>>` month/year clamp. Its table tests and month-clamp property
test pass under `go test ./...` and `go vet ./...`.

This is not conformance evidence and does not change the Ruby baseline. No Go
CLI adapter exists yet to invoke this package against the listed fixtures, so
the required differential run remains for a later slice/tick once that adapter
is available. Calendar schedules remain explicitly out of scope. The next
medium-tier context must perform source-fidelity and Go-idiom reviews, then
either wire a legal differential surface or record the resulting harness
blocker before this slice can move to review.

## Source-fidelity review (2026-08-02)

**Rejected — repair required before Go-idiom review.** A separate review of
`go/internal/recur/recur.go` against `lib/tasks/recur.rb` found these exact
divergences:

1. **Blocking:** `parseFriendlyInterval` converts a friendly count with
   `strconv.Atoi` (Go lines 151 and 166), and `NextDate` does the same (line
   102). Ruby's `String#to_i` / `Integer` are arbitrary precision, so a valid
   positive input such as `999999999999999999999999999999999 days` is accepted,
   canonicalized, humanized, and projected by Ruby but rejected or fails later
   in Go. The manifest's prior handoff called this uncertainty out; it is a Go
   defect, not an intentional difference. Represent counts without a machine
   `int` limit (and define safely bounded date stepping) or explicitly block
   the slice through the required Marcus decision; do not silently reject
   values Ruby accepts.
2. **Blocking:** Ruby `Recur.humanize` returns `nil` for an empty/whitespace
   value (`lib/tasks/recur.rb:178-180`), while Go `Humanize` returns `""`
   (`go/internal/recur/recur.go:75-80`). Preserve the nullable result in the
   Go interface before the downstream calendar humanizer consumes it.

No source-fidelity approval is granted. This review made no implementation
edits. After repair, rerun the focused Ruby oracle and Go package tests, then
request a new source-fidelity review from a different context; the independent
Go-idiom review and differential-harness decision remain outstanding.

## Nullable humanization repair (2026-08-02)

`Humanize` now returns `nil` for blank or whitespace-only input, matching
Ruby's `Recur.humanize`; non-interval values remain a non-nil trimmed string.
The Go interface is deliberately nullable so the later calendar humanizer can
preserve the same distinction. Added package tests cover both boundaries.

Verification after the repair:

```sh
(cd go && go test ./... && go test -race ./internal/recur && go vet ./...)
ruby test/test_recur.rb
git diff --check
```

All commands passed (the Ruby focused oracle: 20 runs, 66 assertions). The
arbitrary-size count defect remains unresolved: parse, humanize, and projection
still use `int`/`strconv.Atoi`, so a fresh source-fidelity review is premature.
The next tick must replace that representation and define a range-safe date
projection before requesting review.

## Arbitrary-size projection characterization (2026-08-02)

The follow-up Ruby probe establishes that this is not merely an input parsing
edge. With `n = 999999999999999999999999999999999`, Ruby accepts
`"#{n} days"`, stores `.+#{n}d`, humanizes it as
`"every #{n} days from completion"`, and projects from 2026-01-01 to
`2737907006988507635338165741226-09-01`. The same count is accepted for all
four units; `+#{n}m` projects to
`83333333333333333333333333335359-04-30` and `+#{n}y` to
`1000000000000000000000000000002025-01-31`.

This classifies the outstanding mismatch as a **Go defect**, not an intentional
difference: `time.Time` cannot represent Ruby's valid projected dates. The
next translation tick must introduce an arbitrary-precision civil-date value at
the recurrence package boundary (including Gregorian day arithmetic and
Ruby-compatible month/year clamp), then migrate `NextDate`/`Step` and their
tests to that value. Do not add an overflow rejection or truncate the count:
either would reject or change a valid Ruby result. The current branch has no
code change from this characterization; its last implementation commit remains
`a3485d9`.

## Arbitrary-size projection repair (2026-08-02)

The Go recurrence boundary now uses arbitrary-precision interval counts and an
un-zoned proleptic-Gregorian `CivilDate`, rather than `int` and `time.Time`.
`Step` and `NextDate` therefore preserve Ruby's valid projections outside
`time.Time`'s range while retaining Date-style month/year clamping. The focused
test includes the oracle's 33-digit count for day, month, and year projections.

Verification after the repair:

```sh
(cd go && go test ./internal/recur && go test ./... && go vet ./... && go test -race ./internal/recur)
ruby test/test_recur.rb
git diff --check
```

All commands passed. Differential fixture conformance remains unavailable until
a Go CLI adapter reaches recurrence; this slice remains `translating` pending a
fresh independent source-fidelity review, Go-idiom review, and the recorded
differential-harness decision.

## Source-fidelity repair: token boundaries and caller prefix (2026-08-02)

The fresh source-fidelity review found two Go defects in `ba148da`, both now
repaired. Friendly parsing only treats a count and unit as a joined form when
they are one token (`2w`); it no longer concatenates numeric tokens, so `2 3
days` and `2 3days` are rejected as Ruby rejects them. A bare interval now
preserves the supplied `default_prefix` unchanged, including Ruby's observable
`parse("weekly", default_prefix: "wat") => "wat1w"` boundary.

Ruby probes confirmed both observations against `lib/tasks/recur.rb`.
Verification passed:

```sh
(cd go && go test ./... && go test -race ./internal/recur && go vet ./...)
ruby test/test_recur.rb
git diff --check
```

The focused Ruby oracle remained green: 20 runs and 66 assertions. The Go CLI
adapter still does not expose recurrence, so differential fixture conformance
cannot run; this is a documented harness gap, not a substitute for Go output.
The next step is a fresh independent source-fidelity review (mid-tier), then a
separate Go-idiom review if it passes.

## Source-fidelity repair: Ruby Date Italy reform (2026-08-02)

`CivilDate` now preserves Ruby `Date`'s default `Date::ITALY` calendar rather
than applying a proleptic Gregorian calendar to every year. Day and week
projection use Julian arithmetic before 1582-10-15 and Gregorian arithmetic
from that date, so `1582-10-04 + 1d` becomes `1582-10-15`. Month and year
projection retain Ruby's clamp behavior when their target lands in the omitted
1582-10-05 through 1582-10-14 range: `1582-09-10 >> 1` becomes `1582-10-04`.
The pre-reform leap rule is also Julian (`1500-02-28 + 1d` becomes
`1500-02-29`).

The package regression covers those three direct projections plus the public
`NextDate("+1d", 1582-10-04)` path. Verification passed:

```sh
(cd go && go test ./internal/recur && go test ./... && go test -race ./internal/recur && go vet ./...)
ruby test/test_recur.rb
git diff --check
```

The Ruby focused oracle remains 20 runs and 66 assertions. Differential fixture
conformance is still unavailable because no Go CLI adapter reaches recurrence.
The next step is a fresh independent, mid-tier source-fidelity review of this
repair; the Go-idiom review follows only if that review passes.

## Source-fidelity repair: signed proleptic years (2026-08-02)

`CivilDate.String` now writes negative years with Ruby `Date`'s signed,
zero-padded spelling: `-0001-01-01`, rather than Go's former `00-1-01-01`.
The package regression covers both direct formatting and `NextDate("+1d")`
from that date. A direct Ruby oracle probe produced `-0001-01-01` and
`-0001-01-02` for the same inputs.

Verification passed:

```sh
(cd go && go test ./internal/recur && go test ./... && go test -race ./internal/recur && go vet ./...)
ruby test/test_recur.rb
git diff --check
```

The focused Ruby oracle remains 20 runs and 66 assertions. Differential
fixture conformance remains unavailable because no Go CLI adapter reaches
recurrence. The next steps are fresh, independent mid-tier source-fidelity
and Go-idiom reviews; reviewers must not edit the implementation.

## Source-fidelity review: natural-phrase tokenization (2026-08-02)

**Rejected — repair required before the Go-idiom review.** This review is a
fresh independent context; it read `1ce36ed` against `lib/tasks/recur.rb` and
made no implementation edits. The signed-year, Italy-reform, arbitrary-count,
nullable-humanize and prefix repairs all hold. One blocking divergence remains,
with two sub-causes that share a root:

**Blocking — `parseFriendlyInterval` is not `tokenize` + `take_interval`.**
`go/internal/recur/recur.go:147-186` recognizes only a leading literal
`"every"` and then whitespace-separated fields. Ruby reaches the same
"bare interval" result (`lib/tasks/recur.rb:465-478`) through
`tokenize` (`:504-513`) and `take_interval` (`:517-527`), which do three
things Go does not:

1. `FILLER` (`:108`) drops *every* filler token anywhere in the phrase —
   `on of the each every a an in at and` — not just one leading `every`. So
   Ruby stores `"a week" => ".+1w"`, `"each 2 weeks" => ".+2w"`,
   `"in 3 days" => ".+3d"`, `"and 2 days" => ".+2d"`; Go rejects all of them.
2. `tokenize` translates `, & /` to spaces (`text = s.tr(",&/", "   ")`), so
   `"2,weeks"` and `"2/weeks"` both store `".+2w"`; Go rejects both.
3. `tokenize` splits digit/letter boundaries in *both* directions
   (`(\d)([a-z])` and `([a-z])(\d)`), so `"every3days"` stores `".+3d"`. Go's
   `countUnit` regexp only covers the digits-then-letters joined form.

Observed divergence table (13 of 19 probes disagree, all Go-rejects-valid):
`porting/evidence/recur-interval-cookies/source-fidelity-probe-tokenize.txt`.

Classification: **Go defect**, not an intentional difference and not a legacy
Ruby rule to preserve-by-omission — the affected inputs produce an observable
canonical stored cookie in Ruby today. Correction: port `tokenize` and
`take_interval` faithfully (separator translation, digit/letter splitting,
ordinal marking, filler removal, then the count+unit / `WORDS` / `BARE_UNITS`
peel) and drive the bare-interval branch from `spec.empty? && unit &&
count.positive?` as Ruby does, rather than pattern-matching field counts.
Non-interval `spec` remainders stay out of scope for this slice, but they must
fall through to the calendar/monthly path's rejection rather than being
unreachable.

Confirmed still correct on this branch: `2 3days`, `2 3 days`, `days`,
`weeks`, `every-3-days`, `+007d`, `.+0d`, `++0d`, `0 days`, `2 wks`,
`one week`, `twice weekly`, `every fortnight` all reject in both; `weekly`,
`WEEKLY`, ` .+1w `, `++12w`, `+1y`, `5 Days`, `3 d`, `07 days`, `biweekly`,
`fortnightly`, `quarterly`, `annually`, the four bare units, and all six off
words agree exactly.

Branch state at review time: `go build ./...`, `go vet ./...` and
`go test ./internal/recur` pass. Differential fixture conformance is still
unavailable (no Go CLI adapter reaches recurrence) — unchanged harness gap.
The next tick is a **mid-tier translation** repairing the tokenizer, then a
fresh independent source-fidelity review, then the Go-idiom review.

## Source-fidelity repair: natural-phrase tokenization (2026-08-02)

`parseFriendlyInterval` now follows Ruby's `tokenize` then `take_interval`
sequence for the interval-only branch. It removes every `FILLER` token,
converts `,`, `&`, and `/` to token boundaries, splits ASCII digit/letter
boundaries in both directions, and then peels only a leading count/unit,
`WORDS`, or `BARE_UNITS` interval. It deliberately preserves separate token
boundaries, so two leading numeric tokens remain invalid rather than being
silently concatenated.

New Go regressions cover Ruby-valid `a week`, `each 2 weeks`, `2,weeks`,
`2/weeks`, `in 3 days`, and `every3days`, as well as the Ruby-invalid
`2 3 days`, `2 3days`, `2and3days`, and `2,3days`. A direct Ruby probe returned
the matching canonical cookies and `nil` rejections for those same cases.

Verification passed:

```sh
(cd go && gofmt -w internal/recur/recur.go internal/recur/recur_test.go)
(cd go && go test ./internal/recur && go test ./... && go test -race ./internal/recur && go vet ./...)
ruby test/test_recur.rb # 20 runs, 66 assertions
git diff --check
```

Differential fixture conformance remains unavailable because no Go CLI adapter
reaches recurrence; this is the existing harness gap, not a substituted Go
oracle. The next step is a fresh independent mid-tier source-fidelity review,
followed (only if it passes) by a separate Go-idiom review and independent
approval.

## Source-fidelity repair: Ruby `String#inspect` rejection spelling (2026-08-02)

The prior review classified Go's `%q` echo as a Go defect. Both Recur error
sites now render the echoed input with `rubyInspect`
(`go/internal/recur/inspect.go`) instead: `Parse`'s `unrecognized schedule:`
and `NextDate`'s `not a repeater cookie:`. Ruby quotes the caller's spelling
through `String#inspect` at `lib/tasks/recur.rb:283`, `:335`, `:382`, `:467`
and `:476`, so the quoted form is user-visible output, not a formatting detail.

Ruby oracle capture (39 inputs against both error sites, plus a hex dump of
every input so the Go table is transcribed from bytes rather than from a
literal an editor could rewrite):

```sh
ruby porting/evidence/recur-interval-cookies/oracle-inspect-probe.rb
```

Its output is recorded verbatim in [`oracle-inspect.txt`](oracle-inspect.txt).
The divergences it pins, all of which `%q` got wrong:

- ESC is `\e`, where Go writes `\x1b`.
- Other C0/C1 controls and DEL use Ruby's 4-digit `\uXXXX` escape with
  UPPERCASE hex, where Go writes `\x00`-style escapes.
- An interpolation-introducing `#` is escaped (`\#{`, `\#$`, `\#@`), which Go
  never does; a bare or trailing `#` stays unescaped in both.
- Format (Cf) and private-use (Co) characters print verbatim — soft hyphen,
  ZWSP, BOM, tag characters, U+1D173, U+E000, U+F0000 — where Go's `%q`
  escapes them, because `unicode.IsPrint` excludes both categories.
- U+2028, U+2029 and unassigned codepoints (U+0378, U+2FFFF, U+10FFFF) are
  escaped, using the braced `\u{...}` form above the BMP.

`rubyPrintable` therefore tests `unicode.IsGraphic || Cf || Co`, which is what
Onigmo's print property comes to for these inputs.
`go/internal/recur/inspect_test.go` asserts all 39 captures against both error
sites.

Verification passed:

```sh
(cd go && gofmt -l internal/recur && go build ./... && go vet ./... && go test ./... && go test -race ./internal/recur)
ruby test/test_recur.rb  # 20 runs, 66 assertions, 0 failures
git diff --check
```

### Found while capturing: invalid UTF-8 raises in Ruby (unrepaired Go defect)

The capture's last section feeds Recur three byte strings tagged UTF-8 that are
not valid UTF-8. Ruby never reaches `inspect` for them — the regexp match
raises first: `ArgumentError: invalid byte sequence in UTF-8` for `"\xff"`, and
`Encoding::CompatibilityError: invalid byte sequence in UTF-8` for `"zz\xe6"`
and `"zz\x80"`. `Recur.next_date` propagates the same message. Go's `regexp`
does not raise, so Go returns an ordinary rejection where Ruby raises.

Classified a **Go defect**, not an intentional difference — but outside this
repair's scope, because matching it means deciding how the Go boundary spells a
raised Ruby exception, which is a package-interface question the CLI adapter
slice has to answer anyway. `rubyInspect`'s `\xHH` branch is retained and is
correct for `String#inspect` itself; it is simply unreachable through `Recur`
today. The next source-fidelity reviewer should treat this as recorded, not
overlooked.

### Correction to the standing differential-conformance claim

Earlier entries here say differential fixture conformance "remains unavailable
because no Go CLI adapter exposes internal/recur." That is no longer the whole
picture: three landed slices reach a non-CLI package another way, through a
direct probe pair — `porting/runners/ruby/delegation-probe` plus
`go/cmd/delegation-probe`, driven by
`porting/runners/cases/delegation-record-shape-direct.jsonl`, and likewise for
`format-parse` and `check-meta-and-ids`. Building a
`recur-interval-cookies-direct.jsonl` with a Ruby and Go `recur-probe` pair is
the concrete next step that closes this slice's one never-skippable gap. The
capture script above is oracle evidence, not that harness.

### Direct differential conformance

`porting/runners/ruby/recur-probe` invokes the Ruby interval-package boundary;
`go/cmd/recur-probe` invokes `internal/recur`. The direct case list covers
canonical and friendly parsing, exact rejection spelling, cookie acceptance,
interval humanization, default-prefix passthrough, fixed/completion/catch-up
projection, Date::ITALY's reform day, and an arbitrary-size count. Calendar
grammar remains deliberately excluded until `recur-calendar-grammar` owns that
behavior.

The conformance command runs only the interval-owned cases; the direct case
list documents the deferred control separately so the boundary is visible.

```console
$ porting/evidence/recur-interval-cookies/conformance
recur-interval-cookies direct conformance: 17/17 cases matched
```

The direct probe compares decoded JSON values, so JSON object-member order in
the probe transport cannot hide a semantic mismatch. It is package-boundary
conformance, not a claim that a Go CLI recurrence adapter exists.

### Independent source-fidelity review (2026-08-02): rejected

The 16/16 direct corpus and declared test gates pass, but the Go conformance
adapter is not faithful at one observable package boundary. Ruby's probe uses
`spec.fetch("default_prefix", ".+")`, so an explicitly supplied empty prefix is
preserved: `Tasks::Recur.parse_result("weekly", default_prefix: "")` returns
`{canonical: "1w"}`. In contrast, `go/cmd/recur-probe/main.go` decodes into a
plain string and then replaces `""` with `".+"`, so it cannot distinguish an
absent key from an explicit empty one and would report `.+1w` for the same
case. `internal/recur.Parse` itself preserves the empty prefix; this is a Go
direct-probe/conformance defect, not a reason to bless the incomplete corpus.

Correction applied (2026-08-02): `go/cmd/recur-probe` now decodes
`default_prefix` as a presence-aware `*string`, defaulting only when the JSON
key is absent. The direct corpus includes `explicit-empty-default-prefix`,
which pins Ruby's `weekly` plus an explicit empty prefix to `1w`; the Ruby-vs-Go
comparator passes 17/17 cases. A fresh source-fidelity review is still required
before approval. The existing invalid-UTF-8 boundary remains the separately
recorded CLI-adapter ownership issue above.

### Conformance-adapter repair: signed ISO years (2026-08-02)

The independent source-fidelity review at `786ee22` rejected the slice on a Go
**conformance-adapter** defect, not an `internal/recur` defect: `civilDate` in
`go/cmd/recur-probe/main.go` split the whole date on `-`, so a Ruby-valid
signed year such as `-0001-01-01` produced four segments and panicked. Direct
conformance passed only because the corpus carried no signed year, which left
the arbitrary-precision `CivilDate` boundary unproved at the observable edge.

`civilDate` now peels an optional leading `+`/`-` into the year before
splitting the three components, requires all three components to be digits,
and keeps the year arbitrary-precision. This mirrors the Ruby probe's
`Date.iso8601`, which accepts `-0044-03-15`, `+0000-01-01`, `+2026-01-01` and
`10000-01-01` and renders a negative year back with a signed, zero-padded
spelling.

Eight direct cases were added to
`porting/runners/cases/recur-interval-cookies-direct.jsonl`, including the two
the review named:

| case | from → next |
|---|---|
| `negative-year-fixed-hop` (`+1d`) | `-0044-03-15` → `-0044-03-16` |
| `negative-year-month-clamp` (`+1m`) | `-0044-01-31` → `-0044-02-29` |
| `negative-year-catch-up` (`++1w`) | `-0044-03-01` → `-0044-03-29` |
| `year-zero-boundary` (`+1y`) | `0000-12-31` → `0001-12-31` |
| `explicit-plus-year` (`+1d`) | `+2026-01-01` → `2026-01-02` |
| `five-digit-year` (`+1d`) | `10000-02-28` → `10000-02-29` |
| `review-signed-negative-year` (`+1d`) | `-0001-01-01` → `-0001-01-02` |
| `review-signed-zero-year` (`+1d`) | `+0000-01-01` → `0000-01-02` |

Every expected value above is Ruby's, produced by
`porting/runners/ruby/recur-probe`; none was read from Go. The negative-year
month clamp and the five-digit-year hop both land on a Feb 29, which is Ruby
`Date`'s proleptic-Julian leap rule before 1582 and the Gregorian rule after —
so these cases exercise the calendar split, not only the string parsing.

Verification passed:

```console
$ porting/evidence/recur-interval-cookies/conformance
recur-interval-cookies direct conformance: 25/25 cases matched
```

```sh
(cd go && gofmt -l . && go vet ./... && go test ./... && go test -race ./internal/recur)
ruby test/test_recur.rb  # 20 runs, 66 assertions, 0 failures
git diff --check
```

No `internal/recur` behavior changed in this repair; only the probe adapter and
the case corpus. The invalid-UTF-8 boundary remains owned by the later CLI
adapter slice, as recorded above. The next step is a **fresh independent
mid-tier source-fidelity review**, then the Go-idiom review and independent
approval.

### Conformance-adapter repair: Date.iso8601 shape and validity (2026-08-02)

The independent source-fidelity review at `fa313b0` rejected the slice on a Go
**conformance-adapter** defect: `civilDate` in `go/cmd/recur-probe/main.go`
accepted date strings Ruby's `Date.iso8601` rejects, and crashed rather than
reporting a rejection. Two consequences, both observable at the direct probe
boundary:

1. Narrow fields (`2026-1-1`), out-of-range fields (`2026-13-01`, `2026-04-31`,
   `2026-02-29`) and the ten omitted `Date::ITALY` reform days (`1582-10-05`
   through `1582-10-14`) were all accepted by Go and turned into projections,
   where Ruby raises `Date::Error: invalid date`.
2. Ruby's `next_outcome` rescues `ArgumentError`, and `Date::Error` *is* an
   `ArgumentError`, so both `Date.iso8601` calls sit inside that rescue: a
   rejected `from` or `today` is reported as `{"value": null, "error":
   "invalid date"}`. Go's probe panicked instead, so no corpus could pin it.

Repairs, adapter-side only apart from one new exported constructor:

- `recur.NewCheckedCivilDate` applies Ruby `Date`'s validity rule under
  `Date::ITALY` — month 1..12, day within the month's length on the calendar in
  force for that year, and none of the ten reform days — returning
  `recur.ErrInvalidDate`, whose text is Ruby's message verbatim. The existing
  month-clamp path now shares its `inReformGap` predicate.
- `civilDate` requires an optional sign, then a year, then two-digit month and
  day. The sign is part of the year *and* suppresses Ruby's short-year
  expansion, so a signed year of any length is literal while an unsigned one
  must be four digits or more (`Date.iso8601("1-01-01")` raises, but
  `"12-01-01"` is 2012 and `"123-01-01"` is 2023).
- `nextOutcome` mirrors the Ruby probe's rescue placement and its `from`-then-
  `today` evaluation order.

**Recorded probe-domain restriction.** `Date.iso8601` also accepts basic
(`20260101`), ordinal (`2026-001`), week-date (`2026-W01-1`), truncated
(`--01-01`) and datetime (`2026-01-01T12:00`) forms, and expands an unsigned
two-or-three-digit year. The Go probe models none of them. Rather than guess —
a silent divergence — it prints `recur-probe: "…" is outside the probe's ISO
8601 date domain` and exits 2, so any future case using one of those shapes
fails loudly instead of being answered wrongly. This is a deliberate limit of
the harness, not of `internal/recur`; it is recorded in the manifest entry's
`oracle_gaps`.

Sixteen direct cases were added. Every expected value below is Ruby's, produced
by `porting/runners/ruby/recur-probe`; none was read from Go:

| case | `from` | Ruby result |
|---|---|---|
| `review-narrow-month-and-day` | `2026-1-1` | `invalid date` |
| `review-narrow-day-only` | `2026-01-1` | `invalid date` |
| `review-one-digit-year` | `1-01-01` | `invalid date` |
| `review-month-out-of-range` | `2026-13-01` | `invalid date` |
| `review-month-zero` | `2026-00-10` | `invalid date` |
| `review-day-zero` | `2026-01-00` | `invalid date` |
| `review-day-past-month-length` | `2026-04-31` | `invalid date` |
| `review-non-leap-february-29` | `2026-02-29` | `invalid date` |
| `review-julian-leap-february-29` | `1500-02-29` | `1500-03-01` |
| `review-negative-year-leap-february-29` | `-0044-02-29` | `-0044-03-01` |
| `review-negative-year-non-leap-february-29` | `-0045-02-29` | `invalid date` |
| `review-reform-gap-first-day` | `1582-10-05` | `invalid date` |
| `review-reform-gap-last-day` | `1582-10-14` | `invalid date` |
| `review-reform-gap-boundaries-valid` | `1582-10-04` | `1582-10-15` |
| `review-invalid-today-after-valid-from` | `today` `2026-02-30` | `invalid date` |
| `review-invalid-from-precedes-invalid-today` | both invalid | `invalid date` |

The last four rows are the point of the group: February 29 is valid in 1500 and
in 44 BCE under the Julian leap rule and invalid in 2026 and in 45 BCE, and the
reform gap's two edges bracket a valid day on each side — so the corpus
exercises the calendar split, not only the string shape.

The new corpus has teeth: checking out the pre-repair `go/cmd/recur-probe`
against it makes the comparator abort with a mismatch, and restoring the repair
makes it pass.

Verification passed:

```console
$ porting/evidence/recur-interval-cookies/conformance
recur-interval-cookies direct conformance: 41/41 cases matched
```

```sh
(cd go && gofmt -l . && go vet ./... && go test ./... && go test -race ./internal/recur)
ruby test/test_recur.rb  # 20 runs, 66 assertions, 0 failures
git diff --check
```

`TestNewCheckedCivilDateMatchesRubyDateValidity` pins the same validity rule as
a package unit test, its expectations transcribed from Ruby. No `internal/recur`
*behavior* changed: the new constructor is additive and unused by `Parse`,
`Humanize`, `NextDate` or `Step`. The invalid-UTF-8 boundary remains owned by
the later CLI adapter slice, as recorded above. The next step is a **fresh
independent mid-tier source-fidelity review**, then the Go-idiom review and
independent approval.

## Source-fidelity review: Unicode whitespace and case folding (2026-08-02)

**Rejected — repair required before the Go-idiom review.** A fresh independent
context (`ses_bce601`) read `e59e363` against `lib/tasks/recur.rb` and
`porting/runners/ruby/recur-probe` and made no implementation edits. Everything
the previous three repairs claimed holds: 62 adversarial probes were run through
both probes and 50 matched, including every case that exercises `e59e363`'s
`Date.iso8601`/`Date::ITALY` work — the 1582 reform crossing in both directions,
a `>>` landing inside the ten omitted days (`1582-09-14 + 1m` → `1582-10-04` in
both), Julian-rule leap days (`1500-02-29 + 1y`), the `Jan 31 + 1m` and
leap-day-`+1y` clamps, negative and zero years, a rejected `from`, a `from`
inside the reform gap, and 20- and 30-digit counts. Direct conformance is
41/41.

Two divergences remain, both **inside this slice's own domain** (bare-interval
parsing), both in the direction that matters most — **Go accepts and stores a
canonical cookie for input Ruby rejects**. Evidence:
[`source-fidelity-probe-unicode.txt`](source-fidelity-probe-unicode.txt),
cases in
[`source-fidelity-probe-unicode-cases.jsonl`](source-fidelity-probe-unicode-cases.jsonl).

**Blocking 1 — `tokenize` splits on Unicode space; Ruby splits on ASCII space.**
`go/internal/recur/recur.go:199` ends `tokenize` with `strings.Fields`, which
splits on `unicode.IsSpace`. Ruby's `Recur.tokenize` (`lib/tasks/recur.rb:509`)
ends with `text.split(/\s+/)`, and Ruby's `\s` is `[ \t\r\n\f\v]` — ASCII only.
So `"2 weeks"` is one unsplittable token in Ruby and rejected
(`unrecognized schedule: "2 weeks"`), while Go tokenizes it to
`["2","weeks"]` and stores `.+2w`. Same for U+3000, U+0085 and U+1680. Leading
and trailing Unicode spaces diverge for the same reason plus `rubyStrip`
(`:237-239`), which correctly strips only Ruby's `"\0\t\n\v\f\r "` — so
`" weekly"` is rejected by Ruby and stored as `.+1w` by Go.
Classification: **Go defect**. Correction: replace `strings.Fields` with a split
on exactly `" \t\r\n\f\v"`, keeping the empty-token drop Ruby gets from
`filter_map`. `test_case_and_whitespace_insensitive` (`test/test_recur.rb:48`)
pins only ASCII padding, which is why the Ruby tests stay green either way.

**Blocking 2 — `strings.ToLower` is simple case mapping; `String#downcase` is
full case mapping.** `go/internal/recur/recur.go:62` lowercases the stripped
input with `strings.ToLower`. Ruby's `String#downcase` (`lib/tasks/recur.rb:156`)
applies full Unicode case mapping, under which U+0130 (`İ`) lowercases to the
two codepoints `i` + U+0307, not to `i`. So `"DAİLY"` downcases to `"dai̇ly"` in
Ruby and is rejected, while Go downcases it to `"daily"` and stores `.+1d`. The
same applies wherever an `I` appears in a keyword or a filler word (`biweekly`,
`fortnightly`, `in`). A full sweep of all codepoints comparing `String#downcase`
against `strings.ToLower` found 56 disagreements, 55 of which are Unicode-table
version skew on letters no keyword contains; U+0130 is the only one that changes
an accept/reject outcome. Classification: **Go defect**. Correction: fold the
one full-mapping case explicitly (map U+0130 to `i` + U+0307 before, or instead
of, `strings.ToLower`) rather than adopting `strings.ToLower`'s simple mapping;
do not "fix" Ruby's behavior here — Ruby's rejection is what is observable
today.

**Not divergences — the declared calendar boundary.** Four further probe rows
disagree because the calendar grammar belongs to `recur-calendar-grammar`:
`"w:mon"` (Ruby `cookie?` true, `parse` `w:mon`, `next_date` projects; Go
rejects all three), `"2nd"` (Ruby `m:2`), and the two natural phrases with a
trailing qualifier, `"every 2 weeks a month"` and `"fortnightly week"`, where
both sides reject but Ruby's message is the monthly-schedule one. These are the
boundary `Parse`'s doc comment declares, not defects, and the rejection
vocabulary is already recorded as unobservable until `cli-recur-command` lands.
They are listed here so the next reviewer does not re-derive them.

Next step after repair: re-run the direct conformance and the Unicode probe
above, then a fresh source-fidelity review, then the Go-idiom review and
independent approval. Medium tier forbids self-approval.

## Repair: Ruby whitespace and full-case normalization (2026-08-02)

The two blocking defects from the preceding independent review are repaired in
`go/internal/recur/recur.go`. `tokenize` now splits only on Ruby regexp `\s`
characters (`space`, tab, CR, LF, FF, VT), rather than Go's broader Unicode
space set. Parse also expands U+0130 to `i` + U+0307 before applying Go's simple
lowercase mapping, preserving the only Ruby full-case expansion that changes
this interval grammar's accept/reject result.

Regressions transcribed from
`source-fidelity-probe-unicode-cases.jsonl` cover all six Unicode-space
rejections and the dotted-I rejection. Positive controls retain ASCII-space
acceptance and the Kelvin-sign lowercase behavior.

Verification after the repair:

- direct differential conformance: 41/41 cases matched;
- the 62-case Unicode/calendar adversarial probe, compared as parsed JSON,
  matched 57/62 and now differs only on the five documented
  `recur-calendar-grammar` boundary rows (`ordinal-2nd`,
  `every-2-weeks-qual`, `cookie-pred-cal`, `not-cookie-next`, and
  `fortnightly-extra`); all interval-owned rows match;
- `go test ./internal/recur`, `go test -race ./internal/recur`, and
  `go vet ./...` passed.

A fresh independent source-fidelity review is next, followed by the separate
Go-idiom review and independent approval required for this medium-risk slice.

## Source-fidelity review: Kelvin-sign case fold (2026-08-02)

**Rejected — repair required before the Go-idiom review.** This independent
mid-tier review made no implementation edits. `go/internal/recur/recur.go:220-223`
implements the Ruby `String#downcase` seam with `strings.ToLower` (after the
U+0130 expansion). That mapping is too broad for the Ruby oracle: Go's Unicode
table explicitly maps U+212A KELVIN SIGN to `k`
(`$GOROOT/src/unicode/letter_test.go:163-166`), while Ruby 4.0's
`Tasks::Recur.parse_result("KEEKLY")` returns
`{error: "unrecognized schedule: \\"KEEKLY\\""}` and `cookie?` is false.
Therefore this Go path accepts and stores `.+1w` for an input Ruby rejects.

Classification: **Go defect**. Preserve U+212A through the downcase adapter
instead of delegating that compatibility-fold to `strings.ToLower`, and add a
Ruby-oracle-backed regression to the direct corpus/probe. Re-run direct
conformance, the Unicode adversarial probe, focused Ruby oracle, Go tests,
race, vet, and diff check; then request a fresh source-fidelity review. The
Go-idiom review and approval remain downstream of that review.

## Kelvin-sign review correction (2026-08-02)

The requested U+212A repair was not applied: its stated oracle comparison was
incorrect. Ruby 4.0 and the existing Go implementation both reject
`"KEEKLY"` as `unrecognized schedule: "KEEKLY"`; Go's `strings.ToLower`
produces `"keekly"`, not the `"weekly"` needed to recognize the keyword.
Conversely, Ruby accepts `"weeKly"` as `.+1w`, as does the existing Go path.
Preserving every Kelvin sign through Go lowercasing would create that latter
divergence, so it is not a valid source-fidelity correction.

`kelvin-leading-rejection` was added to the direct Ruby-owned corpus and the
Go package regression suite. Verification: direct conformance matched 42/42
cases; the 62-case Unicode adversarial probe matched all 57 interval-owned
cases, with only the five declared calendar-grammar boundary rows differing;
`ruby test/test_recur.rb` passed (20 runs, 66 assertions); and `go test ./...`,
`go test -race ./internal/recur`, `go vet ./...`, and `git diff --check`
passed. A new independent source-fidelity review is still required; it should
validate this correction against the Ruby probe before proceeding to Go-idiom
review or approval.

## Independent source-fidelity review: passed (2026-08-02)

An independent medium-tier review of `f7c8b70` made no implementation edits.
The manifest source closure is still byte-identical at
`lib/tasks/recur.rb` to `28424b77950f59f2d78bf089be316c38d9f54aec`.

The review re-ran the Ruby-owned direct corpus and the package boundaries:

- direct Ruby-vs-Go conformance: **42/42** cases matched;
- the Unicode adversarial corpus: **57/62** matched, with exactly the five
  documented `recur-calendar-grammar` boundary rows differing;
- direct Ruby probes confirm `KEEKLY` is rejected, `weeKly` canonicalizes to
  `.+1w`, and `DAİLY` is rejected; the focused Go regressions match;
- `ruby test/test_recur.rb` (20 runs, 66 assertions), `go test ./...`,
  `go test -race ./internal/recur`, `go vet ./...`, `gofmt -l .`, and
  `git diff --check` all passed.

No source-fidelity divergence was found in the interval-cookie slice. The
remaining next step is a **separate independent Go-idiom review**, followed by
independent approval if it passes. The recorded invalid-UTF-8 package-boundary
gap and the five calendar-grammar rows remain out of scope for this review.
