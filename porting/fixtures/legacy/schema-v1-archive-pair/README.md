# legacy/schema-v1-archive-pair

Both files at schema version 1: the state of a data directory that has archived
work and has never been migrated.

## What it exercises

- Version detection on `archive.jsonl` as well as `tasks.jsonl`.
- `migrate` rewriting *both* headers in one transaction, with a backup per file.
- `check --all-files` reporting one error per file rather than stopping at the
  first.

## What a correct implementation must do

Treat the pair as one migration unit: either both files reach version 2 or
neither does, with a rollback that restores both.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: unsupported meta version 1 (expected 2)
1 error(s), 0 warning(s)
exit 1

$ tasks check --all-files
error  line 1: tasks.jsonl: unsupported meta version 1 (expected 2)
error  line 1: archive.jsonl: unsupported meta version 1 (expected 2)
2 error(s), 0 warning(s)
exit 1
```
