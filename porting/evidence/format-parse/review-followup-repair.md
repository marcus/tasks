# `format-parse` review follow-up repair

Date: 2026-08-02

The independent source-fidelity review at `c449339` found unpinned Ruby parser
boundaries. Before changing Go, this step captured Ruby's exact behavior for a
high-high surrogate pair, four malformed numbers, and adjacent nested arrays
and objects. It also made the direct oracle transport represent Ruby's invalid
UTF-8 string produced by a lone low surrogate; this is transport-only and does
not alter persisted JSONL behavior.

`internal/record` now distinguishes invalid from incomplete surrogate pairs,
recognizes the captured malformed-number forms before separator diagnostics,
and identifies adjacent composite values without treating object keys as
values. The typed low-surrogate transport preserves Ruby's `edb080` bytes,
which `encoding/json` would otherwise replace with U+FFFD. Ruby remains the
expected result for every new case; no Go output was blessed.

```console
$ cd go && go test ./...
ok   tasks-go/internal/record
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 48/48 cases matched
```

Fresh independent source-fidelity and Go-idiom reviews are still required for
this medium-risk slice.
