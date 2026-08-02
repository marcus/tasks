# Query-filter parse oracle and translation

`ruby.jsonl` is the captured Ruby oracle. `go.jsonl` is the Go translation's
current direct-probe output; it is compared structurally, not promoted to an
expected result. Run `./porting/evidence/query-filter-parse/conformance` to
reproduce the 40-case differential result, including explicit `scope: null`,
scalar collection, mixed collection, collection-null, non-boolean boolean
keyword, and non-string scope/priority/state constructor inputs.

The package also has a state-intersection property test across every scope and
state vocabulary value, plus `coerce_test.go` for the `Array(values).map(&:to_s)`
collection coercion described in `translation.md`. Source-fidelity and Go-idiom
review remain independent medium-tier steps and are not claimed by this
translation handoff.

`source-fidelity-review-2026-08-02-scalar-collections.md` is repaired as of the
40/40 run above; its reproduction case set is now part of the committed corpus.
So is `source-fidelity-review-2026-08-02-scalar-kwargs.md` — see
`source-fidelity-repair-2026-08-02-scalar-kwargs.md`, which also records one
new, deliberately unrepaired divergence (unknown constructor keywords) with its
Ruby capture in `unknown-keyword-{cases,ruby}.jsonl`.
