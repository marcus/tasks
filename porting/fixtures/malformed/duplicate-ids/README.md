# malformed/duplicate-ids

Two records carrying the id `c0000002`.

## What it exercises

- The id uniqueness invariant every ref resolution depends on.
- Error placement: the error is reported on the **last** line of the duplicate
  set, and lists all involved lines in the message.
- Duplicate detection accumulating across the whole file rather than firing on
  first sight.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 4: duplicate id "c0000002" (lines 3, 4) — id refs will be wrong
1 error(s), 0 warning(s)
exit 1
```
