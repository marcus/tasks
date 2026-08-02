# Format parse extended malformed-diagnostic oracle

Date: 2026-08-02

This characterization extends the seven malformed JSON probes in
`malformed-diagnostic-preflight.md`. Ruby remains the oracle: each input was
passed directly to `Tasks::Format.parse`, and the table records its observable
one-line parser error. No Go output was used to create an expected result.

| Input | Ruby diagnostic suffix |
| --- | --- |
| `[` | `unexpected end of input at line 1 column 2` |
| `{"a":tru}` | `unexpected token 'tru}' at line 1 column 6` |
| `{"a":01}` | `invalid number: '01}' at line 1 column 6` |
| `{"a":"\\u12G4"}` | `incomplete unicode character escape sequence at '\\u12G4"}' at line 1 column 7` |
| `{"a" "b"}` | `expected ':' after object key at line 1 column 6` |
| `{"a":,}` | `unexpected character: ',}' at line 1 column 6` |
| `{"a": [1,]}` | `unexpected character: ']}' at line 1 column 10` |
| `{"a": true false}` | `expected ',' or '}' after object value, got: 'false}' at line 1 column 12` |
| `{"a":1}{"b":2}` | `unexpected token at end of stream '{"b":2}' at line 1 column 8` |
| `{"a": "\\x"}` | `invalid escape character in string: '\\x"}' at line 1 column 8` |
| `{"a": 1e}` | `invalid number: '1e}' at line 1 column 7` |

## Translation and differential conformance

The initial classification was **Go defect**, not an intentional difference
and not a Go-derived expectation. The source-aware compatibility adapter in
`go/internal/record/parse.go` now derives Ruby clauses and physical columns
from the malformed input rather than branching on `encoding/json` wording.

Each row now has a persistent one-line fixture under
`porting/fixtures/malformed/diagnostic-*/store/tasks.jsonl` and an entry in
`porting/runners/cases/format-parse.jsonl`. The slice-local direct-conformance
runner compared Ruby and Go across all 23 cases successfully, so this former
bounded oracle gap is closed.

```console
$ porting/evidence/format-parse/conformance
format-parse direct conformance: 23/23 cases matched
```

## Oracle command

```console
$ ruby -Ilib -r tasks/format -r json <extended malformed diagnostic matrix>
```

The exact command and JSON output were recorded in the td session log for
`td-2f853a`; the table above is the durable, reviewable oracle capture.
