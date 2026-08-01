# malformed/dangling-parent

A record whose `parent` names an id that appears nowhere in the file.

## What it exercises

- Parent resolution against *earlier* records only — a parent defined later in
  the file is as bad as one that does not exist.
- That the DFS check is skipped for this record (the ancestor stack is left
  untouched) so one dangling pointer does not cascade into order errors for
  every record after it.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 7: parent "ffffffff" does not resolve to an earlier record
1 error(s), 0 warning(s)
exit 1
```

Exactly one error, not one per following record: reproducing this containment is
the point of the fixture.
