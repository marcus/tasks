# Format parse diagnostic translation

Date: 2026-08-02

The parser's EOF diagnostic adapter now preserves the Ruby wording and
one-based physical column for the three malformed JSONL records captured by
the oracle:

| Fixture | Ruby-compatible diagnostic suffix |
| --- | --- |
| `malformed/invalid-json` | `expected object key, got: EOF at line 1 column 148` |
| `malformed/truncated-final-line` | `unexpected end of input, expected closing " at line 1 column 65` |
| `adversarial/mid-write-torn-file` | `unexpected end of input, expected closing " at line 1 column 13` |

`internal/record.Parse` still delegates JSON grammar to `encoding/json`; the
adapter only recognizes its EOF condition and whether the physical line has an
unclosed JSON string. The fixture tests assert the full parser message, rather
than deriving a new expected value from Go output.

## Checks run

```console
$ (cd go && go test ./...)
ok   tasks-go/internal/record

$ (cd go && go test -race ./...)
ok   tasks-go/internal/record

$ (cd go && go vet ./...)

$ ruby test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips

$ porting/compare/validate porting/evidence/format-parse/ruby
validate: 11/11 observations valid against observations.schema.json and internally coherent
```

## Remaining

No Go CLI/probe/runner exists yet, so this is source-level compatibility proof,
not a differential-conformance result. The next medium-risk step is to expose
the parser through that runner and compare its observations with the pinned
Ruby oracle; it must also expand direct malformed-JSON coverage before claiming
the full parser-diagnostic surface.
