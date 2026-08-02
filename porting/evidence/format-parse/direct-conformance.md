# Format-parse direct conformance

The full runner protocol compares CLI observations, but `tasks-go check` and
`tasks-go list` belong to later validation and query slices. Until those
surfaces exist, this slice compares its declared observable parser result
directly: parsed records with their physical `line` stamps and parse-error
tuples.

`porting/runners/ruby/format-parse-probe` invokes the Ruby oracle's
`Tasks::Format.parse`; `go/cmd/format-parse-probe` invokes
`internal/record.Parse`. `conformance` runs both probes over every entry in
`porting/runners/cases/format-parse.jsonl` and compares decoded JSON values, so
object member order in the diagnostic transport cannot conceal or create a
parser difference. The corpus includes a dedicated malformed record containing
a complete second JSON value, which guards against accepting only the first
decoded value.

The invalid-UTF-8 fixture follows the same raw-read guard as the Ruby store:
the direct probe reports the observable line-zero diagnostic instead of calling
`Format.parse` on bytes it explicitly assumes are valid UTF-8.

## Result

```console
$ porting/evidence/format-parse/conformance
format-parse direct conformance: 12/12 cases matched
```

This is differential conformance for the `format-parse` manifest boundary. It
is not a claim that CLI `check`/`list`, structural validation, revisions, lock
side effects, or the generic Go runner have been ported; those claims remain
with their own slices and must use the language-neutral runner protocol.
