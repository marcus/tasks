# `query-filter-parse` Go-idiom re-review after lint repair

Reviewed `port/query-filter-parse` at
`9178a735c0164d9a31a16b34ec7a0ff8d25c3b71`.

## Result

Pass. The repair handles the externally supplied case-file close error at the
probe boundary without changing successful JSONL output, and the escaped test
literals are clearer and satisfy the configured style rule. The diff is
limited to that error handling, the non-printing test characters, and evidence.
No Go-idiom correction is required.

`golangci-lint run ./...` still reports
`internal/record/parse.go:351 QF1001`; `main` contains the identical line, so
it is the documented pre-existing out-of-slice finding rather than a finding
against this slice.

## Independent checks

- `gofmt -d cmd/query-filter-parse-probe/main.go internal/query/coerce_repair_test.go` — no diff
- `go test ./...`, `go vet ./...`, and `go test -race ./...` — passed
- `golangci-lint run ./...` — only the pre-existing `internal/record/parse.go:351 QF1001`
- `ruby test/test_task_queries.rb` — 25 runs, 198 assertions, passed
- `porting/evidence/query-filter-parse/conformance` — 156/156 Ruby-versus-Go cases matched
- `porting/manifest-issues validate` — passed, 144 slices
- `git diff --check a273019..9178a73` — clean

The previously recorded independent source-fidelity review at `a273019` is
still applicable: this lint-only repair does not alter the ported behavior.
