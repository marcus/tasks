# `format-parse` independent source-fidelity review

Reviewed `port/format-parse` at `83230ef46dcc59b32aa3da08a12187aef3efdd74`
against `lib/tasks/format.rb`'s `Tasks::Format.parse` contract.

## Finding

No source-fidelity divergence found in the slice boundary. The Go parser
preserves the Ruby behavior under review: leading-BOM removal, physical line
stamping, blank-line handling, invalid-UTF-8 whole-file failure, lenient
per-line recovery, JSON-object-only acceptance, duplicate-key semantics, and
the Ruby-observed malformed-input diagnostics represented by the fixture
corpus. Its ordered `Field` representation also preserves the parsed Hash
member order needed by the downstream canonical-emission slice.

## Reproduction

Run from `go/` on this commit:

```console
$ go test ./internal/record
ok   tasks-go/internal/record
$ go test -race ./internal/record
ok   tasks-go/internal/record
$ go vet ./...
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 48/48 cases matched
$ ruby ../porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
$ ruby ../test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
```

This is the source-fidelity half of the required medium-risk split review.
The separate Go-idiom review and independent approval remain required.
