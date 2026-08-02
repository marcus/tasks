# Ruby oracle capture — check-meta-and-ids

Captured on 2026-08-02 from Ruby at source revision `174165be85c9`. No Go
implementation was present or exercised.

`porting/runners/ruby/run --out porting/evidence/check-meta-and-ids/ruby
porting/runners/cases/check-meta-and-ids.jsonl` produced the nine checked-in
observations. The two healthy cases exit 0 without mutation:

- `check-meta-valid-empty-store`: `ok — 0 tasks parsed, no structural errors`
- `check-meta-valid-archive-pair`: `ok — 6 records parsed, no structural errors`

The seven malformed or incompatible cases exit 1 without mutation. Their raw
stdout and byte hashes are in `ruby/*.json`; the cases cover missing/non-meta
line one, later meta records, an empty file, same-file and cross-file duplicate
ids, a non-string id (reported, not raised), and future schema version 3.

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

This is not yet differential conformance: the Go runner and the full `check`
CLI/report layer belong to later work. In particular, task-field and tree
diagnostics remain separate slices, so the new package deliberately does not
claim the complete output of the `wrong-types` fixture. The next tick must add
a Go conformance path that compares this slice's owned diagnostics to the Ruby
baseline, then request independent source-fidelity and Go-idiom reviews.
