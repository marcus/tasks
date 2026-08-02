# Format parse malformed-diagnostic translation

Date: 2026-08-02

This medium-risk translation step closes the six Go defects classified in
`malformed-diagnostic-preflight.md`. `internal/record.Parse` still delegates
JSON grammar to `encoding/json`; its deliberately bounded compatibility layer
now maps the Ruby-captured diagnostic clauses for the following inputs:

| Input | Ruby-compatible diagnostic suffix |
| --- | --- |
| `{"a":` | `unexpected end of input at line 1 column 6` |
| `{"a":1,}` | `expected object key, got: '}' at line 1 column 8` |
| `{"a":[1` | `expected ',' or ']' after array value at line 1 column 8` |
| `{"a":"\\q"}` | `invalid escape character in string: '\\q"}' at line 1 column 7` |
| `{"a":1} nope` | `unexpected token at end of stream 'nope' at line 1 column 9` |
| `{` | `expected object key, got EOF at line 1 column 2` |

The direct table-driven Go test pins the Ruby strings, including the distinct
`got EOF` and `got: EOF` forms. Existing fixture coverage retains the latter
form for a truncated record ending in a comma. No result captured from Go was
used as an oracle.

## Checks run

```console
$ (cd go && go test ./...)
ok   tasks-go/internal/record

$ (cd go && go test -race ./...)
ok   tasks-go/internal/record

$ (cd go && go vet ./...)

$ (cd go && go test -fuzz=FuzzParseKeepsPhysicalLineBounds -fuzztime=3s ./internal/record)
PASS

$ ruby test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips

$ porting/compare/validate porting/evidence/format-parse/ruby
validate: 11/11 observations valid against observations.schema.json and internally coherent
```

## Remaining

No Go CLI/probe/runner exists, so this is source-level compatibility proof and
not a differential-conformance result. The next medium-risk step should build
the parser probe and compare it against the already validated 11-observation
Ruby oracle before either review is requested.
