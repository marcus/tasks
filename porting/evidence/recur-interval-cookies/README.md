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
