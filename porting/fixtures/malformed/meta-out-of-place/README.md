# malformed/meta-out-of-place

The header swapped with line 2, plus a second `meta` record appended at the end.

## What it exercises

- `meta` is valid on line 1 and nowhere else, and a *second* one is its own
  error even if line 1 were correct.
- Two independent complaints about the same record class in one file.
- That a section on line 1 does not satisfy the header requirement.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: line 1 must be a meta record ({"type":"meta","version":2})
error  line 2: unexpected meta record (only valid on line 1)
error  line 8: unexpected meta record (only valid on line 1)
3 error(s), 0 warning(s)
exit 1
```
