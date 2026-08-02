# `format-parse` adjacent missing-colon repair

Date: 2026-08-02

An independent source-fidelity review found that `{"a"1}` was not part of the
persistent Ruby oracle corpus. Ruby reports `expected ':' after object key at
line 1 column 5`; the Go parser previously fell through to its generic trailing
token diagnostic. This was classified as a Go defect before any Go result was
used for comparison.

The new persistent fixture
`malformed/diagnostic-object-missing-colon-adjacent` records Ruby's result in
the direct conformance case list. `missingColonDiagnostic` now recognizes a
completed object key followed immediately by a value, while
`missingSeparatorDiagnostic` separately keeps a completed string value followed
by a value in the object-value diagnostic family. Direct Go tests cover both
boundaries so a value such as `{"a":"x"1}` cannot be mislabeled as a missing
object-key colon.

```console
$ cd go && go test ./...
ok   tasks-go/internal/record
$ go test -race ./...
ok   tasks-go/internal/record
$ go test -fuzz=FuzzParseKeepsPhysicalLineBounds -fuzztime=3s ./internal/record
ok   tasks-go/internal/record
$ go vet ./...
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 32/32 cases matched
$ ruby ../test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
$ ruby ../porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
```

The manifest remains `reviewing`; fresh, separate independent source-fidelity
and Go-idiom reviews are still required. This translation tick neither approves
nor closes the medium-risk slice.
