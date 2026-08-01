# adversarial/journal-cursor-behind-store

A real journal — seven recorded mutations, cursor at the tip — plus a store that
has since been edited **out of band**: a canonically-formatted task was appended
by a foreign writer, so the live bytes match no journal state.

See the corpus README for how a runner installs `journal/`.

## What it exercises

- The undo precondition: `history_step` compares the current snapshot against
  the plan's `expect` snapshot and refuses on mismatch.
- The journal as convenience state, not truth: an out-of-band edit costs you the
  undo, never the edit.
- The refusal message quoting the label of the mutation that would have been
  undone, in curly quotes.
- Real journal internals: content-addressed blobs, a `states` array with a
  labelled entry per mutation, an unlabelled baseline at index 0, and
  `archive_sha: null` throughout (no archive file exists).

## Recorded behavior

```console
$ tasks undo
tasks.jsonl changed since that edit — refusing to undo “priority [#A]: Book the boiler service”
exit 1

$ tasks list
(four tasks, including the foreign writer's — the store is untouched)
exit 0
```

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 4 tasks parsed, no structural errors
exit 0
```

The store is valid. Only the journal disagrees with it — which is the point:
`check` cannot detect this class of problem at all.
