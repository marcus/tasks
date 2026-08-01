# legacy/schema-v1

A store at on-disk schema **version 1** — the JSONL format as it stood before
the temporal (timed-task) migration landed in `80691f4`.

## What it exercises

- `{"type":"meta","version":1}` on line 1.
- Records with no `scheduled_time` / `deadline_time` anywhere — the only
  structural difference between v1 and v2.
- The two different reactions to an old version: `check` reports it as an error,
  while the store's read path classifies it as `:migration_required` and
  `tasks migrate` converts it.

## What a correct implementation must do

Refuse to *operate* on a v1 store, tell the user to run `migrate`, and implement
`migrate` as: validate against v1 rules, refuse if any record carries time
metadata, write a backup, rewrite the header to version 2, and drop a journal
barrier so undo cannot walk back across the schema change.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: unsupported meta version 1 (expected 2)
1 error(s), 0 warning(s)
exit 1
```

Note the diagnostic does not mention `migrate`. `check` treats a v1 header as a
plain version error; only the store read path and `tasks migrate` know it is a
migratable state.
