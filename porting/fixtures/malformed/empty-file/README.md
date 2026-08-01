# malformed/empty-file

A zero-byte `tasks.jsonl`.

## What it exercises

- The one branch that produces `missing meta record on line 1` rather than
  `line 1 must be a meta record` — the first fires when there is no record on
  line 1 at all, the second when line 1 parsed but is not a `meta`.
- The difference from `valid/empty-store`, which has the header and is healthy.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: missing meta record on line 1
1 error(s), 0 warning(s)
exit 1
```
