# valid/single-task

One task, no sections at all — the task record is itself a tree root.

## What it exercises

- A `task` record with no `parent`: roots are not required to be sections.
- A one-record DFS pre-order (the degenerate walk).
- Singular pluralization ("1 task", not "1 tasks").

## What a correct implementation must do

Treat a parentless task as a legitimate top-level node — tree building,
rendering, and placement must not assume every task hangs under a section.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0
```
