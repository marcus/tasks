# `format-parse` review diagnostic repair

Date: 2026-08-02

An independent source-fidelity review at `056466e` found eight unpinned
Ruby-vs-Go boundaries. This translation step captured each as a persistent
fixture before changing Go: tab-separated object keys, adjacent literals in an
object and array, a bare minus, an incomplete signed exponent, a leading
decimal point, an unpaired high surrogate, and tab whitespace before trailing
JSON. Ruby observations remain the expected results throughout.

`internal/record` now derives the separator and malformed-number diagnostics
from source positions, and rejects an unpaired high surrogate before
`encoding/json` can replace it with U+FFFD. The direct corpus grew from 32 to
40 cases and now agrees with the Ruby parser on all 40. This does not approve
the medium-risk slice: it needs fresh, separate source-fidelity and Go-idiom
reviews in later sessions.

```console
$ cd go && go test ./...
ok   tasks-go/internal/record
$ go test -race ./...
ok   tasks-go/internal/record
$ go test -fuzz=FuzzParseKeepsPhysicalLineBounds -fuzztime=3s ./internal/record
ok   tasks-go/internal/record
$ go vet ./...
$ ruby ../test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
$ ruby ../porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 40/40 cases matched
```
