# malformed/missing-ids-many

The same damage as `malformed/missing-id-single`, three records over instead of
one: lines 3, 4 and 6 carry no `id`. Line 5 does.

It exists to pin the boundary that the single-record fixture cannot show — that
`ensure_id!` repairs a store with **one** missing id and cannot repair a store
with two.

## Why `malformed/`

Same argument as its sibling: `check` reports each id-less record as an error and
exits 1. See `malformed/missing-id-single` for the full reasoning about why this
is damage rather than version skew.

## What it exercises

`Store#with_history` has no preflight. It snapshots, runs the mutation, and then
runs `post_write_failure` — a full `Check` of the live file (and the archive when
one exists). If that fails, it restores the snapshot and reports failure.

`ensure_id_impl` mints **one** id, for the one record it was asked about. On this
store that leaves two records still missing ids, so the post-write `Check` still
fails, so the write is rolled back. The repair path cannot converge here: running
`tasks id` on each of the three in turn fails three times, because each attempt
is validated against the whole file.

That is the entire behavior this fixture pins, and it is why the single- and
many- fixtures are a pair rather than one file with three holes in it.

## What a correct implementation must do

Refuse. Write nothing that survives the invocation, and leave the store
byte-identical. Report the rollback wording, not the preflight wording — the two
refusals are distinguishable, and this one is the rollback.

## Recorded Ruby outcomes

Under the pinned environment in the corpus README, plus the runner's
`TASKS_PIN_NOW=2026-03-14T15:09:26Z` and `TASKS_PIN_IDS=bbbb0001`.

### `tasks check` — exit 1

```console
$ tasks check
error  line 3: record missing id
error  line 4: record missing id
error  line 6: record missing id
3 error(s), 0 warning(s)
exit 1
```

`check --all-files` reports the same three, prefixed `tasks.jsonl:`.

### `tasks id "First hand-appended"` — exit 1, **rolled back**

```console
$ tasks id "First hand-appended"
failed to assign id
file failed validation after the edit — run `tasks check`
exit 1
```

The store is byte-identical afterwards: line 3 still has no id.

## Findings

**1. `ensure_id!` is not a store repair, it is a record repair — and it only
lands when that record is the file's last remaining error.** The wording is the
tell. `missing-id-single` refuses `capture` with `task file is already invalid …
(nothing was written)`; this fixture refuses `tasks id` with `file failed
validation after the edit`. The first is a refusal, the second is a write that
happened and was undone. A port that adds a preflight to `ensure_id!` — which
looks like an optimization, since the write is doomed — would produce the first
message here instead of the second, and would be wrong.

**2. This was a real dead end for a user, not just for the harness.** No
*mutation* repairs this store: every path that could mint the second id is gated
on the file being clean afterwards, and it never is until the last id is minted.

`tasks repair` (td-d6ed92) is the escape hatch added afterwards, and it does not
disturb anything above. It is a **separate command**, not a change to any path
this fixture records: `tasks id` still writes, still fails the post-write
`Check`, and still rolls back with the wording pinned above, and `ensure_id!`
still has no preflight. `repair` mints an id for *every* id-less record across
the store in one pass and writes once, so the file `Check` sees is already whole:

```console
$ tasks repair
fixed  tasks.jsonl line 3: record missing id → minted <id>
fixed  tasks.jsonl line 4: record missing id → minted <id>
fixed  tasks.jsonl line 6: record missing id → minted <id>
3 repairs written — the store validates; `undo` restores the previous bytes
exit 0
```

It leaves `updated` exactly as it found it — a repair asserts nothing about a
task's content, and `stamp_changed_tasks!` indexes originals by id, so a
just-minted id would be stamped as a brand-new task. `--dry-run` reports the
same plan and writes nothing. Every recorded outcome above remains true.

**3. The rollback is invisible in the bytes.** `files.before` and `files.after`
are identical, exactly as they are for the `capture` refusal on the sibling
fixture. Only the exit status, stderr, and the runner's `files.mutated`
cross-check separate "refused" from "wrote and reverted" — which is the same gap
`porting/runners/README.md` records under "`files.rolled_back` is always null".
This fixture is a concrete case where that gap has teeth.
