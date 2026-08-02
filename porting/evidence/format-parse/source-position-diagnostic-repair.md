# `format-parse` source-position diagnostic repair

Date: 2026-08-02

The prior independent reviews found five malformed JSON inputs where the Go
parser collapsed Ruby's observable diagnostic into a generic trailing-token
error. Ruby remained the oracle: the persistent fixtures and case entries were
created from Ruby output before this repair.

`go/internal/record/parse.go` now derives the missing-separator,
missing-colon, malformed-number, and trailing-value diagnostics from lexical
tokens and physical offsets in the input line. It does not select a diagnostic
from `encoding/json` error text. The top-level balanced-value scan also finds
the end of the first complete object, so a following malformed value retains
Ruby's token and column.

Verification on `port/format-parse`:

```console
$ cd go && go test ./...
ok   tasks-go/internal/record
$ go test -race ./...
ok   tasks-go/internal/record
$ go vet ./...
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 28/28 cases matched
$ ruby ../porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
$ ruby ../test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
```

The slice is not ready for approval. A later tick must adopt an ordered parsed
record representation before canonical emission, then request fresh independent
source-fidelity and Go-idiom reviews.
