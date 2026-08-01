# legacy/schema-v1-mixed-versions

`tasks.jsonl` is still version 1 while `archive.jsonl` is already version 2 —
the state a migration interrupted between its two file writes would leave.

## What it exercises

- Per-file version resolution: the migrator must migrate only the file that
  needs it and leave the already-current one untouched.
- A store whose two halves disagree about the schema.

## What a correct implementation must do

Migrate the live file alone, report `from_version: 1`, and not rewrite (or back
up) the archive that was already current.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: unsupported meta version 1 (expected 2)
1 error(s), 0 warning(s)
exit 1

$ tasks check --all-files
error  line 1: tasks.jsonl: unsupported meta version 1 (expected 2)
1 error(s), 0 warning(s)
exit 1
```

The archive contributes no error: it is already at version 2.
