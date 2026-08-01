# valid/archive-pair

A live store and an `archive.jsonl` beside it, both healthy.

## What it exercises

- The two-file store: `tasks.jsonl` plus `archive.jsonl`, each with its own
  `meta` header and its own independent DFS pre-order.
- `archived` stamps on a swept subtree root and on a standalone archived task.
- The difference between `tasks check` (live file only) and
  `tasks check --all-files` (both files plus the cross-file id invariant).
- Record counting across both files.

## What a correct implementation must do

Validate each file independently, enforce that no id appears in both, and keep
the two files' orders separate — the archive is not a continuation of the live
tree.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 2 tasks parsed, no structural errors
exit 0

$ tasks check --all-files
ok — 6 records parsed, no structural errors
exit 0
```
