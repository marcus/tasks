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
