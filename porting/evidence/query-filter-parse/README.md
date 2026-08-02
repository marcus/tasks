# Query-filter parse oracle and translation

`ruby.jsonl` is the captured Ruby oracle. `go.jsonl` is the Go translation's
current direct-probe output; it is compared structurally, not promoted to an
expected result. Run `./porting/evidence/query-filter-parse/conformance` to
reproduce the 60-case differential result, including explicit `scope: null`,
scalar collection, mixed collection, collection-null, non-boolean boolean
keyword, non-string scope/priority/state, and unknown-constructor-keyword
inputs. `symbol-inspect-capture` captures Ruby's `Symbol#inspect` for 73 name
shapes into `symbol-inspect-ruby.jsonl`, the oracle for how an unknown keyword
is named.

The package also has a state-intersection property test across every scope and
state vocabulary value, plus `coerce_test.go` for the `Array(values).map(&:to_s)`
collection coercion described in `translation.md`. Source-fidelity and Go-idiom
review remain independent medium-tier steps and are not claimed by this
translation handoff.

`source-fidelity-review-2026-08-02-scalar-collections.md` is repaired as of the
40/40 run above; its reproduction case set is now part of the committed corpus.
So is `source-fidelity-review-2026-08-02-scalar-kwargs.md` — see
`source-fidelity-repair-2026-08-02-scalar-kwargs.md`, which also recorded one
new divergence (unknown constructor keywords) with its Ruby capture in
`unknown-keyword-{cases,ruby}.jsonl`. That divergence is repaired as of the
49/49 run above; see `repair-2026-08-02-unknown-keywords.md`.

`source-fidelity-review-2026-08-02-parse-cli-regexes.md` failed with three
`parse_cli` defects; all three are repaired as of the 60/60 run above, with
their cases in the committed corpus — see
`repair-2026-08-02-parse-cli-regexes.md`. That repair also gave both probes an
`argv_base64` argument encoding, because JSONL cannot carry the invalid-UTF-8
arguments finding 2 turns on.

That fresh independent review is
`source-fidelity-review-2026-08-02-unicode-tables.md`. It confirms all three
repairs and **fails** with one new Go defect: `text_query` downcases with Go's
Unicode 15.0.0 tables while Ruby 4.0.6 uses 17.0.0, leaving 55 codepoints
uppercased. Its adversarial corpus is
`source-fidelity-unicode-{cases,ruby,go}-2026-08-02.jsonl` (23/28 matched) and
the exhaustive whole-Unicode sweep is
`downcase-divergence-2026-08-02.jsonl`. Those cases are deliberately *not* in
`porting/runners/cases/query-filter-parse.jsonl` yet — the repair tick moves
them there so conformance guards the fix.
