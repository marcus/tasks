# Format parse malformed-diagnostic preflight

Date: 2026-08-02

This oracle expansion probes malformed one-line JSON values not represented by
the fixture corpus. Ruby was queried directly through `Tasks::Format.parse`;
the following diagnostic suffixes are observable behavior and therefore must
not be derived from Go output.

| Input | Ruby diagnostic suffix |
| --- | --- |
| `{` | `expected object key, got EOF at line 1 column 2` |
| `{"a` | `unexpected end of input, expected closing " at line 1 column 4` |
| `{"a":` | `unexpected end of input at line 1 column 6` |
| `{"a":1,}` | `expected object key, got: '}' at line 1 column 8` |
| `{"a":[1` | `expected ',' or ']' after array value at line 1 column 8` |
| `{"a":"\\q"}` | `invalid escape character in string: '\\q"}' at line 1 column 7` |
| `{"a":1} nope` | `unexpected token at end of stream 'nope' at line 1 column 9` |

## Classification

The existing Go adapter matches only the unclosed-key case. The other six
cases diverge: it maps every EOF to an object-key error and otherwise exposes
`encoding/json` diagnostics. This is a **Go defect**, not missing oracle
coverage and not an intentional difference. No expected Go output was changed
or blessed.

The next translation tick should replace the EOF-only adapter with a bounded
diagnostic compatibility layer, backed by direct tests for this matrix, before
building the Go runner. That is medium risk: it changes user-visible parser
messages while leaving JSON grammar ownership in `encoding/json`.

## Commands

```console
$ ruby -Ilib <direct Tasks::Format.parse diagnostic matrix>
$ (cd go && go test ./internal/record -run TestParseUsesRubyMalformedJSONDiagnostics -v)
```

The second command was intentionally a temporary preflight assertion, not
committed: it failed for the six divergences above. The passing suite remains
the existing fixture and parser coverage until the translation repair lands.
