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
