# Ruby oracle capture — check-meta-and-ids

Captured on 2026-08-02 from Ruby at source revision `174165be85c9`. No Go
implementation was present or exercised.

`porting/runners/ruby/run --out porting/evidence/check-meta-and-ids/ruby
porting/runners/cases/check-meta-and-ids.jsonl` produced the checked-in
observations. The two healthy cases exit 0 without mutation:

- `check-meta-valid-empty-store`: `ok — 0 tasks parsed, no structural errors`
- `check-meta-valid-archive-pair`: `ok — 6 records parsed, no structural errors`

The malformed or incompatible cases exit 1 without mutation. Their raw
stdout and byte hashes are in `ruby/*.json`; the cases cover missing/non-meta
line one, later meta records, an empty file, same-file and cross-file duplicate
ids, a non-string id (reported, not raised), future schema version 3, exponent,
null, and object-valued schema versions, and an unknown type with no ID.

Two test-only branches were also characterized directly before any Go work:

```json
{"case_id":"check-meta-missing-file","result":{"ok":false,"errors":[{"line":0,"message":"file not found: /definitely-not-present/tasks.jsonl"}],"warnings":[]}}
{"case_id":"check-meta-float-version","result":{"ok":false,"errors":[{"line":1,"message":"unsupported meta version 2.0 (expected 2)"}],"warnings":[]}}
```

The focused Ruby suite passed: 38 runs, 193 assertions. The next step is a
mid-tier translation against this capture, then property tests and independent
source-fidelity and Go-idiom reviews. Differential conformance waits for the Go
check runner; this capture is its Ruby baseline and does not bless Go output.

## Translation step — 2026-08-02

`go/internal/check` now implements the slice-owned metadata and ID rules over
the already-ported lenient parser: line-one meta/version validation, malformed
and duplicate IDs, missing-file reporting, and live/archive duplicate IDs.
Its fixture tests assert the Ruby-captured diagnostics for every owned outcome,
and its generated ID grammar property test covers 500 mixed valid/invalid
strings. `go test ./...`, `go vet ./...`, and the race-enabled test suite pass.

The direct differential path is `ruby
porting/evidence/check-meta-and-ids/compare.rb`. It drives
`go/cmd/check-meta-and-ids-probe` over the initial nine fixtures and compares
its owned metadata/ID entries to the checked-in Ruby CLI observations. It
initially passed all nine comparisons. The comparator extracts only this
slice's declared
diagnostics from Ruby's full report: task-field and tree diagnostics in the
`wrong-types` fixture remain separate slices, rather than becoming an
unintentional completion claim here.

The remaining medium-tier work at that point was the split independent
source-fidelity and Go-idiom reviews; independent approval is required before
landing.

## Source-fidelity repair — 2026-08-02

The later source-fidelity review found two diagnostic/order divergences in the
initial translation. `rubyInspect` now decodes JSON before rendering it, so
`2e0` is reported as Ruby's `2.0` and JSON `null` as Ruby's `nil`. The ID pass
now runs only for `section` and `task` records, matching Ruby's unknown-type
short-circuit. The oracle corpus and differential comparator now cover all 12
fixture cases and pass 12/12; the unknown-type case intentionally compares an
empty slice-owned diagnostic set until the shared report owns its one unknown
type error.

## Object-order repair — 2026-08-02

The source-fidelity re-review found that malformed object-valued metadata had
been decoded into a Go map and therefore rendered in sorted-key order. Ruby's
`Hash#inspect` retains parsed JSON member order. `rubyInspect` now walks JSON
decoder tokens, preserving object order while retaining the Ruby spelling for
scalars and arrays. The added `check-meta-object-version-order` fixture is
captured from Ruby and included in the direct differential comparator.
