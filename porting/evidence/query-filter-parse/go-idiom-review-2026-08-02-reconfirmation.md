# `query-filter-parse` independent Go-idiom re-review

Reviewed `port/query-filter-parse` at
`a273019a1c1bf5674742344fbb7f6b644e6da021`.

## Result

Fail — one correction is required before independent approval.

`go/cmd/query-filter-parse-probe/main.go:84` defers `file.Close()` and drops
its error. The configured `golangci-lint` reports this as `errcheck`; the
probe's input is an externally supplied case file, so the ignored close error
is not an acceptable exception. Return or report the close error without
changing the captured JSONL output on a successful run, then re-run this
review and the required evidence suite.

The prior review's shape questions do not need a correction: `quoteRuby` is a
shared implementation whose `hexC0` selector directly represents the only
observable difference between String and Symbol quoting; `identifier`'s byte
index is correct because it only distinguishes the first rune and now has a
comment stating that invariant; and the source-derived compatibility helpers
are appropriately scoped to `internal/query` rather than probe-local code.

## Checks

- `gofmt -d` over the slice's Go files — no diff
- `go test ./...`, `go vet ./...`, and `go test -race ./...` — passed
- `ruby test/test_task_queries.rb` — 25 runs, 198 assertions, passed
- `porting/evidence/query-filter-parse/conformance` — 156/156 Ruby-versus-Go
  cases matched
- `porting/manifest-issues validate` — passed, 144 slices
- `golangci-lint run ./...` — failed: the new `errcheck` finding above. Its
  two `ST1018` findings in `coerce_repair_test.go` are also on this branch and
  should be resolved while restoring the required lint-clean gate; the
  `internal/record/parse.go` `QF1001` finding predates this slice and is out of
  scope.
