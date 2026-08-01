# adversarial/journal-missing-blob

A real journal whose store bytes match the tip exactly — so undo *should* work —
but with the blob for the previous state deleted from `journal/blobs/`.

## What it exercises

- Blob integrity on the undo path: `read` verifies the SHA-256 of the blob's
  bytes against its filename, and a missing or non-regular blob raises
  internally.
- The containment contract: journal trouble degrades to "nothing to undo", never
  to a crash and never to a mangled store.
- That the degradation is silent — the user is not told the history is damaged.

## Recorded behavior

```console
$ tasks undo
nothing to undo
exit 1

$ tasks undo
nothing to undo
exit 1
```

## Finding

A corrupt journal is indistinguishable, at the CLI, from an empty one: both say
`nothing to undo` and exit 1. Note also that "nothing to undo" is exit **1**,
not 0 — an empty history is a failed command.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```
