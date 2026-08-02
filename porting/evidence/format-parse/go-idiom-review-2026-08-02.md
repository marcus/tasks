# `format-parse` independent Go-idiom review

Reviewed `port/format-parse` at `b7a63eb` without implementation edits.

## Finding

Pass. The public parser surface is small (`Parse`, `Result`, `Record`, and
`ParseError`), while the Ruby-specific diagnostic adaptation is kept private.
The parser uses the standard decoder only to establish JSON validity and
derives Ruby-compatible wording from the original bytes; it does not branch on
unstable `encoding/json` error strings. Ordered fields use a slice plus a
private position map, which is an idiomatic and explicit way to retain source
order while applying duplicate-key replacement. The lexical helpers are
purpose-specific, documented at the adapter boundary, and table/fuzz coverage
makes their byte/column contracts reviewable.

No Go-idiom divergence requiring correction was found in this slice boundary.

## Reproduction

```console
$ cd go && gofmt -d internal/record/parse.go internal/record/parse_test.go
$ go test ./...
ok   tasks-go/internal/record
$ go test -race ./...
ok   tasks-go/internal/record
$ go vet ./...
$ cd .. && ruby porting/evidence/format-parse/conformance
format-parse direct conformance: 48/48 cases matched
$ ruby porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
$ ruby test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
$ git diff --check
```

This is the Go-idiom half of the required medium-risk split review. The
source-fidelity review is recorded separately in
`source-fidelity-review-2026-08-02.md`; independent approval remains next.
