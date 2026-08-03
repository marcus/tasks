# Repair — scalar keyword coercion (answers the FAILED review at f0f8dc1)

Session ses_c4fbec, resuming the handoff recorded by ses_634ca0. This repairs
both findings in `source-fidelity-review-2026-08-02-scalar-kwargs.md`. It is a
repair, not a review: the fresh independent source-fidelity review, and the
re-confirmation of the Go-idiom review, are still owed.

## Order of work

Ruby was captured **before** Go changed. The eight review cases were appended
to `porting/runners/cases/query-filter-parse.jsonl` and
`porting/evidence/query-filter-parse/ruby.jsonl` was re-captured from the Ruby
probe; the recorded Ruby results reproduce the review's table exactly (three
boolean cases accepted with `true`, `body_search: null` false, and the four
scalar cases rejected with the domain messages). Only then was Go touched.

## Finding 1 — boolean keywords

`query.CoerceBool` (`go/internal/query/coerce.go`) is `!!value` for the JSON
value shapes: false only for `null` and `false`, so `0`, `""`, `[]`, and `{}`
are all true. `decodeFilterOptions` now routes all seven boolean keywords
(`deferred_only`, `unavailable_only`, `someday_only`, `recurring_only`,
`body_search`, `delegated_only`, `agent_ready_only`) through it from a generic
decode instead of unmarshalling into `bool`.

## Finding 2 — `scope`, `priority`, `state`

`query.CoerceString` exports the existing `rubyToS` rule. `decodeFilterOptions`
applies it to these three at the same boundary. The omitted-vs-explicit-null
distinction from `9391345` is preserved and is now expressed as the source
rule rather than as a special case: `scope.to_s` runs unconditionally, so an
explicit null scope coerces to `""` and is rejected by name; `priority&.to_s`
and `state&.to_s` skip null, so an explicit null there stays
indistinguishable from omitted.

No typed decode of kwargs remains — every constructor keyword is now coerced
or truth-tested at the dynamic boundary, which is where Ruby does it.
`NewFilter`'s typed signature is unchanged, as the review required.

## Evidence

- Differential conformance: **40/40** via
  `./porting/evidence/query-filter-parse/conformance` (32 prior cases plus the
  eight review cases). `go.jsonl` re-captured from the same corpus.
- `gofmt -l` clean, `go vet ./...` clean, `go test -race ./...` passes.
- New unit tests in `go/internal/query/coerce_test.go`:
  `TestCoerceBoolIsRubyTruthiness`, `TestCoerceStringMatchesRubyToS`,
  `TestCoercedScalarsReachTheDomainRule` (a coerced scalar is rejected by the
  ported domain message, not by a decoder).

## New divergence found while repairing — NOT fixed here

Ruby's `initialize` raises on an unknown keyword; the Go probe silently ignores
unknown JSON keys. Captured Ruby first, in `unknown-keyword-cases.jsonl` /
`unknown-keyword-ruby.jsonl`:

| kwargs | Ruby | Go |
|---|---|---|
| `{"nope":1}` | `unknown keyword: :nope` | accepted, default filter |
| `{"nope":1,"also":2}` | `unknown keywords: :nope, :also` | accepted |
| `{"scope":"done","nope":1}` | `unknown keyword: :nope` | accepted |

Left for the next tick on purpose: the multi-key message preserves the order
the keys were given, so a faithful fix needs order-preserving decoding of the
kwargs object rather than a Go map, which is a larger boundary change than the
repair this handoff assigned. Recorded as a manifest oracle gap; the captured
Ruby above is the corpus that fix must satisfy.
