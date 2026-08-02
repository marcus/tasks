# adversarial/journal-redo-pending-delete

The only fixture in the corpus whose journal cursor sits **behind the tip**:
eight states, `cursor: 6`, one undone delete still reachable ahead of it. This
is the sole input state from which `redo` is reachable at all, and the state
that drives the redo-tail truncation in `Journal#record`.

See the corpus README for how a runner installs `journal/`.

**How it was made.** Generated, not hand-assembled. It is
`adversarial/journal-undo-redo-delete` after exactly one more real CLI
invocation — `tasks undo` — under the same pinned environment
(`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`, empty `XDG_CONFIG_HOME`,
`TASKS_DIR` and `XDG_STATE_HOME` in a scratch directory). See that fixture's
README for the seven-mutation sequence that built the history. The shipped store
and journal are that run's output verbatim, with only the org path in
`index.json` replaced by `{{ORG_PATH}}`.

The store here is therefore genuinely the bytes Ruby's own undo wrote, and the
index is genuinely the index Ruby's own commit persisted. Store and journal
agree because they were produced together.

## What it exercises

- **Redo reachable from a cold start.** A fresh process, given only these bytes,
  must find a redo step and replay it.
- **Byte-exact redo of a cascade delete**, re-removing a three-record subtree and
  landing on `journal-undo-redo-delete`'s shipped store exactly.
- **The redo tail is dropped by any new mutation.** `Journal#record` truncates
  `states[0..cursor]` before appending; the redo entry becomes unreachable and
  its blob is garbage-collected unless another state still references it.
- **Undo still walks backwards** from a behind-the-tip cursor — the cursor is a
  position in a timeline, not a stack depth.
- **The undo/redo commit strips coalesce metadata**, so a redo landing on a
  former tip cannot resume an editor session's coalescing.
- Incidentally: `tasks delete` on a parent without `--cascade` refuses here
  (`a2e66c43` has two children), writing nothing and leaving the cursor put —
  a refusal that must not consume or disturb history.

## Recorded behavior

Store as shipped: four tasks, `cursor: 6` of 8 states.

```console
$ tasks redo
redid: delete 3 tasks: Rebuild the potting bench
exit 0
                       # tasks.jsonl now byte-identical to
                       # adversarial/journal-undo-redo-delete/store/tasks.jsonl
                       # — verified with cmp
```

From a *fresh* install of the fixture, undo instead:

```console
$ tasks undo
undid: delete: File the quarterly expenses
exit 0
                       # byte-identical to the post-capture state of the
                       # generating run — verified with cmp
```

From a *fresh* install, a new mutation instead of a redo:

```console
$ tasks capture "Order the replacement bulb" --no-host-context --context @errands
INBOX Order the replacement bulb :@errands:
exit 0

$ tasks redo
nothing to redo
exit 1
```

After that capture the index is still eight states with `cursor: 7`, but state 7
is now `capture: Order the replacement bulb` — the `delete 3 tasks: Rebuild the
potting bench` entry was truncated away, exactly as "a new edit clears redo"
requires.

From a *fresh* install, the parent-without-cascade refusal:

```console
$ tasks delete a2e66c43
refusing to delete: "Rebuild the potting bench" has 2 descendants (2 open). Re-run with --cascade to remove the whole subtree.
exit 1
```

`tasks.jsonl` is unchanged (sha compared before and after) and `cursor` is still
6.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 4 tasks parsed, no structural errors
exit 0
```

The store is valid. `check` cannot see the cursor at all — the divergence
between "what the file says" and "where history thinks you are" is invisible to
it, which is why this class of state needs a fixture rather than a lint rule.
