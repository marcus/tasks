# `format-parse` translation step

Commit: recorded with this translation change.

The initial Go module now contains `internal/record.Parse`. It preserves the
read-side boundaries captured by the Ruby oracle:

- records and errors retain physical one-based line numbers;
- a leading UTF-8 BOM is ignored, while invalid UTF-8 is a line-zero file error;
- malformed and non-object lines do not prevent subsequent records from being
  returned; and
- empty input, blank lines, and a final newline follow Ruby's line behavior.

`go test ./...` and `go vet ./...` passed from `go/` on 2026-08-02. The tests
exercise the captured malformed and adversarial fixtures without writing them.

This is deliberately not conformance evidence. The Go CLI and language-neutral
Go runner do not exist yet, so the committed Ruby CLI observations cannot be
compared to this package. In particular, the final Ruby JSON parser diagnostic
wording and all CLI-visible `check`/`list` behavior remain to be proven by the
next implementation step. No Go output was accepted as an oracle.
