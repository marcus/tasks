# malformed/non-record-lines

Three lines that are not records: an empty line, a JSON array, and a bare JSON
string.

## What it exercises

- Blank lines are an error, not whitespace to skip — the format has no
  separators.
- A line must be a JSON **object**; valid JSON of another type is rejected with
  the Ruby class name in the message (`Array`, `String`).
- Line numbering that counts the junk lines, so subsequent records report
  shifted numbers.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 4: blank line
error  line 6: expected a JSON object, got Array
error  line 8: expected a JSON object, got String
3 error(s), 0 warning(s)
exit 1
```

`got Array` / `got String` are Ruby class names. A port must emit these exact
spellings or record an intentional difference.
