# adversarial/journal-undo-redo-delete

The first journal fixture in the corpus that is **not** a refusal. A real
seven-mutation history whose last two entries are deletes, with the cursor at
the tip and the store bytes matching the tip state exactly — so `tasks undo`
succeeds, and succeeds *byte-for-byte*.

See the corpus README for how a runner installs `journal/`.

**How it was made.** Generated, not hand-assembled. The store and the journal
were produced by running the real CLI through this sequence under the pinned
environment (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`, empty
`XDG_CONFIG_HOME`, `TASKS_DIR` and `XDG_STATE_HOME` in a scratch directory):

```sh
tasks capture "Book the venue for the autumn workshop" --no-host-context --context @calls
tasks capture "Rebuild the potting bench"              --no-host-context --context @home
tasks capture "Cut the frame to length"                --no-host-context --context @home --under "Rebuild the potting bench"
tasks capture "Sand and oil the top"                   --no-host-context --context @home --under "Rebuild the potting bench"
tasks capture "File the quarterly expenses"            --no-host-context --context @computer
tasks delete 0f209fcb              # leaf
tasks delete a2e66c43              # refused: 2 descendants, wrote nothing
tasks delete a2e66c43 --cascade    # the subtree
```

The shipped `store/tasks.jsonl` and `journal/` are that run's output verbatim;
only the org path in `index.json` was replaced with `{{ORG_PATH}}`. Nothing here
was authored by hand, which is the point: a hand-built journal that did not
correspond to a real sequence of writes would prove the wrong thing.

## What it exercises

- **The undo happy path against a Ruby-written journal.** Every other journal
  fixture in this corpus refuses; this one replays.
- **Byte-exact undo.** Undoing a delete must reproduce the pre-delete file
  byte-for-byte, not merely an equivalent record set. Ruby gets this for free
  because the journal stores whole-file blobs — a port that reconstructs the
  file by re-serializing its records instead has to land on the same bytes.
- **Byte-exact redo**, and `redo` past the tip degrading to `nothing to redo`.
- **Content-addressed dedup as an observable consequence.** Deleting the leaf
  produced org blob `2a4d6446…`, which is *the same blob* as the state four
  captures earlier: the delete's after-state is byte-identical to the state
  before that task was created. Likewise the cascade delete lands back on
  `bc4aca6b…`, state 1's blob. Eight states reference five blobs.
- **A multi-record undo**, not just a one-line one: the tip entry restores a
  three-record subtree into its exact DFS position.
- **The unlabelled baseline with `org_sha: null`** — this history starts before
  `tasks.jsonl` existed, a shape the other journal fixtures do not carry.
- Labels the port must reproduce verbatim, including the pluralized
  `delete 3 tasks: …` form.

## Recorded behavior

Store as shipped: one task (`effebb47`), cursor at state 7.

```console
$ tasks undo
undid: delete 3 tasks: Rebuild the potting bench
exit 0
                       # tasks.jsonl now byte-identical to the post-leaf-delete
                       # state — verified with cmp against the generating run

$ tasks undo
undid: delete: File the quarterly expenses
exit 0
                       # byte-identical to the post-capture state — verified

$ tasks redo
redid: delete: File the quarterly expenses
exit 0

$ tasks redo
redid: delete 3 tasks: Rebuild the potting bench
exit 0
                       # byte-identical to store/tasks.jsonl as shipped — verified

$ tasks redo
nothing to redo
exit 1
```

**Byte-exactness is verified, not assumed.** Each undo/redo target above was
compared with `cmp` against the file the generating CLI run left on disk at that
point in the sequence. All four comparisons are byte-identical, including the
`updated` stamps — undo restores stamps, it does not refresh them.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 1 task parsed, no structural errors
exit 0
```

Singular "task" — the store is deliberately down to one record after the two
deletes.
