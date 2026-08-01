# malformed/cross-file-duplicate-id

The same id in `tasks.jsonl` and `archive.jsonl` — the state a concurrent
archive-vs-edit merge can produce, where a task exists in both files at once.

## What it exercises

- The store-wide id invariant, which no single file can violate on its own: each
  file here is perfectly valid.
- The gap between `tasks check` and `tasks check --all-files`: the default run
  cannot see this.
- Error attribution: the error is reported against the archive's line number.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0

$ tasks check --all-files
error  line 3: id "c0000002" appears in both tasks.jsonl line 3 and archive.jsonl line 3
1 error(s), 0 warning(s)
exit 1
```

A fixture that passes one invocation and fails another is exactly why the runner
must record the argv alongside the outcome.
