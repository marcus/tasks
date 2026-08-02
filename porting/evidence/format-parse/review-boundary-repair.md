# `format-parse` review-boundary repair

Date: 2026-08-02

This repair addresses the three exact gaps from the independent source-fidelity review of `e33d71e` without changing the port boundary.

- The parser now uses Ruby-compatible `String#strip` byte semantics for blank classification: NUL-only input is blank, while a UTF-8 non-breaking-space line is invalid JSON.
- Only the file's first UTF-8 BOM is stripped. A BOM at the start of a later line is preserved in Ruby's observable parser diagnostic.
- The direct differential probes encode Ruby's `Infinity` / `-Infinity` as a JSON-safe typed value. This is probe transport only; JSONL records are still parsed and retained as source JSON bytes.

The three new fixture cases are persistent Ruby-oracle inputs in `porting/runners/cases/format-parse.jsonl`; Go output was not used to produce their expected results.

```console
$ cd go && go test ./...
ok   tasks-go/internal/record
$ go test -race ./...
ok   tasks-go/internal/record
$ go vet ./...
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 31/31 cases matched
$ ruby ../test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
$ ruby ../porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
```

The slice remains in review. It still requires fresh, separate independent medium-risk source-fidelity and Go-idiom reviews; this repair does not approve or close it.
