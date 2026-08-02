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

## Classification and bounded gap

These are **Go defects**, not intentional differences and not Go-derived
expectations. `internal/record/rubyJSONError` only recognizes the previously
captured EOF, comma-before-close, invalid-escape, and trailing-token shapes;
the eleven rows above fall through to `encoding/json` wording or an existing
wrong compatibility branch. The static source boundary is
`go/internal/record/parse.go:118-153`.

The fixture corpus and 12-case direct conformance runner therefore do not yet
claim this taxonomy. The manifest records that bounded exclusion explicitly;
the next translation tick must implement a source-aware diagnostic adapter,
add durable differential cases for this table, and remove the gap only after
they compare Ruby and Go successfully.

## Oracle command

```console
$ ruby -Ilib -r tasks/format -r json <extended malformed diagnostic matrix>
```

The exact command and JSON output were recorded in the td session log for
`td-2f853a`; the table above is the durable, reviewable oracle capture.
