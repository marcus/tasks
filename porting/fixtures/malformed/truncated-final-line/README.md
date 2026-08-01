# malformed/truncated-final-line

A healthy store with its last 30 bytes cut off — the classic non-atomic-write
crash, and the reason `Atomic.write` exists.

## What it exercises

- A final line that is valid JSON *prefix* but not valid JSON.
- Error line numbering on the last line of the file.
- The parser's per-line containment: lines 1..6 parse fine and only line 7 fails.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 7: invalid JSON: unexpected end of input, expected closing " at line 1 column 65
1 error(s), 0 warning(s)
exit 1
```
