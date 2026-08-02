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
recur-interval-cookies direct conformance: 16/16 cases matched
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

Correction before a fresh source-fidelity review: decode `default_prefix` as
presence-aware (for example a `*string`) and default only when the JSON key is
absent, then add an explicit-empty-prefix direct case and rerun the Ruby-vs-Go
comparator. No implementation edit was made by this reviewer. The existing
invalid-UTF-8 boundary remains the separately recorded CLI-adapter ownership
issue above.
