# malformed/invalid-json

A record in the middle of the file whose closing brace was replaced with a
comma — invalid JSON surrounded by valid records.

## What it exercises

- Recovery: the parse does not abort at the first bad line, so later records are
  still read and later errors still reported.
- The exact `JSON::ParserError` message text, truncated to its first line, which
  the diagnostic embeds verbatim.

## What a correct implementation must do

Emit one error for the bad line, keep parsing, and reproduce the parser message.
The embedded message is the port's most brittle string: a Go JSON decoder will
not phrase it the same way, so this fixture is where that intentional difference
(if it is accepted as one) must be recorded in
`porting/intentional-differences.md`.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 4: invalid JSON: expected object key, got: EOF at line 1 column 148
1 error(s), 0 warning(s)
exit 1
```
