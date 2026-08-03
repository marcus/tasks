# `query-filter-parse` lint repair

This repair addresses the independent Go-idiom re-review at `a273019`.

- `cmd/query-filter-parse-probe/main.go` now reports an input case-file close
  error instead of discarding it.
- `internal/query/coerce_repair_test.go` expresses the three non-printing test
  characters through escapes, eliminating the slice-local `ST1018` findings.

## Verification

- `gofmt -d cmd/query-filter-parse-probe/main.go internal/query/coerce_repair_test.go` — no diff
- `go test ./...`, `go vet ./...`, and `go test -race ./...` — passed
- `golangci-lint run ./...` — only the pre-existing out-of-slice
  `internal/record/parse.go:351 QF1001` remains
- `ruby test/test_task_queries.rb` — 25 runs, 198 assertions, passed
- `porting/evidence/query-filter-parse/conformance` — 156/156 matched
- `porting/manifest-issues validate` — 144 slices passed
