# Format parse conformance preflight

Date: 2026-08-02

This is not a differential-conformance result.  There is no Go CLI, probe, or
runner yet, so the required runner comparison cannot be run.  It records the
first source-to-oracle check needed before that boundary is built.

## Result: Go defect to repair before a conformance claim

The Ruby oracle's `check` observations require exact parser diagnostics for
malformed JSON:

| Case | Ruby diagnostic after `invalid JSON:` |
| --- | --- |
| `format-parse-invalid-json` | `expected object key, got: EOF at line 1 column 148` |
| `format-parse-truncated-final-line` | `unexpected end of input, expected closing " at line 1 column 65` |
| `format-parse-torn-file-check` | `unexpected end of input, expected closing " at line 1 column 13` |

`go/internal/record/parse.go` currently delegates token and position detail to
`encoding/json`, and `rubyJSONError` only maps its `unexpected EOF` case to
`unexpected end of input`.  It cannot produce the required `expected object
key` or `expected closing` clauses (nor their columns).  The mismatch is a
**Go defect**, not a Ruby-rule exception or an intentional difference: the
slice explicitly makes parser error text observable through `tasks check`.

The next translation step must provide a parser diagnostic adapter with the
Ruby wording and physical-column behavior, covered directly before exposing it
through the Go CLI/probe/runner path.  Do not update the Ruby observations.

## Checks run

```console
$ (cd go && go test ./...)
ok   tasks-go/internal/record

$ ruby test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips

$ porting/compare/validate porting/evidence/format-parse/ruby
validate: 11/11 observations valid against observations.schema.json and internally coherent
```
