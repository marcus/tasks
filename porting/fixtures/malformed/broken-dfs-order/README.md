# malformed/broken-dfs-order

A record whose `parent` resolves to a real earlier record, but one whose subtree
has already closed — the parent is no longer on the open ancestor stack.

## What it exercises

- File order as tree order: a subtree must be a *contiguous* run of lines.
- The distinction from `dangling-parent`: here the parent exists and was seen,
  so the error is about position, not resolution.
- The ancestor-stack walk itself (pop until the parent is on top; error if it
  never is).

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 8: record "c0000007" breaks DFS pre-order (parent "c0000001" is not an open ancestor)
1 error(s), 0 warning(s)
exit 1
```
